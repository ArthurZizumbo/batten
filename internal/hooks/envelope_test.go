package hooks

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// codeOf pulls the machine-readable fields back out the way a loop would.
var codeRe = regexp.MustCompile(`(?m)^batten\.code: (\S+)$`)
var retryRe = regexp.MustCompile(`(?m)^batten\.retry: (true|false)$`)
var fixRe = regexp.MustCompile(`(?m)^batten\.fix: (.+)$`)

func fields(t *testing.T, o *Output) (code, fix string, retry bool) {
	t.Helper()
	if o == nil {
		t.Fatal("no output at all: nothing to read fields out of")
	}
	text := o.HookSpecific.PermissionDecisionReason
	if text == "" {
		text = o.HookSpecific.AdditionalContext
	}
	if m := codeRe.FindStringSubmatch(text); m != nil {
		code = m[1]
	}
	if m := fixRe.FindStringSubmatch(text); m != nil {
		fix = strings.TrimSpace(m[1])
	}
	if m := retryRe.FindStringSubmatch(text); m != nil {
		retry = m[1] == "true"
	}
	return
}

// TestEveryDenialCarriesACodeAndAWayOut.
//
// batten's denials were already good prose — they named the unit, said what was missing, and
// printed a command. But prose is what a model has to INTERPRET, and interpreting it is exactly
// the step that goes wrong at 3am in an unattended run: the loop reads "has no batten-verified
// pass" and has to infer that the remedy is `batten check`. A fair inference, and one it can get
// wrong with nobody awake to catch it.
//
// Idea from gentle-ai's uniform failure envelope (v2.1.6).
func TestEveryDenialCarriesACodeAndAWayOut(t *testing.T) {
	t.Run("no verdict", func(t *testing.T) {
		h, _ := gateFixture(t, nil, "enforce")
		out, err := h.verdictGate(commitInput(), `git commit -m "x"`)
		if err != nil {
			t.Fatal(err)
		}
		code, fix, retry := fields(t, out)
		if code != CodeNoVerdict {
			t.Errorf("code = %q, want %q", code, CodeNoVerdict)
		}
		if fix == "" {
			t.Error("a denial with no way out sends an agent loop hunting")
		}
		// The crucial one. Re-running the same commit changes nothing, and a loop that retries
		// it burns the window on an identical denial.
		if retry {
			t.Error("a missing verdict is NOT fixed by retrying the same tool call")
		}
	})

	t.Run("checks not run", func(t *testing.T) {
		h, run := gateFixture(t, []string{"go test ./..."}, "enforce")
		if err := h.Store.SaveVerdict(store.Verdict{
			RunID: run.RunID, Gate: "qa", CheckID: "r", Result: "ok", Source: "agent",
			Evidence: []string{"judged"},
		}, true); err != nil {
			t.Fatal(err)
		}
		out, err := h.verdictGate(commitInput(), `git commit -m "x"`)
		if err != nil {
			t.Fatal(err)
		}
		code, fix, _ := fields(t, out)
		if code != CodeChecksNotRun {
			t.Errorf("code = %q, want %q", code, CodeChecksNotRun)
		}
		if !strings.Contains(fix, "batten check") {
			t.Errorf("the fix must be the command that produces what is missing; got %q", fix)
		}
	})

	t.Run("over budget", func(t *testing.T) {
		h, run := budgetFixture(t, nil, 1000)
		if err := h.Store.SaveVerdict(store.Verdict{
			RunID: run.RunID, Gate: "qa", CheckID: "r", Result: "ok", Source: "agent",
			Evidence: []string{"judged"},
		}, true); err != nil {
			t.Fatal(err)
		}
		burn(t, h, run, 5000)
		out, err := h.verdictGate(commitInput(), `git commit -m "x"`)
		if err != nil {
			t.Fatal(err)
		}
		code, _, retry := fields(t, out)
		if code != CodeOverBudget {
			t.Errorf("code = %q, want %q", code, CodeOverBudget)
		}
		if retry {
			t.Error("spending less does not happen by retrying; the ceiling exists precisely to " +
				"stop a loop from grinding on")
		}
	})

	t.Run("ungated commit", func(t *testing.T) {
		h, run := gateFixture(t, nil, "enforce")
		if err := h.Store.CloseRun(run.RunID, "ok"); err != nil {
			t.Fatal(err)
		}
		in := commitInput()
		in.SessionID = "fresh"
		in.CWD = h.Spec.Root
		out, err := h.verdictGate(in, `git commit -m "wip"`)
		if err != nil {
			t.Fatal(err)
		}
		code, fix, _ := fields(t, out)
		if code != CodeUnattributed {
			t.Errorf("code = %q, want %q", code, CodeUnattributed)
		}
		if !strings.Contains(fix, "batten phase") {
			t.Errorf("the fix must say how to start being governed; got %q", fix)
		}
	})
}

