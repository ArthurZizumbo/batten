package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

const battenYAML = `
version: 1
project: batten
unit:
  name: US
  pattern: US-\d{3}
  plan: PLAN.md
  locator: "### {id}"
artifacts:
  plan: docs/{id}/plan.md
phases:
  - id: explore
  - id: implement
    fanout: true
  - id: verify
    gate: close
    requires_verdict: ok
domains:
  api:
    path: internal/api
    check: ["go test ./internal/api/..."]
    invariants:
      - "never widen a public signature"
  web:
    path: web/src
    check: ["npm test"]
gates:
  close:
    checks: ["go test ./..."]
    verdict: required
    evidence: required
budget:
  tokens_per_run: 1000000
  imputed_usd_per_run: 10
  quota_pct_per_run: 20
  on_exceed: block
`

// fixture builds a real spec (parsed from YAML, so Validate actually runs) over a temp
// database. Everything below queries this, not a mock: the handlers' whole job is to be
// faithful to the store, and a mock would let them drift.
func fixture(t *testing.T) *queries {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(battenYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "state", "batten.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &queries{sp: sp, st: st}
}

// seed lays down the shape of a real fan-out: a phase that spawned two domain agents,
// one of which failed and was retried.
func seed(t *testing.T, q *queries) *store.Run {
	t.Helper()
	r, err := q.st.EnsureRun("batten", "US-001", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.st.SetPhase(r.RunID, "implement"); err != nil {
		t.Fatal(err)
	}
	nodes := []store.Node{
		{NodeID: "p-implement", RunID: r.RunID, Kind: "phase", Label: "implement", Status: "running"},
		{NodeID: "n-a1", RunID: r.RunID, Kind: "subagent", Label: "api", Domain: "api",
			Status: "failed", AgentID: "a1", AgentType: "api-dev"},
		{NodeID: "n-a2", RunID: r.RunID, Kind: "subagent", Label: "api", Domain: "api",
			Status: "ok", AgentID: "a2", AgentType: "api-dev"},
		{NodeID: "n-b1", RunID: r.RunID, Kind: "subagent", Label: "web", Domain: "web",
			Status: "ok", AgentID: "b1", AgentType: "web-dev"},
	}
	for _, n := range nodes {
		if err := q.st.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []store.Edge{
		{Src: "p-implement", Dst: "n-a1", Rel: "spawn"},
		{Src: "p-implement", Dst: "n-b1", Rel: "spawn"},
		{Src: "n-a2", Dst: "n-a1", Rel: "retry_of"},
	} {
		if err := q.st.AddEdge(r.RunID, e.Src, e.Dst, e.Rel); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.st.ClaimWriteSet(r.RunID, "n-a2", []string{"internal/api/handler.go"}); err != nil {
		t.Fatal(err)
	}
	if err := q.st.ClaimWriteSet(r.RunID, "n-b1", []string{"web/src/app.ts"}); err != nil {
		t.Fatal(err)
	}
	// The timestamp must fall INSIDE the run's lifetime. RecordUsage fences out anything older
	// than started_at — usage that predates the run belongs to the session, not to this run — so
	// a fixture stamped at the epoch would be silently dropped and every assertion below it would
	// fail for a reason that has nothing to do with what it tests.
	added, err := q.st.RecordUsage([]store.Usage{{
		RequestID: "req-1", RunID: r.RunID, NodeID: "n-a2", Model: "opus", TS: r.StartedAt,
		InputTokens: 1000, OutputTokens: 500, CacheRead: 200, ImputedUSD: 0.25,
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Assert the row landed. RecordUsage skips silently by design, and a seed that quietly seeds
	// nothing is how three tests came to fail on a change that never touched them.
	if added != 1 {
		t.Fatalf("seed recorded %d usage rows, want 1 — the time fence dropped the fixture", added)
	}
	got, err := q.st.Run(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ctx() context.Context { return context.Background() }

// ---------- the round trip ----------

// TestToolsRoundTrip is the test that matters most: it wires the real server to a real client
// over an in-memory transport, so the SDK infers every input/output schema and VALIDATES every
// result against it. A handler returning a shape its own schema rejects fails here, which a
// direct call to the handler would never catch.
func TestToolsRoundTrip(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	if err := q.st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "close", CheckID: "close", Result: "ok",
		Evidence: []string{"go test ./...: ok, 42 passed"},
	}, true); err != nil {
		t.Fatal(err)
	}

	srv := newServer(q.sp, q.st)
	st1, st2 := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx(), st1, nil); err != nil {
		t.Fatal(err)
	}
	cli := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := cli.Connect(ctx(), st2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	lt, err := cs.ListTools(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"batten_runs": true, "batten_run_graph": true, "batten_verdict_status": true,
		"batten_budget": true, "batten_writeset_owner": true, "batten_spec": true,
	}
	for _, tool := range lt.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		delete(want, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %q has no description: the description is the only thing the model reads "+
				"to decide whether to call it", tool.Name)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing tools: %v", want)
	}

	calls := []struct {
		name string
		args map[string]any
	}{
		{"batten_runs", map[string]any{}},
		{"batten_run_graph", map[string]any{"unit": "US-001"}},
		{"batten_verdict_status", map[string]any{"unit": "US-001"}},
		{"batten_budget", map[string]any{"unit": "US-001"}},
		{"batten_writeset_owner", map[string]any{"path": "internal/api/handler.go", "agent_id": "b1"}},
		{"batten_spec", map[string]any{}},
	}
	for _, c := range calls {
		res, err := cs.CallTool(ctx(), &sdk.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s: protocol error: %v", c.name, err)
		}
		if res.IsError {
			t.Fatalf("%s: tool error: %s", c.name, textOf(res))
		}
		if res.StructuredContent == nil {
			t.Fatalf("%s: no structured content", c.name)
		}
	}
}

func textOf(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// ---------- the gate: the one answer the agent needs ----------

func TestVerdictStatusDeniesWithoutVerdict(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.CommitDenied {
		t.Fatal("a unit with no verdict must be denied: that is the gate's entire reason to exist")
	}
	if out.HasVerdict || out.Verdict != nil {
		t.Error("reported a verdict that does not exist")
	}
	if out.Gate != "close" {
		t.Errorf("gate = %q, want close", out.Gate)
	}
	if !strings.Contains(out.DenyReason, "no verdict") {
		t.Errorf("deny_reason does not say what is missing: %q", out.DenyReason)
	}
	if out.HowToFix == "" {
		t.Error("denied with no way out: the agent cannot self-correct")
	}
	if len(out.GateChecks) != 1 || out.GateChecks[0] != "go test ./..." {
		t.Errorf("gate_checks = %v; the agent needs these to produce evidence", out.GateChecks)
	}
}

// The one failure batten exists to kill.
func TestVerdictStatusDeniesOkWithEmptyEvidence(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	// evidenceRequired=false to force the bad envelope in past the store's own guard —
	// exactly the case where a verdict arrived by some other path.
	if err := q.st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "close", CheckID: "close", Result: "ok",
	}, false); err != nil {
		t.Fatal(err)
	}

	_, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.CommitDenied {
		t.Fatal("result=ok with an empty evidence[] must be denied")
	}
	if out.Verdict == nil || out.Verdict.EvidenceCount != 0 {
		t.Fatalf("verdict = %+v, want an evidence count of 0", out.Verdict)
	}
	if !strings.Contains(strings.ToLower(out.DenyReason), "evidence") {
		t.Errorf("deny_reason must name the missing evidence, got %q", out.DenyReason)
	}
}

func TestVerdictStatusAllowsWithEvidence(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	if err := q.st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "close", CheckID: "close", Result: "ok",
		Evidence: []string{"go test ./...: ok (42 tests)", "go vet: clean"},
	}, true); err != nil {
		t.Fatal(err)
	}

	_, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if out.CommitDenied {
		t.Fatalf("a verdict with evidence must clear the gate, got deny_reason=%q", out.DenyReason)
	}
	if out.Verdict.EvidenceCount != 2 {
		t.Errorf("evidence_count = %d, want 2", out.Verdict.EvidenceCount)
	}
	if out.DenyReason != "" {
		t.Errorf("deny_reason should be empty when nothing is denied, got %q", out.DenyReason)
	}
}

func TestVerdictStatusDeniesBlockedResult(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	if err := q.st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "close", CheckID: "close", Result: "blocked",
		Evidence: []string{"3 tests failing"}, Why: "the suite is red",
		SafeNextStep: "fix TestFoo",
	}, true); err != nil {
		t.Fatal(err)
	}
	_, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.CommitDenied {
		t.Fatal("result=blocked must deny the commit")
	}
	if out.HowToFix != "fix TestFoo" {
		t.Errorf("how_to_fix = %q, want the verdict's safe_next_step", out.HowToFix)
	}
}

// An over-budget run with a clean verdict is still denied when on_exceed=block. The tool must
// agree with the hook here, or it tells the agent it is fine and the commit blows up anyway.
func TestVerdictStatusDeniesOverBudget(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	if err := q.st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "close", CheckID: "close", Result: "ok",
		Evidence: []string{"all green"},
	}, true); err != nil {
		t.Fatal(err)
	}
	q.sp.Budget.TokensPerRun = 10 // the seeded run spent 1700

	_, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.CommitDenied {
		t.Fatal("over budget with on_exceed=block must deny the commit")
	}
	if !strings.Contains(out.DenyReason, "budget") {
		t.Errorf("deny_reason = %q, want it to name the budget", out.DenyReason)
	}
}

