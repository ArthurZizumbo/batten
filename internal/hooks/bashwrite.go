package hooks

// Which files a shell command is about to write.
//
// THE HOLE THIS CLOSES, and it is the worst one batten had: `Edit` on a file another agent owns is
// denied, and the SAME write through `sed -i` goes through in silence. The write-set guard — the
// mechanism the fan-out's whole safety argument rests on — was one `sed` away from being optional.
//
// What makes it worse than an ordinary fail-open is that batten is not confused here. It knows the
// owner; it named them in a denial one tool call earlier. This is not "I cannot determine blame",
// it is "I did not look".
//
// ADVISORY, NOT A DENIAL, and that is the point of the first cycle rather than a hedge. This is a
// heuristic reading of shell, and the guard it feeds is a hard block on the critical path of every
// tool call. A false positive here does not inconvenience someone — it stops a legitimate agent
// from working and gets the plugin uninstalled. So: one cycle warning out loud, the warnings land
// in the decision log under their own rule, and `batten report` counts them. When that count is
// boring, it becomes a denial. Promoting it is a two-line change (advise -> h.gate); shipping it
// as a denial today would be trusting a parser nobody has measured.
//
// WHAT IT CANNOT SEE, said plainly rather than left for someone to discover: a python script, a
// Makefile target, a `go run`, anything a third-party tool does. No shell parser reaches those.
// That is not this check's failure, it is its boundary — and it is exactly why the design puts
// `scan-diff` (which compares the real git diff against the declared write-sets afterwards) first
// in priority even though this one comes first in the list.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// bashWriteGuard is the advisory half of the write-set guard: the same ownership question the
// Edit path asks, asked about the files a shell command is about to write.
func (h *Handler) bashWriteGuard(in Input, cmd string) *Output {
	targets := BashWriteTargets(cmd)
	if len(targets) == 0 {
		return nil
	}
	myRun, myNode := h.attribute(in)

	for _, t := range targets {
		rel, ok := h.repoRel(in, t.Path)
		if !ok {
			continue
		}
		if myRun != "" {
			owner, err := h.Store.WriteSetOwner(myRun, h.Spec.Root, rel)
			if err == nil && owner != "" && owner != myNode {
				return h.bashAdvisory(rel, t, "another agent in this run",
					store.DisplayNodeID(owner))
			}
		}
		cross, err := h.Store.WriteSetOwnerAcrossOpenRuns(h.Spec.Project, rel, myRun)
		if err == nil && cross != nil && !h.inADifferentTreeFrom(cross.Worktree) {
			return h.bashAdvisory(rel, t, "another open run", cross.UnitID)
		}
	}
	return nil
}

// bashAdvisory says what the Edit path would have said, and says out loud that it is not saying
// it with the same authority yet.
func (h *Handler) bashAdvisory(rel string, t BashTarget, whose, who string) *Output {
	out := adviseWith("PreToolUse", envelope{
		Code: CodeWriteSet,
		// Not retryable in the sense that matters: running the same command again changes
		// nothing. It is an advisory, so the loop is not blocked either way — but a `retry: true`
		// here would invite exactly the pointless re-run the field exists to prevent.
		Retry: false,
		Message: fmt.Sprintf(
			"batten: this command writes %s via `%s`, and that file belongs to %s (%s).\n"+
				"The same write through Edit would have been DENIED. This is a warning: batten is "+
				"measuring its reading of shell commands for false positives before it starts "+
				"denying on it.\n"+
				"If the file is genuinely yours, the plan is wrong — fix the plan rather than "+
				"crossing the fence in a shell.",
			rel, t.How, whose, who),
	})
	out.Rule = store.RuleBashWrite
	return out
}

// BashTarget is one file a command appears about to write, and the utility that would write it.
type BashTarget struct {
	Path string // exactly as it appeared in the command
	How  string // "> redirect", "sed -i", "tee", ...
}

// redirectOps are the redirection operators that name a file. `>` truncates and `>>` appends;
// both are writes as far as ownership is concerned. `2>` and `&>` are included because a command
// that writes its stderr over another agent's file has still written over it.
var redirectOps = []string{">>", ">", "&>", "&>>"}

