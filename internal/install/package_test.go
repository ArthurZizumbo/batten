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