func TestVerdictStatusOverrideClearsTheGate(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	if err := q.st.Override(r.RunID, "close", "hotfix, ci is down"); err != nil {
		t.Fatal(err)
	}
	_, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Overridden {
		t.Fatal("the override was not reported")
	}
	if out.CommitDenied {
		t.Error("an overridden gate does not deny; the escape hatch exists, on the record")
	}
}

// ---------- graph ----------

func TestRunGraph(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, out, err := q.runGraph(ctx(), nil, graphInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Run == nil || out.Run.Unit != "US-001" {
		t.Fatalf("run = %+v", out.Run)
	}
	if len(out.Nodes) != 4 {
		t.Fatalf("got %d nodes, want 4", len(out.Nodes))
	}
	if len(out.Edges) != 3 {
		t.Fatalf("got %d edges, want 3", len(out.Edges))
	}
	// The whole point of the tool: the retry is visible. A static workflow diagram cannot show it.
	if out.Retries != 1 {
		t.Errorf("retries = %d, want 1 (n-a2 retry_of n-a1)", out.Retries)
	}

	byID := map[string]nodeInfo{}
	for _, n := range out.Nodes {
		byID[n.NodeID] = n
	}
	a2 := byID["n-a2"]
	if a2.Usage == nil {
		t.Fatal("n-a2 has recorded usage but reported none")
	}
	if a2.Usage.TotalTokens != 1700 { // 1000 in + 500 out + 200 cache read
		t.Errorf("n-a2 total_tokens = %d, want 1700 (cache reads count: cheap is not free)", a2.Usage.TotalTokens)
	}
	if len(a2.WriteSet) != 1 || a2.WriteSet[0] != "internal/api/handler.go" {
		t.Errorf("n-a2 write_set = %v", a2.WriteSet)
	}
	// Never invent a number: a node with no usage rows reports null, not zero.
	if b1 := byID["n-b1"]; b1.Usage != nil {
		t.Errorf("n-b1 has no usage rows; usage must be null, not %+v", b1.Usage)
	}
	if out.UnattributedUsage != nil {
		t.Errorf("no unattributed usage was seeded, got %+v", out.UnattributedUsage)
	}
}

// ---------- budget ----------

func TestBudgetReportsUnmeasurableCeilingAsUnavailable(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, out, err := q.budget(ctx(), nil, budgetInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Declared {
		t.Fatal("the spec declares a budget")
	}
	if len(out.Ceilings) != 3 {
		t.Fatalf("got %d ceilings, want 3", len(out.Ceilings))
	}
	byKind := map[string]ceilingInfo{}
	for _, c := range out.Ceilings {
		byKind[c.Kind] = c
	}

	tok := byKind["tokens"]
	if !tok.Available || tok.Spent == nil || *tok.Spent != 1700 {
		t.Errorf("tokens ceiling = %+v, want spent=1700", tok)
	}
	if tok.Remaining == nil || *tok.Remaining != 1000000-1700 {
		t.Errorf("tokens remaining = %v", tok.Remaining)
	}
	if tok.Exceeded {
		t.Error("1700 tokens does not exceed 1e6")
	}

	// No statusline has sampled the 5h window, so this ceiling is NOT enforced — and must
	// say so instead of showing a comfortable 0%.
	quota := byKind["quota_pct"]
	if quota.Available {
		t.Fatal("quota was never sampled; it cannot be available")
	}
	if quota.Spent != nil {
		t.Errorf("an unmeasurable ceiling must report spent=null, got %v — never invent a number", *quota.Spent)
	}
	if quota.UnavailableReason == "" {
		t.Error("unavailable with no reason is a shrug; the agent cannot act on it")
	}
	if quota.Exceeded {
		t.Error("an unmeasurable ceiling cannot be exceeded")
	}
}

func TestBudgetWithNoDeclaredCeiling(t *testing.T) {
	q := fixture(t)
	seed(t, q)
	q.sp.Budget = spec.Budget{}

	_, out, err := q.budget(ctx(), nil, budgetInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Declared {
		t.Error("no ceiling is declared")
	}
	if len(out.Ceilings) != 0 {
		t.Errorf("ceilings = %v, want none", out.Ceilings)
	}
	if out.Note == "" {
		t.Error("silence implies enforcement; say that nothing is enforced")
	}
}

// ---------- write-set ----------

func TestWriteSetOwnerDeniesForeignFile(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	// b1 owns web/src/app.ts; internal/api/handler.go belongs to n-a2.
	_, out, err := q.writeSetOwner(ctx(), nil, writeSetInput{
		Path: "internal/api/handler.go", AgentID: "b1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Owned || out.OwnerNode != "n-a2" {
		t.Fatalf("owner = %q, want n-a2", out.OwnerNode)
	}
	if out.OwnedByYou {
		t.Error("b1 does not own this file")
	}
	if out.WriteAllowed {
		t.Fatal("a write here would be denied by the guard; the tool must say so BEFORE the denial")
	}
	if out.OwnerAgentID != "a2" || out.OwnerAgentType != "api-dev" {
		t.Errorf("owner agent = %q/%q, want a2/api-dev", out.OwnerAgentID, out.OwnerAgentType)
	}
	if len(out.YourWriteSet) != 1 || out.YourWriteSet[0] != "web/src/app.ts" {
		t.Errorf("your_write_set = %v, want [web/src/app.ts]", out.YourWriteSet)
	}
	if out.Domain != "api" {
		t.Errorf("domain = %q, want api", out.Domain)
	}
}

func TestWriteSetOwnerAllowsOwnFile(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, out, err := q.writeSetOwner(ctx(), nil, writeSetInput{Path: "web/src/app.ts", AgentID: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.OwnedByYou || !out.WriteAllowed {
		t.Fatalf("b1 owns web/src/app.ts: %+v", out)
	}
}

func TestWriteSetOwnerUnclaimedFile(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, out, err := q.writeSetOwner(ctx(), nil, writeSetInput{Path: "README.md", AgentID: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Owned {
		t.Error("README.md was never claimed")
	}
	if !out.WriteAllowed {
		t.Error("an unclaimed file is not fenced")
	}
}

// An absolute Windows path must key the same row as the slash-normalized relative one, or the
// tool's answer disagrees with the hook's on the author's own machine.
func TestWriteSetOwnerNormalizesAbsolutePath(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	abs := filepath.Join(q.sp.Root, "internal", "api", "handler.go")
	_, out, err := q.writeSetOwner(ctx(), nil, writeSetInput{Path: abs, AgentID: "b1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != "internal/api/handler.go" {
		t.Fatalf("path = %q, want the slash-normalized repo-relative form", out.Path)
	}
	if out.OwnerNode != "n-a2" {
		t.Errorf("owner = %q, want n-a2", out.OwnerNode)
	}
}

func TestWriteSetOwnerOutsideRepo(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, out, err := q.writeSetOwner(ctx(), nil, writeSetInput{Path: "../elsewhere/x.go"})
	if err != nil {
		t.Fatalf("a path outside the repo is a normal answer, not an error: %v", err)
	}
	if out.Owned {
		t.Error("nothing outside the repo is claimed")
	}
	if out.Note == "" {
		t.Error("say why the answer is empty")
	}
}

// ---------- spec ----------

func TestSpecExposesInvariantsAndChecks(t *testing.T) {
	q := fixture(t)

	_, out, err := q.spec(ctx(), nil, specInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Project != "batten" || out.UnitName != "US" {
		t.Fatalf("project/unit = %q/%q", out.Project, out.UnitName)
	}
	if len(out.Phases) != 3 || out.Phases[2].RequiresVerdict != "ok" {
		t.Fatalf("phases = %+v", out.Phases)
	}
	if len(out.Domains) != 2 || out.Domains[0].Name != "api" {
		t.Fatalf("domains must be sorted and complete, got %+v", out.Domains)
	}
	api := out.Domains[0]
	if len(api.Invariants) != 1 || api.Invariants[0] != "never widen a public signature" {
		t.Errorf("invariants = %v: they must ride into the agent's prompt verbatim", api.Invariants)
	}
	if len(api.Check) != 1 {
		t.Errorf("check = %v: without it the agent cannot produce evidence", api.Check)
	}
	// A domain with no invariants must report [] (there are none), not null (unknown).
	if web := out.Domains[1]; web.Invariants == nil {
		t.Error("empty invariants must serialize as [], not null")
	}
	if len(out.Gates) != 1 || !out.Gates[0].EvidenceRequired {
		t.Fatalf("gates = %+v, want close with evidence required", out.Gates)
	}
	if out.Budget.OnExceed != "block" {
		t.Errorf("on_exceed = %q", out.Budget.OnExceed)
	}
}

// ---------- degraded states must never kill the server ----------

func TestNoSpecNoStore(t *testing.T) {
	q := &queries{}

	if _, out, err := q.runs(ctx(), nil, runsInput{}); err != nil || out.Note == "" || len(out.Runs) != 0 {
		t.Errorf("runs: err=%v out=%+v; an ungoverned repo is an empty answer, not an error", err, out)
	}
	if _, out, err := q.runGraph(ctx(), nil, graphInput{}); err != nil || out.Note == "" {
		t.Errorf("run_graph: err=%v out=%+v", err, out)
	}
	if _, out, err := q.verdictStatus(ctx(), nil, verdictInput{}); err != nil || out.CommitDenied {
		t.Errorf("verdict_status: err=%v out=%+v; batten does not gate a repo it does not govern", err, out)
	}
	if _, out, err := q.budget(ctx(), nil, budgetInput{}); err != nil || out.Declared {
		t.Errorf("budget: err=%v out=%+v", err, out)
	}
	if _, out, err := q.writeSetOwner(ctx(), nil, writeSetInput{Path: "a.go"}); err != nil || out.Owned {
		t.Errorf("writeset_owner: err=%v out=%+v", err, out)
	}
	if _, out, err := q.spec(ctx(), nil, specInput{}); err != nil || out.Note == "" {
		t.Errorf("spec: err=%v out=%+v", err, out)
	}
}

func TestEmptyDatabase(t *testing.T) {
	q := fixture(t) // spec, but nothing recorded

	_, runs, err := q.runs(ctx(), nil, runsInput{})
	if err != nil {
		t.Fatalf("an empty database is not an error: %v", err)
	}
	if len(runs.Runs) != 0 || runs.Note == "" {
		t.Errorf("runs = %+v, want empty with a note", runs)
	}
	_, vs, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-404"})
	if err != nil {
		t.Fatal(err)
	}
	if vs.CommitDenied {
		t.Error("with no run recorded the hook cannot attribute a commit, so it does not block")
	}
	if vs.HowToFix == "" || !strings.Contains(vs.HowToFix, "not an approval") {
		t.Errorf("how_to_fix = %q: silence here reads as approval, which it is not", vs.HowToFix)
	}
}

// batten refuses to guess which of several open runs the caller means: attributing one unit's
// budget or verdict to another is exactly the quiet wrongness the tool exists to prevent.
func TestResolveRunRefusesToGuess(t *testing.T) {
	q := fixture(t)
	seed(t, q)
	if _, err := q.st.EnsureRun("batten", "US-002", "sess-1"); err != nil {
		t.Fatal(err)
	}

	r, note, err := q.resolveRun("", "")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Fatalf("guessed run %s with two open", r.RunID)
	}
	if !strings.Contains(note, "US-001") || !strings.Contains(note, "US-002") {
		t.Errorf("note = %q: it must name the candidates so the caller can choose", note)
	}
}

func TestResolveRunByUnitFallsBackToClosedRun(t *testing.T) {
	q := fixture(t)
	r := seed(t, q)
	if err := q.st.CloseRun(r.RunID, "ok"); err != nil {
		t.Fatal(err)
	}

	got, note, err := q.resolveRun("", "US-001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.RunID != r.RunID {
		t.Fatalf("got %+v, want the closed run reported with a note", got)
	}
	if note == "" {
		t.Error("reporting a closed run silently would mislead: say so")
	}
}

func TestUnknownRunID(t *testing.T) {
	q := fixture(t)
	_, out, err := q.runGraph(ctx(), nil, graphInput{RunID: "nope"})
	if err != nil {
		t.Fatalf("an unknown run id is an answer, not an error: %v", err)
	}
	if out.Note == "" || len(out.Nodes) != 0 {
		t.Errorf("out = %+v", out)
	}
}

// Nothing may reach stdout: it carries the MCP framing. This asserts the shape of every
// result is JSON-serializable structured content and nothing more.
func TestOutputsAreSerializable(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	_, g, err := q.runGraph(ctx(), nil, graphInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	// Absence must survive the wire as null, never as a fabricated zero.
	if !strings.Contains(string(b), `"usage":null`) {
		t.Errorf("a node with no usage must serialize usage as null: %s", b)
	}
}
