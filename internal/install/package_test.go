package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// What ships, and whether the machine that receives it can run it.
//
// The repo is authored on Windows, where git does not track the execute bit and nothing local ever
// notices its absence. .gitattributes already settles line endings for the same reason; the mode is
// the other half of that lesson, and it was missing: both bootstrap.sh copies were committed 100644,
// so `${CLAUDE_PLUGIN_ROOT}/scripts/bootstrap.sh` was `Permission denied` on macOS and Linux — the
// two platforms this machine cannot reproduce.

// TestTrackedShellScriptsAreExecutable reads the INDEX, not the working tree. On Windows the
// working tree mode is meaningless (core.fileMode is false); what travels to a Unix clone is
// whatever git recorded.
func TestTrackedShellScriptsAreExecutable(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	out, err := exec.Command(git, "-C", repoRoot(t), "ls-files", "-s", "*.sh").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("git tracks no shell scripts at all; this test is looking in the wrong place")
	}
	for _, line := range lines {
		mode, rest, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("cannot parse `git ls-files -s` output: %q", line)
		}
		if mode != "100755" {
			name := rest
			if i := strings.LastIndex(rest, "\t"); i >= 0 {
				name = rest[i+1:]
			}
			t.Errorf("%s is tracked %s: on macOS and Linux that is `Permission denied`. "+
				"For bootstrap.sh it is worse than an error message — the binary is never "+
				"downloaded and every hook no-ops in silence.\n"+
				"    Fix: git update-index --chmod=+x %s", name, mode, name)
		}
	}
}

// Every /batten-* command and every skill opens with a bash block that runs a bare `batten`. That
// resolves only because Claude Code puts the plugin's bin/ on PATH — so when the binary is missing
// the model gets `command not found` from a block it was told to run, and carries on: it plans, it
// fans out, it writes the artifact, and nothing was ever gated. The failure has to be loud at the
// first line, because everything after it is worthless.
func TestEveryCommandRefusesToRunWithoutTheBinary(t *testing.T) {
	pkg := filepath.Join(repoRoot(t), "plugin", "claude-code")
	var docs []string
	for _, pat := range []string{
		filepath.Join(pkg, "commands", "*.md"),
		filepath.Join(pkg, "skills", "*", "SKILL.md"),
	} {
		found, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, found...)
	}
	if len(docs) == 0 {
		t.Fatal("no commands or skills found; this test is looking in the wrong place")
	}

	for _, doc := range docs {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		block, ok := firstBashBlockCalling(string(b), "batten")
		if !ok {
			continue // a document that never runs batten needs no preflight
		}
		if !strings.Contains(block, "command -v batten") {
			t.Errorf("%s runs batten in its first bash block with no preflight:\n%s\n"+
				"Without `command -v batten || exit 1` the block is a command-not-found the model "+
				"reads and moves past, and the whole phase runs ungated.",
				filepath.Base(doc), block)
		}
	}
}

// firstBashBlockCalling returns the first ```bash block whose body starts a line with cmd.
func firstBashBlockCalling(src, cmd string) (string, bool) {
	var body []string
	in := false
	for _, line := range strings.Split(src, "\n") {
		switch {
		case !in && strings.TrimSpace(line) == "```bash":
			in, body = true, nil
		case in && strings.TrimSpace(line) == "```":
			in = false
			for _, l := range body {
				if strings.HasPrefix(strings.TrimSpace(l), cmd+" ") {
					return strings.Join(body, "\n"), true
				}
			}
		case in:
			body = append(body, line)
		}
	}
	return "", false
}

// The mode is the fix; invoking through an interpreter is the belt. An archive that loses the bit
// (a zip round-trip, a `cp` on a filesystem with no exec bit, an extraction as a different user)
// would otherwise take the gate down with it.
func TestTheBootstrapHookNamesAnInterpreter(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "plugin", "claude-code", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "bootstrap.sh") {
			continue
		}
		if !strings.Contains(line, "bash ") {
			t.Errorf("hooks.json runs bootstrap.sh directly:\n  %s\n"+
				"Name the interpreter (`bash \"...\"`) so a lost execute bit degrades to a working "+
				"install instead of Permission denied.", strings.TrimSpace(line))
		}
		return
	}
	t.Error("hooks.json never invokes bootstrap.sh")
}
