package hooks

// The four absolute rules of `/batten-night`, as mechanism rather than markdown.
//
// This is the loudest possible example of batten not taking its own medicine. The README's first
// line is that a rule a document can only ASK for, a hook can IMPOSE — and the most dangerous
// command in the plugin was governed entirely by 112 lines of prose asking the model to behave.
// The one place where the failure is unrecoverable and nobody is awake to catch it was the one
// place with no mechanism at all.
//
// One flag on the run — `runs.mode = 'unattended'`, set by `batten unattended <unit>` when the
// loop starts — turns all four into denials:
//
//	1. never delete            → this file: a PreToolUse/Bash matcher
//	2. honour max_iterations   → `batten iterate` counts, `batten phase` refuses past the ceiling
//	3. never override the gate → `batten override` refuses
//	4. do not commit           → the verdict gate refuses, verdicts or no verdicts
//
// No new orchestration. batten still does not run the loop — the loop already exists and is more
// mature than anything batten would add. batten just stops being the only participant in an
// unsupervised run that cannot say no.
//
// NONE of these denials carries a `fix`. That is the same deliberate omission the write-set
// collision makes: every other denial hands over the way out, and these must not, because the way
// out is `batten unattended <unit> --off`, and printing that to a loop nobody is watching is an
// instruction to take its own fence down. The way through is a human in the morning.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// destructiveRe matches the commands rule 1 names, and a few it obviously means.
//
// Written as a set of narrow patterns rather than one clever one, because each entry needs to be
// readable by the person deciding whether it is too broad. The order of the alternation does not
// matter; what matters is that every pattern is anchored to a command position (start of line, or
// after a separator) so `echo "rm -rf /"` and `grep -r rm .` are not treated as deletions.
var destructiveRe = []*regexp.Regexp{
	// rm in any form. `rmdir` too: an empty directory is still somebody's directory.
	regexp.MustCompile(`(?:^|[;&|(]|&&|\|\|)\s*(?:sudo\s+)?rm(?:dir)?(?:\.exe)?\s`),
	// The three git commands that throw work away without a reflog entry you would find at 3am.
	regexp.MustCompile(`(?:^|[;&|(]|&&|\|\|)\s*git(?:\.exe)?\s+(?:[^\s|;&]+\s+)*reset\s+(?:[^\s|;&]+\s+)*--hard`),
	regexp.MustCompile(`(?:^|[;&|(]|&&|\|\|)\s*git(?:\.exe)?\s+(?:[^\s|;&]+\s+)*checkout\s+(?:[^\s|;&]+\s+)*--\s`),
	regexp.MustCompile(`(?:^|[;&|(]|&&|\|\|)\s*git(?:\.exe)?\s+(?:[^\s|;&]+\s+)*(?:clean(?:\s|$)|branch\s+(?:-D|-d\s)|push\s+(?:.*\s)?--force)`),
	// `git restore <path>` discards working-tree changes as thoroughly as `checkout --` did; it
	// is the modern spelling of the same thing.
	//
	// `(?:\s|$)` rather than `\b`, and that is not fussiness: `\b` matches between `restore` and
	// the `-` of `git restore-me`, so the word-boundary spelling refused a command that does not
	// exist and would not have deleted anything if it did. Every subcommand here ends the same
	// way for the same reason.
	regexp.MustCompile(`(?:^|[;&|(]|&&|\|\|)\s*git(?:\.exe)?\s+(?:[^\s|;&]+\s+)*restore(?:\s|$)`),
	// Windows and PowerShell spellings, because this is a Windows-first tool and `del` deletes
	// exactly as permanently as `rm`.
	regexp.MustCompile(`(?i)(?:^|[;&|(]|&&|\|\|)\s*(?:del|erase|rd|Remove-Item)\s`),
	// truncate(1) says what it does.
	regexp.MustCompile(`(?:^|[;&|(]|&&|\|\|)\s*truncate\s`),
}

// truncatingRedirectRe finds `> target`, which empties a file as thoroughly as rm does, and which
// rule 1 names explicitly. `>>` appends and is left alone; Go's regexp has no lookbehind, so the
// `[^>]` before the `>` does that job, and the `[^>&|\s]` after it keeps `2>&1` out.
var truncatingRedirectRe = regexp.MustCompile(`(?:^|[^>])>\s*([^>&|;\s]+)`)

