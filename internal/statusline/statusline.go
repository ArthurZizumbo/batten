// Package statusline reads the one thing a hook can never see: how much of the
// subscription plan this session has already burned.
//
// On a Claude subscription the marginal dollar cost of a token is zero, so "dollars spent"
// is the wrong ceiling; "what fraction of my plan is gone" is the right one. Claude Code
// exposes rate_limits ONLY in the JSON it pipes to the configured statusLine command — hooks
// receive transcript_path and nothing else. That asymmetry is the entire reason this package
// exists: it is batten's quota SENSOR first and a display second.
//
// Two rules govern everything below.
//
//  1. Absent is not zero. rate_limits appears only for Claude.ai Pro/Max subscribers, and only
//     after the session's first API response; each window can be missing independently. Every
//     quota field is therefore a pointer, and a missing one is recorded as unknown — never as
//     0%. "0% of the window used" and "we do not know" are different claims, and conflating
//     them would make batten's budget lie in the direction of "keep going".
//
//  2. Never fail loudly. This command runs on every turn and its stdout IS the user's status
//     line. An error here degrades the terminal of someone who did not ask us to. Run always
//     returns a printable line and a nil error.
package statusline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/arthu/batten/internal/spec"
	"github.com/arthu/batten/internal/store"
)

// Input is the statusLine stdin payload. Absent quota fields stay nil — nil means
// "unknown", which is NOT the same as zero.
type Input struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	PromptID       string `json:"prompt_id"`

	Model     Model      `json:"model"`
	Effort    *Effort    `json:"effort"`
	Workspace *Workspace `json:"workspace"`

	ContextWindow *ContextWindow `json:"context_window"`
	Exceeds200k   bool           `json:"exceeds_200k_tokens"`

	Cost *Cost `json:"cost"`

	// RateLimits is nil for API-key users and for every invocation before the session's
	// first API response. Nil here means "this account/session does not expose quota",
	// which is a fact worth having, not a zero to average in.
	RateLimits *RateLimits `json:"rate_limits"`
}

type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type Effort struct {
	Level string `json:"level"`
}

type Workspace struct {
	CurrentDir string `json:"current_dir"`
	ProjectDir string `json:"project_dir"`
}

// ContextWindow deliberately exposes only used_percentage.
//
// The payload may also carry total_input_tokens/total_output_tokens, but those are the
// CONTEXT-WINDOW CONTENTS OF THE LAST RESPONSE, not cumulative session totals. Summing them
// produces a dashboard that is confidently wrong, so this struct does not offer the footgun.
// Cumulative accounting comes from the transcript (internal/usage), never from here.
type ContextWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
}

// Cost is parsed but never rendered as batten's own number: total_cost_usd is Claude Code's
// figure, not our ledger, and on a subscription it is not a bill anyway. The tokens and the
// imputed dollars on the line come from the store, which we can defend line by line.
type Cost struct {
	TotalCostUSD       *float64 `json:"total_cost_usd"`
	TotalAPIDurationMS *int64   `json:"total_api_duration_ms"`
}

type RateLimits struct {
	FiveHour *Window `json:"five_hour"`
	SevenDay *Window `json:"seven_day"`
}

// Window is one rate-limit window. Both fields are pointers because a window can appear with
// only part of its data, and a zeroed float here would be indistinguishable from a real 0%.
type Window struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"` // Unix epoch SECONDS, not millis
}

// pct returns the window's used percentage, or nil when the window itself is absent.
func (w *Window) pct() *float64 {
	if w == nil {
		return nil
	}
	return w.UsedPercentage
}

func (w *Window) reset() *int64 {
	if w == nil {
		return nil
	}
	return w.ResetsAt
}

// Thresholds at which a segment earns its space on a one-line display. Below them the
// information is true but not worth the width.
const (
	ctxWarnPct   = 80 // context window: below this you are not close to a compaction
	quotaWarnPct = 80 // rate-limit window: below this the reset time is not yet interesting
)

// chainTimeout caps a wrapped third-party statusLine command. The status line is redrawn
// constantly; a slow chained command must degrade to "no extra segment", never to a stall.
const chainTimeout = 3 * time.Second

// Run snapshots the quota into the store and returns the line to print.
//
// It must NEVER fail loudly: on any internal error it returns a minimal line and a nil error.
// sp may be nil (cwd is not a batten repo) — the quota is still account-global and still worth
// sampling, so we record it and render only the generic segments.
func Run(sp *spec.Spec, st *store.Store, raw []byte) (line string, err error) {
	// The status line is drawn on every turn. A panic in here would paint a Go stack trace
	// across the user's terminal forever, so the whole path is fenced.
	defer func() {
		if r := recover(); r != nil {
			line, err = "batten", nil
		}
	}()

	var in Input
	if e := json.Unmarshal(raw, &in); e != nil {
		return "batten", nil
	}

	var run *store.Run
	if st != nil {
		snapshot(st, in)
		run = attach(sp, st, in)
	}
	return render(sp, st, in, run, chain(sp, in, raw)), nil
}

