package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// worktreeOf cuts a linked worktree off repo and returns its path.
func worktreeOf(t *testing.T, repoDir, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt-"+branch)
	out, err := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", branch, wt).CombinedOutput()
	if err != nil {
		t.Skipf("this git cannot create worktrees here (%v): %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", wt).Run()
	})
	return wt
}

// TestTheLockIsSharedBetweenWorktrees is the entire point of scoping to the git COMMON dir, and
// it is the assertion that fails if somebody "simplifies" it to --git-dir.
//
// A linked worktree's `.git` is a FILE, and --git-dir there resolves to that worktree's PRIVATE
// directory. Lock there and every process takes its own lock, every process succeeds, and the
// mutual exclusion is imaginary — which is precisely the race gentle-ai closed in v2.1.9 and
// precisely the arrangement batten now creates on purpose.
func TestTheLockIsSharedBetweenWorktrees(t *testing.T) {
	main := repo(t)
	wt := worktreeOf(t, main, "other")

	mainCommon, err := CommonDir(main)
	if err != nil {
		t.Fatal(err)
	}
	wtCommon, err := CommonDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if !SameTree(mainCommon, wtCommon) {
		t.Fatalf("the two trees report different shared git dirs (%s vs %s); a lock placed there "+
			"excludes nothing", mainCommon, wtCommon)
	}

	release, err := Lock(main)
	if err != nil {
		t.Fatalf("could not take the lock from the main tree: %v", err)
	}
	// Held. A second attempt FROM THE OTHER TREE must not get it.
	if _, err := lockFor(wt, 50*time.Millisecond); err == nil {
		t.Error("the worktree took the lock while the main tree was holding it — the lock is " +
			"per-worktree, which is the same as no lock at all")
	} else if !strings.Contains(err.Error(), "holding the repository lock") {
		t.Errorf("the contention message does not say what is happening: %v", err)
	}

	release()
	if _, err := os.Stat(filepath.Join(mainCommon, LockName)); err == nil {
		t.Error("release left the lock file behind; the next batten would wait for a ghost")
	}

	// And after release the other tree gets it, or the lock is a one-shot.
	release2, err := Lock(wt)
	if err != nil {
		t.Fatalf("the lock was not reusable after release: %v", err)
	}
	release2()
}

// TestTheLockNamesItsHolder. A stale lock file is a thing that happens — a machine that lost
// power mid-merge leaves one — and the recovery has to be obvious. batten never steals it: a tool
// that breaks a lock on a timer eventually breaks one that was doing something.
func TestTheLockNamesItsHolder(t *testing.T) {
	main := repo(t)
	release, err := Lock(main)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, err = lockFor(main, 50*time.Millisecond)
	if err == nil {
		t.Fatal("a second lock succeeded")
	}
	msg := err.Error()
	for _, want := range []string{"pid ", LockName, "delete it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the contention message is missing %q, so the reader cannot recover from it:\n%s",
				want, msg)
		}
	}
}
