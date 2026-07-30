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

// TestNoPrivateProjectTokensAreTracked is the local reading of the CI step of the same name.
//
// batten was field-tested against a private repo, and the name of that repo did not stay in the
// field-test report: it reached `internal/scan`, `cmd/batten`, both matrix scripts, the ROADMAP and
// the manual — ten files of live code, tests and docs, none of which any decision about
// `docs/field-test/` would ever touch.
//
// Two details are load-bearing:
//
//   - The pattern is masked with single-character classes. It matches the real token; the bytes of
//     the files that CARRY the pattern (this one, the CI step, and the plan that documents both) do
//     not match themselves. Unmasked, the guard would fail on its own source and the obvious "fix"
//     would be a path exclusion that blinds it. `\b` is not decoration either: without a word
//     boundary the second token matches inside ordinary Spanish words.
//   - Nothing is excluded by PATH except `docs/field-test/`, which is the open subject of decision
//     1.1 in docs/general/plan_publicacion.md. In particular `graphify-out/` is IN scope, unlike in
//     the personal-paths guard next to it: a token is not a path, and a 2 MB generated `graph.json`
//     is the one file in this repo where something private can land without a human or a check ever
//     seeing it.
//
// And there is a SECOND lock on that same door, found by planting the token in `graph.json` and
// watching a guard that had already dropped `:!graphify-out` still pass. `.gitattributes` marks
// the three generated artifacts `-diff` so a rebuild does not bury real changes — and `git grep -I`
// skips `-diff` files as binary. The flag that keeps the graph out of diffs also keeps it out of
// the grep. So no `-I` here: a "Binary file ... matches" line is the correct output for a
// minified 2 MB JSON, and it is the only output that arrives at all.
func TestNoPrivateProjectTokensAreTracked(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	const masked = `[p]royecto[_]ui|\bM[N]A\b`

	cmd := exec.Command(git, "-C", repoRoot(t), "grep", "-nE", masked, "--", ".", ":!docs/field-test")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return // git grep exits 1 on no match: the tree is clean
		}
		t.Skipf("not a git checkout: %v", err)
	}
	t.Errorf("the private field-test subject is named outside docs/field-test/:\n%s\n"+
		"Use the neutral fixture name (replica-ui), or describe the repo without naming it. "+
		"Every one of these files travels to anyone who clones this repo.", out)
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

// TestEveryDocumentThatDrivesAVerdictNamesBattenCheck is finding #16 (plan_publicacion.md §5), as a rule rather
// than a list — so the next command that learns to record a verdict inherits it.
//
// A gate that declares `checks:` needs TWO verdicts from two different producers: `batten check`
// proves the declared checks were RUN, and the envelope proves somebody judged the work against
// its acceptance criteria. `/batten-verify` walked the agent all the way to `batten verdict` and
// told it to run the gate's checks *by hand* — which produces citations, not a batten-sourced
// pass. The flow therefore ended where the field test found it: at a commit denied with "has no
// batten-verified pass. The gate's checks must be RUN, not asserted. Run: batten check <unit>",
// naming a command that appeared in none of these documents.
//
// The adopter cannot recover from that: they followed the instructions and the instructions do not
// mention the way out. Which is why this is a BLOCKS-adoption finding and not a documentation
// nicety.
func TestEveryDocumentThatDrivesAVerdictNamesBattenCheck(t *testing.T) {
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

	checked := 0
	for _, doc := range docs {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "batten verdict") {
			continue // a document that never records a verdict cannot walk anyone into this deny
		}
		checked++
		if !strings.Contains(src, "batten check") {
			t.Errorf("%s tells the agent to record a verdict and never names `batten check`.\n"+
				"With `checks:` declared, that path ends at a denied commit — \"has no "+
				"batten-verified pass\" — naming a command this document does not contain, so the "+
				"reader has nowhere to go.", filepath.Base(doc))
		}
	}
	if checked == 0 {
		t.Fatal("no document drives a verdict at all; this test is asserting nothing")
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