// destructionGuard is rule 1. Returns nil when there is nothing to say.
func (h *Handler) destructionGuard(in Input, cmd string) *Output {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	// The cheap question first: almost every tool call in the world is not a deletion, and this
	// runs on the PreToolUse path of every Bash call in the session.
	matched := ""
	for _, re := range destructiveRe {
		if m := re.FindString(cmd); m != "" {
			matched = strings.TrimSpace(m)
			break
		}
	}
	if matched == "" {
		matched = h.truncatesSomethingThatExists(in, cmd)
	}
	if matched == "" {
		return nil
	}
	run, err := h.Store.UnattendedOpenRun(h.Spec.Project)
	if err != nil || run == nil {
		return nil // supervised: there is a human, and rule 1 is theirs to apply
	}
	return h.gateWith("PreToolUse", envelope{
		Code:  CodeUnattendedDelete,
		Retry: false,
		Message: fmt.Sprintf(
			"batten: %s is running unattended, and an unattended run does not delete.\n"+
				"  refused: %s\n"+
				"Write it in the WANTED TO DELETE section of the morning report, with the reason, "+
				"and carry on.\n"+
				"The asymmetry is the whole rule: a stale file costs somebody ten seconds tomorrow; "+
				"deleting the wrong one costs work nobody gets back, in a run nobody was watching. "+
				"At 3am you cannot tell which one this is.",
			run.UnitID, firstLine(cmd)),
	})
}

// truncatesSomethingThatExists narrows `>` down to the case rule 1 is actually about.
//
// Denying every truncating redirect would deny `go test ./... > out.txt`, and an unattended loop
// that cannot capture output to a scratch file is an unattended loop that does not run. The
// dangerous case is precise and checkable: `>` onto a file that ALREADY EXISTS and is inside the
// repository. Creating a new file destroys nothing; emptying a tracked one destroys exactly what
// rule 1 says nobody can get back.
//
// Erring toward allowing here is the right direction ONLY because it is paired with a rule that
// errs toward denying everywhere else: a path this cannot resolve is left alone rather than
// guessed at, and every `rm` is refused whether or not its target exists.
func (h *Handler) truncatesSomethingThatExists(in Input, cmd string) string {
	root := h.Spec.Root
	cwd := in.CWD
	if cwd == "" {
		cwd = root
	}
	for _, m := range truncatingRedirectRe.FindAllStringSubmatch(cmd, -1) {
		target := strings.Trim(m[1], `"'`)
		if target == "" || strings.HasPrefix(target, "/dev/") || target == "NUL" {
			continue
		}
		p := target
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "../") {
			continue // outside the repo: not this run's work to protect
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return "> " + target
		}
	}
	return ""
}

// unattendedCommitGuard is rule 4. It runs BEFORE the verdict gate's own checks, because in an
// unsupervised run a commit is refused whether or not the verdicts are there: the point is not
// that the work is unverified, it is that a human closes.
func (h *Handler) unattendedCommitGuard(unit string) *Output {
	run, err := h.Store.UnattendedOpenRun(h.Spec.Project)
	if err != nil || run == nil {
		return nil
	}
	if unit != "" && run.UnitID != unit {
		return nil // some other unit's unattended run; this commit is not its business
	}
	// Tagged here rather than by the caller. preToolUse labels everything the Bash branch returns
	// as `verdict_gate`, so this denial was being counted in `batten report` under "commits denied
	// (no verdict, empty evidence, or checks not run)" — none of which was true of it. A report
	// that files a refusal under the wrong cause is the honesty-of-surface failure this project
	// keeps finding in other people's tools.
	out := h.gateWith("PreToolUse", envelope{
		Code:  CodeUnattendedCommit,
		Retry: false,
		Message: fmt.Sprintf(
			"batten: %s is running unattended, and an unattended run stops before the close.\n"+
				"That is deliberate and it is not a shortfall: leave the work committed to nothing, "+
				"write the report, and let a human read it and close.\n"+
				"A blocked or unclosed unit at the end of an unsupervised run is a SUCCESSFUL outcome.",
			run.UnitID),
	})
	out.Rule = store.RuleUnattended
	return out
}

// IterationCeiling reports whether a run has used up `budget.max_iterations`, and is the reason
// that field stops being the worst entry on the declared-as-future list.
//
// It was declared in the spec, returned over MCP, drawn in the TUI as `iters %d/%d`, and never
// incremented by anything: runs.iterations read 0 all night, every night. The ceiling on the most
// dangerous command batten ships was a sentence in a markdown file.
func IterationCeiling(run *store.Run, max int) (reached bool, used, ceiling int) {
	if run == nil || max <= 0 {
		return false, 0, 0
	}
	return run.Iterations >= max, run.Iterations, max
}
