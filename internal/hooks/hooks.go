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
	"sort"
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

	// Rule names the wedge that produced this output. It never goes over the wire — Claude Code
	// has no use for it — but it is what the replay log needs to group denials by cause instead
	// of pattern-matching their English later.
	Rule string `json:"-"`
}

// withRule tags an output with the wedge that produced it. Nil-safe, because "no opinion" is a
// normal return and every caller would otherwise need the same guard.
func withRule(o *Output, rule string) *Output {
	if o != nil && o.Rule == "" {
		o.Rule = rule
	}
	return o
}

// decisionOf reads back what an output actually DID, in the audit log's vocabulary.
func decisionOf(o *Output) (decision, reason string) {
	if o == nil {
		return store.DecisionAllow, "" // silence is no opinion, which is an allow
	}
	if o.HookSpecific != nil && o.HookSpecific.PermissionDecision == "deny" {
		return store.DecisionDeny, firstLine(o.HookSpecific.PermissionDecisionReason)
	}
	if o.SystemMessage != "" {
		return store.DecisionAdvise, firstLine(o.SystemMessage)
	}
	// Context injection (SessionStart's banner) is information, not a judgement.
	return store.DecisionAllow, ""
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

// Advise is advise, exported for the one caller outside this package: cmd/batten's hook entry
// point, which has to report that batten degraded BEFORE a Handler could be built at all.
func Advise(event, reason string) *Output { return advise(event, reason) }

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

	out, err := h.route(event, in)
	h.record(event, in, raw, out)
	return out, err
}

