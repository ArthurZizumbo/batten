package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/hooks"
	"github.com/ArthurZizumbo/batten/internal/store"
)

const nightSpec = "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
	"enforcement: enforce\nbudget:\n  max_iterations: 3\n" +
	"phases:\n  - id: build\n  - id: fix\n  - id: close\n    requires_verdict: ok\n"

func nightFixture(t *testing.T) (dir, db string) {
	t.Helper()
	dir = writeSpec(t, nightSpec)
	db = filepath.Join(t.TempDir(), "batten.db")
	t.Setenv("BATTEN_DB", db)
	_ = captureStdout(t, func() { inDir(t, dir, func() { _ = cmdPhase([]string{"TASK-1", "build"}) }) })
	_ = captureStdout(t, func() { inDir(t, dir, func() { _ = cmdUnattended([]string{"TASK-1"}) }) })
	return dir, db
}

// TestRuleTwoCountsTheIterationsAndThenRefuses.
//
// `budget.max_iterations` was the worst entry on the declared-as-future list: declared in the
// spec, returned over MCP, DRAWN in the TUI as `iters %d/%d` — and runs.iterations was 0 forever,
// because nothing anywhere incremented it. The only brake an unsupervised loop had against
// spending the whole window was a sentence in a markdown file.
func TestRuleTwoCountsTheIterationsAndThenRefuses(t *testing.T) {
	dir, db := nightFixture(t)

	for i := 1; i <= 3; i++ {
		var err error
		out := captureStdout(t, func() {
			inDir(t, dir, func() { err = cmdIterate([]string{"TASK-1"}) })
		})
		if err != nil {
			t.Fatalf("iteration %d of a ceiling of 3 was refused: %v", i, err)
		}
		if !strings.Contains(out, "iteration "+strconv.Itoa(i)) {
			t.Errorf("round %d does not report its number:\n%s", i, out)
		}
	}

	// The counter is real, not a print statement.
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.LatestRun("p", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Iterations != 3 {
		t.Fatalf("runs.iterations = %d after three rounds; the TUI has been drawing this number "+
			"all along and it was always 0", run.Iterations)
	}
	st.Close()

	// The fourth is refused, with a code a loop can branch on.
	err = nil
	inDir(t, dir, func() { err = cmdIterate([]string{"TASK-1"}) })
	if err == nil {
		t.Fatal("the ceiling was passed; a loop that failed the same check three times just got " +
			"a fourth go at the window")
	}
	if !strings.Contains(err.Error(), hooks.CodeIterationCeiling) {
		t.Errorf("the refusal has no machine-readable code:\n%v", err)
	}

	// And the phase cannot be advanced either, which is what catches the loop that never called
	// `batten iterate` in the first place.
	err = nil
	_ = captureStdout(t, func() {
		inDir(t, dir, func() { err = cmdPhase([]string{"TASK-1", "fix"}) })
	})
	if err == nil {
		t.Error("a run past its iteration ceiling advanced a phase anyway")
	}
}

// TestRuleThreeRefusesAnOverrideWhileNobodyIsWatching. `--reason` exists because an override has
// to cost a sentence a HUMAN wrote; a sentence the loop wrote satisfies the letter of the rule and
// none of its purpose.
func TestRuleThreeRefusesAnOverrideWhileNobodyIsWatching(t *testing.T) {
	dir, _ := nightFixture(t)

	var err error
	_ = captureStdout(t, func() {
		inDir(t, dir, func() {
			err = cmdOverride([]string{"TASK-1", "--reason", "the check is flaky, I am sure it is fine"})
		})
	})
	if err == nil {
		t.Fatal("an unattended run overrode its own gate")
	}
	if !strings.Contains(err.Error(), hooks.CodeUnattendedOverride) {
		t.Errorf("the refusal has no machine-readable code:\n%v", err)
	}
	// No `fix`: the way out is a human, and printing `--off` to the loop would be handing it the
	// key to its own fence.
	if strings.Contains(err.Error(), "batten.fix:") {
		t.Errorf("the refusal offers the loop a way through:\n%v", err)
	}

	// Control: the human turns the mode off in the morning, and the override works as always.
	_ = captureStdout(t, func() {
		inDir(t, dir, func() { _ = cmdUnattended([]string{"TASK-1", "--off"}) })
	})
	err = nil
	_ = captureStdout(t, func() {
		inDir(t, dir, func() {
			err = cmdOverride([]string{"TASK-1", "--reason", "shipping the hotfix, I own this"})
		})
	})
	if err != nil {
		t.Errorf("a supervised override was refused: %v", err)
	}
}

// A spec with no ceiling declared must say so rather than invent one. "No max_iterations" is not
// "max_iterations: 3" — the person starting an unsupervised run should learn that now.
func TestNoDeclaredCeilingIsSaidOutLoud(t *testing.T) {
	dir := writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n")
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))
	_ = captureStdout(t, func() { inDir(t, dir, func() { _ = cmdPhase([]string{"TASK-2", "build"}) }) })

	out := captureStdout(t, func() {
		inDir(t, dir, func() { _ = cmdUnattended([]string{"TASK-2"}) })
	})
	if !strings.Contains(out, "NOT set") {
		t.Errorf("a run with no declared ceiling was not told so:\n%s", out)
	}

	var err error
	out = captureStdout(t, func() {
		inDir(t, dir, func() { err = cmdIterate([]string{"TASK-2"}) })
	})
	if err != nil {
		t.Fatalf("iterate refused with no ceiling declared: %v", err)
	}
	if !strings.Contains(out, "no budget.max_iterations") {
		t.Errorf("the count does not say there is nothing stopping it:\n%s", out)
	}
}
