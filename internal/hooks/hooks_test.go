package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// gateFixture builds a handler over a spec whose close phase demands a verdict on gate "qa",
// with exactly the checks given. checks == nil is the case that matters: a repo that has not
// declared what "passing" means yet.
func gateFixture(t *testing.T, checks []string, enforcement string) (*Handler, *store.Run) {
	t.Helper()
	dir := t.TempDir()

	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"
	if enforcement != "" {
		y += "enforcement: " + enforcement + "\n"
	}
	y += "phases:\n  - id: verify\n    gate: qa\n  - id: close\n    gate: qa\n    requires_verdict: ok\ngates:\n  qa:\n    verdict: required\n    evidence: required\n"
	if len(checks) > 0 {
		y += "    checks: ['" + strings.Join(checks, "', '") + "']\n"
	}
	if err := writeFile(filepath.Join(dir, "batten.yaml"), y); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v\n%s", err, y)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	run, err := st.EnsureRun("p", "TASK-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{Spec: sp, Store: st}, run
}

func commitInput() Input {
	ti, _ := json.Marshal(bashInput{Command: `git commit -m "feat: x"`})
	return Input{HookEventName: "PreToolUse", ToolName: "Bash", ToolInput: ti, SessionID: "sess-1"}
}

func decision(o *Output) string {
	if o == nil {
		return "silent-allow"
	}
	if o.HookSpecific != nil && o.HookSpecific.PermissionDecision == "deny" {
		return "deny"
	}
	if o.SystemMessage != "" {
		return "warn"
	}
	return "silent-allow"
}

// TestGateWithNoChecksWarnsInsteadOfPassingSilently is the hole this test exists to keep shut.
//
// The batten-verified pass is demanded only when the gate DECLARES checks. A repo that has not
// written its checks yet — no Makefile, no package.json, a spec fresh out of `batten init` —
// therefore skips that demand entirely, and the commit lands on the agent's own claim that
// everything is fine. That is precisely the failure the gate exists to kill.
//
// Blocking would be wrong: refusing every commit in a repo that has not declared its checks would
// get batten uninstalled on day one. So it passes. But principle #3 is "fail open only with a
// warning", and a gate that silently degrades to trusting the model is worse than no gate,
// because it will be believed.
func TestGateWithNoChecksWarnsInsteadOfPassingSilently(t *testing.T) {
	h, run := gateFixture(t, nil, "enforce")

	// The agent approves its own work, with evidence it simply asserted.
	if err := h.Store.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"tests pass"}, Source: "agent",
	}, true); err != nil {
		t.Fatal(err)
	}

	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "warn" {
		t.Fatalf("an unverifiable gate must warn, got %q (output %+v)", got, out)
	}
	msg := out.SystemMessage + " " + out.HookSpecific.PermissionDecisionReason
	if !strings.Contains(msg, "no checks") {
		t.Errorf("the warning must name the cause; got %q", msg)
	}
	if !strings.Contains(msg, "agent's word") {
		t.Errorf("the warning must say what the approval actually rests on; got %q", msg)
	}
}

// With checks declared, the agent's word is explicitly not enough: batten demands a verdict it
// produced itself by running them. This is the other half of the contract, and it must keep
// DENYING rather than warning.
func TestGateWithChecksStillDemandsABattenVerifiedPass(t *testing.T) {
	h, run := gateFixture(t, []string{"go test ./..."}, "enforce")

	if err := h.Store.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"tests pass, I promise"}, Source: "agent",
	}, true); err != nil {
		t.Fatal(err)
	}

	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("an agent-only verdict against a gate with checks must DENY, got %q", got)
	}
	if !strings.Contains(out.HookSpecific.PermissionDecisionReason, "batten check") {
		t.Errorf("the denial must tell the agent what to run; got %q",
			out.HookSpecific.PermissionDecisionReason)
	}

	// Once batten itself has run them, the same commit goes through.
	if err := h.Store.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"go test ./...: PASS (exit 0)"}, Source: "batten",
	}, true); err != nil {
		t.Fatal(err)
	}
	out, err = h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Fatalf("a batten-verified pass must allow, got %q (output %+v)", got, out)
	}
}

// No verdict at all is a denial regardless of what the gate declares — the base case.
func TestNoVerdictDenies(t *testing.T) {
	h, _ := gateFixture(t, nil, "enforce")
	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("a commit with no verdict must be denied, got %q", got)
	}
}