// BashWriteTargets extracts the files a command line appears about to write.
//
// It is deliberately literal: it recognises redirection and a fixed list of utilities whose job is
// to modify a file in place. It does not try to understand the shell. Every case it cannot read is
// a case it stays quiet about, which is the right direction for something feeding a warning that
// will one day feed a denial.
func BashWriteTargets(cmd string) []BashTarget {
	var out []BashTarget
	seen := map[string]bool{}
	add := func(path, how string) {
		path = strings.Trim(path, `"'`)
		if path == "" || strings.HasPrefix(path, "-") || strings.HasPrefix(path, "/dev/") ||
			path == "NUL" || strings.ContainsAny(path, "$`") {
			// A path with an unexpanded variable in it is a path batten does not know. Guessing at
			// it would produce a warning naming a file that does not exist.
			return
		}
		key := how + "\x00" + path
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, BashTarget{Path: path, How: how})
	}

	for _, seg := range splitCommands(cmd) {
		tokens := shellSplit(seg)
		if len(tokens) == 0 {
			continue
		}
		// Redirections can appear anywhere in a command, including before the program name.
		for i := 0; i < len(tokens); i++ {
			t := tokens[i]
			op, rest, ok := splitRedirect(t)
			if !ok {
				continue
			}
			if rest != "" {
				add(rest, op+" redirect")
				tokens[i] = ""
				continue
			}
			if i+1 < len(tokens) {
				add(tokens[i+1], op+" redirect")
				tokens[i], tokens[i+1] = "", ""
				i++
			}
		}
		argv := compact(tokens)
		// Leading VAR=value assignments are not the program.
		for len(argv) > 0 && isAssignment(argv[0]) {
			argv = argv[1:]
		}
		if len(argv) == 0 {
			continue
		}
		prog := filepath.Base(strings.TrimSuffix(argv[0], ".exe"))
		args := argv[1:]

		switch prog {
		case "sed":
			// Only `-i` writes. Without it sed is a filter and touches nothing — treating every
			// sed as a write would bury the real ones.
			if !hasInPlaceFlag(args) {
				continue
			}
			// The first non-flag argument is the script, unless an explicit -e/-f carried it.
			files := nonFlagArgs(args, map[string]bool{"-e": true, "-f": true, "--expression": true, "--file": true})
			if !hasExplicitScriptFlag(args) && len(files) > 0 {
				files = files[1:]
			}
			for _, f := range files {
				add(f, "sed -i")
			}
		case "tee":
			for _, f := range nonFlagArgs(args, nil) {
				add(f, "tee")
			}
		case "mv", "cp", "install", "rsync":
			// The LAST operand is the destination. The sources are reads (mv also removes them,
			// which is the destruction guard's business, not this one's).
			if files := nonFlagArgs(args, nil); len(files) >= 2 {
				add(files[len(files)-1], prog)
			}
		case "dd":
			for _, a := range args {
				if v, ok := strings.CutPrefix(a, "of="); ok {
					add(v, "dd of=")
				}
			}
		case "patch":
			// `patch file < diff` names its target; `patch -p1 < diff` does not, and nothing short
			// of reading the diff would tell us. The named form is caught, the other is not, and
			// that gap is what scan-diff exists for.
			for _, f := range nonFlagArgs(args, map[string]bool{"-p": true, "-i": true, "--input": true}) {
				add(f, "patch")
			}
		case "truncate":
			for _, f := range nonFlagArgs(args, map[string]bool{"-s": true, "--size": true}) {
				add(f, "truncate")
			}
		}
	}
	return out
}

// splitRedirect recognises a redirection token, returning the operator and whatever was glued to
// it (`>out.txt` is one token; `> out.txt` is two). A leading file descriptor (`2>`) is stripped.
func splitRedirect(tok string) (op, glued string, ok bool) {
	t := tok
	// A leading descriptor number, as in `2>` or `1>>`.
	for len(t) > 0 && t[0] >= '0' && t[0] <= '9' {
		t = t[1:]
	}
	for _, o := range redirectOps {
		if strings.HasPrefix(t, o) {
			return o, t[len(o):], true
		}
	}
	return "", "", false
}

// splitCommands breaks a command line at the separators that start a new command. Quoted regions
// are respected, so `echo "a && b"` is one command.
func splitCommands(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case ';', '\n', '(', ')':
			flush()
		case '&', '|':
			// `&&`, `||` and a plain pipe all end the current command. A single `&` backgrounds it,
			// which also ends it.
			if i+1 < len(cmd) && cmd[i+1] == c {
				i++
			}
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// shellSplit splits on whitespace, keeping quoted runs together and dropping the quotes.
func shellSplit(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	started := false
	flush := func() {
		if started {
			out = append(out, cur.String())
		}
		cur.Reset()
		started = false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
			started = true
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return out
}

func compact(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func isAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	if i <= 0 {
		return false
	}
	for _, r := range tok[:i] {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// hasInPlaceFlag reports whether a sed invocation edits in place. `-i`, `--in-place`, `-i.bak`
// and bundled short flags like `-ri` all count.
func hasInPlaceFlag(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		if strings.ContainsRune(a[1:], 'i') {
			return true
		}
	}
	return false
}

func hasExplicitScriptFlag(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-e" || a == "-f" || a == "--expression" || a == "--file":
			return true
		case strings.HasPrefix(a, "--expression=") || strings.HasPrefix(a, "--file="):
			return true
		}
	}
	return false
}

// nonFlagArgs returns the operands, skipping flags and the values of the flags in takesValue.
func nonFlagArgs(args []string, takesValue map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			if takesValue[a] && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}
