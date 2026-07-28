package canvas

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/store"
)

func fixture() (*store.Run, []store.Node, []store.Edge, *store.Verdict, *store.Verdict) {
	run := &store.Run{RunID: "r1", Project: "p", UnitID: "US-034", Phase: "verify", Status: "running"}
	nodes := []store.Node{
		{NodeID: "p-build", RunID: "r1", Kind: "phase", Label: "build", Status: "ok"},
		{NodeID: "n-a1", RunID: "r1", Kind: "subagent", Label: "api", Domain: "api", Status: "failed"},
		{NodeID: "n-a2", RunID: "r1", Kind: "subagent", Label: "api", Domain: "api", Status: "ok"},
		{NodeID: "n-b1", RunID: "r1", Kind: "subagent", Label: "web", Domain: "web", Status: "running"},
	}
	edges := []store.Edge{
		{Src: "p-build", Dst: "n-a1", Rel: "spawn"},
		{Src: "p-build", Dst: "n-b1", Rel: "spawn"},
		{Src: "n-a2", Dst: "n-a1", Rel: "retry_of"},
	}
	rv := &store.Verdict{RunID: "r1", Gate: "qa", CheckID: "qa", Result: "blocked", Source: "agent",
		Evidence: []string{"3 tests failing"}, Why: "the suite is red", SafeNextStep: "fix TestFoo"}
	bv := &store.Verdict{RunID: "r1", Gate: "qa", CheckID: "checks", Result: "ok", Source: "batten",
		Evidence: []string{"go build ./...: exit 0"}, Why: "the declared checks ran"}
	return run, nodes, edges, rv, bv
}

// TestRenderProducesValidJSONCanvas pins the contract with the format, not with our code.
// Obsidian is the reader, and it is unforgiving: a node missing a dimension or an edge pointing
// at an id that is not in nodes[] renders as a broken or empty canvas, with no error to explain
// why. https://jsoncanvas.org/spec/1.0/
func TestRenderProducesValidJSONCanvas(t *testing.T) {
	c := Render(fixture())
	if len(c.Nodes) == 0 {
		t.Fatal("a run with 4 nodes must render some canvas nodes")
	}

	ids := map[string]bool{}
	for _, n := range c.Nodes {
		if n.ID == "" {
			t.Error("every node needs an id")
		}
		if ids[n.ID] {
			t.Errorf("duplicate node id %q — Obsidian keeps one and silently drops the rest", n.ID)
		}
		ids[n.ID] = true

		switch n.Type {
		case "text", "file", "link", "group":
		default:
			t.Errorf("node %q has type %q, which is not in the 1.0 spec", n.ID, n.Type)
		}
		// Zero width or height is a node you cannot see. The spec requires all four.
		if n.Width <= 0 || n.Height <= 0 {
			t.Errorf("node %q has no size (%dx%d)", n.ID, n.Width, n.Height)
		}
		if n.Type == "text" && n.Text == "" {
			t.Errorf("text node %q carries no text", n.ID)
		}
	}

	for _, e := range c.Edges {
		if e.ID == "" {
			t.Error("every edge needs an id")
		}
		// A dangling edge is the failure that renders as an empty canvas rather than an error.
		if !ids[e.FromNode] {
			t.Errorf("edge %q starts at %q, which is not a node in this canvas", e.ID, e.FromNode)
		}
		if !ids[e.ToNode] {
			t.Errorf("edge %q ends at %q, which is not a node in this canvas", e.ID, e.ToNode)
		}
	}
}

// The retry is the thing a static workflow diagram cannot show, and the reason this canvas is
// worth drawing at all: it records the path the work ACTUALLY took.
func TestRetryEdgeSurvivesRendering(t *testing.T) {
	c := Render(fixture())
	var retries int
	for _, e := range c.Edges {
		if e.Label == "retry_of" || e.Label == "retry" {
			retries++
		}
	}
	if retries == 0 {
		t.Error("the retry_of edge must reach the canvas; it is what a plan diagram cannot show")
	}
}

// A failed node and a healthy one must not look the same. Colour is the only channel the canvas
// has for it.
func TestStatusColoursDistinguishOutcomes(t *testing.T) {
	seen := map[string]string{}
	for _, s := range []string{"ok", "failed", "running"} {
		seen[s] = statusColor(s)
	}
	if seen["ok"] == seen["failed"] {
		t.Errorf("ok and failed share colour %q — a red run would read as a green one", seen["ok"])
	}
	if statusColor("something-we-have-never-seen") == "" && seen["ok"] == "" {
		t.Error("statusColor should still return a usable value for an unknown status")
	}
}

