package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// fixture builds a repo with a spec, a vault, and a run that fanned out to two subagents with
// disjoint write-sets — the shape the run note is meant to describe.
func fixture(t *testing.T, vault string) (*spec.Spec, *store.Store) {
	t.Helper()
	dir := t.TempDir()

	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    requires_verdict: ok\n"
	if vault != "" {
		y += "capabilities:\n  obsidian:\n    vault: '" + filepath.ToSlash(vault) + "'\n    export: [runs, verdicts, canvas]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	r, err := st.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []store.Node{
		{NodeID: "p-build", RunID: r.RunID, Kind: "phase", Label: "build", Status: "ok"},
		{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "api", Domain: "api", Status: "ok"},
		{NodeID: "n-b", RunID: r.RunID, Kind: "subagent", Label: "web", Domain: "web", Status: "ok"},
	} {
		if err := st.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.AddEdge(r.RunID, "p-build", "n-a", "spawn")
	_ = st.AddEdge(r.RunID, "p-build", "n-b", "spawn")
	if err := st.ClaimWriteSet(r.RunID, "n-a", []string{"server/handler.go", "server/handler_test.go"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimWriteSet(r.RunID, "n-b", []string{"client/app.tsx"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"go test ./...: PASS"},
	}, true); err != nil {
		t.Fatal(err)
	}
	return sp, st
}

// TestRunNoteReportsTheWriteSetsThatWereActuallyClaimed is the regression test for a bug that
// lived in every release of the vault export: the note rendered "not recorded" in the write-set
// column and never printed the Files touched list, because export.Run — the only production
// path — never populated Writer.WriteSets. The rendering was right; nothing filled it.
func TestRunNoteReportsTheWriteSetsThatWereActuallyClaimed(t *testing.T) {
	vault := t.TempDir()
	sp, st := fixture(t, vault)

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.RunNotePath == "" {
		t.Fatal("a configured vault must produce a run note")
	}
	b, err := os.ReadFile(res.RunNotePath)
	if err != nil {
		t.Fatal(err)
	}
	note := string(b)

	if strings.Contains(note, "not recorded") {
		t.Errorf("write-sets WERE claimed, so the note must not say \"not recorded\":\n%s", note)
	}
	if !strings.Contains(note, "2 files") || !strings.Contains(note, "1 file") {
		t.Errorf("the fan-out table must show each agent's write-set size (2 files / 1 file):\n%s", note)
	}
	for _, f := range []string{"server/handler.go", "server/handler_test.go", "client/app.tsx"} {
		if !strings.Contains(note, f) {
			t.Errorf("Files touched is missing %q:\n%s", f, note)
		}
	}
}

// The other half of that fix: when nothing was claimed, the note must say "not recorded" rather
// than "0 files". "Nobody recorded a write-set" and "this agent owned nothing" are different
// facts, and reporting the second when the first is true is inventing one.
func TestRunNoteSaysNotRecordedWhenNothingWasClaimed(t *testing.T) {
	vault := t.TempDir()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n" +
		"capabilities:\n  obsidian:\n    vault: '" + filepath.ToSlash(vault) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	r, _ := st.EnsureRun("p", "TASK-9", "sess-a")
	_ = st.AddNode(store.Node{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "solo", Status: "ok"})

	res, err := Run(sp, st, "TASK-9")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.RunNotePath)
	if !strings.Contains(string(b), "not recorded") {
		t.Errorf("with no claims at all the note must read \"not recorded\", never \"0 files\":\n%s", b)
	}
}

// A closed run is exactly the one the vault should reflect — the Stop hook exports AFTER work
// concludes. Exporting only open runs meant a finished unit never reached the vault at all.
func TestExportWorksForAClosedRun(t *testing.T) {
	vault := t.TempDir()
	sp, st := fixture(t, vault)
	r, err := st.LatestRun("p", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CloseRun(r.RunID, "ok"); err != nil {
		t.Fatal(err)
	}

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatalf("a closed run must still export: %v", err)
	}
	if res.RunNotePath == "" || res.CanvasPath == "" {
		t.Fatalf("closed run produced note=%q canvas=%q", res.RunNotePath, res.CanvasPath)
	}
	if _, err := os.Stat(res.CanvasPath); err != nil {
		t.Errorf("the canvas was not written: %v", err)
	}
	if res.Nodes == 0 {
		t.Error("the canvas has no nodes")
	}
}

// With no vault configured the canvas still gets written, into .batten/ — the feature degrades
// rather than disappearing, and no note is claimed that does not exist.
func TestWithoutAVaultTheCanvasStillLands(t *testing.T) {
	sp, st := fixture(t, "")

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.RunNotePath != "" {
		t.Errorf("no vault is configured, so no note path may be reported; got %q", res.RunNotePath)
	}
	if !strings.Contains(filepath.ToSlash(res.CanvasPath), "/.batten/") {
		t.Errorf("without a vault the canvas belongs in .batten/, got %q", res.CanvasPath)
	}
	if _, err := os.Stat(res.CanvasPath); err != nil {
		t.Errorf("the canvas was not written: %v", err)
	}
}

// A unit with no run at all is not an error worth breaking a hook over — the Stop hook calls
// this on every session end, including sessions that never started a unit.
func TestUnknownUnitFailsQuietlyEnoughForAHook(t *testing.T) {
	sp, st := fixture(t, t.TempDir())
	if _, err := Run(sp, st, "TASK-404"); err == nil {
		t.Error("an unknown unit should report an error to its caller, which ignores it on the hook path")
	}
}

// TestTheNoteKeepsBothVerdictsApartByProducer is the third site of the one-verdict defect.
//
// The gate needs two verdicts from two different producers: a reviewer's judgement of the work,
// and batten's own proof that the declared checks RAN. `batten check` writes the second, and its
// row is always the newest — so a surface that reads "the latest verdict" shows check output
// where the reviewer's evidence should be. `batten show` (92ae1cb) and the TUI (24d7cd2) were
// fixed; export.Run, which writes the Obsidian note AND the canvas, was still reading one row.
//
// The shape below is the one that does the damage: the reviewer BLOCKED the unit, then `batten
// check` passed. Reading the latest row alone, the note said `verdict: ok`, printed the check
// output as its evidence, and dropped the run out of the "blocked-verdicts" dashboard — which is
// the one view whose whole job is to show a human that this unit is stuck.
func TestTheNoteKeepsBothVerdictsApartByProducer(t *testing.T) {
	vaultDir := t.TempDir()
	sp, st := fixture(t, vaultDir)

	r, err := st.LatestRun("p", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	// Explicit timestamps, both after the fixture's own row, and batten's the newest of all —
	// which is exactly what let it win before. `batten check` always writes last.
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "review", Result: "blocked", Source: "agent",
		Evidence: []string{"acceptance criterion 3 is not met: no rate-limit header"},
		Why:      "the reviewer rejected it", SafeNextStep: "implement the header",
		TS: 9_000_000_000,
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "checks", Result: "ok", Source: "batten",
		Evidence: []string{"go test ./...: PASS by batten"}, Why: "the declared checks ran",
		TS: 9_000_000_001,
	}, true); err != nil {
		t.Fatal(err)
	}

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	b, err := os.ReadFile(res.RunNotePath)
	if err != nil {
		t.Fatal(err)
	}
	note := string(b)

	// The property the "needs a human" dashboard selects on must be the REVIEWER's.
	if !strings.Contains(note, "verdict: blocked") {
		t.Errorf("the reviewer blocked this unit, so `verdict` must be blocked — `batten check` "+
			"passing its own checks does not overrule a reviewer:\n%s", note)
	}
	// And the reviewer's actual finding has to survive into the body.
	if !strings.Contains(note, "acceptance criterion 3 is not met") {
		t.Errorf("the reviewer's evidence was painted over with check output:\n%s", note)
	}
	// batten's pass is not deleted — it is reported as what it is, under its own heading.
	if !strings.Contains(note, "batten_verdict: ok") || !strings.Contains(note, "go test ./...: PASS by batten") {
		t.Errorf("batten's own check pass must still be reported, separately:\n%s", note)
	}

	// Same rule on the canvas: both envelopes get a node, so opening it cannot show one green
	// pass for a gate that has a blocked half.
	cb, err := os.ReadFile(res.CanvasPath)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Nodes []struct{ ID, Text string } `json:"nodes"`
	}
	if err := json.Unmarshal(cb, &c); err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, n := range c.Nodes {
		byID[n.ID] = n.Text
	}
	if !strings.Contains(byID["verdict"], "acceptance criterion 3 is not met") {
		t.Errorf("the canvas verdict node does not carry the reviewer's evidence: %q", byID["verdict"])
	}
	if !strings.Contains(byID["verdict-batten"], "go test ./...: PASS by batten") {
		t.Errorf("the canvas has no separate node for batten's check pass: %q", byID["verdict-batten"])
	}
}

