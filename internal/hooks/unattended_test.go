package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/store"
)

func bashInputFor(cmd, cwd string) Input {
	ti, _ := json.Marshal(map[string]string{"command": cmd})
	return Input{
		HookEventName: "PreToolUse", ToolName: "Bash",
		ToolInput: ti, SessionID: "sess-a", CWD: cwd,
	}
}

// unattendedFixture is guardFixture with an open run already in unattended mode.
func unattendedFixture(t *testing.T) (*Handler, string, *store.Run) {
	t.Helper()
	h, root := guardFixture(t)
	r, err := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.SetMode(r.RunID, store.ModeUnattended); err != nil {
		t.Fatal(err)
	}
	return h, root, r
}

// TestRuleOneRefusesEveryWayToDestroyWork.
//
// This was 112 lines of markdown asking the model not to delete anything, guarding the one command
// where the mistake is unrecoverable and nobody is awake to catch it. The asymmetry in the rule's
// own words is the argument for the mechanism: a stale file costs somebody ten seconds tomorrow;
// deleting the wrong one costs work nobody gets back.
func TestRuleOneRefusesEveryWayToDestroyWork(t *testing.T) {
	h, root, _ := unattendedFixture(t)

	for _, cmd := range []string{
		"rm -rf build/",
		"rm old.go",
		"sudo rm -rf /tmp/x",
		"rmdir empty",
		"git reset --hard HEAD~3",
		"git -C . reset --hard origin/main",
		"git checkout -- src/app.go",
		"git restore src/app.go",
		"git clean -fdx",
		"git branch -D feature/old",
		"git push --force origin main",
		"npm test && rm -rf node_modules",
		"cd tmp; del notes.txt",
		"truncate -s 0 log.txt",
	} {
		out := h.destructionGuard(bashInputFor(cmd, root), cmd)
		if decision(out) != "deny" {
			t.Errorf("%q was not refused during an unattended run (got %q)", cmd, decision(out))
			continue
		}
		if !strings.Contains(reasonOf(out), CodeUnattendedDelete) {
			t.Errorf("%q was refused without the machine-readable code, so a loop has to parse "+
				"English at 3am:\n%s", cmd, reasonOf(out))
		}
	}
}

