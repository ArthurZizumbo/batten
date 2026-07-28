package hooks

import (
	"encoding/json"
	"fmt"
	"os"
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