// budgetFixture is gateFixture plus a declared token ceiling and `on_exceed: block`.
// checks == nil is the case the field test caught: the advisory for an undeclared gate
// used to return before the budget was ever consulted.
func budgetFixture(t *testing.T, checks []string, tokensPerRun int64) (*Handler, *store.Run) {
	t.Helper()
	dir := t.TempDir()

	y := "version: 1\nproject: p\nenforcement: enforce\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: verify\n    gate: qa\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n"
	if len(checks) > 0 {
		y += "    checks: ['" + strings.Join(checks, "', '") + "']\n"
	}
	y += fmt.Sprintf("budget:\n  tokens_per_run: %d\n  on_exceed: block\n", tokensPerRun)

	if err := writeFile(filepath.Join(dir, "batten.yaml"), y); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v\n%s", err, y)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	run, err := st.EnsureRun("p", "TASK-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{Spec: sp, Store: st}, run
}

// burn puts tokens on the run's ledger, stamped after started_at so the fence keeps them.
func burn(t *testing.T, h *Handler, run *store.Run, tokens int64) {
	t.Helper()
	added, err := h.Store.RecordUsage([]store.Usage{{
		RequestID: fmt.Sprintf("req-%d", tokens), RunID: run.RunID,
		Model: "claude-opus-4-5", TS: run.StartedAt + 1,
		InputTokens: tokens, ImputedUSD: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("seed was silently fenced out: added=%d (run started_at=%d)", added, run.StartedAt)
	}
}

// TestBudgetBlocksEvenWhenTheGateDeclaresNoChecks is the regression this file exists to hold.
//
// The advisory for a gate that declares no checks is correct and must stay. What was wrong is
// that it RETURNED, so everything after it — including `on_exceed: block` — stopped running.
// A repo that had not declared its checks yet quietly lost its budget ceiling too, and the two
// have nothing to do with each other. An advisory must never outrank a denial.
func TestBudgetBlocksEvenWhenTheGateDeclaresNoChecks(t *testing.T) {
	h, run := budgetFixture(t, nil, 1000)
	if err := h.Store.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"tests pass"}, Source: "agent",
	}, true); err != nil {
		t.Fatal(err)
	}

	// Under the ceiling: the undeclared gate is still worth a warning, and nothing denies.
	burn(t, h, run, 100)
	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "warn" {
		t.Fatalf("under budget with an undeclared gate must still warn, got %q", got)
	}

	// Over the ceiling: the budget denies, and the advisory does not shield it.
	burn(t, h, run, 5000)
	out, err = h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("on_exceed=block must deny an over-budget commit even when the gate "+
			"declares no checks, got %q (output %+v)", got, out)
	}
	if !strings.Contains(out.HookSpecific.PermissionDecisionReason, "over budget") {
		t.Errorf("the denial must name the budget as the cause; got %q",
			out.HookSpecific.PermissionDecisionReason)
	}
}

// The same over-budget run with checks declared already denied before the fix. Keeping both
// halves asserted is what proves the two conditions are independent.
func TestBudgetBlocksWhenTheGateDeclaresChecks(t *testing.T) {
	h, run := budgetFixture(t, []string{"go test ./..."}, 1000)
	for _, src := range []string{"agent", "batten"} {
		if err := h.Store.SaveVerdict(store.Verdict{
			RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
			Evidence: []string{"go test ./...: PASS (exit 0)"}, Source: src,
		}, true); err != nil {
			t.Fatal(err)
		}
	}
	burn(t, h, run, 5000)

	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("on_exceed=block must deny an over-budget commit, got %q", got)
	}
}

