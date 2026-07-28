// Package hooks implements batten's enforcement surface.
//
// This is where prose becomes mechanism. Two rules that a document can only ask for,
// a PreToolUse hook can actually impose:
//
//  1. verdict gate    — a commit without cited evidence is denied.
//  2. write-set guard — an agent writing another agent's file is denied.
//
// The hook payload arrives as JSON on stdin and the decision leaves as JSON on stdout.
// No jq, no curl, no bash: the binary does it. (engram's hooks shell out, which is why
// they are fragile on Windows.)
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/export"
	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
	"github.com/ArthurZizumbo/batten/internal/usage"
)

// Input is the common hook payload. Fields absent for a given event stay zero.
type Input struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode"`

	// PreToolUse / PostToolUse
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`

	// SubagentStart / SubagentStop
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`

	// SubagentStop / Stop
	LastAssistantMessage string `json:"last_assistant_message"`

	// SessionStart
	Source string `json:"source"`
}

// Output is what Claude Code reads back from us.
type Output struct {
	Continue      *bool         `json:"continue,omitempty"`
	SystemMessage string        `json:"systemMessage,omitempty"`
	HookSpecific  *HookSpecific `json:"hookSpecificOutput,omitempty"`
}

type HookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"` // allow|deny|ask
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

func allow(event string) *Output { return nil } // no output == no opinion

func deny(event, reason string) *Output {
	return &Output{HookSpecific: &HookSpecific{
		HookEventName:            event,
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}
}

func context(event, ctx string) *Output {
	return &Output{HookSpecific: &HookSpecific{
		HookEventName:     event,
		AdditionalContext: ctx,
	}}
}

// Handler carries everything a hook needs. A nil Spec or Store means batten is not
// configured for this repo — every hook then no-ops. Never break a session you
// were not asked to govern.
type Handler struct {
	Spec  *spec.Spec
	Store *store.Store
	// TapPath, when set and its ".on" flag exists, receives a copy of every raw payload.
	// This is the E0 instrument for the agent_id question; empty in normal operation.
	TapPath string
}

// gate turns a gate decision into the right output for the current enforcement mode.
// In "enforce" it denies; in "report" it lets the tool through but surfaces the reason as a
// visible warning — the adoption ramp, so a project mid-sprint isn't blocked on day one.
func (h *Handler) gate(event, reason string) *Output {
	if h.Spec.ReportOnly() {
		return advise(event, reason)
	}
	return deny(event, reason)
}

// advise is the warning form: the tool call proceeds, but the reason lands both in front of
// the user (systemMessage) and in the model's context. Used by report mode, and by any check
// that cannot attribute blame well enough to justify a hard deny.
func advise(event, reason string) *Output {
	return &Output{
		SystemMessage: "batten (warning — not blocking): " + firstLine(reason),
		HookSpecific: &HookSpecific{
			HookEventName:     event,
			AdditionalContext: "batten warning:\n" + reason,
		},
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// tap appends the raw payload to the tap file when the tap flag is present. Best-effort and
// silent: a debug instrument must never affect whether a hook allows or denies.
func tap(path string, raw []byte) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path + ".on"); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(bytesTrimNL(raw), '\n'))
}

func bytesTrimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// truncateBytes caps a payload for the event log. The cut is marked so a reader of the
// replay log knows it is looking at a prefix, not the whole event.
func truncateBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return append(append([]byte{}, b[:n]...), []byte(`...[truncated by batten]`)...)
}

// Dispatch routes one hook event. Returns the JSON to print (or nil for silence).
func (h *Handler) Dispatch(event string, raw []byte) (*Output, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("bad hook payload: %w", err)
	}
	if in.HookEventName == "" {
		in.HookEventName = event
	}
	tap(h.TapPath, raw) // E0: capture the real payload when a debug tap is active

	if h.Spec == nil || h.Store == nil {
		return nil, nil // not a batten repo: stay out of the way
	}

	// The replay log wants every event, not every byte: a Write tool's payload embeds the
	// whole file body, and this INSERT sits on the fast path of every tool call in every
	// session sharing the DB. 4KB keeps the log diagnostic (event shape, ids, decisions)
	// without turning the events table into a mirror of the working tree.
	_ = h.Store.LogEvent("", "", event, truncateBytes(raw, 4096))

	switch event {
	case "PreToolUse":
		return h.preToolUse(in)
	case "PostToolUse":
		return h.postToolUse(in)
	case "SubagentStart":
		return h.subagentStart(in)
	case "SubagentStop":
		return h.subagentStop(in)
	case "SessionStart":
		return h.sessionStart(in)
	case "Stop":
		return h.stop(in)
	}
	return nil, nil
}

