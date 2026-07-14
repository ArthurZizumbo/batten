// Package hooks implements batten's enforcement surface.
//
// This is where prose becomes mechanism. Two rules that a document can only ask for,
// a PreToolUse hook can actually impose:
//
//	1. verdict gate    — a commit without cited evidence is denied.
//	2. write-set guard — an agent writing another agent's file is denied.
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

	"github.com/arthu/batten/internal/export"
	"github.com/arthu/batten/internal/spec"
	"github.com/arthu/batten/internal/store"
	"github.com/arthu/batten/internal/usage"
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
	Continue      *bool          `json:"continue,omitempty"`
	SystemMessage string         `json:"systemMessage,omitempty"`
	HookSpecific  *HookSpecific  `json:"hookSpecificOutput,omitempty"`
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
		return &Output{
			SystemMessage: "batten (report mode — not blocking): " + firstLine(reason),
			HookSpecific: &HookSpecific{
				HookEventName:     event,
				AdditionalContext: "batten would DENY this in enforce mode:\n" + reason,
			},
		}
	}
	return deny(event, reason)
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

	_ = h.Store.LogEvent("", "", event, raw)

	switch event {
	case "PreToolUse":
		return h.preToolUse(in)
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
var commitRe = regexp.MustCompile(`(^|[;&|]|\s)git\s+(-[^\s]+\s+)*commit\b`)

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

	// Budget is also a closing condition: a run that blew its ceiling should not quietly land.
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
	return nil, nil
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
// non-owner is denied.
func (h *Handler) writeSetGuard(in Input, path string) (*Output, error) {
	if path == "" || in.AgentID == "" {
		return nil, nil // the orchestrator itself is not fenced; only fanned-out agents are
	}
	node, err := h.Store.NodeByAgent(in.AgentID)
	if err != nil {
		return nil, nil // this agent has no declared write-set: nothing to enforce
	}
	rel, err := filepath.Rel(h.Spec.Root, path)
	if err != nil {
		return nil, nil
	}
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, "../") {
		return nil, nil // outside the repo
	}

	owner, err := h.Store.WriteSetOwner(node.RunID, rel)
	if err != nil || owner == "" || owner == node.NodeID {
		return nil, nil // unclaimed, or claimed by this very agent
	}

	mine, _ := h.Store.WriteSet(node.RunID, node.NodeID)
	return h.gate("PreToolUse", fmt.Sprintf(
		"batten: write-set collision. %s belongs to another agent's write-set (%s); you are %s.\n"+
			"Two agents must never write the same file — that is what makes the fan-out safe.\n"+
			"Your write-set:\n  %s\n"+
			"If this file genuinely belongs to you, the plan is wrong: fix the plan, do not cross the fence.",
		rel, owner, node.NodeID, strings.Join(mine, "\n  "))), nil
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
	nodeID := "n-" + in.AgentID
	domain, _ := h.Spec.DomainFor(in.AgentType) // best-effort
	_ = h.Store.AddNode(store.Node{
		NodeID: nodeID, RunID: run.RunID, Kind: "subagent",
		Label: in.AgentType, Domain: domain, Status: "running",
		AgentID: in.AgentID, AgentType: in.AgentType,
	})
	if run.Phase != "" {
		_ = h.Store.AddEdge(run.RunID, "p-"+run.Phase, nodeID, "spawn")
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
	out := b.String()
	if !strings.Contains(out, "- **") {
		return nil, nil
	}
	return context("SessionStart", out), nil
}

// ---------- unit resolution ----------

// activeUnit figures out which work item we are in: the current git branch usually
// names it (feature/E7-US-034-slug), otherwise fall back to the single open run.
func (h *Handler) activeUnit(in Input) string {
	dir := in.CWD
	if dir == "" {
		dir = h.Spec.Root
	}
	if br, err := gitBranch(dir); err == nil {
		if u := h.Spec.MatchUnit(br); u != "" {
			return u
		}
	}
	runs, err := h.Store.ListRuns(h.Spec.Project, 10)
	if err != nil {
		return ""
	}
	var open []string
	for _, r := range runs {
		if r.Status == "running" {
			open = append(open, r.UnitID)
		}
	}
	if len(open) == 1 {
		return open[0] // unambiguous
	}
	return "" // ambiguous: refuse to guess, and therefore refuse to block
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