// The other half of the same rule, and the more dangerous one in practice: only `batten check`
// has run. Nothing has reviewed the work, and every surface must say so rather than draw the one
// green node it has. Silence here reads as approval.
func TestACheckOnlyRunSaysTheReviewerIsMissing(t *testing.T) {
	vaultDir := t.TempDir()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n" +
		"capabilities:\n  obsidian:\n    vault: '" + filepath.ToSlash(vaultDir) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	r, _ := st.EnsureRun("p", "TASK-7", "sess-a")
	_ = st.AddNode(store.Node{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "solo", Status: "ok"})
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "checks", Result: "ok", Source: "batten",
		Evidence: []string{"go test ./...: PASS"}, Why: "the declared checks ran",
	}, true); err != nil {
		t.Fatal(err)
	}

	res, err := Run(sp, st, "TASK-7")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.RunNotePath)
	note := string(b)

	// `verdict: none` is what keeps this run in the blocked-verdicts dashboard. Before the fix
	// it read `verdict: ok` — a run nobody had reviewed, filed under approved.
	if !strings.Contains(note, "verdict: none") {
		t.Errorf("no reviewer has judged this unit, so `verdict` must be none:\n%s", note)
	}
	if !strings.Contains(note, "No reviewer verdict") {
		t.Errorf("the note must name the missing half out loud:\n%s", note)
	}

	cb, _ := os.ReadFile(res.CanvasPath)
	var c struct {
		Nodes []struct{ ID, Text, Color string } `json:"nodes"`
	}
	if err := json.Unmarshal(cb, &c); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range c.Nodes {
		if n.ID == "verdict" {
			found = true
			if !strings.Contains(n.Text, "missing") {
				t.Errorf("the canvas draws a reviewer verdict that does not exist: %q", n.Text)
			}
		}
	}
	if !found {
		t.Error("the canvas omits the missing reviewer verdict entirely; an absent half of the " +
			"gate is a fact about the run, not an empty slot to skip")
	}
}

