package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-q", "-m", "seed"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git is not usable here (%v): %s", err, out)
		}
	}
	return dir
}

// TestChangedFilesNeverReportsAnEmptyDiffForAFailure is the whole reason this package exists.
//
// "git could not tell me" and "the unit changed nothing" are opposite facts, and the consumers
// turn the second one into a statement to the agent about what it may touch. A missing anchor, a
// base rewritten by a rebase, and a directory that is not a repository must all be errors — the
// same rule store.TargetDigest follows for the empty digest.
func TestChangedFilesNeverReportsAnEmptyDiffForAFailure(t *testing.T) {
	dir := repo(t)

	if _, err := ChangedFiles(dir, ""); err == nil {
		t.Error("no base at all reported success; an unanchored run has an UNKNOWN diff, not an empty one")
	}
	if _, err := ChangedFiles(dir, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("a base that is not in this repository reported success — that is what a rebase " +
			"leaves behind, and it must not read as 'nothing changed'")
	}
	if _, err := ChangedFiles(t.TempDir(), "HEAD"); err == nil {
		t.Error("a directory that is not a git repository reported success")
	}
	if _, err := Output("", "status"); err == nil {
		t.Error("an empty root reported success")
	}
}

// TestChangedFilesSeesUncommittedWork. A phase scoped to `diff_from: anchor` runs while the work
// is in progress, so diffing base..HEAD would report an empty scope for a unit that has changed
// twenty files and not committed any of them.
func TestChangedFilesSeesUncommittedWork(t *testing.T) {
	dir := repo(t)
	base, err := Output(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("seed.txt", "modified\n")  // tracked, unstaged
	write("added.go", "package p\n") // untracked
	if err := os.MkdirAll(filepath.Join(dir, ".batten"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(".batten", "batten.db"), "sqlite") // batten's own state

	got, err := ChangedFiles(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range got {
		have[p] = true
	}
	for _, want := range []string{"seed.txt", "added.go"} {
		if !have[want] {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	if have[".batten/batten.db"] {
		t.Errorf("batten's own ledger is being reported as the unit's work: %v", got)
	}

	// Staged changes count too: the work is done whether or not `git add` has run.
	if out, err := exec.Command("git", "-C", dir, "add", "added.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	got, err = ChangedFiles(dir, base)
	if err != nil {
		t.Fatal(err)
	}
	have = map[string]bool{}
	for _, p := range got {
		have[p] = true
	}
	if !have["added.go"] {
		t.Errorf("a staged new file dropped out of the diff: %v", got)
	}
}

// TestCanonicalAgreesWhetherOrNotThePathExists is the invariant the write-set guard rests on.
//
// Canonical resolved symlinks only for paths that EXIST. That looked harmless and was not: a repo
// root exists and resolves (on macOS `/var` is a symlink to `/private/var`), while a file an agent
// is about to CREATE does not and came back unresolved. The two sides of every comparison then sat
// in different spellings exactly when one existed and the other did not — filepath.Rel reported a
// file inside the repo as outside it, and the write-set guard allowed every write it exists to
// deny. Seven tests went "silent-allow" on the two platforms that have path aliases, and none on
// the one it was written on.
//
// So the property is not "resolves symlinks", it is "answers the same way for a directory and for
// a path under it, whether or not that path exists yet".
func TestCanonicalAgreesWhetherOrNotThePathExists(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable here (%v); the aliasing this guards against is macOS /var "+
			"and Windows 8.3, and CI covers both", err)
	}

	root := Canonical(link)
	if root == link {
		t.Skip("this filesystem did not alias the link; nothing to prove here")
	}

	// A file that exists, and one that does not. Both must land under the same canonical root, or
	// the guard's containment check is deciding on a spelling rather than on a location.
	existing := filepath.Join(link, "sub")
	future := filepath.Join(link, "sub", "not-created-yet.go")
	for _, c := range []struct{ name, path, want string }{
		{"existing", existing, filepath.Join(root, "sub")},
		{"not created yet", future, filepath.Join(root, "sub", "not-created-yet.go")},
	} {
		if got := Canonical(c.path); got != c.want {
			t.Errorf("Canonical(%s) = %q, want %q", c.name, got, c.want)
		}
	}

	// And the consequence, stated as the thing that actually broke: a file under the root must be
	// INSIDE it, created or not.
	for _, p := range []string{existing, future} {
		if rel, inside := RelTo(link, p); !inside {
			t.Errorf("RelTo(root, %q) says outside the repo (rel=%q). That is the answer that "+
				"makes the write-set guard wave a trespass through", p, rel)
		}
	}
}
