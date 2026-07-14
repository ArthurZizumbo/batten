// Package mcp lets the agent interrogate its own run graph.
//
// The hooks in internal/hooks are the enforcement surface: they say NO, after the fact.
// This package is the other half — the surface that lets a model find out WHY it would be
// told no, before it walks into the wall. `/workflows` can show a plan; it cannot show the
// path actually taken, with its retries, its per-node token burn, and the one fact that
// decides whether the next `git commit` lands: does this unit have a verdict with evidence.
//
// Two constraints shape everything here:
//
//   - This runs as a stdio subprocess. stdout carries the MCP framing and NOTHING else.
//     A stray fmt.Println corrupts the stream and the client drops the server. All logging
//     goes to stderr; the SDK's default logger is a discard handler for the same reason.
//   - Absence is not zero. A ceiling we cannot sample is reported as unavailable, a node with
//     no usage rows reports null usage. A number the agent cannot trust is worse than no number,
//     because it will act on it.
//
// A missing batten.yaml or an empty database is a normal state, not an error: the tools return
// empty results with a note explaining why. The server must survive being pointed at a repo
// batten does not govern.
package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/arthu/batten/internal/spec"
	"github.com/arthu/batten/internal/store"
)

const version = "0.1.0"

// Serve runs the MCP server on stdio until the client disconnects.
//
// sp or st may be nil (no batten.yaml, or no database yet). That is deliberately not an
// error: the server still starts and every tool answers "batten does not govern this repo".
// A server that refuses to boot teaches the model nothing.
func Serve(sp *spec.Spec, st *store.Store) error {
	return newServer(sp, st).Run(context.Background(), &sdk.StdioTransport{})
}

func newServer(sp *spec.Spec, st *store.Store) *sdk.Server {
	q := &queries{sp: sp, st: st}

	s := sdk.NewServer(&sdk.Implementation{
		Name:    "batten",
		Title:   "batten — run graph, verdicts, budget",
		Version: version,
	}, &sdk.ServerOptions{
		// stderr, never stdout: stdout is the protocol.
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Instructions: "batten records what actually happened in this repo's workflow: the run graph " +
			"(phases, fanned-out subagents, retries), the verdict envelope that gates `git commit`, the " +
			"declared write-sets, and the token/budget ledger. Before committing a work item call " +
			"batten_verdict_status. Before writing a file as a fanned-out subagent call batten_writeset_owner. " +
			"To learn this project's process (phases, domains, invariants, checks) call batten_spec.",
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:  "batten_runs",
		Title: "List batten runs",
		Description: "List this project's work-item runs (newest first): status, current phase, unit id, " +
			"tokens spent, imputed USD, and whether a verdict exists with how many evidence items. " +
			"Call this to answer \"where did we leave off?\" or to find the run id for batten_run_graph.",
	}, q.runs)

	sdk.AddTool(s, &sdk.Tool{
		Name:  "batten_run_graph",
		Title: "Inspect one run's graph",
		Description: "Show one run's real execution graph: every node (phase, fanned-out subagent, gate) " +
			"with its status and per-node token usage, plus the TYPED edges between them " +
			"(spawn, depends_on, retry_of, rollback). This is the path actually taken — including retries " +
			"and rollbacks — which a static workflow diagram cannot show. Call it to see what a fan-out " +
			"really did, which subagent failed, and where the tokens went. Nodes with no recorded usage " +
			"report null, not zero.",
	}, q.runGraph)

	sdk.AddTool(s, &sdk.Tool{
		Name:  "batten_verdict_status",
		Title: "Can this work item be committed?",
		Description: "Check whether a work item can be committed. Call this BEFORE attempting a commit, or " +
			"after a commit was denied, to see exactly what the close gate requires. Reports whether a " +
			"verdict envelope exists, its result, its evidence items — and explicitly whether `git commit` " +
			"would be DENIED right now and why, with the concrete next step to unblock it. " +
			"An approval (result=ok) with an empty evidence[] is always denied.",
	}, q.verdictStatus)

	sdk.AddTool(s, &sdk.Tool{
		Name:  "batten_budget",
		Title: "Budget ceilings and where the run stands",
		Description: "Report the budget ceilings declared in batten.yaml (tokens per run, imputed USD, share " +
			"of the rolling 5-hour subscription quota) and how much of each this run has consumed. Call it " +
			"before starting an expensive fan-out, or when deciding whether to retry. A ceiling that cannot " +
			"be measured is reported as unavailable with the reason — never as zero.",
	}, q.budget)

	sdk.AddTool(s, &sdk.Tool{
		Name:  "batten_writeset_owner",
		Title: "Who owns this file in the current fan-out?",
		Description: "Given a file path, report which agent owns it in the active run's write-set. Call this " +
			"BEFORE writing a file when you are a fanned-out subagent: if another agent owns it, the " +
			"PreToolUse hook will DENY your Write/Edit. Two agents must never write the same file. " +
			"Pass your agent_id to also get back your own write-set.",
	}, q.writeSetOwner)

	sdk.AddTool(s, &sdk.Tool{
		Name:  "batten_spec",
		Title: "This project's declared process",
		Description: "Return the effective batten.yaml: the phases of the workflow, the domains a fan-out " +
			"splits into (each with its path, its invariants, and the check commands that must pass), the " +
			"gates and what evidence they demand, and the budget. Call this to learn how this project works " +
			"instead of asking the user to paste their process document.",
	}, q.spec)

	return s
}