// TestCommitReCatchesTheSpellingsAnAgentActuallyUses pins the gate's entry condition.
//
// `git -c user.email=… commit` is the standard non-interactive commit a CI job or an agent
// issues, and it used to walk through the gate untouched: the option's value is a separate
// token, so the pattern stopped looking before it reached the subcommand. The same regex
// closes the run in postToolUse, so a miss both opened the gate and stranded the run holding
// its write-set claims.
func TestCommitReCatchesTheSpellingsAnAgentActuallyUses(t *testing.T) {
	commits := []string{
		`git commit -m "feat: x"`,
		`git -C . commit -m x`,
		`git -c user.email=a@b.c commit -m x`,
		`git -c user.name=Bot -c user.email=b@o.t commit -m x`,
		`git.exe commit -m x`,
		`git --git-dir=.git commit -m x`,
		`git --work-tree /w --git-dir /w/.git commit`,
		`git -C /path/to/repo -c user.name=CI commit -am wip`,
		`cd repo && git commit -m x`,
		`git -c core.hooksPath=/dev/null commit --no-verify`,
	}
	for _, c := range commits {
		if !commitRe.MatchString(c) {
			t.Errorf("gate does not see a commit in %q", c)
		}
	}

	// The other half of the contract: a pattern loose enough to catch every spelling would
	// start denying commands that are not commits at all.
	notCommits := []string{
		`git log --oneline`,
		`git status`,
		`git diff --stat`,
		`git rev-parse HEAD`,
		`git config --global user.name commit`,
		`git log --format=commit`,
		`echo mycommit`,
		`gitcommit`,
	}
	for _, c := range notCommits {
		if commitRe.MatchString(c) {
			t.Errorf("gate sees a commit in %q, which is not one", c)
		}
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

// TestAnUngatedCommitSaysSoInsteadOfPassingSilently.
//
// The commit gate did not exist until some batten command had created a run, so the FIRST
// commit after adopting batten went through in silence. That is the worst possible moment for
// silence: the newcomer branches, codes, commits, watches it succeed, and concludes the gate
// is working. It is not — there is nothing to check against.
//
// Not blocking is still right. batten cannot deny what it cannot attribute, and denying every
// commit in a repo that has not opened a run would get it uninstalled the same day. But
// principle #3 is "fail open only OUT LOUD", and this path had no voice at all.
func TestAnUngatedCommitSaysSoInsteadOfPassingSilently(t *testing.T) {
	h, run := gateFixture(t, []string{"go test ./..."}, "enforce")

	// Positive control FIRST: silence from this hook has at least six innocent causes, so a
	// later silence proves nothing unless the gate is shown to deny in the same fixture.
	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("control failed: a bound run with no verdict must DENY, got %q — "+
			"the assertions below would prove nothing", got)
	}

	// Now the case under test, reproduced the way a newcomer meets it: a real repo on a
	// branch that names a unit, and no run ever opened for it. gateFixture's own run is
	// bound to sess-1 and to a different unit, so it cannot resolve this session.
	if err := h.Store.CloseRun(run.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	gitBranchAt(t, h.Spec.Root, "feature/TASK-002-add-export")

	in := commitInput()
	in.SessionID = "sess-unbound"
	in.CWD = h.Spec.Root

	out, err = h.verdictGate(in, `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got == "deny" {
		t.Fatalf("an unattributable commit must not be denied — batten cannot deny what it "+
			"cannot attribute; got %q", got)
	}
	if out == nil {
		t.Fatal("an ungated commit passed in complete silence, which reads to the user as " +
			"approval by a gate that never ran")
	}
	msg := out.SystemMessage + " " + out.HookSpecific.AdditionalContext
	for _, want := range []string{"NOT gated", "TASK-002", "batten phase"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the warning must say the commit is not gated, name the unit the branch "+
				"points at, and say how to start governing it. Missing %q from:\n%s", want, msg)
		}
	}
}

// gitBranchAt makes dir a real repo sitting on branch, because activeUnit resolves the unit
// from the branch name by shelling out to git. Faking that would test the fake.
func gitBranchAt(t *testing.T, dir, branch string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "t@t.io"},
		{"config", "user.name", "tester"},
		{"commit", "-q", "--allow-empty", "-m", "base"},
		{"checkout", "-q", "-b", branch},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable in this environment (%v): %s", err, out)
		}
	}
}

// The other unattributable shape: several units open and a session bound to none of them.
// batten cannot tell which unit is being committed, so it cannot deny — but it must say that
// the commit is ungated and name the units it is choosing between.
func TestAnAmbiguousCommitNamesTheUnitsItCannotChooseBetween(t *testing.T) {
	h, _ := gateFixture(t, []string{"go test ./..."}, "enforce")
	for _, u := range []string{"TASK-2", "TASK-3"} {
		if _, err := h.Store.EnsureRun("p", u, ""); err != nil {
			t.Fatal(err)
		}
	}

	in := commitInput()
	in.SessionID = "sess-unbound"
	in.CWD = h.Spec.Root

	out, err := h.verdictGate(in, `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("with several units open and none bound, the commit is ungated and silent")
	}
	msg := out.SystemMessage + " " + out.HookSpecific.AdditionalContext
	if !strings.Contains(msg, "NOT gated") {
		t.Errorf("the warning must say the commit is not gated; got:\n%s", msg)
	}
	for _, u := range []string{"TASK-2", "TASK-3"} {
		if !strings.Contains(msg, u) {
			t.Errorf("the warning must name the competing unit %s; got:\n%s", u, msg)
		}
	}
}

// TestTheVeryFirstCommitAfterAdoptingIsNotSilent.
//
// Found by rebuilding the proyecto_ui replica (docs/field-test/REPLICA-UI.md, test 5) — the one
// shape batten had never been exercised against: a repo that is planned but not yet built, with
// no code, no build files and no git history at all.
//
// It is also the most likely first interaction anyone has with batten. They install it, they
// commit something, and there is no run open, the message names no unit, and no branch names one
// either. This returned nothing at all, on the argument that SessionStart carries it. SessionStart
// does say it — into additionalContext, which reaches the model and not the user's screen, once,
// at a session start that may be two hundred turns before the commit. At the commit itself nobody
// was told anything, and `batten hook` exits 0 with no output for six different reasons, of which
// "allowed" is only one.
func TestTheVeryFirstCommitAfterAdoptingIsNotSilent(t *testing.T) {
	h, run := gateFixture(t, nil, "enforce")
	// gateFixture opens TASK-1 bound to sess-1. Close it: nothing is open, which is the state of
	// a repo that just adopted batten.
	if err := h.Store.CloseRun(run.RunID, "ok"); err != nil {
		t.Fatal(err)
	}

	in := commitInput()
	in.SessionID = "sess-brand-new"
	in.CWD = h.Spec.Root // a temp dir, not a git repo: no branch can name a unit

	out, err := h.verdictGate(in, `git commit -m "primer commit del equipo"`)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("the first commit after adopting batten passed in COMPLETE SILENCE. That is " +
			"indistinguishable from an approval by a gate that never ran, which is the exact " +
			"failure batten exists to remove from other people's workflows")
	}
	if got := decision(out); got != "warn" {
		t.Fatalf("an unattributable commit must warn, never deny — batten cannot deny what it "+
			"cannot attribute, and denying here gets it uninstalled on day one; got %q", got)
	}
	msg := out.SystemMessage + " " + out.HookSpecific.AdditionalContext
	for _, want := range []string{"NOT gated", "batten phase"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the warning must say the commit is not gated and how to start governing it. "+
				"Missing %q from:\n%s", want, msg)
		}
	}
	// And it must be visible to the HUMAN, not only to the model. The whole defect was a true
	// statement delivered somewhere the person committing never looks.
	if out.SystemMessage == "" {
		t.Error("the warning reached additionalContext (the model) but not systemMessage (the " +
			"user) — which is precisely the half-measure this replaces")
	}
}

