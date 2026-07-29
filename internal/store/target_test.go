package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestBattenCannotInvalidateAVerdictByWritingItsOwnLedger.
//
// Found by testing, twice, and neither time by reading the code.
//
// The first version of TargetDigest hashed everything git reported. The database lives at
// .batten/batten.db in a repo-local setup, and SQLite's WAL and SHM files churn on every write —
// so RECORDING the verdict changed the tree the verdict was about, and the gate declared its own
// freshly written pass stale a moment later.
//
// The second version filtered untracked files and still broke, because some repos COMMIT the
// database to share a run history. Then batten's own writes are tracked modifications and show
// up in `git diff HEAD` instead.
//
// Both paths have to be closed, or the gate is permanently stale in a perfectly ordinary setup.
func TestBattenCannotInvalidateAVerdictByWritingItsOwnLedger(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		name := "db untracked"
		if tracked {
			name = "db committed to the repo"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) {
				c := exec.Command("git", args...)
				c.Dir = dir
				if out, err := c.CombinedOutput(); err != nil {
					t.Skipf("git unavailable (%v): %s", err, out)
				}
			}
			git("init", "-q", ".")
			git("config", "user.email", "t@t.io")
			git("config", "user.name", "t")

			if err := os.MkdirAll(filepath.Join(dir, ".batten"), 0o755); err != nil {
				t.Fatal(err)
			}
			s, err := Open(filepath.Join(dir, ".batten", "batten.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tracked {
				git("add", "-A") // sweeps the database in too, which is the case under test
			} else {
				git("add", "app.go")
			}
			git("commit", "-q", "-m", "base")
			if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app // wip\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			before := TargetDigest(dir)
			if before == "" {
				t.Skip("no digest available here")
			}

			// Exactly what `batten check` does: write a verdict. The tree the developer cares
			// about is untouched.
			r, err := s.EnsureRun("p", "TASK-1", "s1")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				if err := s.SaveVerdict(Verdict{
					RunID: r.RunID, Gate: "qa", CheckID: "c", Result: "ok",
					Evidence: []string{"go test: PASS"}, Source: "batten",
				}, true); err != nil {
					t.Fatal(err)
				}
			}

			if after := TargetDigest(dir); after != before {
				t.Errorf("batten writing to its own ledger moved the fingerprint of the user's "+
					"tree (%s -> %s). The gate would call every verdict stale the instant it "+
					"was recorded", before, after)
			}

			// And a real edit must still move it, or the whole check is decorative.
			if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app // edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if after := TargetDigest(dir); after == before {
				t.Error("a real edit to a tracked file did not move the fingerprint")
			}
		})
	}
}

// A directory that is not a git repo has nothing to fingerprint, and must say so with an empty
// string rather than a constant that would read as "I checked and nothing changed".
func TestAnUnmeasurableTreeReturnsEmptyRatherThanAConstant(t *testing.T) {
	if got := TargetDigest(t.TempDir()); got != "" {
		t.Errorf("TargetDigest on a non-repo = %q, want empty", got)
	}
	if got := TargetDigest(""); got != "" {
		t.Errorf("TargetDigest(\"\") = %q, want empty", got)
	}
}