// queries is the read-only view the tools share. Both fields are nilable: nil spec means
// no batten.yaml was found, nil store means no database. Every handler must survive both.
type queries struct {
	sp *spec.Spec
	st *store.Store
}

func (q *queries) governed() bool { return q.sp != nil && q.st != nil }

const notGoverned = "batten does not govern this repo: no batten.yaml was found (or no run database exists yet). " +
	"Run `batten init` to declare the workflow."

func (q *queries) project() string {
	if q.sp == nil {
		return ""
	}
	return q.sp.Project
}

// ---------- shared shapes ----------

type verdictBrief struct {
	Gate          string `json:"gate"`
	Result        string `json:"result" jsonschema:"ok | warn | blocked"`
	EvidenceCount int    `json:"evidence_count" jsonschema:"number of cited evidence items; result=ok with 0 is always denied"`
}

type verdictInfo struct {
	Gate                 string   `json:"gate"`
	CheckID              string   `json:"check_id"`
	Result               string   `json:"result" jsonschema:"ok | warn | blocked"`
	Evidence             []string `json:"evidence" jsonschema:"what the approval cites: command output, test counts, criteria verified"`
	EvidenceCount        int      `json:"evidence_count"`
	Why                  string   `json:"why"`
	SafeNextStep         string   `json:"safe_next_step"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	RecordedAt           string   `json:"recorded_at" jsonschema:"RFC3339"`
}

type runInfo struct {
	RunID      string        `json:"run_id"`
	Project    string        `json:"project"`
	Unit       string        `json:"unit" jsonschema:"the work-item id, e.g. US-034"`
	Phase      string        `json:"phase" jsonschema:"the phase the run is currently in"`
	Status     string        `json:"status" jsonschema:"running | ok | blocked | failed | rolled_back"`
	BaseSHA    string        `json:"base_sha" jsonschema:"the anchor commit the unit's diff is computed from"`
	Tokens     int64         `json:"tokens" jsonschema:"exact token count across every bucket, including cache reads"`
	ImputedUSD float64       `json:"imputed_usd" jsonschema:"what those tokens WOULD have cost on the API; not a bill"`
	Iterations int           `json:"iterations"`
	StartedAt  string        `json:"started_at" jsonschema:"RFC3339"`
	EndedAt    string        `json:"ended_at" jsonschema:"RFC3339, empty while the run is open"`
	Verdict    *verdictBrief `json:"verdict" jsonschema:"null means NO verdict has been recorded: a commit would be denied"`
}

func (q *queries) runInfo(r store.Run) runInfo {
	ri := runInfo{
		RunID: r.RunID, Project: r.Project, Unit: r.UnitID, Phase: r.Phase,
		Status: r.Status, BaseSHA: r.BaseSHA, Tokens: r.TokensSpent, ImputedUSD: r.ImputedUSD,
		Iterations: r.Iterations, StartedAt: rfc3339(r.StartedAt), EndedAt: rfc3339p(r.EndedAt),
	}
	if v, err := q.st.LatestVerdict(r.RunID, ""); err == nil {
		ri.Verdict = &verdictBrief{Gate: v.Gate, Result: v.Result, EvidenceCount: len(v.Evidence)}
	}
	return ri
}

// ---------- batten_runs ----------

type runsInput struct {
	Project string `json:"project,omitempty" jsonschema:"restrict to one project; defaults to the project declared in batten.yaml"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum runs to return, newest first (default 20)"`
	Status  string `json:"status,omitempty" jsonschema:"keep only runs with this status, e.g. running"`
}

type runsOutput struct {
	Project string    `json:"project"`
	Runs    []runInfo `json:"runs"`
	Note    string    `json:"note,omitempty" jsonschema:"present when the result is empty for a reason worth knowing"`
}