// TestExportHonorsTheDeclaredList closes the guard's tenth instance (ítem 23, plan §8).
// `capabilities.obsidian.export` declared WHICH files the vault gets and nothing read it:
// a user who wrote `export: [canvas]` received the note and the dashboards anyway. A field
// the user writes believing it governs, and that does not govern, is worse than its absence.
func TestExportHonorsTheDeclaredList(t *testing.T) {
	vault := t.TempDir()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n" +
		"capabilities:\n  obsidian:\n    vault: '" + filepath.ToSlash(vault) + "'\n    export: [canvas]\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r, _ := st.EnsureRun("p", "TASK-3", "sess-a")
	_ = st.AddNode(store.Node{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "solo", Status: "ok"})

	res, err := Run(sp, st, "TASK-3")
	if err != nil {
		t.Fatal(err)
	}
	if res.CanvasPath == "" {
		t.Fatal("export: [canvas] must still write the canvas")
	}
	if _, err := os.Stat(res.CanvasPath); err != nil {
		t.Errorf("the canvas was not written: %v", err)
	}
	if res.RunNotePath != "" {
		t.Errorf("export: [canvas] excludes the run note, and one was written anyway: %q", res.RunNotePath)
	}
	notePath := VaultWriter(sp).RunNotePath("TASK-3")
	if _, err := os.Stat(notePath); err == nil {
		t.Errorf("export: [canvas] excludes the run note, and %q exists on disk", notePath)
	}
	// The dashboards are the `verdicts` export; none was asked for.
	bases, _ := filepath.Glob(filepath.Join(vault, "batten", "p", "*.base"))
	if len(bases) > 0 {
		t.Errorf("export: [canvas] excludes the dashboards, and %d .base file(s) were written: %v",
			len(bases), bases)
	}

	// Control: the fixture declares all three and must keep getting all three — and an EMPTY
	// list must also keep the historical everything-by-default, or adopting the field would
	// silently empty every existing vault.
	sp2, st2 := fixture(t, t.TempDir())
	res2, err := Run(sp2, st2, "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if res2.RunNotePath == "" || res2.CanvasPath == "" {
		t.Errorf("export: [runs, verdicts, canvas] must produce note and canvas; got note=%q canvas=%q",
			res2.RunNotePath, res2.CanvasPath)
	}
}

func TestExpandHomeLeavesAbsolutePathsAlone(t *testing.T) {
	if got := expandHome("/var/vaults/acme"); got != "/var/vaults/acme" {
		t.Errorf("an absolute path must not be rewritten; got %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	got := expandHome("~/vaults/acme")
	if !strings.HasPrefix(got, home) {
		t.Errorf("~ must expand to the home directory; got %q", got)
	}
	if strings.Contains(got, "~") {
		t.Errorf("the tilde survived expansion: %q", got)
	}
}
