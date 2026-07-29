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
	"os/exec"
	"path/filepath"
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