// The write-set collision is the ONE denial that must not carry a fix, and the reason is the
// whole design: there is no legitimate way through. If two agents both need the file, the PLAN
// is wrong, and the repair is to merge or sequence the sub-tasks. A `fix:` here would be an
// instruction to cross the fence.
func TestTheWriteSetCollisionOffersNoWayThrough(t *testing.T) {
	h, run := gateFixture(t, nil, "enforce")
	for _, n := range []store.Node{
		{NodeID: "n-a", RunID: run.RunID, Kind: "subagent", AgentID: "a", Status: "running"},
		{NodeID: "n-b", RunID: run.RunID, Kind: "subagent", AgentID: "b", Status: "running"},
	} {
		if err := h.Store.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Store.ClaimWriteSet(run.RunID, "n-a", []string{"x.go"}); err != nil {
		t.Fatal(err)
	}

	in := Input{HookEventName: "PreToolUse", ToolName: "Write", AgentID: "b", SessionID: "sess-1"}
	out, err := h.writeSetGuard(in, h.Spec.Root+"/x.go")
	if err != nil {
		t.Fatal(err)
	}
	code, fix, _ := fields(t, out)
	if code != CodeWriteSet {
		t.Fatalf("code = %q, want %q", code, CodeWriteSet)
	}
	if fix != "" {
		t.Errorf("the collision offered a way through (%q). There is none: if both agents need "+
			"this file the plan is wrong, and a fix here would tell an agent to cross the fence", fix)
	}
}

// A degraded batten is the one case where retrying IS right — a busy database or an antivirus
// holding a file for a few milliseconds — and it is also the case a loop could least easily
// infer from the prose.
func TestADegradedBattenIsMarkedRetryable(t *testing.T) {
	out := AdviseDegraded("batten did NOT run for this tool call.\ncause: database is locked")
	code, fix, retry := fields(t, out)
	if code != CodeDegraded {
		t.Errorf("code = %q, want %q", code, CodeDegraded)
	}
	if !retry {
		t.Error("contention clears on its own; this is the one denial a loop should retry")
	}
	if fix != "batten doctor" {
		t.Errorf("fix = %q, want batten doctor", fix)
	}
}

// The prose must survive. A message that is only machine-readable fails the person who has to
// understand what their agent just ran into.
func TestTheProseStaysFirst(t *testing.T) {
	h, _ := gateFixture(t, nil, "enforce")
	out, err := h.verdictGate(commitInput(), `git commit -m "x"`)
	if err != nil {
		t.Fatal(err)
	}
	reason := out.HookSpecific.PermissionDecisionReason
	if !strings.HasPrefix(reason, "batten: ") {
		t.Errorf("the human-readable sentence must come first:\n%s", reason)
	}
	if strings.Index(reason, "batten.code:") < strings.Index(reason, "has no verdict") {
		t.Errorf("the machine fields displaced the prose:\n%s", reason)
	}
	// And the whole thing still has to be valid JSON on the wire.
	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("the output no longer marshals: %v", err)
	}
}
