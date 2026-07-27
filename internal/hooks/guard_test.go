package hooks

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// guardFixture builds a handler whose spec root is a real temp directory, because the write-set
// guard resolves the edited file relative to that root.
func guardFixture(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\nenforcement: enforce\n" +
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    requires_verdict: ok\n"
	if err := writeFile(filepath.Join(dir, "batten.yaml"), y); err != nil {
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
	return &Handler{Spec: sp, Store: st}, dir
}

func writeInputFor(root, rel string) Input {
	ti, _ := json.Marshal(map[string]string{"file_path": filepath.Join(root, rel)})
	return Input{HookEventName: "PreToolUse", ToolName: "Edit", ToolInput: ti, SessionID: "sess-a"}
}

// TestGuardDeniesAnAttributedTrespassAndOnlyWarnsOnAnUnattributedOne is the asymmetry the whole
// guard is built around.
//
// With an agent_id we know exactly who is writing, so a collision is a hard deny. WITHOUT one we
// cannot tell the rightful owner from a trespasser — and if Claude Code ever stops carrying
// agent_id in subagent hooks, denying here would deny the owner too and brick every fan-out.
// The risks are not symmetric: a loud warning on a real trespass is recoverable, silently
// blocking every legitimate write is not.
func TestGuardDeniesAnAttributedTrespassAndOnlyWarnsOnAnUnattributedOne(t *testing.T) {
	h, root := guardFixture(t)
	r, err := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []store.Node{
		{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "a", AgentID: "agent-a"},
		{NodeID: "n-b", RunID: r.RunID, Kind: "subagent", Label: "b", AgentID: "agent-b"},
	} {
		if err := h.Store.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Store.ClaimWriteSet(r.RunID, "n-a", []string{"ml/train.py"}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.ClaimWriteSet(r.RunID, "n-b", []string{"api/routes.go"}); err != nil {
		t.Fatal(err)
	}

	in := writeInputFor(root, "ml/train.py")

	// Agent B, identified, reaching into agent A's file.
	in.AgentID = "agent-b"
	out, err := h.writeSetGuard(in, filepath.Join(root, "ml/train.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("an identified agent writing another's file must be DENIED, got %q", got)
	}
	reason := out.HookSpecific.PermissionDecisionReason
	for _, want := range []string{"ml/train.py", "n-a", "n-b", "api/routes.go"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the denial must name the file, the owner, the trespasser and the trespasser's own\n"+
				"write-set (so the agent can act on it). Missing %q from:\n%s", want, reason)
		}
	}

	// The owner writing its own file is never blocked.
	in.AgentID = "agent-a"
	out, err = h.writeSetGuard(in, filepath.Join(root, "ml/train.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Errorf("the owner writing its own file must pass silently, got %q", got)
	}

	// No agent_id: advisory, never a deny, even under enforcement: enforce.
	in.AgentID = ""
	out, err = h.writeSetGuard(in, filepath.Join(root, "ml/train.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "warn" {
		t.Fatalf("an unattributed write must WARN, never deny — denying would brick the fan-out\n"+
			"if agent_id ever stops arriving. Got %q", got)
	}
}

// A file nobody claimed is nobody's business, and a path outside the repo is not batten's at all.
func TestGuardIgnoresUnclaimedAndOutOfRepoPaths(t *testing.T) {
	h, root := guardFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.AddNode(store.Node{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", AgentID: "agent-a"})
	_ = h.Store.ClaimWriteSet(r.RunID, "n-a", []string{"ml/train.py"})

	in := writeInputFor(root, "docs/notes.md")
	in.AgentID = "agent-b"
	out, err := h.writeSetGuard(in, filepath.Join(root, "docs/notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Errorf("an unclaimed file must pass, got %q", got)
	}

	// Outside the repo: batten governs where it was invited, nowhere else.
	out, err = h.writeSetGuard(in, filepath.Join(filepath.Dir(root), "elsewhere", "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Errorf("a path outside the repo is not batten's to police, got %q", got)
	}

	// An empty path is a payload shape we did not expect; it must no-op, not panic.
	if out, err := h.writeSetGuard(in, ""); err != nil || out != nil {
		t.Errorf("an empty path must no-op; got %v (%v)", out, err)
	}
}

// TestGuardStopsASecondSessionFromRacingTheSameFile: the multi-session rule, from the hook side.
func TestGuardStopsASecondSessionFromRacingTheSameFile(t *testing.T) {
	h, root := guardFixture(t)

	a, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.AddNode(store.Node{NodeID: "n-a", RunID: a.RunID, Kind: "subagent", AgentID: "agent-a"})
	_ = h.Store.ClaimWriteSet(a.RunID, "n-a", []string{"ml/train.py"})

	// A second session, on its own unit, reaching for the same file.
	b, _ := h.Store.EnsureRun("p", "TASK-2", "sess-b")
	in := writeInputFor(root, "ml/train.py")
	in.SessionID = "sess-b"

	out, err := h.writeSetGuard(in, filepath.Join(root, "ml/train.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "deny" {
		t.Fatalf("a second open session must not race a claimed file, got %q", got)
	}
	reason := out.HookSpecific.PermissionDecisionReason
	if !strings.Contains(reason, "TASK-1") {
		t.Errorf("the denial must name the unit holding the file; got %q", reason)
	}
	_ = b

	// Once the first run closes, its claims are released and session B may proceed.
	if err := h.Store.CloseRun(a.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	out, err = h.writeSetGuard(in, filepath.Join(root, "ml/train.py"))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision(out); got != "silent-allow" {
		t.Errorf("after the other run closed, the file is free; got %q", got)
	}
}

// TestReportModeWarnsWhereEnforceDenies: the adoption ramp. Same collision, same evidence, one
// line of spec between a warning and a block.
func TestReportModeWarnsWhereEnforceDenies(t *testing.T) {
	h, root := guardFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.AddNode(store.Node{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", AgentID: "agent-a"})
	_ = h.Store.AddNode(store.Node{NodeID: "n-b", RunID: r.RunID, Kind: "subagent", AgentID: "agent-b"})
	_ = h.Store.ClaimWriteSet(r.RunID, "n-a", []string{"ml/train.py"})

	in := writeInputFor(root, "ml/train.py")
	in.AgentID = "agent-b"

	if got := decision(mustGuard(t, h, in, filepath.Join(root, "ml/train.py"))); got != "deny" {
		t.Fatalf("enforce mode must deny, got %q", got)
	}

	h.Spec.Enforcement = "report"
	out := mustGuard(t, h, in, filepath.Join(root, "ml/train.py"))
	if got := decision(out); got != "warn" {
		t.Fatalf("report mode must warn instead of denying, got %q", got)
	}
	// Report mode must not lose the reason — a warning nobody can act on is noise.
	if !strings.Contains(out.SystemMessage+out.HookSpecific.PermissionDecisionReason, "ml/train.py") {
		t.Error("the warning must still name the file")
	}
}

func mustGuard(t *testing.T, h *Handler, in Input, path string) *Output {
	t.Helper()
	out, err := h.writeSetGuard(in, path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDispatchNeverBreaksASession: a hook runs on every tool call. Malformed input, an unknown
// event, or a handler with no spec must all degrade quietly — principle #2.
func TestDispatchNeverBreaksASession(t *testing.T) {
	h, _ := guardFixture(t)

	if _, err := h.Dispatch("PreToolUse", []byte("{not json")); err == nil {
		t.Error("a malformed payload should report a parse error to the caller, which swallows it")
	}
	if out, err := h.Dispatch("SomeEventFromTheFuture", []byte(`{"session_id":"s"}`)); err != nil || out != nil {
		t.Errorf("an unknown event must no-op; got %v (%v)", out, err)
	}

	// No spec: batten governs where it was invited, nowhere else.
	bare := &Handler{}
	if out, err := bare.Dispatch("PreToolUse", []byte(`{"session_id":"s"}`)); err != nil || out != nil {
		t.Errorf("a handler with no spec must stay out of the way; got %v (%v)", out, err)
	}
}
