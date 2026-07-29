package hooks

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func hookInput(event, session string) Input {
	return Input{HookEventName: event, SessionID: session}
}

// TestFanOutBecomesARunGraph: SubagentStart/Stop is the only place the run DAG comes from, and
// that DAG is what the canvas draws, the TUI shows and the vault note tabulates. If these hooks
// record the wrong shape, every surface downstream is confidently wrong.
func TestFanOutBecomesARunGraph(t *testing.T) {
	h, _ := guardFixture(t)
	r, err := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.SetPhase(r.RunID, "build"); err != nil {
		t.Fatal(err)
	}
	// The phase node the spawn edges will hang off, named the way production names it.
	if err := h.Store.AddNode(store.Node{
		NodeID: store.PhaseNodeID(r.RunID, "build"), RunID: r.RunID,
		Kind: "phase", Label: "build", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}

	start := hookInput("SubagentStart", "sess-a")
	start.AgentID, start.AgentType = "ag-1", "backend"
	if _, err := h.subagentStart(start); err != nil {
		t.Fatal(err)
	}

	nodes, err := h.Store.Nodes(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var sub *store.Node
	for i := range nodes {
		if nodes[i].Kind == "subagent" {
			sub = &nodes[i]
		}
	}
	if sub == nil {
		t.Fatal("SubagentStart recorded no subagent node")
	}
	if sub.AgentID != "ag-1" {
		t.Errorf("agent_id = %q; the write-set guard resolves blame through this field", sub.AgentID)
	}
	if sub.Status != "running" {
		t.Errorf("a subagent that just started is running, got %q", sub.Status)
	}

	// The spawn edge is what makes the canvas a graph rather than a list. It must point at
	// THIS run's phase node: a bare "p-build" is one row for the whole database, and the
	// second unit to enter build would take it out of this run.
	edges, _ := h.Store.Edges(r.RunID)
	var spawned bool
	for _, e := range edges {
		if e.Src == store.PhaseNodeID(r.RunID, "build") && e.Dst == sub.NodeID && e.Rel == "spawn" {
			spawned = true
		}
	}
	if !spawned {
		t.Errorf("no spawn edge from this run's phase to the subagent; edges = %+v", edges)
	}
	if !strings.Contains(sub.NodeID, r.RunID) {
		t.Errorf("subagent node id %q is not scoped to its run; agent ids are only unique "+
			"within the session that minted them", sub.NodeID)
	}

	// Stop closes it out. A message that reads as a failure must NOT be recorded as ok — the
	// fan-out table and the canvas colour depend on this being right.
	stop := hookInput("SubagentStop", "sess-a")
	stop.AgentID = "ag-1"
	stop.LastAssistantMessage = "the build failed: 3 tests are red"
	if _, err := h.subagentStop(stop); err != nil {
		t.Fatal(err)
	}
	nodes, _ = h.Store.Nodes(r.RunID)
	for _, n := range nodes {
		if n.NodeID == sub.NodeID && n.Status != "failed" {
			t.Errorf("a subagent reporting failure was recorded as %q", n.Status)
		}
	}

	// And a clean finish is ok.
	start2 := hookInput("SubagentStart", "sess-a")
	start2.AgentID, start2.AgentType = "ag-2", "frontend"
	_, _ = h.subagentStart(start2)
	stop2 := hookInput("SubagentStop", "sess-a")
	stop2.AgentID, stop2.LastAssistantMessage = "ag-2", "done, all green"
	_, _ = h.subagentStop(stop2)
	nodes, _ = h.Store.Nodes(r.RunID)
	for _, n := range nodes {
		if n.AgentID == "ag-2" && n.Status != "ok" {
			t.Errorf("a clean subagent was recorded as %q", n.Status)
		}
	}
}

// The agent_type usually IS the domain name, because the fan-out launches one agent per domain.
// Getting this wrong loses the domain column everywhere it is displayed.
func TestSubagentTakesItsDomainFromTheAgentType(t *testing.T) {
	// This one needs a spec that actually declares domains — the mapping has nothing to match
	// against otherwise, and a fixture without them would pass for the wrong reason.
	h, _ := domainFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")

	for _, at := range []string{"backend", "frontend", "something-unknown"} {
		in := hookInput("SubagentStart", "sess-a")
		in.AgentID, in.AgentType = "ag-"+at, at
		if _, err := h.subagentStart(in); err != nil {
			t.Fatal(err)
		}
	}
	nodes, _ := h.Store.Nodes(r.RunID)
	got := map[string]string{}
	for _, n := range nodes {
		if n.Kind == "subagent" {
			got[n.AgentType] = n.Domain
		}
	}
	if got["backend"] != "backend" || got["frontend"] != "frontend" {
		t.Errorf("agent types that name a domain must carry it: %v", got)
	}
	// An agent type that is not a domain leaves the field empty rather than guessing at one.
	if got["something-unknown"] != "" {
		t.Errorf("an unknown agent type invented the domain %q", got["something-unknown"])
	}
}

// ---------- retries ----------
//
// `edges.rel = retry_of` had FIVE readers and no writer: `batten pr` counts retries for its badge
// and draws the dotted edge, the canvas colours it orange, the vault lists it under Relations,
// MCP answers `retries: N`, and the TUI hangs it off the node. The headline of `batten pr` is
// that the DAG shows what a plan diagram cannot — and the only thing a plan diagram cannot show
// IS the retry. It looked like it worked in testing purely because the edge had been inserted
// into SQLite by hand.

// startAgent / stopAgent drive the two hooks the run graph is built from, so the tests below
// exercise the real path rather than calling AddEdge themselves.
func startAgent(t *testing.T, h *Handler, id, typ string) {
	t.Helper()
	in := hookInput("SubagentStart", "sess-a")
	in.AgentID, in.AgentType = id, typ
	if _, err := h.subagentStart(in); err != nil {
		t.Fatal(err)
	}
}

func stopAgent(t *testing.T, h *Handler, id, msg string) {
	t.Helper()
	in := hookInput("SubagentStop", "sess-a")
	in.AgentID, in.LastAssistantMessage = id, msg
	if _, err := h.subagentStop(in); err != nil {
		t.Fatal(err)
	}
}

// retriesIn returns the retry_of edges as "new->old" pairs, named by agent id rather than node
// id so a failure message says which agents batten thinks retried which.
func retriesIn(t *testing.T, h *Handler, runID string) []string {
	t.Helper()
	nodes, err := h.Store.Nodes(runID)
	if err != nil {
		t.Fatal(err)
	}
	name := map[string]string{}
	for _, n := range nodes {
		name[n.NodeID] = n.AgentID
	}
	edges, err := h.Store.Edges(runID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range edges {
		if e.Rel == "retry_of" {
			out = append(out, name[e.Src]+"->"+name[e.Dst])
		}
	}
	sort.Strings(out)
	return out
}

func attemptOf(t *testing.T, h *Handler, agentID string) int {
	t.Helper()
	n, err := h.Store.NodeByAgent(agentID)
	if err != nil {
		t.Fatalf("no node for %s: %v", agentID, err)
	}
	return n.Attempt
}

// TestARetryIsRecordedAsARetry is the regression test for the missing producer. Against the
// commit before this one it fails on the first assertion: the run has zero retry_of edges no
// matter what happens, because nothing in the binary ever wrote one.
func TestARetryIsRecordedAsARetry(t *testing.T) {
	h, _ := domainFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")

	startAgent(t, h, "ag-1", "backend")
	stopAgent(t, h, "ag-1", "the backend agent failed: 3 tests are red")
	startAgent(t, h, "ag-2", "backend")

	if got, want := retriesIn(t, h, r.RunID), []string{"ag-2->ag-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry_of edges = %v, want %v — a subagent picking up a domain whose previous "+
			"agent ended failed IS the retry every surface promises to draw", got, want)
	}
	if got := attemptOf(t, h, "ag-2"); got != 2 {
		t.Errorf("the retry is attempt %d, want 2 — without it the canvas draws two identically "+
			"labelled `backend` cards and a reviewer cannot tell which is the retry", got)
	}
	if got := attemptOf(t, h, "ag-1"); got != 1 {
		t.Errorf("the first attempt is %d, want 1", got)
	}

	// It chains. A second failure retried again is attempt 3, hanging off attempt 2 — not off
	// attempt 1, which is already accounted for.
	stopAgent(t, h, "ag-2", "failed again: the migration is still missing")
	startAgent(t, h, "ag-3", "backend")
	if got, want := retriesIn(t, h, r.RunID), []string{"ag-2->ag-1", "ag-3->ag-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("retry chain = %v, want %v", got, want)
	}
	if got := attemptOf(t, h, "ag-3"); got != 3 {
		t.Errorf("the third attempt is numbered %d", got)
	}
}

// TestParallelAgentsOnOneDomainAreNotRetries is the false positive that would make the feature
// worse than its absence. A fan-out that puts two agents on one domain is the ordinary case, and
// calling the second one a retry of the first would report a retry count for a run that never
// retried anything — a number `batten pr` prints in its badge.
func TestParallelAgentsOnOneDomainAreNotRetries(t *testing.T) {
	h, _ := domainFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")

	startAgent(t, h, "par-1", "backend")
	startAgent(t, h, "par-2", "backend") // side by side, neither finished
	if got := retriesIn(t, h, r.RunID); len(got) != 0 {
		t.Fatalf("two agents running side by side on one domain were recorded as a retry: %v", got)
	}
	if got := attemptOf(t, h, "par-2"); got != 1 {
		t.Errorf("a parallel sibling is attempt %d, want 1", got)
	}

	// Now one of them fails and a THIRD agent takes the domain. That one is a genuine retry, and
	// it must attach to the agent that failed — not to the one still running.
	stopAgent(t, h, "par-1", "error: could not reach the database")
	startAgent(t, h, "par-3", "backend")
	if got, want := retriesIn(t, h, r.RunID), []string{"par-3->par-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("retry_of edges = %v, want %v", got, want)
	}
}

// A domain that failed once and was fixed is CLOSED. Every later agent on that domain is new
// work, not a third attempt at work that finished — which is why the query excludes a failure
// something has already retried instead of just taking the most recent one.
func TestAFailureThatWasAlreadyRetriedIsNotRetriedAgain(t *testing.T) {
	h, _ := domainFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")

	startAgent(t, h, "a1", "backend")
	stopAgent(t, h, "a1", "failed")
	startAgent(t, h, "a2", "backend")
	stopAgent(t, h, "a2", "done, all green")
	startAgent(t, h, "a3", "backend")

	if got, want := retriesIn(t, h, r.RunID), []string{"a2->a1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("retry_of edges = %v, want %v — a1 was already retried by a2, so a3 is new work", got, want)
	}
	if got := attemptOf(t, h, "a3"); got != 1 {
		t.Errorf("a3 is attempt %d, want 1", got)
	}
}

// Retries are per fan-out axis. A frontend agent starting after the backend agent failed is the
// fan-out proceeding, not a retry — and the axis is the domain, which is what the fan-out
// actually divides.
func TestAFailureInOneDomainIsNotRetriedByAnother(t *testing.T) {
	h, _ := domainFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")

	startAgent(t, h, "b1", "backend")
	stopAgent(t, h, "b1", "failed")
	startAgent(t, h, "f1", "frontend")

	if got := retriesIn(t, h, r.RunID); len(got) != 0 {
		t.Errorf("a different domain was recorded as a retry: %v", got)
	}
}

// The retry loop batten actually ships — /batten-night's build → verify → fix → re-verify — puts
// the retry in a DIFFERENT phase from the failure. Scoping retries to a phase would make the one
// retry batten most wants to draw the one it cannot see.
func TestARetryInALaterPhaseStillCounts(t *testing.T) {
	h, _ := domainFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")
	startAgent(t, h, "n1", "backend")
	stopAgent(t, h, "n1", "failed: the gate is red")

	_ = h.Store.SetPhase(r.RunID, "close")
	startAgent(t, h, "n2", "backend")

	if got, want := retriesIn(t, h, r.RunID), []string{"n2->n1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("retry_of edges = %v, want %v — the fix loop retries in a later phase", got, want)
	}
}

// TestSessionStartTellsYouWhatTheGateWillDo. This banner is the only warning before a commit is
// attempted, and its two jobs are to say which unit this session owns and to say plainly when
// nothing is being gated.
func TestSessionStartTellsYouWhatTheGateWillDo(t *testing.T) {
	h, _ := guardFixture(t)

	// Nothing recorded is NOT nothing to report: it is the state in which the commit gate
	// governs nothing at all. Staying quiet here is how a newcomer branches, codes, commits,
	// watches it succeed, and concludes the gate is on.
	out, err := h.sessionStart(hookInput("SessionStart", "sess-a"))
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.HookSpecific == nil {
		t.Fatal("with no runs the gate is governing nothing, and that must be said once")
	}
	if ctx := out.HookSpecific.AdditionalContext; !strings.Contains(ctx, "not\ngoverning anything") &&
		!strings.Contains(ctx, "not governing anything") {
		t.Errorf("the zero-run banner must say the gate is not governing anything; got:\n%s", ctx)
	}
	if !strings.Contains(out.HookSpecific.AdditionalContext, "batten phase") {
		t.Errorf("it must also say how to start:\n%s", out.HookSpecific.AdditionalContext)
	}

	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	_ = h.Store.SetPhase(r.RunID, "build")

	out, err = h.sessionStart(hookInput("SessionStart", "sess-a"))
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || out.HookSpecific == nil {
		t.Fatal("an open run must produce a banner")
	}
	ctx := out.HookSpecific.AdditionalContext
	if !strings.Contains(ctx, "TASK-1") {
		t.Errorf("the banner does not name the open unit:\n%s", ctx)
	}
	if !strings.Contains(ctx, "no verdict yet") {
		t.Errorf("a run with no verdict must warn that the close gate will deny; got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "this session is working") {
		t.Errorf("a bound session must be told which unit it owns:\n%s", ctx)
	}
}

// Ambiguity must be loud. Two open units and an unbound session means the gates cannot attribute
// anything — and a gate that silently is not there is worse than no gate.
func TestAmbiguousSessionIsToldSoLoudly(t *testing.T) {
	h, _ := guardFixture(t)
	a, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	b, _ := h.Store.EnsureRun("p", "TASK-2", "sess-b")
	_ = h.Store.SetPhase(a.RunID, "build")
	_ = h.Store.SetPhase(b.RunID, "build")

	out, err := h.sessionStart(hookInput("SessionStart", "sess-nobody"))
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("two open runs is exactly when the banner matters most")
	}
	ctx := out.HookSpecific.AdditionalContext
	if !strings.Contains(ctx, "isn't bound") {
		t.Errorf("an unbound session must be told it is unbound:\n%s", ctx)
	}
	if !strings.Contains(ctx, "batten phase") {
		t.Errorf("the warning must say how to fix it:\n%s", ctx)
	}
	for _, u := range []string{"TASK-1", "TASK-2"} {
		if !strings.Contains(ctx, u) {
			t.Errorf("the competing unit %s is not named:\n%s", u, ctx)
		}
	}
}

// activeUnit is what every hook resolves blame through. Its priority order is the multi-session
// rule, and a session already bound must never be re-attributed by a branch name.
func TestActiveUnitPrefersTheSessionBinding(t *testing.T) {
	h, _ := guardFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-7", "sess-a")

	if got := h.activeUnit(hookInput("PreToolUse", "sess-a")); got != "TASK-7" {
		t.Errorf("activeUnit = %q, want TASK-7 from the session binding", got)
	}
	// An unknown session with exactly one unowned run may adopt it; with none, nothing.
	if got := h.activeUnit(hookInput("PreToolUse", "sess-unknown")); got != "" && got != "TASK-7" {
		t.Errorf("activeUnit for an unknown session = %q, which is neither empty nor the sole run", got)
	}
	_ = r
}

// looksFailed decides the colour of a node on the canvas and in the TUI. It errs toward marking
// failure, because a red run shown as green is the worse mistake.
func TestLooksFailedErrsTowardFailure(t *testing.T) {
	for _, s := range []string{
		"the build failed",
		"error: cannot compile",
		"blocked on the missing migration",
		"FAILED",
	} {
		if !looksFailed(s) {
			t.Errorf("%q should read as a failure", s)
		}
	}
	for _, s := range []string{"done", "all green, 14 tests pass", ""} {
		if looksFailed(s) {
			t.Errorf("%q should not read as a failure", s)
		}
	}
}

// Dispatch must survive every event shape without taking the session with it — principle #2.
func TestDispatchHandlesEveryDeclaredEvent(t *testing.T) {
	h, _ := guardFixture(t)
	_, _ = h.Store.EnsureRun("p", "TASK-1", "sess-a")

	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop", "Stop"} {
		payload, _ := json.Marshal(Input{
			HookEventName: ev, SessionID: "sess-a", AgentID: "ag-x", AgentType: "backend",
			ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`),
		})
		if _, err := h.Dispatch(ev, payload); err != nil {
			t.Errorf("Dispatch(%s) returned an error a hook would surface: %v", ev, err)
		}
	}

	// A payload missing everything must still not panic or error.
	for _, ev := range []string{"SessionStart", "PreToolUse", "SubagentStop", "Stop"} {
		if _, err := h.Dispatch(ev, []byte(`{}`)); err != nil {
			t.Errorf("Dispatch(%s) with an empty payload: %v", ev, err)
		}
	}
}

// domainFixture is guardFixture plus declared domains, for the tests that exercise how a
// subagent is attributed to one.
func domainFixture(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\nenforcement: enforce\n" +
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    requires_verdict: ok\n" +
		"domains:\n  backend:\n    path: backend/\n  frontend:\n    path: frontend/\n"
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

// TestTwoUnitsInTheSamePhaseDoNotStealEachOthersNodes is the collision the field test found.
//
// Phase node ids used to be `"p-" + phaseID`, and node_id is a PRIMARY KEY under an
// INSERT OR REPLACE. A phase called "build" was therefore one row for the whole database:
// the second unit to enter build rewrote the first one's row to point at its own run, and
// the first run's canvas collapsed to a bare header while its subagents were left pointing
// at a parent that had moved. Two work items open at once is the headline use case.
func TestTwoUnitsInTheSamePhaseDoNotStealEachOthersNodes(t *testing.T) {
	h, _ := gateFixture(t, nil, "enforce")

	r1, err := h.Store.EnsureRun("p", "TASK-1", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := h.Store.EnsureRun("p", "TASK-2", "sess-2")
	if err != nil {
		t.Fatal(err)
	}
	if r1.RunID == r2.RunID {
		t.Fatal("fixture error: both units share a run")
	}

	// Both units enter a phase of the same name, and each fans out one subagent under an
	// agent id the other also uses — the ordinary case, since agent ids are only unique
	// within the session that minted them.
	for _, r := range []*store.Run{r1, r2} {
		if err := h.Store.SetPhase(r.RunID, "build"); err != nil {
			t.Fatal(err)
		}
		if err := h.Store.AddNode(store.Node{
			NodeID: store.PhaseNodeID(r.RunID, "build"), RunID: r.RunID,
			Kind: "phase", Label: "build", Status: "running",
		}); err != nil {
			t.Fatal(err)
		}
		in := hookInput("SubagentStart", r.SessionID)
		in.AgentID, in.AgentType = "worker-1", "api"
		if _, err := h.subagentStart(in); err != nil {
			t.Fatal(err)
		}
	}

	// The first run must still own everything it recorded.
	for _, r := range []*store.Run{r1, r2} {
		nodes, err := h.Store.Nodes(r.RunID)
		if err != nil {
			t.Fatal(err)
		}
		var phase, sub int
		for _, n := range nodes {
			switch n.Kind {
			case "phase":
				phase++
			case "subagent":
				sub++
			}
		}
		if phase != 1 || sub != 1 {
			t.Errorf("run %s has %d phase and %d subagent nodes, want 1 and 1 — a second unit "+
				"entering the same phase took a row out of this run", r.UnitID, phase, sub)
		}
	}

	// And the spawn edge must resolve inside its own run, or the canvas drops the subagent.
	for _, r := range []*store.Run{r1, r2} {
		edges, err := h.Store.Edges(r.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 {
			t.Fatalf("run %s: want exactly one spawn edge, got %+v", r.UnitID, edges)
		}
		if edges[0].Src != store.PhaseNodeID(r.RunID, "build") {
			t.Errorf("run %s: spawn edge points at %q, not at this run's phase node",
				r.UnitID, edges[0].Src)
		}
	}
}

// A closed run has no phase still running. Every surface used to paint every phase as
// `running` forever, in the same frame whose header said the run finished ok.
func TestClosingARunFinishesItsPhases(t *testing.T) {
	h, run := gateFixture(t, nil, "enforce")
	if err := h.Store.AddNode(store.Node{
		NodeID: store.PhaseNodeID(run.RunID, "build"), RunID: run.RunID,
		Kind: "phase", Label: "build", Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.CloseRun(run.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	nodes, err := h.Store.Nodes(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Kind != "phase" {
			continue
		}
		if n.Status == "running" || n.EndedAt == nil {
			t.Errorf("phase %q is still %q with ended_at=%v after the run closed ok",
				n.Label, n.Status, n.EndedAt)
		}
	}
}