// phaseRe catches `batten phase <UNIT> ...` in a shell command, so the session that ran it can
// be bound to that unit's run. The CLI subprocess never sees session_id; this hook does.
var phaseRe = regexp.MustCompile(`\bbatten(?:\.exe)?\s+phase\s+(\S+)`)

// postToolUse binds a session to the unit it just started working. This is the load-bearing
// step for multi-session: once `batten phase US-034` runs, THIS session owns that run, and
// activeUnit resolves it decisively for every later gate — no more guessing from a shared branch.
func (h *Handler) postToolUse(in Input) (*Output, error) {
	if in.ToolName != "Bash" || in.SessionID == "" {
		return nil, nil
	}
	var bi bashInput
	if json.Unmarshal(in.ToolInput, &bi) != nil {
		return nil, nil
	}
	if m := phaseRe.FindStringSubmatch(bi.Command); m != nil {
		if unit := h.Spec.MatchUnit(m[1]); unit != "" {
			if r, err := h.Store.ActiveRun(h.Spec.Project, unit); err == nil {
				_ = h.Store.AdoptSession(r.RunID, in.SessionID)
			}
		}
		return nil, nil
	}

	// A successful commit is the natural end of a run: close it so its write-set claims release
	// instead of lingering and denying edits forever. The PreToolUse gate already let this commit
	// through, so if we reach here the verdict was ok (or overridden) — either way it's done.
	if commitRe.MatchString(bi.Command) {
		unit := h.activeUnit(in)
		if unit == "" {
			return nil, nil
		}
		closing, ok := h.Spec.ClosingPhase()
		if !ok {
			return nil, nil
		}
		if r, err := h.Store.ActiveRun(h.Spec.Project, unit); err == nil && r.Phase == closing.ID {
			_ = h.Store.CloseRun(r.RunID, "ok")
		}
	}
	return nil, nil
}

// ---------- PreToolUse: the two gates ----------

type bashInput struct {
	Command string `json:"command"`
}
type writeInput struct {
	FilePath string `json:"file_path"`
}

func (h *Handler) preToolUse(in Input) (*Output, error) {
	switch in.ToolName {
	case "Bash":
		var bi bashInput
		if err := json.Unmarshal(in.ToolInput, &bi); err != nil {
			return nil, nil
		}
		return h.verdictGate(in, bi.Command)
	case "Write", "Edit", "NotebookEdit":
		var wi writeInput
		if err := json.Unmarshal(in.ToolInput, &wi); err != nil {
			return nil, nil
		}
		return h.writeSetGuard(in, wi.FilePath)
	}
	return nil, nil
}

// commitRe matches a git commit anywhere in a (possibly compound) shell command.
//
// The hard part is everything git allows between the binary and the subcommand. An option
// whose value is glued on (`--git-dir=.git`) is one token, but `-c user.email=a@b.c` and
// `-C /path` are two, and the first spelling is the standard non-interactive commit an agent
// or CI issues. A pattern that stops at the value token stops seeing the commit — the gate
// then opens for the exact caller it was built to govern, silently and with no audit record.
// So each option may carry one separate value, and `git.exe` is spelled out because this is
// a Windows-first tool and the sibling phaseRe already handles `batten.exe`.
var commitRe = regexp.MustCompile(`(^|[;&|]|\s)git(\.exe)?\s+((-{1,2}[^\s=]+(=[^\s]*)?)(\s+[^\s-][^\s]*)?\s+)*commit\b`)