// snapshot persists the quota reading. It writes NOTHING when the payload carries no
// percentage: an empty row would later be read back as a legitimate sample, and a run whose
// baseline was "0%" would look like it had the whole window to burn. Absent stays absent.
func snapshot(st *store.Store, in Input) {
	if in.SessionID == "" || in.RateLimits == nil {
		return
	}
	five, seven := in.RateLimits.FiveHour, in.RateLimits.SevenDay
	if five.pct() == nil && seven.pct() == nil {
		return // nothing measurable arrived; do not manufacture a data point
	}
	_ = st.SaveQuota(store.QuotaSnapshot{
		SessionID:     in.SessionID,
		FiveHourPct:   five.pct(),
		FiveHourReset: five.reset(),
		SevenDayPct:   seven.pct(),
		SevenDayReset: seven.reset(),
	})
}

// attach finds the run this status line is about and, if it is ours, wires the session and the
// quota baseline to it.
//
// A run usually opens BEFORE the first statusline invocation of a session (the hook that opens
// it fires earlier, and rate_limits does not even exist until the first API response). Without
// this back-fill a run would never have a baseline, and every per-run quota delta would be
// unmeasurable. This is what makes those deltas possible at all.
func attach(sp *spec.Spec, st *store.Store, in Input) *store.Run {
	if sp == nil {
		return nil // not a batten repo: no run to attribute anything to
	}
	run, mine := pick(st, sp.Project, in.SessionID)
	if run == nil {
		return nil
	}
	if !mine {
		// The run belongs to another Claude session in this repo. Showing it is honest (it IS
		// the state of the repo); adopting it or baselining it from OUR sample would attribute
		// another session's quota to it.
		return run
	}
	if run.SessionID == "" {
		if err := st.AdoptSession(run.RunID, in.SessionID); err == nil {
			run.SessionID = in.SessionID
		}
	}
	if run.QuotaStart5h == nil && in.RateLimits != nil {
		if p := in.RateLimits.FiveHour.pct(); p != nil {
			if err := st.SetQuotaBaseline(run.RunID, *p); err == nil {
				v := *p
				run.QuotaStart5h = &v
			}
		}
	}
	return run
}

// pick resolves the open run for this project. mine reports whether this session may claim it;
// an ambiguous repo (several open runs, none ours) yields no run rather than a guess, because
// the wrong guess would silently mis-attribute a quota baseline.
func pick(st *store.Store, project, sessionID string) (run *store.Run, mine bool) {
	runs, err := st.ListRuns(project, 20) // ListRuns is newest-first
	if err != nil {
		return nil, false
	}
	var open []store.Run
	for i := range runs {
		if runs[i].Status == "running" {
			open = append(open, runs[i])
		}
	}
	if sessionID != "" {
		for i := range open {
			if open[i].SessionID == sessionID {
				return &open[i], true
			}
		}
	}
	if len(open) == 1 {
		r := open[0]
		// Unclaimed runs (created by the CLI, or by a hook before a session existed) are ours
		// to adopt. One already bound to a different session is only ours to display.
		return &r, r.SessionID == "" && sessionID != ""
	}
	return nil, false
}

// render assembles the line. Every segment is omitted when its input is unknown — an empty
// space says "we did not measure this", whereas "5h 0%" would be a claim we cannot support.
func render(sp *spec.Spec, st *store.Store, in Input, run *store.Run, tail string) string {
	var seg []string

	head := "batten"
	if run != nil {
		head += " " + run.UnitID
		if run.Phase != "" {
			head += " " + run.Phase
		}
	}
	seg = append(seg, head)

	if run != nil && st != nil {
		if s := verdictSeg(sp, st, run); s != "" {
			seg = append(seg, s)
		}
		if s := spendSeg(run); s != "" {
			seg = append(seg, s)
		}
		if s := budgetSeg(sp, st, run); s != "" {
			seg = append(seg, s)
		}
	}

	if in.ContextWindow != nil && in.ContextWindow.UsedPercentage != nil {
		if p := *in.ContextWindow.UsedPercentage; p >= ctxWarnPct {
			seg = append(seg, "ctx "+pctStr(p)+"%")
		}
	}
	if in.RateLimits != nil {
		if s := windowSeg("5h", in.RateLimits.FiveHour); s != "" {
			seg = append(seg, s)
		}
		if s := windowSeg("7d", in.RateLimits.SevenDay); s != "" {
			seg = append(seg, s)
		}
	}
	if tail != "" {
		seg = append(seg, tail)
	}
	return strings.Join(seg, " · ")
}

