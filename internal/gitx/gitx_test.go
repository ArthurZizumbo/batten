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
