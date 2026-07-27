package hooks

import (
	"encoding/json"
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

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}