// The other half, and it is the half that decides whether the rule is usable. A loop that cannot
// run its own test suite is not an unattended loop, so the matcher has to be anchored to command
// positions rather than looking for substrings.
func TestRuleOneDoesNotRefuseOrdinaryWork(t *testing.T) {
	h, root, _ := unattendedFixture(t)

	for _, cmd := range []string{
		"go test ./...",
		"go build ./... && go vet ./...",
		"grep -rn rm .",               // the word appears; nothing is deleted
		"echo 'rm -rf /' >> notes.md", // quoted, and an APPEND besides
		"git status --porcelain",
		"git checkout -b feature/new",   // creates a branch, destroys nothing
		"git restore-me --help",         // not `git restore`
		"npm run build 2>&1",            // a redirect that is not a truncation
		"go test ./... > fresh-out.txt", // a NEW file: nothing is destroyed
		"cat a.txt | head -5",
	} {
		if out := h.destructionGuard(bashInputFor(cmd, root), cmd); out != nil {
			t.Errorf("%q was refused, and an unattended loop that cannot do this cannot run:\n%s",
				cmd, reasonOf(out))
		}
	}

	// The one truncation case that IS refused: onto a file that already exists.
	existing := filepath.Join(root, "already.txt")
	if err := os.WriteFile(existing, []byte("work somebody did\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := "go test ./... > already.txt"
	if got := decision(h.destructionGuard(bashInputFor(cmd, root), cmd)); got != "deny" {
		t.Errorf("truncating an EXISTING file is exactly what rule 1 names, and it was %q", got)
	}
}

// TestTheRulesOnlyApplyWhileNobodyIsWatching is the control for all four. A supervised run has a
// human in it, and a tool that refuses `rm` during ordinary work gets uninstalled by lunchtime.
func TestTheRulesOnlyApplyWhileNobodyIsWatching(t *testing.T) {
	h, root, r := unattendedFixture(t)

	cmd := "rm -rf build/"
	if got := decision(h.destructionGuard(bashInputFor(cmd, root), cmd)); got != "deny" {
		t.Fatalf("fixture is wrong: the run is unattended and %q was %q", cmd, got)
	}
	// A human turns the mode off in the morning. Everything goes back to normal.
	if err := h.Store.SetMode(r.RunID, ""); err != nil {
		t.Fatal(err)
	}
	if out := h.destructionGuard(bashInputFor(cmd, root), cmd); out != nil {
		t.Errorf("a supervised run was refused a delete:\n%s", reasonOf(out))
	}
}

// TestRuleFourRefusesACommitEvenWithBothVerdicts is the rule that is easiest to get subtly wrong.
//
// The commit gate already denies a commit with no verdict. Rule 4 is a different statement: even a
// unit that passed everything does not get committed at 3am, because the point is not that the
// work is unverified — it is that a human closes. A version of this that only fired when the
// verdicts were missing would look identical in most tests and enforce nothing.
func TestRuleFourRefusesACommitEvenWithBothVerdicts(t *testing.T) {
	h, _, r := unattendedFixture(t)

	// Both halves of the gate, from two producers, exactly as a satisfied gate has them.
	for _, v := range []store.Verdict{
		{RunID: r.RunID, Gate: "qa", CheckID: "checks", Result: "ok", Source: "batten",
			Evidence: []string{"go test ./...: ok"}},
		{RunID: r.RunID, Gate: "qa", CheckID: "criteria", Result: "ok", Source: "agent",
			Evidence: []string{"AC-1 covered by api/handler_test.go"}},
	} {
		if err := h.Store.SaveVerdict(v, true); err != nil {
			t.Fatal(err)
		}
	}

	in := bashInputFor(`git commit -m "TASK-1: done"`, "")
	out, err := h.verdictGate(in, `git commit -m "TASK-1: done"`)
	if err != nil {
		t.Fatal(err)
	}
	if decision(out) != "deny" {
		t.Fatalf("an unattended run committed its own work (got %q)", decision(out))
	}
	if !strings.Contains(reasonOf(out), CodeUnattendedCommit) {
		t.Errorf("the refusal is not the unattended one — it may just be the ordinary verdict "+
			"gate, which would mean rule 4 does nothing for a unit that passed:\n%s", reasonOf(out))
	}
	// And no `fix`. Every other denial hands over the way out; this one must not, because the way
	// out is turning the mode off, and printing that to a loop nobody is watching is an
	// instruction to take its own fence down.
	if strings.Contains(reasonOf(out), "batten.fix:") {
		t.Errorf("the refusal offers a way through:\n%s", reasonOf(out))
	}
	// Filed under the right cause. It reached `batten report` as a verdict-gate denial — counted
	// beside "no verdict, empty evidence, or checks not run", none of which describes it.
	if out.Rule != store.RuleUnattended {
		t.Errorf("rule 4's denial is filed as %q, so the report attributes it to the wrong "+
			"cause", out.Rule)
	}

	// Control: with a human present, this refusal is gone. (The fixture's closing phase names no
	// gate, so the ordinary gate still has its own say about a gate with no checks — that is a
	// different complaint, and the point here is that rule 4's is no longer among them.)
	if err := h.Store.SetMode(r.RunID, ""); err != nil {
		t.Fatal(err)
	}
	out, _ = h.verdictGate(in, `git commit -m "TASK-1: done"`)
	if decision(out) == "deny" {
		t.Errorf("a supervised commit with both verdicts was denied:\n%s", reasonOf(out))
	}
	if strings.Contains(reasonOf(out), CodeUnattendedCommit) {
		t.Errorf("rule 4 fired on a supervised run:\n%s", reasonOf(out))
	}
}

// IterationCeiling is rule 2's arithmetic, and the reason budget.max_iterations stopped being the
// worst entry on the declared-as-future list.
func TestIterationCeiling(t *testing.T) {
	cases := []struct {
		used, max int
		reached   bool
		why       string
	}{
		{0, 3, false, "a run that has not looped yet is not at its ceiling"},
		{2, 3, false, "one round left"},
		{3, 3, true, "the ceiling is reached AT the ceiling, not past it"},
		{4, 3, true, "and stays reached"},
		{9, 0, false, "no ceiling declared is no ceiling — not a ceiling of zero"},
		{0, -1, false, "a negative ceiling is not a ceiling either"},
	}
	for _, c := range cases {
		got, _, _ := IterationCeiling(&store.Run{Iterations: c.used}, c.max)
		if got != c.reached {
			t.Errorf("IterationCeiling(used=%d, max=%d) = %v, want %v — %s",
				c.used, c.max, got, c.reached, c.why)
		}
	}
	if got, _, _ := IterationCeiling(nil, 3); got {
		t.Error("a nil run reported as over its ceiling")
	}
}