// verdictSeg is the single most useful thing batten can say mid-session: whether the close gate
// is currently holding a commit hostage. Silence about a missing verdict is what lets someone
// discover it only when the commit is denied.
func verdictSeg(sp *spec.Spec, st *store.Store, run *store.Run) string {
	gate := ""
	gated := false
	if sp != nil {
		if closing, ok := sp.ClosingPhase(); ok && closing.RequiresVerdict != "" {
			gate, gated = closing.Gate, true
		}
	}
	v, err := st.LatestVerdict(run.RunID, gate)
	if err != nil {
		if !gated {
			return "no verdict"
		}
		if ok, e := st.HasOverride(run.RunID, gate); e == nil && ok {
			return "no verdict (overridden)"
		}
		return "no verdict: commit DENIED"
	}
	switch {
	case v.Result == "ok" && len(v.Evidence) == 0:
		// SaveVerdict refuses to write this, but if one arrived by another path the line must
		// not display it as an approval — an approval that cites nothing is not an approval.
		return "verdict ok/NO EVIDENCE: commit DENIED"
	case v.Result == "ok":
		return fmt.Sprintf("verdict ok (%d ev)", len(v.Evidence))
	default:
		return "verdict " + v.Result
	}
}

// spendSeg shows what we counted exactly. Zero tokens means "the transcript has not been
// ingested yet", not "this run was free", so we print nothing rather than "0 tok".
func spendSeg(run *store.Run) string {
	if run.TokensSpent <= 0 {
		return ""
	}
	s := tokStr(run.TokensSpent) + " tok"
	if run.ImputedUSD > 0 {
		s += fmt.Sprintf(" $%.2f", run.ImputedUSD)
	}
	return s
}

// budgetSeg reports declared ceilings only. An unmeasurable ceiling contributes nothing to the
// line — it is reported as unavailable by `batten budget`, and inventing a figure here to fill
// the space is exactly the failure mode this product refuses.
func budgetSeg(sp *spec.Spec, st *store.Store, run *store.Run) string {
	if sp == nil || !sp.Budget.Set() {
		return ""
	}
	b := sp.Budget
	over, cs, err := st.OverBudget(run.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
	if err != nil {
		return ""
	}
	if over {
		var kinds []string
		for _, c := range cs {
			if c.Exceeded {
				kinds = append(kinds, c.Kind)
			}
		}
		return "OVER BUDGET (" + strings.Join(kinds, ",") + ")"
	}
	for _, c := range cs {
		if c.Kind == "quota_pct" && c.Available {
			return fmt.Sprintf("quota %s/%s%%", pctStr(c.Spent), pctStr(c.Cap))
		}
	}
	return ""
}

// windowSeg renders a rate-limit window, or nothing at all when it is absent. The reset time is
// only shown once the window is nearly gone — before that, knowing when it resets changes no
// decision, and the line is one line.
func windowSeg(label string, w *Window) string {
	p := w.pct()
	if p == nil {
		return ""
	}
	s := label + " " + pctStr(*p) + "%"
	if *p >= quotaWarnPct {
		if d := untilReset(w.reset()); d != "" {
			s += " (" + d + ")"
		}
	}
	return s
}

// untilReset formats the time left in a window. resets_at is epoch SECONDS; a past or missing
// value yields "" rather than a negative duration.
func untilReset(at *int64) string {
	if at == nil {
		return ""
	}
	d := time.Until(time.Unix(*at, 0))
	if d <= 0 {
		return ""
	}
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%dh%02dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// pctStr keeps one decimal only where it carries information: "4.2%" matters, "23.0%" is noise.
func pctStr(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

func tokStr(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ---------- chaining ----------

// chain runs the statusLine command batten displaced at install time and returns its output, so
// wrapping someone's existing status line costs them nothing. Failures are swallowed: a broken
// third-party command must cost the user their segment, not their status line.
func chain(sp *spec.Spec, in Input, raw []byte) string {
	dir := projectDir(sp, in)
	if dir == "" {
		return ""
	}
	cmdline, err := chainedCommand(dir)
	if err != nil || cmdline == "" || IsBatten(cmdline) { // never re-enter ourselves
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), chainTimeout)
	defer cancel()

	c := shellCommand(ctx, cmdline)
	c.Dir = dir
	c.Stdin = bytes.NewReader(raw) // the chained command expects the same payload we got
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = nil // a chained command's stderr must never leak into the line
	if err := c.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(firstLine(out.String()))
}

// shellCommand runs a user-configured command string the way Claude Code itself runs it:
// through a shell, so pipes and quoting behave as the user wrote them. On Windows that is
// cmd.exe — batten never requires a POSIX shell to be installed.
func shellCommand(ctx context.Context, cmdline string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", cmdline)
	}
	return exec.CommandContext(ctx, "sh", "-c", cmdline)
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// projectDir resolves which repo this status line is about. The spec's root wins; otherwise the
// payload's own idea of where it is, which is all we have when cwd is not a batten repo.
func projectDir(sp *spec.Spec, in Input) string {
	if sp != nil && sp.Root != "" {
		return sp.Root
	}
	if in.Workspace != nil && in.Workspace.ProjectDir != "" {
		return in.Workspace.ProjectDir
	}
	if in.CWD != "" {
		return in.CWD
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Clean(wd)
}
