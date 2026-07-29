package gitx

// The lock two worktrees need, scoped where it actually excludes anything.
//
// Taken from gentle-ai v2.1.9, which closed an inode race in `flock` over movable lineages with a
// shared lock scoped to the git COMMON dir. batten did not need it while it never created a
// worktree. It does now, and the failure it prevents is not hypothetical:
//
//   - `git worktree add` writes into the shared administrative area. Two of them at once corrupt
//     it, and the corruption shows up later as a worktree git will not prune.
//   - the gated merge reads the verdicts, decides, and then runs `git merge`. Two merges racing
//     means the second one decided against a repository state the first was in the middle of
//     changing — the check-then-act that the whole verdict gate exists to prevent elsewhere.
//
// WHERE the lock lives is the entire lesson. A linked worktree's `.git` is a FILE, and a lock
// placed relative to it is per-worktree: every process takes its own, every process succeeds, and
// nothing is excluded. See CommonDir.
//
// WHAT IT DOES NOT DO: it does not protect the SQLite database. That is WAL plus busy_timeout,
// which handles many short writers correctly and is the right tool. This lock protects the
// git-level operations that have no equivalent, and holding it across a database write would
// serialise the hook path for no reason.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockName is the lock file batten takes inside the git common dir. Named for batten so that a
// human who finds it knows who left it and can delete it.
const LockName = "batten-worktree.lock"

// lockTimeout is how long to wait for another batten to finish. Generous, because the operations
// under it are `git worktree add` and `git merge` on a real repository, and short, because the
// alternative to waiting is failing in front of somebody.
const lockTimeout = 20 * time.Second

// Lock takes the repository-wide batten lock and returns the release function.
//
// It never steals. A lock file that will not go away is reported by name, with its age and the
// process that wrote it, and the caller is told how to remove it — because the alternative is
// stealing a lock from a `git merge` that is halfway through, and a tool that breaks a lock on a
// timer eventually breaks one that was doing something.
func Lock(dir string) (release func(), err error) { return lockFor(dir, lockTimeout) }

// lockFor is Lock with the wait made explicit, so the contention tests do not have to sit
// through the real timeout to prove that contention is detected.
func lockFor(dir string, timeout time.Duration) (release func(), err error) {
	common, err := CommonDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot locate the shared git directory: %w", err)
	}
	path := filepath.Join(common, LockName)

	deadline := time.Now().Add(timeout)
	wait := 20 * time.Millisecond
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "pid=%d\nsince=%d\n", os.Getpid(), time.Now().Unix())
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("cannot create %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another batten is holding the repository lock%s\n"+
				"  lock file: %s\n"+
				"  If no batten is running, that file is stale — delete it and try again.",
				holderOf(path), path)
		}
		time.Sleep(wait)
		if wait < time.Second {
			wait *= 2
		}
	}
}

// holderOf describes whoever wrote the lock, for the message. Best-effort: a lock we cannot read
// is still a lock, and saying nothing about it beats guessing.
func holderOf(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var pid, since string
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "pid":
			pid = v
		case "since":
			since = v
		}
	}
	out := ""
	if pid != "" {
		out += " (pid " + pid
		if n, err := strconv.ParseInt(since, 10, 64); err == nil {
			out += fmt.Sprintf(", held %s", time.Since(time.Unix(n, 0)).Round(time.Second))
		}
		out += ")"
	}
	return out
}