func TestHumanTokensStaysReadable(t *testing.T) {
	cases := []struct{ in int64 }{{0}, {999}, {1000}, {1_500_000}, {2_000_000_000}}
	for _, c := range cases {
		if got := humanTokens(c.in); got == "" {
			t.Errorf("humanTokens(%d) returned empty", c.in)
		}
	}
}

// WriteFile must create the directory and emit JSON that parses. The vault path it writes into
// frequently does not exist yet.
func TestWriteFileCreatesItsDirectoryAndEmitsParseableJSON(t *testing.T) {
	c := Render(fixture())
	path := filepath.Join(t.TempDir(), "nested", "deeper", "US-034.canvas")

	if err := c.WriteFile(path); err != nil {
		t.Fatalf("WriteFile into a directory that does not exist yet: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back Canvas
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("the canvas we wrote is not valid JSON: %v", err)
	}
	if len(back.Nodes) != len(c.Nodes) {
		t.Errorf("round-trip lost nodes: wrote %d, read %d", len(c.Nodes), len(back.Nodes))
	}

	// nodes and edges must be present as arrays even when empty — Obsidian rejects a canvas
	// whose keys are missing, and Go would omit a nil slice only if tagged omitempty.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"nodes", "edges"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("the canvas is missing the %q key", k)
		}
	}
}

// An empty run still has to produce a canvas that opens. Rendering nothing is a legitimate
// state — a unit whose run just started — and it must not emit malformed JSON.
func TestEmptyRunRendersAnOpenableCanvas(t *testing.T) {
	run := &store.Run{RunID: "r0", Project: "p", UnitID: "US-000", Status: "running"}
	c := Render(run, nil, nil, nil, nil)
	if c == nil {
		t.Fatal("Render must not return nil")
	}
	path := filepath.Join(t.TempDir(), "empty.canvas")
	if err := c.WriteFile(path); err != nil {
		t.Fatalf("writing an empty canvas: %v", err)
	}
	b, _ := os.ReadFile(path)
	var back Canvas
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("an empty run produced invalid JSON: %v", err)
	}
}

// TestASubagentWhoseParentPhaseIsMissingStillAppears.
//
// A spawn edge can name a phase node that is not in this run: a run recorded before phase ids
// were scoped per run, or one whose phase row was taken by another unit entering the same phase.
// The renderer used to bucket that subagent under an id nothing drew, so it vanished from the
// canvas while `batten show` still listed it — the run looked smaller than it was, and the
// missing agent is exactly the one someone opens the canvas to find.
func TestASubagentWhoseParentPhaseIsMissingStillAppears(t *testing.T) {
	run := &store.Run{RunID: "r1", Project: "p", UnitID: "US-034", Phase: "build", Status: "running"}
	nodes := []store.Node{
		{NodeID: "r1:p-build", RunID: "r1", Kind: "phase", Label: "build", Status: "running"},
		{NodeID: "r1:n-a1", RunID: "r1", Kind: "subagent", Label: "api", Domain: "api", Status: "ok"},
		// Its spawn edge points at a phase row this run no longer has.
		{NodeID: "r1:n-b1", RunID: "r1", Kind: "subagent", Label: "web", Domain: "web", Status: "ok"},
	}
	edges := []store.Edge{
		{Src: "r1:p-build", Dst: "r1:n-a1", Rel: "spawn"},
		{Src: "p-build", Dst: "r1:n-b1", Rel: "spawn"}, // dangling: unscoped, not in nodes
	}

	c := Render(run, nodes, edges, nil, nil)

	ids := map[string]bool{}
	for _, n := range c.Nodes {
		ids[n.ID] = true
	}
	for _, want := range []string{"r1:n-a1", "r1:n-b1"} {
		if !ids[want] {
			t.Errorf("subagent %q was dropped from the canvas; a parent id we cannot resolve "+
				"belongs in the unattributed column, not in the bin", want)
		}
	}

	// And it must not land on top of the phase it does not belong to.
	pos := map[string][2]int{}
	for _, n := range c.Nodes {
		pos[n.ID] = [2]int{n.X, n.Y}
	}
	if pos["r1:n-a1"] == pos["r1:n-b1"] {
		t.Errorf("the orphan was drawn on top of the attributed subagent, both at %v", pos["r1:n-a1"])
	}
}
