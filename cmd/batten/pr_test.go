package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// prFixture builds the shape the PR body exists to describe: a fan-out where one agent failed
// and was retried, with claimed write-sets and both verdicts.
func prFixture(t *testing.T) (*spec.Spec, *store.Store, *store.Run) {
	t.Helper()
	dir := writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"  plan: docs/backlog.md\n"+
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    gate: qa\n    requires_verdict: ok\n"+
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n")
	if err := writeFileAt(filepath.Join(dir, "docs", "backlog.md"),
		"# Backlog\n\n### TASK-42 — rate limit on orders\n\nbody\n"); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	run, err := st.EnsureRun("p", "TASK-42", "s1")
	if err != nil {
		t.Fatal(err)
	}
	run.BaseSHA = "44630cd1234"
	_ = st.SetBaseSHA(run.RunID, run.BaseSHA)

	nodes := []store.Node{
		{NodeID: "p1", RunID: run.RunID, Kind: "phase", Label: "build", Status: "ok", StartedAt: 1},
		{NodeID: "a1", RunID: run.RunID, Kind: "subagent", Domain: "api", AgentID: "api-1",
			AgentType: "domain-agent", Status: "failed", StartedAt: 2},
		{NodeID: "a2", RunID: run.RunID, Kind: "subagent", Domain: "api", AgentID: "api-2",
			AgentType: "domain-agent", Status: "ok", StartedAt: 3},
		{NodeID: "b1", RunID: run.RunID, Kind: "subagent", Domain: "store", AgentID: "store-1",
			AgentType: "domain-agent", Status: "ok", StartedAt: 4},
	}
	for _, n := range nodes {
		if err := st.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][3]string{
		{"p1", "a1", "spawn"}, {"p1", "b1", "spawn"}, {"a2", "a1", "retry_of"},
	} {
		if err := st.AddEdge(run.RunID, e[0], e[1], e[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ClaimWriteSet(run.RunID, "a2", []string{"api/rate.go", "api/rate_test.go"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimWriteSet(run.RunID, "b1", []string{"store/orders.go"}); err != nil {
		t.Fatal(err)
	}
	return sp, st, run
}

func writeFileAt(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// TestPRBodyDrawsThePathActuallyTaken.
//
// GitHub renders Mermaid natively in a pull request description, and batten has had the run's
// DAG in SQLite with typed edges since the beginning. The retry is what makes it worth drawing:
// a plan diagram shows what was supposed to happen, and this shows what did.
func TestPRBodyDrawsThePathActuallyTaken(t *testing.T) {
	sp, st, run := prFixture(t)
	addBothVerdicts(t, st, run)

	body, err := renderPR(sp, st, run)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(body, "```mermaid") {
		t.Fatalf("no mermaid block:\n%s", body)
	}
	// The retry edge, dotted and labelled. Without it the graph is just the plan again.
	if !strings.Contains(body, "-.retry_of.->") {
		t.Errorf("the retry edge is missing — it is the one thing a plan diagram cannot show:\n%s", body)
	}
	// The failing agent must be visibly failing.
	if !strings.Contains(body, ":::fail") {
		t.Errorf("a failed subagent is not marked as failed:\n%s", body)
	}
	// Titled from the backlog, not invented.
	if !strings.Contains(body, "rate limit on orders") {
		t.Errorf("the title should come from the plan document:\n%s", body)
	}
	// Write-set sizes, which is the half of the record nothing else has.
	if !strings.Contains(body, "2 files") || !strings.Contains(body, "1 file") {
		t.Errorf("the write-set sizes are missing from the graph:\n%s", body)
	}
}

// A broken Mermaid block renders on GitHub as a raw error box in the middle of the PR, which
// looks like batten is broken. Every edge must point at a node the diagram declared.
func TestMermaidBlockIsInternallyConsistent(t *testing.T) {
	sp, st, run := prFixture(t)
	addBothVerdicts(t, st, run)

	body, err := renderPR(sp, st, run)
	if err != nil {
		t.Fatal(err)
	}
	block := between(body, "```mermaid", "```")

	declared := map[string]bool{}
	declRe := regexp.MustCompile(`(?m)^\s*(\w+)[\[\(\{]`)
	for _, m := range declRe.FindAllStringSubmatch(block, -1) {
		declared[m[1]] = true
	}
	if len(declared) < 4 {
		t.Fatalf("expected the phase, three subagents and the gate; found %v\n%s", declared, block)
	}

	edgeRe := regexp.MustCompile(`(?m)^\s*(\w+)\s*-[.\->]*.*?->\s*(\w+)\s*$`)
	found := 0
	for _, m := range edgeRe.FindAllStringSubmatch(block, -1) {
		found++
		for _, id := range []string{m[1], m[2]} {
			if !declared[id] {
				t.Errorf("edge references %q, which is not a node in this diagram:\n%s", id, block)
			}
		}
	}
	if found == 0 {
		t.Errorf("no edges were parsed out of the block; the graph is a pile of loose boxes:\n%s", block)
	}

	// Labels are quoted, because `<br/>` and punctuation break a bare Mermaid label.
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "<br/>") && !strings.Contains(line, `("`) {
			t.Errorf("a label containing <br/> is not quoted, which breaks the block:\n%s", line)
		}
	}
}

// TestPRNeverInventsACostItDidNotMeasure is the non-negotiable. This body is the one artefact
// other people read without knowing what to distrust, so a zero here is the most expensive lie
// batten could tell.
func TestPRNeverInventsACostItDidNotMeasure(t *testing.T) {
	sp, st, run := prFixture(t)
	addBothVerdicts(t, st, run)

	body, err := renderPR(sp, st, run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "NOT MEASURED") {
		t.Errorf("no transcript was ingested, so the cost table must say NOT MEASURED:\n%s", body)
	}
	for _, forbidden := range []string{"$0.00", "| tokens | 0 "} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the cost table printed %q for a run nobody measured:\n%s", forbidden, body)
		}
	}
}

// The badge is the first thing a reviewer reads. It may only say batten-verified when the gate
// is genuinely satisfied — and when it is not, it has to name what is missing.
func TestTheBadgeIsHonestAboutAnUnsatisfiedGate(t *testing.T) {
	cases := []struct {
		name    string
		set     func(t *testing.T, st *store.Store, run *store.Run)
		wantBad string
	}{
		{"no verdict at all", func(*testing.T, *store.Store, *store.Run) {}, "no verdict at all"},
		{"checks never run", func(t *testing.T, st *store.Store, run *store.Run) {
			saveVerdict(t, st, run, "agent", "ok", []string{"looked at it"})
		}, "never run"},
		{"nobody reviewed", func(t *testing.T, st *store.Store, run *store.Run) {
			saveVerdict(t, st, run, "batten", "ok", []string{"go test: PASS"})
		}, "judged the work"},
		{"checks failed", func(t *testing.T, st *store.Store, run *store.Run) {
			saveVerdict(t, st, run, "agent", "ok", []string{"looked at it"})
			saveVerdict(t, st, run, "batten", "blocked", []string{"go test: FAIL"})
		}, "did not pass"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp, st, run := prFixture(t)
			c.set(t, st, run)

			body, err := renderPR(sp, st, run)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(body, "**batten-verified**") {
				t.Errorf("claimed batten-verified with %s:\n%s", c.name, body)
			}
			if !strings.Contains(body, "NOT batten-verified") {
				t.Errorf("the badge does not warn:\n%s", body)
			}
			if !strings.Contains(body, c.wantBad) {
				t.Errorf("the badge does not name the shortfall (%q):\n%s", c.wantBad, body)
			}
			// And the gate node in the diagram must agree with the badge.
			if strings.Contains(body, `G{{"gate: PASSED"}}`) {
				t.Errorf("the diagram draws a passed gate that did not pass:\n%s", body)
			}
		})
	}
}

// An approval that cites nothing is the single failure batten exists to make impossible, and the
// PR body is the last place it could still be seen for what it is.
func TestAnApprovalCitingNothingIsCalledOut(t *testing.T) {
	sp, st, run := prFixture(t)
	saveVerdict(t, st, run, "batten", "ok", []string{"go test: PASS"})
	// Written straight to the store: SaveVerdict refuses this, which is the point — if one ever
	// reached the record by another path, the PR must not launder it.
	if err := st.LogDecision(store.Event{RunID: run.RunID, Hook: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "review", Result: "warn", Source: "agent",
		Why: "eyeballed it",
	}, false); err != nil {
		t.Fatal(err)
	}

	body, err := renderPR(sp, st, run)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "none cited") {
		t.Errorf("an evidence-free verdict was rendered without saying so:\n%s", body)
	}
	if strings.Contains(body, "**batten-verified**") {
		t.Errorf("an evidence-free approval must never earn the badge:\n%s", body)
	}
}

// A domain called `api[v2]` would otherwise terminate the Mermaid label and break the diagram.
func TestLabelsWithMermaidSyntaxAreNeutralised(t *testing.T) {
	for _, in := range []string{`api[v2]`, `store"x"`, "a|b", "c{d}", "e<f>"} {
		got := mermaidEscape(in)
		for _, bad := range []string{"[", "]", "{", "}", "|", "<", ">", `"`} {
			if strings.Contains(got, bad) {
				t.Errorf("mermaidEscape(%q) = %q, still contains %q", in, got, bad)
			}
		}
	}
}

func addBothVerdicts(t *testing.T, st *store.Store, run *store.Run) {
	t.Helper()
	saveVerdict(t, st, run, "agent", "ok", []string{"AC-1 covered by TestRateLimit"})
	saveVerdict(t, st, run, "batten", "ok", []string{"go test ./...: PASS (exit 0, 637ms)"})
}

func saveVerdict(t *testing.T, st *store.Store, run *store.Run, source, result string, ev []string) {
	t.Helper()
	if err := st.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "c", Result: result, Source: source,
		Evidence: ev, Why: "because", TS: time.Now().Unix(),
	}, true); err != nil {
		t.Fatal(err)
	}
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return rest
	}
	return rest[:j]
}