func (q *queries) runs(_ context.Context, _ *sdk.CallToolRequest, in runsInput) (*sdk.CallToolResult, runsOutput, error) {
	if !q.governed() {
		return nil, runsOutput{Runs: []runInfo{}, Note: notGoverned}, nil
	}
	proj := in.Project
	if proj == "" {
		proj = q.project()
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	rs, err := q.st.ListRuns(proj, limit)
	if err != nil {
		return nil, runsOutput{}, fmt.Errorf("reading runs: %w", err)
	}
	out := runsOutput{Project: proj, Runs: []runInfo{}}
	for _, r := range rs {
		if in.Status != "" && r.Status != in.Status {
			continue
		}
		out.Runs = append(out.Runs, q.runInfo(r))
	}
	if len(out.Runs) == 0 {
		out.Note = "no runs recorded yet for " + proj + "; batten records a run the first time a hook fires for a work item"
	}
	return nil, out, nil
}

// ---------- batten_run_graph ----------

type graphInput struct {
	RunID string `json:"run_id,omitempty" jsonschema:"the run to inspect; get one from batten_runs"`
	Unit  string `json:"unit,omitempty" jsonschema:"a work-item id such as US-034; resolves to that unit's open run"`
}

type usageInfo struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read"`
	CacheWrite   int64   `json:"cache_write"`
	TotalTokens  int64   `json:"total_tokens" jsonschema:"every bucket, cache reads included: they are cheap, not free"`
	ImputedUSD   float64 `json:"imputed_usd"`
}

type nodeInfo struct {
	NodeID    string     `json:"node_id"`
	Kind      string     `json:"kind" jsonschema:"phase | subagent | gate | tool"`
	Label     string     `json:"label"`
	Domain    string     `json:"domain" jsonschema:"the fan-out axis this node owns, if any"`
	Status    string     `json:"status" jsonschema:"running | ok | blocked | failed"`
	AgentID   string     `json:"agent_id"`
	AgentType string     `json:"agent_type"`
	StartedAt string     `json:"started_at" jsonschema:"RFC3339"`
	EndedAt   string     `json:"ended_at" jsonschema:"RFC3339, empty while the node is still running"`
	WriteSet  []string   `json:"write_set" jsonschema:"the files this node exclusively owns; no other agent may write them"`
	Usage     *usageInfo `json:"usage" jsonschema:"null means no usage was recorded for this node — not that it cost nothing"`
}

type edgeInfo struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel" jsonschema:"spawn | depends_on | retry_of | supersedes | rollback"`
}

type graphOutput struct {
	Run   *runInfo   `json:"run"`
	Nodes []nodeInfo `json:"nodes"`
	Edges []edgeInfo `json:"edges"`
	// Retries is the count of retry_of edges: the number of times the run had to redo work.
	// It is the single number that distinguishes the declared plan from the path actually taken.
	Retries int `json:"retries" jsonschema:"how many nodes were retries of an earlier node"`
	// UnattributedUsage holds token spend the transcript could not pin to a node. Reported
	// rather than folded into a node, so the per-node numbers stay honest.
	UnattributedUsage *usageInfo `json:"unattributed_usage" jsonschema:"usage that could not be attributed to any node; null if none"`
	Note              string     `json:"note,omitempty"`
}

func (q *queries) runGraph(_ context.Context, _ *sdk.CallToolRequest, in graphInput) (*sdk.CallToolResult, graphOutput, error) {
	empty := graphOutput{Nodes: []nodeInfo{}, Edges: []edgeInfo{}}
	if !q.governed() {
		empty.Note = notGoverned
		return nil, empty, nil
	}
	r, note, err := q.resolveRun(in.RunID, in.Unit)
	if err != nil {
		return nil, empty, err
	}
	if r == nil {
		empty.Note = note
		return nil, empty, nil
	}

	nodes, err := q.st.Nodes(r.RunID)
	if err != nil {
		return nil, empty, fmt.Errorf("reading nodes: %w", err)
	}
	edges, err := q.st.Edges(r.RunID)
	if err != nil {
		return nil, empty, fmt.Errorf("reading edges: %w", err)
	}
	byNode, err := q.st.UsageByNode(r.RunID)
	if err != nil {
		return nil, empty, fmt.Errorf("reading usage: %w", err)
	}

	ri := q.runInfo(*r)
	out := graphOutput{Run: &ri, Nodes: []nodeInfo{}, Edges: []edgeInfo{}, Note: note}

	for _, n := range nodes {
		ni := nodeInfo{
			NodeID: n.NodeID, Kind: n.Kind, Label: n.Label, Domain: n.Domain, Status: n.Status,
			AgentID: n.AgentID, AgentType: n.AgentType,
			StartedAt: rfc3339(n.StartedAt), EndedAt: rfc3339p(n.EndedAt),
			WriteSet: []string{},
		}
		if ws, err := q.st.WriteSet(r.RunID, n.NodeID); err == nil && len(ws) > 0 {
			ni.WriteSet = ws
		}
		if u, ok := byNode[n.NodeID]; ok {
			ni.Usage = toUsage(u)
		}
		out.Nodes = append(out.Nodes, ni)
	}
	for _, e := range edges {
		out.Edges = append(out.Edges, edgeInfo{From: e.Src, To: e.Dst, Rel: e.Rel})
		if e.Rel == "retry_of" {
			out.Retries++
		}
	}
	// Sort for a stable, readable answer: edges come out of SQLite in insertion order,
	// which is an artifact of hook timing, not of the graph.
	sort.SliceStable(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		return out.Edges[i].To < out.Edges[j].To
	})
	if u, ok := byNode[""]; ok && u.Tokens() > 0 {
		out.UnattributedUsage = toUsage(u)
	}
	if len(out.Nodes) == 0 && out.Note == "" {
		out.Note = "the run exists but has no nodes yet: no phase or subagent hook has fired for it"
	}
	return nil, out, nil
}

