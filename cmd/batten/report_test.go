package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// reportFixture builds a repo with a run that has a fan-out, claimed write-sets, and no
// ingested usage — which is the ordinary state, and the one where a report is most tempted to
// print a zero it never measured.
func reportFixture(t *testing.T) (dir string, st *store.Store, run *store.Run) {
	t.Helper()
	dir = writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    gate: qa\n    requires_verdict: ok\n"+
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n")

	db := filepath.Join(t.TempDir(), "batten.db")
	t.Setenv("BATTEN_DB", db)
	var err error
	st, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	run, err = st.EnsureRun("p", "TASK-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []store.Node{
		{NodeID: "p-build", RunID: run.RunID, Kind: "phase", Label: "build", Status: "ok"},
		{NodeID: "n-a", RunID: run.RunID, Kind: "subagent", Label: "general-purpose",
			AgentID: "api-agent", Domain: "api", Status: "ok"},
	} {
		if err := st.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ClaimWriteSet(run.RunID, "n-a", []string{"server/a.go", "server/b.go"}); err != nil {
		t.Fatal(err)
	}
	return dir, st, run
}

// TestReportNeverPrintsAZeroItDidNotMeasure is the principle that governs the whole command.
//
// `batten report` is the most visible surface batten has, and the easiest place to lose the
// argument: a run whose transcript nobody ingested has NOT spent zero tokens. It is unmeasured,
// and those are opposite facts that call for opposite responses.
func TestReportNeverPrintsAZeroItDidNotMeasure(t *testing.T) {
	dir, _, _ := reportFixture(t)

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport(nil); err != nil {
				t.Fatal(err)
			}
		})
	})

	if !strings.Contains(out, "NOT MEASURED") {
		t.Errorf("no transcript was ingested, so the run's usage must be reported as unmeasured:\n%s", out)
	}
	for _, forbidden := range []string{"0 tokens", "$0.00"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the report printed %q for a run nobody measured:\n%s", forbidden, out)
		}
	}
	// Same rule one level down: a write-set nobody claimed is "not recorded", never "0 files".
	if strings.Contains(out, "0 files") {
		t.Errorf("an unrecorded write-set was reported as zero files:\n%s", out)
	}
}

// The impact block is the one people screenshot, so it has to be counted rather than estimated —
// and it has to be able to report nothing at all.
func TestReportCountsWhatBattenActuallyStopped(t *testing.T) {
	dir, st, run := reportFixture(t)

	// Two denials from two different rules, plus one advisory. Recorded the way the hook does.
	for _, e := range []store.Event{
		{RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionDeny,
			Rule: store.RuleVerdictGate, Reason: "no verdict envelope"},
		{RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionDeny,
			Rule: store.RuleBudget, Reason: "over budget"},
		{RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionAdvise,
			Rule: store.RuleVerdictGate, Reason: "this commit is NOT gated"},
	} {
		if err := st.LogDecision(e); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport(nil); err != nil {
				t.Fatal(err)
			}
		})
	})

	for _, want := range []string{
		"1 commit(s) denied",
		"1 run(s) stopped on budget",
		"1 warning(s) issued",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the impact block is missing %q:\n%s", want, out)
		}
	}
	// Never money. Nobody knows what the retry that did not happen would have cost, and a
	// number invented here takes down the credibility of the ones that are real.
	for _, forbidden := range []string{"saved", "would have cost you", "~$"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the report estimated a saving it cannot know (%q):\n%s", forbidden, out)
		}
	}
}

// TestReportSaysSinceWhenItHasBeenCounting.
//
// Without this line "3 commits denied" reads as a lifetime total, when batten may have started
// counting on Tuesday — the events table only grew a decision column recently, so every
// installation has a horizon before which it recorded nothing.
func TestReportSaysSinceWhenItHasBeenCounting(t *testing.T) {
	dir, st, run := reportFixture(t)
	if err := st.LogDecision(store.Event{
		RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionDeny,
		Rule: store.RuleVerdictGate, Reason: "no verdict",
	}); err != nil {
		t.Fatal(err)
	}

	// A window far wider than the history: the horizon is inside it, so it must be disclosed.
	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport([]string{"--since", "30d"}); err != nil {
				t.Fatal(err)
			}
		})
	})
	if !strings.Contains(out, "counting since") {
		t.Errorf("the report covers 30 days but only has hours of history, and did not say so:\n%s", out)
	}
}

// A report with nothing to report is a real answer, not an empty one. It must not read as a
// failure, and it must not go looking for something impressive to say.
func TestAQuietWindowIsReportedAsAResult(t *testing.T) {
	dir, st, run := reportFixture(t)
	// batten was running and had no objection to anything.
	if err := st.LogDecision(store.Event{
		RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionAllow,
	}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport(nil); err != nil {
				t.Fatal(err)
			}
		})
	})
	if !strings.Contains(out, "nothing was stopped") {
		t.Errorf("a window in which batten stopped nothing must say so plainly:\n%s", out)
	}
}