// verdictGate is wedge #1. The golden rule of the workflow doc — "never approve with an
// empty evidence[]" — is a request a model can ignore. Here it is a denial it cannot.
func (h *Handler) verdictGate(in Input, cmd string) (*Output, error) {
	if !commitRe.MatchString(cmd) {
		return nil, nil
	}
	closing, ok := h.Spec.ClosingPhase()
	if !ok || closing.RequiresVerdict == "" {
		return nil, nil // spec does not gate closing
	}

	unit := h.activeUnit(in)
	if unit == "" {
		return nil, nil // cannot attribute this commit to a unit: do not block
	}
	run, err := h.Store.ActiveRun(h.Spec.Project, unit)
	if err != nil {
		return nil, nil // no run recorded: the gate has nothing to say
	}

	if ok, _ := h.Store.HasOverride(run.RunID, closing.Gate); ok {
		return nil, nil // explicitly overridden, and it is on the record
	}

	gateName := closing.Gate
	if gateName == "" {
		// The closing phase does not name a gate; use whichever gate any phase demands.
		for _, p := range h.Spec.Phases {
			if p.Gate != "" {
				gateName = p.Gate
			}
		}
	}

	v, err := h.Store.LatestVerdict(run.RunID, gateName)
	if err != nil {
		return h.gate("PreToolUse", fmt.Sprintf(
			"batten: %s has no verdict envelope. Run the %q phase before committing.\n"+
				"To proceed anyway (recorded in the audit log): batten override %s --reason \"...\"",
			unit, gateGuess(h.Spec), unit)), nil
	}

	switch {
	case v.Result == "ok" && len(v.Evidence) == 0:
		// Belt and braces: SaveVerdict already refuses this, but if a verdict got in
		// by another path, the gate still catches it.
		return h.gate("PreToolUse", fmt.Sprintf(
			"batten: %s has result=ok but an empty evidence[]. An approval must cite something.\n%s",
			unit, store.ErrNoEvidence)), nil
	case v.Result != closing.RequiresVerdict && v.Result != "ok":
		return h.gate("PreToolUse", fmt.Sprintf(
			"batten: %s verdict is %q, not %q. %s\nsafe_next_step: %s\n"+
				"To proceed anyway (recorded): batten override %s --reason \"...\"",
			unit, v.Result, closing.RequiresVerdict, v.Why, v.SafeNextStep, unit)), nil
	}

	// A warning we owe the user even when nothing below denies. It is held rather than
	// returned, because returning here would skip every condition after it — which is
	// exactly how the budget stopped being enforced once this branch was added.
	var pending *Output

	// If the gate declares checks, an agent's word is not enough: demand a verdict that BATTEN
	// produced by running those checks. This is what closes the "I typed 'tests pass' without
	// running them" hole — the mechanical part of the gate becomes true by construction.
	if g, ok := h.Spec.Gates[gateName]; ok && len(g.Checks) > 0 {
		bv, err := h.Store.LatestVerdictBySource(run.RunID, gateName, "batten")
		if err != nil || bv.Result != "ok" {
			return h.gate("PreToolUse", fmt.Sprintf(
				"batten: %s has no batten-verified pass. The gate's checks must be RUN, not asserted.\n"+
					"Run: batten check %s\n"+
					"To proceed anyway (recorded): batten override %s --reason \"...\"",
				unit, unit, unit)), nil
		}
	} else {
		// A gate with no checks cannot be verified by construction — batten has nothing to run,
		// so the only thing standing between this commit and main is the agent's own word, which
		// is exactly the failure this gate exists to kill. It still passes: refusing every commit
		// in a repo that has not declared its checks yet would just get batten uninstalled.
		//
		// But it passes OUT LOUD. Principle #3 is "fail open only with a warning", and a gate
		// that silently degrades to trusting the model is worse than no gate, because it will be
		// believed. This is an advise() even under `enforcement: enforce` — the situation is a
		// missing declaration, not a violation, and denying is not the user's fix for it.
		//
		// It is an advisory, so it must never outrank a denial. Hold it and fall through.
		pending = advise("PreToolUse", fmt.Sprintf(
			"batten: gate %q declares no checks, so %s was approved on the agent's word alone — "+
				"nothing was run to verify it. Add gates.%s.checks to make this gate mean something.",
			gateName, unit, gateName))
	}

	// Budget is also a closing condition: a run that blew its ceiling should not quietly land.
	// This is reached whether or not the gate declares checks — an undeclared gate is a reason
	// to warn, never a reason to stop counting tokens.
	if h.Spec.Budget.OnExceed == "block" && h.Spec.Budget.Set() {
		b := h.Spec.Budget
		over, cs, err := h.Store.OverBudget(run.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
		if err == nil && over {
			return h.gate("PreToolUse", fmt.Sprintf(
				"batten: %s is over budget. budget.on_exceed=block.\n%s\n"+
					"To proceed anyway (recorded): batten override %s --reason \"...\"",
				unit, formatCeilings(cs), unit)), nil
		}
	}
	return pending, nil
}

// formatCeilings renders each declared ceiling. An unmeasurable ceiling says so rather
// than showing a zero — a budget report that invents a number is worse than none.
func formatCeilings(cs []store.Ceiling) string {
	var b strings.Builder
	for _, c := range cs {
		mark := " "
		if c.Exceeded {
			mark = "!"
		}
		switch {
		case !c.Available:
			fmt.Fprintf(&b, "  %s %-12s not measurable (install `batten statusline`)\n", mark, c.Kind)
		case c.Kind == "imputed_usd":
			fmt.Fprintf(&b, "  %s %-12s $%.2f of $%.2f\n", mark, c.Kind, c.Spent, c.Cap)
		case c.Kind == "quota_pct":
			fmt.Fprintf(&b, "  %s %-12s %.1f%% of %.1f%% of the 5h window\n", mark, c.Kind, c.Spent, c.Cap)
		default:
			fmt.Fprintf(&b, "  %s %-12s %.0f of %.0f\n", mark, c.Kind, c.Spent, c.Cap)
		}
	}
	return b.String()
}

func gateGuess(s *spec.Spec) string {
	for _, p := range s.Phases {
		if p.Gate != "" {
			return p.ID
		}
	}
	return "verify"
}

// writeSetGuard is wedge #2. "Two agents never write the same file" is the workflow's
// most important and most fragile rule, because today it is only discipline. A distracted
// agent breaks it and you find out at merge time. Here the file has an owner, and a
// non-owner is denied — both WITHIN a run (one agent vs another) and ACROSS open runs
// (session B editing a file session A's agent claimed).
func (h *Handler) writeSetGuard(in Input, path string) (*Output, error) {
	if path == "" {
		return nil, nil
	}
	rel, err := filepath.Rel(h.Spec.Root, path)
	if err != nil {
		return nil, nil
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") {
		return nil, nil // outside the repo
	}

	// Resolve which run and node this write belongs to. A fanned-out agent has an agent_id;
	// the main loop of a bound session has none but still belongs to a run.
	myRun, myNode := "", ""
	if in.AgentID != "" {
		if node, err := h.Store.NodeByAgent(in.AgentID); err == nil {
			myRun, myNode = node.RunID, node.NodeID
		}
	}
	if myRun == "" {
		if u := h.activeUnit(in); u != "" {
			if r, err := h.Store.ActiveRun(h.Spec.Project, u); err == nil {
				myRun = r.RunID
			}
		}
	}

	// Within-run collision (agent vs agent).
	if myRun != "" {
		if owner, err := h.Store.WriteSetOwner(myRun, rel); err == nil && owner != "" && owner != myNode {
			// An UNATTRIBUTED write (no agent_id in the payload) cannot be hard-denied: we
			// cannot tell the owning agent apart from a trespasser, and if Claude Code ever
			// stops carrying agent_id in subagent hooks, denying here would deny the owner
			// too and brick the whole fan-out. The risks are asymmetric — a loud warning on
			// a real trespass is recoverable; silently blocking every legitimate write is
			// not. So: attributed collision -> gate; unattributed -> always advisory.
			if myNode == "" {
				return advise("PreToolUse", fmt.Sprintf(
					"batten: %s is claimed by a fanned-out agent (%s) in this run. If you are that "+
						"agent, ignore this; if you are the orchestrator, let the agent own its file.",
					rel, store.DisplayNodeID(owner))), nil
			}
			// Both ids are shown as the agent was launched, not as the store keys it. The
			// message names who you are and tells you to run `batten claim`, and an internal
			// `n-`/run-scoped id is rejected by that command — handing one over sends the
			// agent to a dead end.
			mine, _ := h.Store.WriteSet(myRun, myNode)
			owned := "(none — declare it with `batten claim " + store.DisplayNodeID(myNode) + " <file>...`)"
			if len(mine) > 0 {
				owned = strings.Join(mine, "\n  ")
			}
			return h.gate("PreToolUse", fmt.Sprintf(
				"batten: write-set collision. %s belongs to another agent's write-set (%s); you are %s.\n"+
					"Two agents must never write the same file — that is what makes the fan-out safe.\n"+
					"Your write-set:\n  %s\n"+
					"If this file genuinely belongs to you, the plan is wrong: fix the plan, do not cross the fence.",
				rel, store.DisplayNodeID(owner), store.DisplayNodeID(myNode), owned)), nil
		}
	}

	// Cross-run collision (another session is working this file right now).
	if cross, err := h.Store.WriteSetOwnerAcrossOpenRuns(h.Spec.Project, rel, myRun); err == nil && cross != nil {
		return h.gate("PreToolUse", fmt.Sprintf(
			"batten: %s is being worked by %s in another open session (run %s).\n"+
				"Editing it now races that work. Coordinate, or use a worktree per unit so each has its own branch.",
			rel, cross.UnitID, cross.RunID)), nil
	}
	return nil, nil
}

// ---------- graph ingestion ----------

func (h *Handler) subagentStart(in Input) (*Output, error) {
	unit := h.activeUnit(in)
	if unit == "" {
		return nil, nil
	}
	run, err := h.Store.EnsureRun(h.Spec.Project, unit, in.SessionID)
	if err != nil {
		return nil, nil
	}
	nodeID := store.AgentNodeID(run.RunID, in.AgentID)
	// The agent_type is usually the domain's own name (the fan-out launches "one agent per
	// domain"), so match it directly first; fall back to path-prefix only if it isn't a domain.
	domain := ""
	if _, ok := h.Spec.Domains[in.AgentType]; ok {
		domain = in.AgentType
	} else {
		domain, _ = h.Spec.DomainFor(in.AgentType)
	}
	_ = h.Store.AddNode(store.Node{
		NodeID: nodeID, RunID: run.RunID, Kind: "subagent",
		Label: in.AgentType, Domain: domain, Status: "running",
		AgentID: in.AgentID, AgentType: in.AgentType,
	})
	if run.Phase != "" {
		_ = h.Store.AddEdge(run.RunID, store.PhaseNodeID(run.RunID, run.Phase), nodeID, "spawn")
	}
	return nil, nil
}

func (h *Handler) subagentStop(in Input) (*Output, error) {
	node, err := h.Store.NodeByAgent(in.AgentID)
	if err != nil {
		return nil, nil
	}
	status := "ok"
	if looksFailed(in.LastAssistantMessage) {
		status = "failed"
	}
	_ = h.Store.FinishNode(node.NodeID, status, 0)
	// This hook is async in the plugin config, so pricing the transcript here costs the
	// session nothing. The parser walks the subagents/ dir too, so a fan-out is counted whole.
	h.ingest(in, node.RunID)
	return nil, nil
}

func looksFailed(msg string) bool {
	l := strings.ToLower(msg)
	return strings.Contains(l, "failed") || strings.Contains(l, "error:") || strings.Contains(l, "blocked")
}

// stop closes out the session's accounting and refreshes the vault. Async in the plugin, so
// neither the JSONL walk nor the vault write sits on the critical path of the user's turn.
func (h *Handler) stop(in Input) (*Output, error) {
	unit := h.activeUnit(in)
	if unit == "" {
		return nil, nil
	}
	if run, err := h.Store.ActiveRun(h.Spec.Project, unit); err == nil {
		h.ingest(in, run.RunID)
		// The vault fills itself here — a run note appears without anyone running `batten canvas`.
		// Best-effort: a vault write must never be able to break the session.
		if h.Spec.Capabilities.Obsidian.Vault != "" {
			_, _ = export.Run(h.Spec, h.Store, unit)
		}
	}
	return nil, nil
}

// ingest prices whatever the transcript now contains into the run's ledger. It is
// idempotent (usage rows dedup on request id), so firing it from several hooks only ever
// tops up — it never double-counts. Best-effort by design: accounting must never be able
// to break a session, so every error here is swallowed.
func (h *Handler) ingest(in Input, runID string) {
	if in.TranscriptPath == "" {
		return
	}
	seen, _ := h.Store.SeenRequests(runID)
	rows, _, err := usage.Parse(in.TranscriptPath, runID, seen)
	if err != nil || len(rows) == 0 {
		return
	}
	for i := range rows {
		if rows[i].AgentID != "" {
			if n, err := h.Store.NodeByAgent(rows[i].AgentID); err == nil {
				rows[i].NodeID = n.NodeID
			}
		}
	}
	_, _ = h.Store.RecordUsage(rows)
}

// sessionStart injects where the active unit stands: which phase, what the verdict says,
// how much of the budget is gone. This is the "¿dónde quedó?" that the handoff doc answers
// by hand today.
func (h *Handler) sessionStart(in Input) (*Output, error) {
	runs, err := h.Store.ListRuns(h.Spec.Project, 5)
	if err != nil || len(runs) == 0 {
		return nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## batten — %s (%s)\n\n", h.Spec.Project, h.Spec.Unit.Name)
	for _, r := range runs {
		if r.Status != "running" {
			continue
		}
		fmt.Fprintf(&b, "- **%s** — phase `%s`, status `%s`", r.UnitID, r.Phase, r.Status)
		if r.TokensSpent > 0 {
			fmt.Fprintf(&b, ", %.1fM tokens / $%.2f imputed",
				float64(r.TokensSpent)/1e6, r.ImputedUSD)
		}
		if v, err := h.Store.LatestVerdict(r.RunID, ""); err == nil {
			fmt.Fprintf(&b, ", verdict `%s` (%d evidence)", v.Result, len(v.Evidence))
		} else {
			b.WriteString(", **no verdict yet** — the close gate will deny a commit")
		}
		b.WriteString("\n")
	}
	// Tell THIS session which unit it's bound to — the whole point when two sessions share a
	// repo. If it's bound, say so. If it's ambiguous, say THAT plainly, because an unbound
	// session means the gates can't act, and a silent non-gate is worse than a loud one.
	mine := h.activeUnit(in)
	switch {
	case mine != "":
		fmt.Fprintf(&b, "\n→ this session is working **%s**.\n", mine)
	default:
		open, _ := h.Store.OpenRuns(h.Spec.Project)
		if len(open) > 1 {
			var names []string
			for _, r := range open {
				names = append(names, r.UnitID)
			}
			fmt.Fprintf(&b, "\n⚠ %d units are open (%s) and this session isn't bound to one.\n"+
				"   The gates can't act until you bind it: run `batten phase <unit> <phase>`.\n"+
				"   For parallel work, a worktree per unit keeps each on its own branch.\n",
				len(open), strings.Join(names, ", "))
		}
	}

	out := b.String()
	if !strings.Contains(out, "- **") {
		return nil, nil
	}
	return context("SessionStart", out), nil
}

// ---------- unit resolution ----------

// activeUnit figures out which work item THIS session is in. Priority, in order:
//
//  1. a run already bound to my session_id — decisive, and the only thing that makes two
//     Claude Code sessions on one repo not collide.
//  2. the git branch names a unit — and if so, adopt the session onto that run, so #1 wins
//     from then on.
//  3. exactly one open run with no owning session — use it, and adopt it.
//  4. ambiguous (2+ open, none mine) — return "" (no gate), but the caller surfaces this so
//     the silence is visible, not a mystery.
func (h *Handler) activeUnit(in Input) string {
	// 1. my session already owns a run.
	if in.SessionID != "" {
		if r, err := h.Store.RunBySession(h.Spec.Project, in.SessionID); err == nil {
			return r.UnitID
		}
	}

	// 2. the branch names a unit -> that's mine; bind the session to it.
	dir := in.CWD
	if dir == "" {
		dir = h.Spec.Root
	}
	if br, err := gitBranch(dir); err == nil {
		if u := h.Spec.MatchUnit(br); u != "" {
			if in.SessionID != "" {
				if r, err := h.Store.ActiveRun(h.Spec.Project, u); err == nil {
					_ = h.Store.AdoptSession(r.RunID, in.SessionID)
				}
			}
			return u
		}
	}

	// 3/4. fall back to open runs, distinguishing "one unowned" from "ambiguous".
	open, err := h.Store.OpenRuns(h.Spec.Project)
	if err != nil {
		return ""
	}
	var unowned []store.Run
	for _, r := range open {
		if r.SessionID == "" {
			unowned = append(unowned, r)
		}
	}
	if len(unowned) == 1 {
		if in.SessionID != "" {
			_ = h.Store.AdoptSession(unowned[0].RunID, in.SessionID)
		}
		return unowned[0].UnitID
	}
	return "" // ambiguous: refuse to guess, and refuse to block (sessionStart makes this visible)
}

func gitBranch(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ReadInput slurps the hook payload from stdin.
func ReadInput(r io.Reader) ([]byte, error) { return io.ReadAll(r) }