func toUsage(u store.Usage) *usageInfo {
	return &usageInfo{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CacheRead:    u.CacheRead,
		CacheWrite:   u.CacheWrite5m + u.CacheWrite1h,
		TotalTokens:  u.Tokens(),
		ImputedUSD:   u.ImputedUSD,
	}
}

// ---------- batten_verdict_status ----------

type verdictInput struct {
	Unit  string `json:"unit,omitempty" jsonschema:"the work-item id, e.g. US-034; omit to use the only open run"`
	RunID string `json:"run_id,omitempty" jsonschema:"inspect a specific run instead of the unit's open one"`
}

type verdictOutput struct {
	Unit  string `json:"unit"`
	RunID string `json:"run_id"`
	Gate  string `json:"gate" jsonschema:"the gate the closing phase must satisfy; empty if the spec gates nothing"`

	// CommitDenied is the whole point of this tool: the answer to "if I run git commit now,
	// does the PreToolUse hook stop me?" Everything else is the explanation.
	CommitDenied bool   `json:"commit_denied" jsonschema:"true if a git commit would be DENIED right now by the batten PreToolUse hook"`
	DenyReason   string `json:"deny_reason" jsonschema:"exactly why it would be denied; empty when it would not be"`
	HowToFix     string `json:"how_to_fix" jsonschema:"the concrete next step that clears the gate"`

	Requirement string   `json:"requirement" jsonschema:"what the close gate demands of a verdict"`
	GateChecks  []string `json:"gate_checks" jsonschema:"the checks this gate declares; their output is the evidence"`

	HasVerdict bool         `json:"has_verdict"`
	Verdict    *verdictInfo `json:"verdict" jsonschema:"null when no verdict envelope has been recorded"`
	Overridden bool         `json:"overridden" jsonschema:"true if the gate was explicitly overridden for this run; the override is on the record"`
	Note       string       `json:"note,omitempty"`
}

// verdictStatus mirrors the decision in hooks.verdictGate. It must stay faithful to it:
// a tool that says "you are fine" and then the hook denies the commit is worse than no tool
// at all, because the agent will trust it. Any change to the gate belongs in BOTH places.
func (q *queries) verdictStatus(_ context.Context, _ *sdk.CallToolRequest, in verdictInput) (*sdk.CallToolResult, verdictOutput, error) {
	out := verdictOutput{GateChecks: []string{}}
	if !q.governed() {
		out.Note = notGoverned
		return nil, out, nil
	}

	closing, gated := q.sp.ClosingPhase()
	gate := q.gateName(closing)
	out.Gate = gate
	if g, ok := q.sp.Gates[gate]; ok {
		if g.Checks != nil {
			out.GateChecks = g.Checks
		}
		out.Requirement = fmt.Sprintf("phase %q requires a verdict with result=%q", closing.ID, closing.RequiresVerdict)
		if g.EvidenceRequired() {
			out.Requirement += " and a non-empty evidence[]: an approval must cite something (command output, test counts, criteria verified)"
		}
	}

	r, note, err := q.resolveRun(in.RunID, in.Unit)
	if err != nil {
		return nil, out, err
	}
	if r == nil {
		out.Unit = in.Unit
		out.Note = note
		// No run means the hook cannot attribute a commit to a unit, so it does not block.
		// Say so plainly rather than implying the work is approved.
		out.HowToFix = "no run is recorded, so the gate has nothing to enforce and will not block a commit. " +
			"That is not an approval: nothing has been verified."
		return nil, out, nil
	}
	out.Unit, out.RunID = r.UnitID, r.RunID

	if !gated || closing.RequiresVerdict == "" {
		out.Note = "batten.yaml declares no closing phase with requires_verdict, so the commit gate is not armed"
		return nil, out, nil
	}

	if ov, err := q.st.HasOverride(r.RunID, gate); err == nil && ov {
		out.Overridden = true
		out.HowToFix = "the gate is overridden for this run; the commit will pass and the override is in the audit log"
	}

	v, err := q.st.LatestVerdict(r.RunID, gate)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// absent verdict: leave Verdict nil.
	case err != nil:
		return nil, out, fmt.Errorf("reading verdict: %w", err)
	default:
		out.HasVerdict = true
		out.Verdict = &verdictInfo{
			Gate: v.Gate, CheckID: v.CheckID, Result: v.Result,
			Evidence: v.Evidence, EvidenceCount: len(v.Evidence),
			Why: v.Why, SafeNextStep: v.SafeNextStep,
			RequiresConfirmation: v.RequiresConfirmation,
			RecordedAt:           rfc3339(v.TS),
		}
		if out.Verdict.Evidence == nil {
			out.Verdict.Evidence = []string{}
		}
	}

	if out.Overridden {
		return nil, out, nil
	}

	switch {
	case !out.HasVerdict:
		out.CommitDenied = true
		out.DenyReason = fmt.Sprintf("%s has no verdict envelope. The close gate %q requires one.", r.UnitID, gate)
		out.HowToFix = fmt.Sprintf("run the %q phase: execute the gate's checks and record a verdict "+
			"whose evidence[] cites their output. To proceed anyway (recorded in the audit log): "+
			"batten override %s --reason \"...\"", closing.ID, r.UnitID)
		return nil, out, nil

	case out.Verdict.Result == "ok" && out.Verdict.EvidenceCount == 0:
		out.CommitDenied = true
		out.DenyReason = fmt.Sprintf("%s has result=ok but an empty evidence[]. %s", r.UnitID, store.ErrNoEvidence)
		out.HowToFix = "re-run the gate's checks and record the verdict again, citing their output in evidence[]"
		return nil, out, nil

	case out.Verdict.Result != closing.RequiresVerdict && out.Verdict.Result != "ok":
		out.CommitDenied = true
		out.DenyReason = fmt.Sprintf("%s verdict is %q, not %q. %s", r.UnitID,
			out.Verdict.Result, closing.RequiresVerdict, out.Verdict.Why)
		out.HowToFix = out.Verdict.SafeNextStep
		if out.HowToFix == "" {
			out.HowToFix = "fix what the verdict flags, then record a new verdict"
		}
		return nil, out, nil
	}

	// Budget is also a closing condition: a run that blew its ceiling should not quietly land.
	if q.sp.Budget.OnExceed == "block" && q.sp.Budget.Set() {
		b := q.sp.Budget
		over, cs, err := q.st.OverBudget(r.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
		if err == nil && over {
			out.CommitDenied = true
			out.DenyReason = fmt.Sprintf("%s is over budget and budget.on_exceed=block: %s",
				r.UnitID, exceededSummary(cs))
			out.HowToFix = fmt.Sprintf("raise the ceiling in batten.yaml, or proceed on the record: "+
				"batten override %s --reason \"...\"", r.UnitID)
			return nil, out, nil
		}
	}

	out.HowToFix = "nothing to fix: the verdict clears the gate and the commit will be allowed"
	return nil, out, nil
}