// --share emits a block to paste, which is the alternative to network telemetry: batten's job
// is to audit, and a tool that audits and phones home has lost the argument before it starts.
func TestShareEmitsPasteableMarkdownAndNothingLeavesTheMachine(t *testing.T) {
	dir, _, _ := reportFixture(t)

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport([]string{"--share"}); err != nil {
				t.Fatal(err)
			}
		})
	})
	if !strings.HasPrefix(strings.TrimSpace(out), "**batten**") {
		t.Errorf("--share must open with a markdown header:\n%s", out)
	}
	if strings.Count(out, "```") != 2 {
		t.Errorf("--share must fence the report so it survives a paste:\n%s", out)
	}
	if !strings.Contains(out, "nothing left this machine") {
		t.Errorf("--share must state that it is local; that claim is the point of the flag:\n%s", out)
	}
}

func TestParseSinceAcceptsTheSpellingPeopleReachFor(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"168h", 168 * time.Hour},
		{"0.5d", 12 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseSince(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseSince(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if _, err := parseSince("last tuesday"); err == nil {
		t.Error("an unparseable duration must be an error, not a silent default window")
	}
}

// A run whose usage IS measured must show it per subagent. This is the half of the ledger no
// other tool in the ecosystem can print, because nothing else records the write-set claims.
func TestReportShowsPerSubagentSpendWhenItWasMeasured(t *testing.T) {
	dir, st, run := reportFixture(t)
	if _, _, err := st.RecordUsageFenced([]store.Usage{{
		RequestID: "r1", RunID: run.RunID, NodeID: "n-a", AgentID: "api-agent",
		Model: "claude-opus-4-5-20251101", TS: time.Now().Unix(),
		InputTokens: 400_000, OutputTokens: 20_000, ImputedUSD: 3.5,
	}}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport(nil); err != nil {
				t.Fatal(err)
			}
		})
	})
	// Labelled by DOMAIN, not by agent type: five subagents all called "general-purpose" tell
	// the reader nothing about which is which.
	if !strings.Contains(out, "api ") {
		t.Errorf("the subagent line must be labelled by its domain:\n%s", out)
	}
	if !strings.Contains(out, "2 files") {
		t.Errorf("the write-set it claimed is missing:\n%s", out)
	}
	if !strings.Contains(out, "420.0k tokens") {
		t.Errorf("measured per-node spend is missing:\n%s", out)
	}
}

func TestReportRejectsAnUnknownFlagInsteadOfIgnoringIt(t *testing.T) {
	dir, _, _ := reportFixture(t)
	inDir(t, dir, func() {
		if err := cmdReport([]string{"--last-week"}); err == nil {
			t.Error("an unknown flag must be an error: silently reporting a different window " +
				"than the one asked for is worse than refusing")
		}
	})
	_ = os.Getenv("BATTEN_DB")
}

// TestReportSaysWhatGotThroughWhileTheGatesWereOnlyWarning.
//
// `enforcement: report` is batten's honest off-switch — gates warn instead of blocking — and it
// is what `init` writes by default, so this is the state most adopters are actually in, not an
// edge case. A kill switch is only worth having if you can find out what happened while it was
// off, and that is the half batten was missing: the events all looked alike, so "we ran in report
// mode for three weeks" had no record of what it cost.
func TestReportSaysWhatGotThroughWhileTheGatesWereOnlyWarning(t *testing.T) {
	dir, st, run := reportFixture(t)

	for _, e := range []store.Event{
		{RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionAdvise,
			Rule: store.RuleVerdictGate, Reason: "no verdict", Enforcement: "report"},
		{RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionAdvise,
			Rule: store.RuleWriteSet, Reason: "collision", Enforcement: "report"},
		{RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionAllow, Enforcement: "report"},
	} {
		if err := st.LogDecision(e); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport(nil); err != nil {
				t.Fatal(err)
			}
		})
	})

	if !strings.Contains(out, "WARNINGS ONLY") {
		t.Errorf("two gate decisions were taken in report mode and the report did not say they "+
			"went through unblocked:\n%s", out)
	}
	if !strings.Contains(out, "2 of these") {
		t.Errorf("the count of unblocked decisions is wrong or missing:\n%s", out)
	}
	// The allowed call is not one of them: batten had no objection to it in the first place.
	if strings.Contains(out, "3 of these") {
		t.Errorf("a call batten never objected to was counted as something that got through:\n%s", out)
	}
}

// And when the gates were actually enforcing, the warning must not appear — a report that always
// carries a caveat teaches the reader to skip it.
func TestNoUnblockedWarningWhenEnforcing(t *testing.T) {
	dir, st, run := reportFixture(t)
	if err := st.LogDecision(store.Event{
		RunID: run.RunID, Hook: "PreToolUse", Decision: store.DecisionDeny,
		Rule: store.RuleVerdictGate, Reason: "no verdict", Enforcement: "enforce",
	}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdReport(nil); err != nil {
				t.Fatal(err)
			}
		})
	})
	if strings.Contains(out, "WARNINGS ONLY") {
		t.Errorf("the gate blocked this one; nothing got through:\n%s", out)
	}
}