func (h *Handler) route(event string, in Input) (*Output, error) {
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

// record writes the replay-log row, AFTER the dispatch.
//
// Before is where it used to happen, and that is why the events table could describe every
// request batten ever received and not one thing batten ever did. "How many commits did you
// deny this week" was unanswerable because the answer was never written down.
//
// Best-effort by contract: a failure to journal must never change a decision. Governing is the
// job; the log is the receipt.
func (h *Handler) record(event string, in Input, raw []byte, out *Output) {
	decision, reason := decisionOf(out)
	rule := ""
	if out != nil {
		rule = out.Rule
	}

	// Attribute to a run when we cheaply can. RunBySession is a plain read — activeUnit would
	// be richer but it ADOPTS sessions as a side effect, and journalling must not change state.
	runID := ""
	if in.SessionID != "" {
		if r, err := h.Store.RunBySession(h.Spec.Project, in.SessionID); err == nil {
			runID = r.RunID
		}
	}

	// The replay log wants every event, not every byte: a Write tool's payload embeds the
	// whole file body, and this INSERT sits on the fast path of every tool call in every
	// session sharing the DB. 4KB keeps the log diagnostic (event shape, ids, decisions)
	// without turning the events table into a mirror of the working tree.
	// The mode in force, recorded with the decision. `enforcement: report` is batten's honest
	// off-switch — gates warn instead of blocking — and this is what lets a report answer the
	// question that matters when it is turned back on: what got through while it was off.
	mode := "enforce"
	if h.Spec.ReportOnly() {
		mode = "report"
	}
	_ = h.Store.LogDecision(store.Event{
		RunID:       runID,
		Hook:        event,
		Payload:     truncateBytes(raw, 4096),
		Decision:    decision,
		Reason:      reason,
		Rule:        rule,
		Enforcement: mode,
	})
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
		// Tagged here rather than at each construction site: this is the one place that knows
		// which wedge is answering, and the budget branch inside re-tags itself.
		out, err := h.verdictGate(in, bi.Command)
		return withRule(out, store.RuleVerdictGate), err
	case "Write", "Edit", "NotebookEdit":
		var wi writeInput
		if err := json.Unmarshal(in.ToolInput, &wi); err != nil {
			return nil, nil
		}
		out, err := h.writeSetGuard(in, wi.FilePath)
		return withRule(out, store.RuleWriteSet), err
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

// GateShortfall reports what a gate with declared checks is still missing, or "" when it is
// satisfied. It is exported because `batten close` must not be able to accept what the commit
// gate refuses — the field test found close taking an agent-asserted verdict that the commit
// hook denied seconds earlier, which turns the gate into a speed bump you walk around.
//
// Two verdicts are required, and they must come from two different producers:
//
//   - batten's own, proving the declared checks were RUN rather than asserted;
//   - somebody else's, proving a reviewer judged the work against the acceptance criteria.
//
// `batten check` writes the first. It used to satisfy the second as well, simply by being the
// newest row — so `batten check` on an empty diff closed a unit with nothing having reviewed
// anything at all.
func GateShortfall(st *store.Store, runID, gate, requires string) string {
	return GateShortfallAt(st, "", runID, gate, requires)
}

// GateShortfallAt is GateShortfall with the repo root, which lets it also check that the tree
// the checks passed against is still the tree being committed.
func GateShortfallAt(st *store.Store, root, runID, gate, requires string) string {
	return gateShortfall(st, root, runID, gate, requires).Message
}

// gateShortfall is the same answer with its reason code and its fix attached. The exported
// string form stays for `batten close`, which prints to a human; the hook uses this one, because
// an unattended loop should not have to infer the remedy from the wording.
func gateShortfall(st *store.Store, root, runID, gate, requires string) envelope {
	unit := shortUnit(st, runID)

	bv, err := st.LatestVerdictBySource(runID, gate, "batten")
	if err != nil || bv.Result != "ok" {
		return envelope{
			Code: CodeChecksNotRun,
			Fix:  "batten check " + unit,
			Message: "has no batten-verified pass. The gate's checks must be RUN, not asserted.\n" +
				"Run: batten check " + unit,
		}
	}
	if e := staleTarget(root, bv, unit); e.Code != "" {
		return e
	}
	av, err := st.LatestVerdictNotBySource(runID, gate, "batten")
	switch {
	case err != nil:
		return envelope{
			Code: CodeNotReviewed,
			Fix:  "batten verdict --unit " + unit + " --file v.json",
			Message: "has only batten's own check result. `batten check` proves the checks ran; it does " +
				"not judge whether the work meets its acceptance criteria.\n" +
				"Record a verdict from the verify phase: batten verdict --file v.json",
		}
	case len(av.Evidence) == 0:
		return envelope{
			Code:    CodeNoEvidence,
			Fix:     "batten verdict --unit " + unit + " --file v.json",
			Message: "has a verdict with an empty evidence[]. An approval must cite something.",
		}
	case av.Result != requires && av.Result != "ok":
		return envelope{
			Code: CodeWrongResult,
			Fix:  av.SafeNextStep,
			Message: fmt.Sprintf("verdict is %q, not %q. %s\nsafe_next_step: %s",
				av.Result, requires, av.Why, av.SafeNextStep),
		}
	}
	return envelope{}
}

// staleTarget reports that the working tree drifted since the checks ran, or "" when it did not
// — or when the question cannot be answered here.
//
// This is the half of "verified" that was missing. `batten check` proves the declared checks
// passed; it proved it about a tree, and between then and the commit a formatter, a build
// script, a file watcher or the agent itself can change that tree. The verdict keeps saying
// batten-verified about a state that no longer exists.
//
// It reports rather than freezes. batten does not own the index and taking it hostage between
// phases would break every other tool the developer runs. And it degrades honestly: an empty
// digest means "not measurable here" — a repo that is not a git repo, which is a real shape
// batten has been field-tested against — and a comparison against "not measurable" would be a
// denial invented out of an absence.
func staleTarget(root string, bv *store.Verdict, unit string) envelope {
	if root == "" || bv.TargetDigest == "" {
		return envelope{}
	}
	now := store.TargetDigest(root)
	if now == "" || now == bv.TargetDigest {
		return envelope{}
	}

	// Named, not generic, and carrying its own way out. An error the agent loop cannot act on
	// sends it hunting; these say what happened and exactly what clears it.
	//
	// And the two causes get different answers. Telling someone to re-run their checks is the
	// right advice when a formatter edited a file and beside the point when they rebased —
	// there, the news is that the history moved under them, and the anchor the run recorded is
	// no longer the commit they are building on.
	wasHead, wasContent := store.SplitDigest(bv.TargetDigest)
	nowHead, nowContent := store.SplitDigest(now)

	// Neither is retryable: running the same commit again does not re-run a check or move an
	// anchor. Saying so is the difference between a loop that fixes this and one that spends the
	// night re-issuing the identical command.
	reanchor := "batten recover " + unit + " && batten check " + unit
	switch {
	case wasHead != "" && wasHead != nowHead && wasContent == nowContent:
		return envelope{Code: CodeMovedBase, Fix: reanchor,
			Message: "has a MOVED BASE: the checks passed on commit " + wasHead + ", and HEAD is now " +
				nowHead + ". Your uncommitted work is untouched — the history moved under it " +
				"(a rebase, an amend, a checkout, or a commit landing underneath).\n" +
				"The anchor this run recorded no longer describes what you are building on.\n" +
				"Re-anchor and re-verify: " + reanchor}

	case wasHead != "" && wasHead != nowHead:
		return envelope{Code: CodeMovedBase, Fix: reanchor,
			Message: "has a MOVED BASE and a changed tree: the checks passed on commit " + wasHead +
				", HEAD is now " + nowHead + ", and the uncommitted work changed too.\n" +
				"Re-anchor and re-verify: " + reanchor}
	}

	return envelope{Code: CodeStaleTarget, Fix: "batten check " + unit,
		Message: "has a STALE TARGET: the working tree changed after `batten check` ran, so the pass " +
			"batten recorded describes a state that no longer exists.\n" +
			"Something edited the tree between the check and this commit — a formatter, a build " +
			"step, a file watcher, or the agent itself.\n" +
			"Re-verify against what you are actually committing: batten check " + unit}
}

// shortUnit resolves a run back to its unit for a message. Falls back to the run id, which is
// worse to read but never wrong.
func shortUnit(st *store.Store, runID string) string {
	if r, err := st.Run(runID); err == nil {
		return r.UnitID
	}
	return runID
}

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

	// Not blocking here is right — batten cannot deny what it cannot attribute, and denying
	// on ambiguity would stop honest work. But principle #3 is "fail open only OUT LOUD", and
	// both of the paths below used to return in silence. Silence from this hook is
	// indistinguishable from approval, so the user concludes the gate is working. It is not.
	unit := h.activeUnit(in)

	// The commit message is evidence about which unit this commit is FOR, and it was never
	// read. So a session bound to a verified TASK-001 could land `feat(TASK-002): ...` — a
	// unit with no verdict at all — because the gate only ever looked at the binding. Under
	// trunk-based development, where the branch names nothing, the message is the only signal
	// there is. batten.schema.json has promised this resolution the whole time.
	if named := h.Spec.MatchUnit(cmd); named != "" && named != unit {
		switch r, err := h.Store.ActiveRun(h.Spec.Project, named); {
		case err == nil:
			// The message names a real open unit. Gate THAT one — it is the more specific
			// statement of intent, and it is the unit whose work is about to land.
			unit = r.UnitID
		case unit != "":
			return h.gateWith("PreToolUse", envelope{
				Code: CodeWrongUnit,
				Fix:  "batten phase " + named + " " + firstPhaseID(h.Spec),
				Message: fmt.Sprintf(
					"batten: this commit message names %s, but this session is working %s and %s has "+
						"no open run.\nCommitting it under %s's verdict would credit one unit's review "+
						"to another's work.\nOpen it (`batten phase %s %s`) or fix the commit message.",
					named, unit, named, unit, named, firstPhaseID(h.Spec)),
			}), nil
		default:
			// Nothing else resolved a unit either, so this message is the only evidence there
			// is about what the commit is for — and the unit it names was never opened. Falling
			// through here would reach the "nothing to attribute" path and say nothing at all,
			// which is the silence this whole class of fix exists to remove.
			return adviseWith("PreToolUse", envelope{
				Code: CodeUnattributed,
				Fix:  "batten phase " + named + " " + firstPhaseID(h.Spec),
				Message: fmt.Sprintf(
					"batten: this commit is NOT gated. Its message names %s, which has no run on "+
						"record, so nothing was verified.\nOpen one with `batten phase %s %s`.",
					named, named, firstPhaseID(h.Spec)),
			}), nil
		}
	}

	if unit == "" {
		open, _ := h.Store.OpenRuns(h.Spec.Project)
		if len(open) == 0 {
			// The literal first commit after adopting batten, and the most likely first thing
			// anyone does with it. Nothing is open, the message names no unit, and no branch
			// names one either — which is the normal state of a repo that is planned but not yet
			// worked, and of trunk-based development generally.
			//
			// This used to return in silence, on the argument that SessionStart carries it. It
			// half does: SessionStart says "the commit gate is not governing anything" — but it
			// says it in additionalContext, which reaches the MODEL and not the user's screen,
			// once, at the start of a session whose commit may be two hundred turns later. At the
			// moment of the ungated commit, nobody was told anything, and this hook's silence is
			// indistinguishable from an approval.
			return adviseWith("PreToolUse", envelope{
				Code: CodeUnattributed,
				Fix:  "batten phase <" + h.Spec.Unit.Name + "> " + firstPhaseID(h.Spec),
				Message: fmt.Sprintf(
					"batten: this commit is NOT gated. No run is open and nothing identifies which unit "+
						"this commit is for, so there was no verdict to check and nothing was verified.\n"+
						"Open one with `batten phase <%s> %s` — the gate governs from there.",
					h.Spec.Unit.Name, firstPhaseID(h.Spec)),
			}), nil
		}
		var names []string
		for _, r := range open {
			names = append(names, r.UnitID)
		}
		// One open run the session does not own is not less ambiguous than five — it is the same
		// failure with a shorter list, and batten still cannot say this commit belongs to it.
		return adviseWith("PreToolUse", envelope{
			Code: CodeUnattributed,
			Fix:  "batten phase " + names[0] + " <phase>",
			Message: fmt.Sprintf(
				"batten: this commit is NOT gated. %d unit(s) are open (%s) and this session is bound "+
					"to none of them, so batten cannot tell which one you are committing.\n"+
					"Bind it with `batten phase <unit> <phase>`, or use a worktree per unit.",
				len(open), strings.Join(names, ", ")),
		}), nil
	}
	run, err := h.Store.ActiveRun(h.Spec.Project, unit)
	if err != nil {
		// The unit is known — the branch names it — but no run was ever opened for it. This is
		// the first commit after adopting batten: the newcomer branches, codes, commits, sees it
		// succeed, and reasonably concludes the gate is on.
		return adviseWith("PreToolUse", envelope{
			Code: CodeUnattributed,
			Fix:  "batten phase " + unit + " " + firstPhaseID(h.Spec),
			Message: fmt.Sprintf(
				"batten: this commit is NOT gated. %s has no run on record, so there is no verdict to "+
					"check and nothing was verified.\n"+
					"Open one with `batten phase %s %s` — the gate starts governing from there.",
				unit, unit, firstPhaseID(h.Spec)),
		}), nil
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
		return h.gateWith("PreToolUse", envelope{
			Code: CodeNoVerdict,
			// Not retryable: running the same commit again changes nothing, and an unattended
			// loop that retries it burns the window on an identical denial.
			Fix: "batten phase " + unit + " " + gateGuess(h.Spec),
			Message: fmt.Sprintf(
				"batten: %s has no verdict envelope. Run the %q phase before committing.\n"+
					"To proceed anyway (recorded in the audit log): batten override %s --reason \"...\"",
				unit, gateGuess(h.Spec), unit),
		}), nil
	}

	switch {
	case v.Result == "ok" && len(v.Evidence) == 0:
		// Belt and braces: SaveVerdict already refuses this, but if a verdict got in
		// by another path, the gate still catches it.
		return h.gateWith("PreToolUse", envelope{
			Code: CodeNoEvidence,
			Fix:  "batten verdict --unit " + unit + " --file v.json",
			Message: fmt.Sprintf(
				"batten: %s has result=ok but an empty evidence[]. An approval must cite something.\n%s",
				unit, store.ErrNoEvidence),
		}), nil
	case v.Result != closing.RequiresVerdict && v.Result != "ok":
		return h.gateWith("PreToolUse", envelope{
			Code: CodeWrongResult,
			Fix:  v.SafeNextStep,
			Message: fmt.Sprintf(
				"batten: %s verdict is %q, not %q. %s\nsafe_next_step: %s\n"+
					"To proceed anyway (recorded): batten override %s --reason \"...\"",
				unit, v.Result, closing.RequiresVerdict, v.Why, v.SafeNextStep, unit),
		}), nil
	}

	// A warning we owe the user even when nothing below denies. It is held rather than
	// returned, because returning here would skip every condition after it — which is
	// exactly how the budget stopped being enforced once this branch was added.
	var pending *Output

	// If the gate declares checks, an agent's word is not enough: demand a verdict that BATTEN
	// produced by running those checks. This is what closes the "I typed 'tests pass' without
	// running them" hole — the mechanical part of the gate becomes true by construction.
	if g, ok := h.Spec.Gates[gateName]; ok && len(g.Checks) > 0 {
		if e := gateShortfall(h.Store, h.Spec.Root, run.RunID, gateName, closing.RequiresVerdict); e.Code != "" {
			e.Message = fmt.Sprintf(
				"batten: %s %s\nTo proceed anyway (recorded): batten override %s --reason \"...\"",
				unit, e.Message, unit)
			return h.gateWith("PreToolUse", e), nil
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
		pending = adviseWith("PreToolUse", envelope{
			Code: CodeNoChecks,
			Fix:  "add gates." + gateName + ".checks to " + spec.Filename,
			Message: fmt.Sprintf(
				"batten: gate %q declares no checks, so %s was approved on the agent's word alone — "+
					"nothing was run to verify it. Add gates.%s.checks to make this gate mean something.",
				gateName, unit, gateName),
		})
	}

	// Budget is also a closing condition: a run that blew its ceiling should not quietly land.
	// This is reached whether or not the gate declares checks — an undeclared gate is a reason
	// to warn, never a reason to stop counting tokens.
	if h.Spec.Budget.OnExceed == "block" && h.Spec.Budget.Set() {
		b := h.Spec.Budget
		over, cs, err := h.Store.OverBudget(run.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
		if err == nil && over {
			// Its own rule, not the verdict gate's: "stopped for spending too much" and
			// "stopped for having no evidence" are different facts, and a report that merges
			// them tells the user nothing they can act on.
			return withRule(h.gateWith("PreToolUse", envelope{
				Code: CodeOverBudget,
				// The only honest "fix" is a decision a human takes: raise the ceiling, or
				// override on the record. Neither is something a loop should do on its own at
				// 3am, which is exactly why the ceiling exists.
				Fix: "batten override " + unit + " --reason \"...\"",
				Message: fmt.Sprintf(
					"batten: %s is over budget. budget.on_exceed=block.\n%s\n"+
						"To proceed anyway (recorded): batten override %s --reason \"...\"",
					unit, formatCeilings(cs), unit),
			}), store.RuleBudget), nil
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
			fmt.Fprintf(&b, "  %s %-12s not measurable — %s\n", mark, c.Kind, c.Reason)
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

// firstPhaseID names the phase a unit should be opened at, so an advisory can print a command
// the reader can paste rather than a placeholder they have to resolve.
func firstPhaseID(s *spec.Spec) string {
	if len(s.Phases) > 0 {
		return s.Phases[0].ID
	}
	return "build"
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
		if owner, err := h.Store.WriteSetOwner(myRun, h.Spec.Root, rel); err == nil && owner != "" && owner != myNode {
			// An UNATTRIBUTED write (no agent_id in the payload) cannot be hard-denied: we
			// cannot tell the owning agent apart from a trespasser, and if Claude Code ever
			// stops carrying agent_id in subagent hooks, denying here would deny the owner
			// too and brick the whole fan-out. The risks are asymmetric — a loud warning on
			// a real trespass is recoverable; silently blocking every legitimate write is
			// not. So: attributed collision -> gate; unattributed -> always advisory.
			if myNode == "" {
				return adviseWith("PreToolUse", envelope{
					Code: CodeWriteSet,
					// Retryable in the one sense that matters to a loop: batten could not
					// attribute this write, so the same call from a payload that carries an
					// agent_id would get a real answer instead of a guess.
					Retry: true,
					Message: fmt.Sprintf(
						"batten: %s is claimed by a fanned-out agent (%s) in this run. If you are that "+
							"agent, ignore this; if you are the orchestrator, let the agent own its file.",
						rel, store.DisplayNodeID(owner)),
				}), nil
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
			return h.gateWith("PreToolUse", envelope{
				Code: CodeWriteSet,
				// Deliberately NO fix command. Every other denial hands over the way out; this
				// one must not, because there is no legitimate way through: if two agents both
				// need this file the PLAN is wrong, and the repair is to merge or sequence the
				// sub-tasks. A `fix:` here would be an instruction to cross the fence.
				Message: fmt.Sprintf(
					"batten: write-set collision. %s belongs to another agent's write-set (%s); you are %s.\n"+
						"Two agents must never write the same file — that is what makes the fan-out safe.\n"+
						"Your write-set:\n  %s\n"+
						"If this file genuinely belongs to you, the plan is wrong: fix the plan, do not cross the fence.",
					rel, store.DisplayNodeID(owner), store.DisplayNodeID(myNode), owned),
			}), nil
		}
	}

	// Cross-run collision (another session is working this file right now).
	if cross, err := h.Store.WriteSetOwnerAcrossOpenRuns(h.Spec.Project, rel, myRun); err == nil && cross != nil {
		return h.gateWith("PreToolUse", envelope{
			Code: CodeWriteSet,
			// Retryable, unlike the within-run collision: the other session finishing is a
			// thing that happens on its own, and this same write succeeds afterwards.
			Retry: true,
			Message: fmt.Sprintf(
				"batten: %s is being worked by %s in another open session (run %s).\n"+
					"Editing it now races that work. Coordinate, or use a worktree per unit so each has its own branch.",
				rel, cross.UnitID, cross.RunID),
		}), nil
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
// phaseBriefing renders what the ACTIVE phase demands: its declared inputs, whether it fans out,
// and what its gate will refuse. Returns "" when the phase is not one the spec declares.
//
// `phases[].reads` is the field that says what a phase's inputs ARE, and until now its only
// consumer was batten_spec echoing it back. This is the second one, and the one that reaches the
// agent without it having to ask.
func phaseBriefing(sp *spec.Spec, phaseID string) string {
	var p *spec.Phase
	for i := range sp.Phases {
		if sp.Phases[i].ID == phaseID {
			p = &sp.Phases[i]
			break
		}
	}
	if p == nil {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n### phase `%s`\n", p.ID)
	if len(p.Reads) > 0 {
		fmt.Fprintf(&b, "- reads: %s\n", strings.Join(p.Reads, ", "))
	}
	if p.Fanout {
		var names []string
		for _, n := range sortedDomains(sp) {
			names = append(names, n)
		}
		fmt.Fprintf(&b, "- fans out over %d domain(s): %s. Each agent gets a DISJOINT write-set; "+
			"claim yours with `batten claim <agent-id> <files...>`.\n", len(names), strings.Join(names, ", "))
	}
	if p.Gate != "" {
		g, ok := sp.Gates[p.Gate]
		switch {
		case !ok:
			fmt.Fprintf(&b, "- gate `%s` — declared on this phase but missing from gates:. "+
				"Nothing will verify it.\n", p.Gate)
		case len(g.Checks) == 0:
			fmt.Fprintf(&b, "- gate `%s` declares NO checks, so it approves on the agent's word "+
				"alone — nothing is run to verify it.\n", p.Gate)
		default:
			fmt.Fprintf(&b, "- gate `%s`: %d check(s) must RUN, not be asserted — `batten check %s`\n",
				p.Gate, len(g.Checks), sp.Unit.Name)
		}
	}
	if p.RequiresVerdict != "" {
		fmt.Fprintf(&b, "- this is the CLOSING phase: a commit is denied without a verdict `%s` "+
			"citing evidence.\n", p.RequiresVerdict)
	}
	return b.String()
}

func sortedDomains(sp *spec.Spec) []string {
	out := make([]string, 0, len(sp.Domains))
	for n := range sp.Domains {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (h *Handler) sessionStart(in Input) (*Output, error) {
	runs, err := h.Store.ListRuns(h.Spec.Project, 5)
	if err != nil {
		return nil, nil
	}
	// No runs at all is not "nothing to report" — it is the state where the commit gate has
	// nothing to gate. Saying so once per session is the honest version of a hook that would
	// otherwise stay quiet through the newcomer's whole first day and let them conclude the
	// gate is on. Once per session, not per commit: this is orientation, not an alarm.
	if len(runs) == 0 {
		if closing, ok := h.Spec.ClosingPhase(); ok && closing.RequiresVerdict != "" {
			return context("SessionStart", fmt.Sprintf(
				"## batten — %s\n\nNo run has been opened yet, so **the commit gate is not "+
					"governing anything**. A commit right now lands unverified.\n"+
					"Start one with `batten phase <%s> %s`.\n",
				h.Spec.Project, h.Spec.Unit.Name, firstPhaseID(h.Spec))), nil
		}
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
		// §4.5: say what the ACTIVE PHASE requires, not just which unit is open. The spec was
		// always available on request; nothing about it ever changed with the phase, so an agent
		// resuming mid-workflow got the same orientation whether it was about to fan out or
		// about to face a gate. Everything below comes out of the user's batten.yaml — batten
		// narrows their process, it does not add one.
		if r, err := h.Store.ActiveRun(h.Spec.Project, mine); err == nil {
			b.WriteString(phaseBriefing(h.Spec, r.Phase))
		}
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