// The same rule with exactly one unit open that the session does not own. One candidate is not
// less ambiguous than five: batten still cannot say this commit belongs to it. This fell into
// the same silent return as the zero case, because the guard was `len(open) > 1`.
func TestOneOpenUnitTheSessionDoesNotOwnIsStillUngated(t *testing.T) {
	h, run := gateFixture(t, nil, "enforce")
	if err := h.Store.CloseRun(run.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	// Exactly one open run, owned by somebody else's session.
	if _, err := h.Store.EnsureRun("p", "TASK-9", "sess-otra"); err != nil {
		t.Fatal(err)
	}

	in := commitInput()
	in.SessionID = "sess-brand-new"
	in.CWD = h.Spec.Root

	out, err := h.verdictGate(in, `git commit -m "arregla un typo"`)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("one open run owned by another session left the commit ungated AND silent")
	}
	msg := out.SystemMessage + " " + out.HookSpecific.AdditionalContext
	if !strings.Contains(msg, "NOT gated") || !strings.Contains(msg, "TASK-9") {
		t.Errorf("the warning must say the commit is not gated and name the unit it could not "+
			"attribute it to; got:\n%s", msg)
	}
}

// TestBattenCheckAloneDoesNotCloseAUnit.
//
// `batten check` writes its own source='batten' verdict. That row was both the newest verdict
// AND the batten-verified one, so it satisfied both of the gate's conditions by itself — and
// `batten check` on an empty diff, still in the build phase, with nothing having judged the
// acceptance criteria, cleared the way for a commit. The gate was one-sided: an agent verdict
// alone denied, batten's own verdict alone passed.
//
// The two conditions only mean anything if they come from two different producers. The machine
// says the checks ran; a reviewer says the work is right.
func TestBattenCheckAloneDoesNotCloseAUnit(t *testing.T) {
	h, run := gateFixture(t, []string{"go test ./..."}, "enforce")

	// Exactly what `batten check` records, and nothing else.
	if err := h.Store.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"go test ./...: PASS (exit 0)"}, Source: "batten",
	}, true); err != nil {
		t.Fatal(err)
	}

	out, err := h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("batten's own check result must not close a unit by itself — nothing has "+
			"judged the acceptance criteria; got %q", got)
	}
	if !strings.Contains(out.HookSpecific.PermissionDecisionReason, "acceptance criteria") {
		t.Errorf("the denial must say what is actually missing, not repeat 'run batten check' "+
			"at someone who just did; got %q", out.HookSpecific.PermissionDecisionReason)
	}

	// Add the reviewer's verdict and the same commit goes through.
	if err := h.Store.SaveVerdict(store.Verdict{
		RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"AC-1 covered by TestExport", "AC-2 covered by TestExportEmpty"},
		Source:   "agent",
	}, true); err != nil {
		t.Fatal(err)
	}
	out, err = h.verdictGate(commitInput(), `git commit -m "feat: x"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Fatalf("both verdicts present must allow, got %q (output %+v)", got, out)
	}
}

// TestTheCommitMessageDecidesWhichUnitIsGated.
//
// activeUnit resolved the unit from the session binding and the branch name, and the commit
// message — the most direct statement of what this commit is FOR — was never read. So a
// session bound to a verified TASK-1 could land `feat(TASK-2): ...` while TASK-2 had no verdict
// at all: one unit's review credited to another unit's work. Under trunk-based development,
// where the branch names nothing, the message is the only signal there is.
func TestTheCommitMessageDecidesWhichUnitIsGated(t *testing.T) {
	h, run := gateFixture(t, []string{"go test ./..."}, "enforce")

	// TASK-1 is fully verified: both verdicts, so a TASK-1 commit is allowed.
	for _, src := range []string{"batten", "agent"} {
		if err := h.Store.SaveVerdict(store.Verdict{
			RunID: run.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
			Evidence: []string{"go test ./...: PASS (exit 0)"}, Source: src,
		}, true); err != nil {
			t.Fatal(err)
		}
	}
	ti, _ := json.Marshal(bashInput{Command: `git commit -m "feat(TASK-1): the reviewed work"`})
	in := Input{HookEventName: "PreToolUse", ToolName: "Bash", ToolInput: ti, SessionID: "sess-1"}
	out, err := h.verdictGate(in, `git commit -m "feat(TASK-1): the reviewed work"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Fatalf("control failed: a fully verified unit must commit; got %q", got)
	}

	// TASK-2 is open but has no verdict. The same session commits FOR TASK-2.
	if _, err := h.Store.EnsureRun("p", "TASK-2", ""); err != nil {
		t.Fatal(err)
	}
	cmd := `git commit -m "feat(TASK-2): unreviewed work"`
	ti2, _ := json.Marshal(bashInput{Command: cmd})
	in2 := Input{HookEventName: "PreToolUse", ToolName: "Bash", ToolInput: ti2, SessionID: "sess-1"}
	out, err = h.verdictGate(in2, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("a commit FOR an unverified unit must be denied even when the session is bound "+
			"to a verified one; got %q", got)
	}
	if !strings.Contains(out.HookSpecific.PermissionDecisionReason, "TASK-2") {
		t.Errorf("the denial must name the unit the commit is actually for; got %q",
			out.HookSpecific.PermissionDecisionReason)
	}

	// And a message naming a unit with no run at all is refused rather than silently
	// attributed to whatever the session happens to be bound to.
	cmd3 := `git commit -m "feat(TASK-9): a unit nobody opened"`
	ti3, _ := json.Marshal(bashInput{Command: cmd3})
	in3 := Input{HookEventName: "PreToolUse", ToolName: "Bash", ToolInput: ti3, SessionID: "sess-1"}
	out, err = h.verdictGate(in3, cmd3)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("a message naming an unopened unit must not ride on the session's verdict; got %q", got)
	}
}
