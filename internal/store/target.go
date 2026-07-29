package store

// The target a verdict was made ABOUT.
//
// `batten check` runs the gate's declared commands and records what they printed. That is the
// whole claim: "verified" means batten watched it pass. But it recorded no trace of WHAT it
// watched pass — and between the check and the commit, a formatter, a build script, a file
// watcher, or the agent itself can change the tree. The verdict still says batten-verified, and
// it is now describing a state that no longer exists.
//
// Idea taken from gentle-ai's CAS-bound receipts (v2.2.0), which freeze the candidate and refuse
// to deliver against a drifted one, reporting `stale_target_identity` rather than a generic
// failure. Adapted rather than copied: batten does not own the index and must not freeze it, so
// it records a digest and REPORTS the drift instead of trying to prevent it.
//
// WHAT THE DIGEST COVERS, stated because a digest nobody understands is worse than none:
//
//   - HEAD, so a commit or a rebase between check and commit is drift.
//   - every tracked modification's CONTENT, via `git diff HEAD`.
//   - the NAME of every untracked file, via `git status --porcelain -uall`.
//
// What it does NOT cover: the contents of untracked files. Adding one is drift because its name
// appears; editing one already present is not. Hashing every untracked byte would make the gate
// walk node_modules on every commit, and the cost lands on the fast path of a hook.

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
)

// TargetDigest fingerprints the working tree a verdict is about.
//
// The empty string means "not measurable here", never "nothing changed", and every caller has
// to treat it that way: a repo that is not a git repo at all — the literal state of the project
// batten was field-tested against — has no tree to fingerprint, and inventing a constant for it
// would turn "I cannot tell" into "I checked and it is fine".
func TargetDigest(root string) string {
	if root == "" {
		return ""
	}
	head, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		// Not a git repo, or a repo with no commits yet. Either way there is nothing to anchor
		// a comparison to.
		return ""
	}
	status, err := gitOutput(root, "status", "--porcelain", "-uall")
	if err != nil {
		return ""
	}
	// The same exclusion as the status filter, applied where git can do it for us. It is not
	// hypothetical: a repo that COMMITS .batten/batten.db — which people do, to share a run
	// history — makes every batten write a tracked modification, so recording a verdict would
	// change the tree the verdict is about and the gate would call its own pass stale.
	diff, err := gitOutput(root, "diff", "HEAD", "--",
		".", ":(exclude).batten/**", ":(exclude)batten.db*", ":(exclude)**/batten.db*")
	if err != nil {
		// An older git without pathspec magic still deserves an answer.
		if diff, err = gitOutput(root, "diff", "HEAD"); err != nil {
			return ""
		}
	}

	// TWO parts, not one hash, because the two ways a tree drifts need opposite answers.
	//
	// "A formatter touched your files" and "you rebased" are both drift, and telling someone to
	// re-run their checks is right for the first and beside the point for the second — they need
	// to know the history moved under them. gentle-ai calls this distinction `scope-changed`, and
	// reports the exact recovery rather than a generic refusal. A single opaque hash cannot make
	// it: it can only say "different".
	h := sha256.New()
	h.Write([]byte(withoutBattensOwnState(status) + "\x00" + diff))
	return head[:min(len(head), 12)] + ":" + hex.EncodeToString(h.Sum(nil))[:24]
}

// SplitDigest separates a target digest into the commit it was taken at and the fingerprint of
// the uncommitted work on top of it. Either may be empty for a digest this version did not write.
func SplitDigest(d string) (head, content string) {
	if i := strings.IndexByte(d, ':'); i >= 0 {
		return d[:i], d[i+1:]
	}
	return "", d
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// withoutBattensOwnState drops batten's own files from the fingerprint.
//
// Found by testing, not by reading: the first version hashed everything git reported, and the
// database lives at .batten/batten.db in a repo-local setup. SQLite's WAL and SHM files churn on
// every single write — so recording the verdict CHANGED the tree the verdict was about, and the
// gate declared its own freshly written pass stale a moment later.
//
// batten must not be able to invalidate a verdict by writing to its own ledger. This is the same
// rule the replay log follows when it refuses to journal its own failure to journal.
func withoutBattensOwnState(status string) string {
	var kept []string
	for _, line := range strings.Split(status, "\n") {
		if line == "" {
			continue
		}
		// Porcelain v1: two status columns, a space, then the path.
		p := line
		if len(p) > 3 {
			p = p[3:]
		}
		p = strings.Trim(p, `"`)
		p = strings.ReplaceAll(p, `\`, "/")
		base := p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[i+1:]
		}
		if strings.HasPrefix(p, ".batten/") || p == ".batten" || strings.HasPrefix(base, "batten.db") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
