// Package gitx is the git plumbing batten shells out to.
//
// It exists because the same two questions are asked from three unrelated places — what a phase
// is scoped to (`diff_from: anchor`), what a fan-out actually touched, and where this working
// tree's shared git directory is — and each caller had started growing its own `exec.Command`.
//
// ONE RULE, and it is the reason this is a package rather than a helper: a failure here returns
// an error, never an empty list. "git could not tell me" and "nothing changed" are opposite
// facts, and every consumer of this package turns the second one into a decision. Collapsing
// them is the same inversion that makes an empty target digest dangerous (see store.TargetDigest)
// and the same one that made `batten hook`'s silence unreadable.
package gitx

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Output runs git in root and returns its trimmed stdout.
func Output(root string, args ...string) (string, error) {
	if root == "" {
		return "", errors.New("no repository root")
	}
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// IsRepo reports whether root is inside a git work tree.
func IsRepo(root string) bool {
	out, err := Output(root, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel is the root of the working tree containing dir. In a worktree this is the WORKTREE's
// root, not the main repository's — which is the whole point: two worktrees of one repo are two
// different trees holding two different checkouts of the same paths.
func TopLevel(dir string) (string, error) {
	out, err := Output(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(out), nil
}

// CommonDir is the git directory SHARED by the main repository and every worktree cut from it —
// the one holding refs, objects and the worktrees/ administrative area.
//
// This is the distinction gentle-ai's v2.1.9 lock fix turns on, and it is easy to get wrong in a
// way that leaves the lock doing nothing. A linked worktree's `.git` is a FILE containing
// `gitdir: <path>`, and `--git-dir` there resolves to that worktree's PRIVATE directory. A lock
// placed in it is per-worktree: every process takes its own lock, every process gets it, and the
// mutual exclusion is entirely imaginary. Only the common dir is shared.
func CommonDir(dir string) (string, error) {
	out, err := Output(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	p := filepath.FromSlash(out)
	if !filepath.IsAbs(p) {
		// git answers relatively when it can; resolve against the tree we asked from.
		abs, aerr := filepath.Abs(filepath.Join(dir, p))
		if aerr != nil {
			return "", aerr
		}
		p = abs
	}
	return p, nil
}

// Worktree is one checkout of a repository: the main one plus every linked worktree.
type Worktree struct {
	Path   string
	Branch string // "refs/heads/x" reduced to "x"; empty when detached
	Head   string
}

// Worktrees lists every working tree attached to this repository.
func Worktrees(dir string) ([]Worktree, error) {
	out, err := Output(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: filepath.FromSlash(strings.TrimPrefix(line, "worktree "))})
			cur = &list[len(list)-1]
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return list, nil
}

// SameTree reports whether two working-tree roots are the same tree. Compared after Clean and
// case-insensitively on Windows, because one side comes from git and the other from a hook
// payload, and those two do not agree about drive-letter case.
func SameTree(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// IsDirty reports whether a working tree has uncommitted work, ignoring the given paths (which
// is how batten's own database is kept from making every tree look dirty).
func IsDirty(root string, ignore ...string) (bool, string, error) {
	out, err := Output(root, "status", "--porcelain", "-uall")
	if err != nil {
		return false, "", err
	}
	drop := map[string]bool{}
	for _, e := range ignore {
		if e == "" {
			continue
		}
		if rel, rerr := filepath.Rel(root, e); rerr == nil {
			e = rel
		}
		e = strings.ReplaceAll(e, `\`, "/")
		for _, sfx := range []string{"", "-wal", "-shm", "-journal"} {
			drop[e+sfx] = true
		}
	}
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.Trim(strings.ReplaceAll(line[3:], `\`, "/"), `"`)
		if drop[p] || isBattenState(p) {
			continue
		}
		kept = append(kept, strings.TrimSpace(line))
	}
	if len(kept) == 0 {
		return false, "", nil
	}
	if len(kept) > 6 {
		kept = append(kept[:6], fmt.Sprintf("... and %d more", len(kept)-6))
	}
	return true, strings.Join(kept, "\n  "), nil
}

// ChangedFiles lists every repo-relative path that differs between base and the CURRENT working
// tree: tracked modifications (staged or not) plus untracked files. Paths are slash-normalised
// and sorted.
//
// The working tree, not HEAD. A phase that declares `diff_from: anchor` is scoped to the work the
// unit has done, and most of that work is uncommitted while the phase is running — diffing
// base..HEAD would report an empty scope for a unit that has changed twenty files and not
// committed yet, which is the answer most likely to be believed and most likely to be wrong.
//
// batten's own state is excluded for the same reason store.TargetDigest excludes it: repos that
// commit their run database exist, and batten writing to its own ledger must not show up as work
// the unit did. `exclude` names that database — pass its path and its WAL and SHM sidecars go
// with it. It is a parameter rather than a pattern because the name is the USER's: BATTEN_DB
// points wherever they say, and a run whose database was called `state.db` reported all three
// SQLite files as part of the unit's diff until it was pointed at a real one and read.
func ChangedFiles(root, base string, exclude ...string) ([]string, error) {
	if base == "" {
		return nil, errors.New("no base commit to diff from")
	}
	drop := map[string]bool{}
	for _, e := range exclude {
		if e == "" {
			continue
		}
		if rel, err := filepath.Rel(root, e); err == nil {
			e = rel
		}
		e = strings.ReplaceAll(e, `\`, "/")
		for _, sfx := range []string{"", "-wal", "-shm", "-journal"} {
			drop[e+sfx] = true
		}
	}
	tracked, err := Output(root, "diff", "--name-only", base, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := Output(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, block := range []string{tracked, untracked} {
		for _, line := range strings.Split(block, "\n") {
			p := strings.ReplaceAll(strings.TrimSpace(line), `\`, "/")
			if p == "" || seen[p] || drop[p] || isBattenState(p) {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// isBattenState is the fallback for the conventional locations, used when the caller cannot say
// where its database is (or is not holding one).
func isBattenState(p string) bool {
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	return p == ".batten" || strings.HasPrefix(p, ".batten/") || strings.HasPrefix(base, "batten.db")
}
