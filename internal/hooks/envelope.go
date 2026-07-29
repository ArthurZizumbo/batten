package hooks

// A denial an agent loop can act on, instead of English it has to parse.
//
// Idea from gentle-ai's uniform failure envelope (v2.1.6), which reports for every failure
// whether it mutated anything, whether it is replayable, whether it applies, and — the part that
// matters most here — WHAT INPUT IS REQUIRED to move forward.
//
// batten's denials were already good prose: they named the unit, said what was missing, and
// printed a command. But prose is what a model has to read and interpret, and interpreting it is
// exactly the step that goes wrong at 3am in an unattended run. The `/batten-night` loop reads
// "has no batten-verified pass" and has to infer that the fix is `batten check` — a fair
// inference, and one it can get wrong in a way nobody is awake to catch.
//
// So every denial now carries three machine-readable fields alongside the prose:
//
//	code     a stable identifier for WHAT is wrong          (no_verdict, stale_target, ...)
//	fix      the exact command that addresses it            ("batten check US-034")
//	retry    whether re-running the same tool call could work without doing anything else
//
// `retry` is the field that is easy to get wrong and expensive to get wrong. A missing verdict is
// NOT retryable — running `git commit` again changes nothing, and a loop that retries it burns
// the window on an identical denial. A stale target is not retryable either. Contention IS.
//
// The prose does not go away. It stays first, because a human reads this too, and because a
// message that is only machine-readable fails the person who has to understand what their agent
// just ran into.

import (
	"fmt"
	"strings"
)

// Reason codes. Stable strings, not an enum with numbers: they travel over JSON to a model, and
// `stale_target` tells it something `E7` does not.
const (
	CodeNoVerdict    = "no_verdict"     // no envelope at all
	CodeNoEvidence   = "no_evidence"    // result ok with an empty evidence[]
	CodeWrongResult  = "wrong_result"   // the verdict exists and does not say what the gate needs
	CodeChecksNotRun = "checks_not_run" // no batten-verified pass
	CodeNotReviewed  = "not_reviewed"   // batten ran the checks; nobody judged the work
	CodeStaleTarget  = "stale_target"   // the tree changed after the checks passed
	CodeMovedBase    = "moved_base"     // HEAD moved under the run
	CodeOverBudget   = "over_budget"    // a declared ceiling was exceeded
	CodeWriteSet     = "write_set"      // another agent owns this file
	CodeUnattributed = "unattributed"   // batten cannot tell which unit this belongs to
	CodeWrongUnit    = "wrong_unit"     // the message names a unit this session is not working
	CodeDegraded     = "degraded"       // batten could not run at all
	CodeNoChecks     = "gate_has_no_checks"
)

// envelope is the machine-readable half of a decision.
type envelope struct {
	Code    string
	Fix     string // a command that can be pasted, or ""
	Retry   bool   // could the SAME tool call succeed on a retry, with nothing else done?
	Message string // the prose, which stays first
}

// render puts the prose first and the fields after, in a shape a model can pick apart with a
// regex and a person can read without one.
func (e envelope) render() string {
	var b strings.Builder
	b.WriteString(e.Message)
	if !strings.HasSuffix(e.Message, "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nbatten.code: %s\nbatten.retry: %t\n", e.Code, e.Retry)
	if e.Fix != "" {
		fmt.Fprintf(&b, "batten.fix: %s\n", e.Fix)
	}
	return strings.TrimRight(b.String(), "\n")
}

// gateWith is h.gate plus the envelope. Every denial in the gate goes through it.
func (h *Handler) gateWith(event string, e envelope) *Output {
	return h.gate(event, e.render())
}

// adviseWith is the same for a warning. An advisory needs the fields just as much: it is the one
// an unattended loop is most likely to sail past, because nothing stopped it.
func adviseWith(event string, e envelope) *Output {
	return advise(event, e.render())
}

// AdviseDegraded is the envelope for "batten could not run at all", built here rather than in
// cmd/batten so the reason code and the retry flag live with every other one.
//
// Retryable, and that is the whole point of saying so: the usual cause is a busy database or an
// antivirus holding a file for a few milliseconds, and the same tool call a moment later works.
// This is the one denial where a loop retrying is the RIGHT behaviour, and it is also the one it
// could least easily infer from the prose.
func AdviseDegraded(message string) *Output {
	return adviseWith("PreToolUse", envelope{
		Code:    CodeDegraded,
		Retry:   true,
		Fix:     "batten doctor",
		Message: message,
	})
}