func exceededSummary(cs []store.Ceiling) string {
	var parts []string
	for _, c := range cs {
		if c.Exceeded {
			parts = append(parts, fmt.Sprintf("%s %.2f of %.2f", c.Kind, c.Spent, c.Cap))
		}
	}
	return strings.Join(parts, "; ")
}

// gateName resolves which gate the commit gate enforces, exactly as hooks does: the closing
// phase's gate, or failing that whichever gate any phase demands.
func (q *queries) gateName(closing spec.Phase) string {
	if closing.Gate != "" {
		return closing.Gate
	}
	name := ""
	for _, p := range q.sp.Phases {
		if p.Gate != "" {
			name = p.Gate
		}
	}
	return name
}

// ---------- batten_budget ----------

type budgetInput struct {
	Unit  string `json:"unit,omitempty" jsonschema:"the work-item id whose open run to report on"`
	RunID string `json:"run_id,omitempty" jsonschema:"report on a specific run instead"`
}

type ceilingInfo struct {
	Kind string  `json:"kind" jsonschema:"tokens | imputed_usd | quota_pct"`
	Cap  float64 `json:"cap" jsonschema:"the ceiling declared in batten.yaml"`
	// Spent and Remaining are nullable ON PURPOSE. A ceiling batten cannot sample reports
	// null, not zero: a budget tool that invents a number is worse than no budget tool,
	// because the agent will spend against it.
	Spent             *float64 `json:"spent" jsonschema:"null when this ceiling cannot be measured — NOT zero"`
	Remaining         *float64 `json:"remaining" jsonschema:"null when this ceiling cannot be measured"`
	Exceeded          bool     `json:"exceeded"`
	Available         bool     `json:"available" jsonschema:"false means the ceiling is declared but NOT enforced, because it cannot be measured"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type budgetOutput struct {
	RunID         string        `json:"run_id"`
	Unit          string        `json:"unit"`
	Declared      bool          `json:"declared" jsonschema:"false when batten.yaml declares no budget: nothing is enforced"`
	OnExceed      string        `json:"on_exceed" jsonschema:"block | warn | downgrade_effort"`
	MaxIterations int           `json:"max_iterations"`
	Iterations    int           `json:"iterations"`
	Ceilings      []ceilingInfo `json:"ceilings"`
	Note          string        `json:"note,omitempty"`
}

func (q *queries) budget(_ context.Context, _ *sdk.CallToolRequest, in budgetInput) (*sdk.CallToolResult, budgetOutput, error) {
	out := budgetOutput{Ceilings: []ceilingInfo{}}
	if !q.governed() {
		out.Note = notGoverned
		return nil, out, nil
	}
	b := q.sp.Budget
	out.OnExceed, out.MaxIterations, out.Declared = b.OnExceed, b.MaxIterations, b.Set()

	r, note, err := q.resolveRun(in.RunID, in.Unit)
	if err != nil {
		return nil, out, err
	}
	if r == nil {
		out.Note = note
		return nil, out, nil
	}
	out.RunID, out.Unit, out.Iterations, out.Note = r.RunID, r.UnitID, r.Iterations, note

	if !b.Set() {
		out.Note = joinNotes(note, "batten.yaml declares no budget ceiling, so nothing is enforced. "+
			"This run has spent "+fmt.Sprintf("%d tokens (~$%.2f imputed).", r.TokensSpent, r.ImputedUSD))
		return nil, out, nil
	}

	cs, err := q.st.Budget(r.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
	if err != nil {
		return nil, out, fmt.Errorf("reading budget: %w", err)
	}
	for _, c := range cs {
		ci := ceilingInfo{Kind: c.Kind, Cap: c.Cap, Exceeded: c.Exceeded, Available: c.Available}
		if c.Available {
			spent, remaining := c.Spent, c.Cap-c.Spent
			ci.Spent, ci.Remaining = &spent, &remaining
		} else {
			ci.UnavailableReason = unavailableReason(c.Kind)
		}
		out.Ceilings = append(out.Ceilings, ci)
	}
	return nil, out, nil
}

// unavailableReason explains WHY a ceiling cannot be measured, so the agent can either fix it
// or knowingly proceed without it. "unavailable" with no reason is just a shrug.
func unavailableReason(kind string) string {
	if kind == "quota_pct" {
		return "the share of the rolling 5-hour subscription window can only be sampled by " +
			"`batten statusline` (hooks never see quota). It is not installed, or it has not sampled " +
			"since this run opened, or the window rolled over mid-run. This ceiling is declared but NOT enforced."
	}
	return "batten has no measurement for this ceiling, so it is declared but NOT enforced"
}

// ---------- batten_writeset_owner ----------

type writeSetInput struct {
	Path    string `json:"path" jsonschema:"the file to check, absolute or relative to the repo root"`
	RunID   string `json:"run_id,omitempty" jsonschema:"defaults to the active run"`
	AgentID string `json:"agent_id,omitempty" jsonschema:"your agent id; pass it to learn whether YOU own this file and what else is yours"`
}

type writeSetOutput struct {
	Path           string   `json:"path" jsonschema:"repo-relative, slash-normalized"`
	RunID          string   `json:"run_id"`
	Owned          bool     `json:"owned" jsonschema:"false means no agent has claimed this file: writing it is allowed"`
	OwnerNode      string   `json:"owner_node"`
	OwnerLabel     string   `json:"owner_label"`
	OwnerAgentID   string   `json:"owner_agent_id"`
	OwnerAgentType string   `json:"owner_agent_type"`
	OwnedByYou     bool     `json:"owned_by_you" jsonschema:"only meaningful when agent_id was supplied"`
	WriteAllowed   bool     `json:"write_allowed" jsonschema:"false means a Write/Edit here would be DENIED by the write-set guard"`
	YourWriteSet   []string `json:"your_write_set" jsonschema:"the files you own, when agent_id was supplied"`
	Domain         string   `json:"domain" jsonschema:"the batten.yaml domain this path falls under, if any"`
	Note           string   `json:"note,omitempty"`
}

func (q *queries) writeSetOwner(_ context.Context, _ *sdk.CallToolRequest, in writeSetInput) (*sdk.CallToolResult, writeSetOutput, error) {
	out := writeSetOutput{YourWriteSet: []string{}, WriteAllowed: true}
	if !q.governed() {
		out.Note = notGoverned
		return nil, out, nil
	}
	rel, err := q.relPath(in.Path)
	if err != nil {
		out.Path = filepath.ToSlash(in.Path)
		out.Note = "that path is outside the repo, so no write-set governs it"
		return nil, out, nil
	}
	out.Path = rel
	if d, ok := q.sp.DomainFor(rel); ok {
		out.Domain = d
	}

	r, note, err := q.resolveRun(in.RunID, "")
	if err != nil {
		return nil, out, err
	}
	if r == nil {
		out.Note = joinNotes(note, "with no active run there are no write-set claims, so nothing is enforced")
		return nil, out, nil
	}
	out.RunID = r.RunID

	var mine string // the caller's node, when they told us who they are
	if in.AgentID != "" {
		if n, err := q.st.NodeByAgent(in.AgentID); err == nil {
			mine = n.NodeID
			if ws, err := q.st.WriteSet(n.RunID, n.NodeID); err == nil && len(ws) > 0 {
				out.YourWriteSet = ws
			}
		} else {
			out.Note = joinNotes(out.Note, "no node is registered for agent_id "+in.AgentID+
				"; you have no declared write-set, so the guard will not fence you")
		}
	}

	owner, err := q.st.WriteSetOwner(r.RunID, rel)
	if err != nil {
		return nil, out, fmt.Errorf("reading write-set: %w", err)
	}
	if owner == "" {
		out.Note = joinNotes(out.Note, "unclaimed: no agent owns this file in run "+r.RunID)
		return nil, out, nil
	}

	out.Owned, out.OwnerNode = true, owner
	if nodes, err := q.st.Nodes(r.RunID); err == nil {
		for _, n := range nodes {
			if n.NodeID == owner {
				out.OwnerLabel, out.OwnerAgentID, out.OwnerAgentType = n.Label, n.AgentID, n.AgentType
				break
			}
		}
	}
	out.OwnedByYou = mine != "" && mine == owner
	// Only a fanned-out agent is fenced: the guard keys off agent_id, so the orchestrator
	// (which has none) is never denied. Reporting otherwise would be a lie.
	out.WriteAllowed = in.AgentID == "" || out.OwnedByYou || mine == ""
	if !out.WriteAllowed {
		out.Note = joinNotes(out.Note, fmt.Sprintf(
			"%s belongs to %s. A Write/Edit from you would be DENIED. Two agents must never write the "+
				"same file — that is what makes the fan-out safe. If this file genuinely belongs to you, "+
				"the plan is wrong: fix the plan, do not cross the fence.", rel, owner))
	}
	return nil, out, nil
}

// relPath normalizes a caller-supplied path to the repo-relative, forward-slash form the
// write-set table is keyed by. Windows paths, absolute paths and ./ prefixes all land here.
func (q *queries) relPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(q.sp.Root, p)
		if err != nil {
			return "", err
		}
		p = rel
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", errors.New("path is outside the repo root")
	}
	return p, nil
}

// ---------- batten_spec ----------

type specInput struct{}

type phaseSpec struct {
	ID              string   `json:"id"`
	Optional        bool     `json:"optional"`
	Interactive     bool     `json:"interactive"`
	Fanout          bool     `json:"fanout" jsonschema:"true means this phase spawns one subagent per domain, with disjoint write-sets"`
	Reads           []string `json:"reads" jsonschema:"artifacts of prior phases this phase consumes"`
	Gate            string   `json:"gate" jsonschema:"the gate this phase must emit a verdict for"`
	RequiresVerdict string   `json:"requires_verdict" jsonschema:"non-empty means this is the HARD gate: a commit is denied without it"`
	When            string   `json:"when"`
}

type domainSpec struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Exclude    []string `json:"exclude"`
	Rules      string   `json:"rules" jsonschema:"the AGENTS.md/CLAUDE.md governing this domain"`
	Check      []string `json:"check" jsonschema:"commands that must pass before this domain's agent may finish; their output is evidence"`
	Invariants []string `json:"invariants" jsonschema:"the rules a reviewer would catch and a distracted agent would break. Obey them verbatim."`
	Agent      string   `json:"agent"`
	Skills     []string `json:"skills"`
	Coverage   int      `json:"coverage"`
	Resources  []string `json:"resources" jsonschema:"scarce resources this domain contends for; they serialize the fan-out"`
}

type gateSpec struct {
	Name             string   `json:"name"`
	Checks           []string `json:"checks"`
	Skills           []string `json:"skills"`
	VerdictRequired  bool     `json:"verdict_required"`
	EvidenceRequired bool     `json:"evidence_required" jsonschema:"true means result=ok with an empty evidence[] is denied"`
}

type budgetSpec struct {
	TokensPerRun     int64   `json:"tokens_per_run"`
	ImputedUSDPerRun float64 `json:"imputed_usd_per_run" jsonschema:"what the tokens WOULD cost on the API; on a subscription this is value pulled, not money spent"`
	QuotaPctPerRun   float64 `json:"quota_pct_per_run" jsonschema:"share of the rolling 5-hour window; requires the batten statusline to be installed, or it is not enforced"`
	MaxIterations    int     `json:"max_iterations"`
	OnExceed         string  `json:"on_exceed" jsonschema:"block | warn | downgrade_effort"`
}

type specOutput struct {
	Project     string            `json:"project"`
	Root        string            `json:"root"`
	UnitName    string            `json:"unit_name" jsonschema:"the work-item noun this project uses: US, ticket, issue..."`
	UnitPattern string            `json:"unit_pattern" jsonschema:"regexp identifying a work-item id"`
	UnitPlan    string            `json:"unit_plan" jsonschema:"the canonical plan document holding the work-item blocks"`
	Artifacts   map[string]string `json:"artifacts" jsonschema:"artifact kind -> path template, with {id} standing for the work-item id"`
	Phases      []phaseSpec       `json:"phases" jsonschema:"the workflow, in order"`
	Domains     []domainSpec      `json:"domains" jsonschema:"the fan-out axes: one subagent per domain, disjoint write-sets"`
	Gates       []gateSpec        `json:"gates"`
	Budget      budgetSpec        `json:"budget"`
	Note        string            `json:"note,omitempty"`
}

func (q *queries) spec(_ context.Context, _ *sdk.CallToolRequest, _ specInput) (*sdk.CallToolResult, specOutput, error) {
	out := specOutput{
		Artifacts: map[string]string{},
		Phases:    []phaseSpec{},
		Domains:   []domainSpec{},
		Gates:     []gateSpec{},
	}
	if q.sp == nil {
		out.Note = "no batten.yaml was found: this repo has no declared process. Run `batten init` to create one."
		return nil, out, nil
	}
	s := q.sp
	out.Project, out.Root = s.Project, filepath.ToSlash(s.Root)
	out.UnitName, out.UnitPattern, out.UnitPlan = s.Unit.Name, s.Unit.Pattern, s.Unit.Plan
	for k, v := range s.Artifacts {
		out.Artifacts[k] = v
	}
	for _, p := range s.Phases {
		out.Phases = append(out.Phases, phaseSpec{
			ID: p.ID, Optional: p.Optional, Interactive: p.Interactive, Fanout: p.Fanout,
			Reads: orEmpty(p.Reads), Gate: p.Gate, RequiresVerdict: p.RequiresVerdict, When: p.When,
		})
	}
	// Maps iterate in random order; sort so two calls give the same answer and a model
	// diffing them is not misled by noise.
	for _, name := range sortedKeys(s.Domains) {
		d := s.Domains[name]
		out.Domains = append(out.Domains, domainSpec{
			Name: name, Path: d.Path, Exclude: orEmpty(d.Exclude), Rules: d.Rules,
			Check: orEmpty(d.Check), Invariants: orEmpty(d.Invariants), Agent: d.Agent,
			Skills: orEmpty(d.Skills), Coverage: d.Coverage, Resources: orEmpty(d.Resources),
		})
	}
	for _, name := range sortedKeys(s.Gates) {
		g := s.Gates[name]
		out.Gates = append(out.Gates, gateSpec{
			Name: name, Checks: orEmpty(g.Checks), Skills: orEmpty(g.Skills),
			VerdictRequired: g.Verdict == "required", EvidenceRequired: g.EvidenceRequired(),
		})
	}
	out.Budget = budgetSpec{
		TokensPerRun: s.Budget.TokensPerRun, ImputedUSDPerRun: s.Budget.ImputedUSDPerRun,
		QuotaPctPerRun: s.Budget.QuotaPctPerRun, MaxIterations: s.Budget.MaxIterations,
		OnExceed: s.Budget.OnExceed,
	}
	return nil, out, nil
}

// ---------- run resolution ----------

// resolveRun picks the run a tool should answer about. Returning (nil, note, nil) is a normal
// outcome, not a failure: "no run yet" and "which of the three open runs did you mean?" are
// both answers the agent can act on.
//
// When nothing is specified we take the single open run — and refuse to guess when there is
// more than one. Guessing here would attribute one unit's budget and verdict to another, which
// is precisely the class of quiet wrongness batten exists to eliminate.
func (q *queries) resolveRun(runID, unit string) (*store.Run, string, error) {
	if runID != "" {
		r, err := q.st.Run(runID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "no run with id " + runID, nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("reading run %s: %w", runID, err)
		}
		return r, "", nil
	}

	if unit != "" {
		r, err := q.st.ActiveRun(q.project(), unit)
		if err == nil {
			return r, "", nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, "", fmt.Errorf("reading active run for %s: %w", unit, err)
		}
		// No OPEN run: fall back to the most recent closed one, and say so, rather than
		// pretending the unit never existed.
		rs, err := q.st.ListRuns(q.project(), 50)
		if err != nil {
			return nil, "", fmt.Errorf("reading runs: %w", err)
		}
		for _, r := range rs {
			if r.UnitID == unit {
				return &r, fmt.Sprintf("%s has no open run; reporting its most recent one (status %s)", unit, r.Status), nil
			}
		}
		return nil, "no run has been recorded for " + unit, nil
	}

	rs, err := q.st.ListRuns(q.project(), 50)
	if err != nil {
		return nil, "", fmt.Errorf("reading runs: %w", err)
	}
	var open []store.Run
	for _, r := range rs {
		if r.Status == "running" {
			open = append(open, r)
		}
	}
	switch len(open) {
	case 0:
		return nil, "no run is currently open. Pass unit or run_id, or list them with batten_runs.", nil
	case 1:
		return &open[0], "", nil
	default:
		var ids []string
		for _, r := range open {
			ids = append(ids, r.UnitID)
		}
		return nil, "several runs are open (" + strings.Join(ids, ", ") +
			"): pass unit or run_id. batten will not guess which one you mean.", nil
	}
}

// ---------- small helpers ----------

func rfc3339(t int64) string {
	if t == 0 {
		return ""
	}
	return time.Unix(t, 0).UTC().Format(time.RFC3339)
}

func rfc3339p(t *int64) string {
	if t == nil {
		return ""
	}
	return rfc3339(*t)
}

// orEmpty keeps nil slices out of the JSON: a tool result showing "invariants": null reads as
// "unknown", while [] reads as "there are none". They are different facts.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}
