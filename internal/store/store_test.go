package store

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNormPathCaseFold pins the platform contract of normPath: slashes are always
// normalized, and casing folds exactly where the default filesystem is case-insensitive
// (Windows, macOS) while staying exact on Linux. The expectation is computed per
// platform rather than skipped, so every CI leg asserts ITS OWN correct behavior.
func TestNormPathCaseFold(t *testing.T) {
	fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"

	expect := func(cleaned string) string {
		if fold {
			return strings.ToLower(cleaned)
		}
		return cleaned
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"mixed case stays or folds per GOOS", "ml/F.py", expect("ml/F.py")},
		{"already lower is untouched everywhere", "ml/f.py", "ml/f.py"},
		// Separator conversion is Windows-only, and that is the correct behaviour rather than an
		// oversight: filepath.ToSlash converts where a backslash IS the separator, and on Linux a
		// backslash is a legal character inside a filename. Rewriting it there would invent a path
		// the user never wrote — and would make two genuinely different files compare equal in the
		// write-set guard, which is the opposite of what the guard is for.
		{"backslashes are separators only on Windows", `Internal\Store\Store.go`, func() string {
			if runtime.GOOS == "windows" {
				return "internal/store/store.go"
			}
			return expect(`Internal\Store\Store.go`)
		}()},
	}
	for _, c := range cases {
		if got := normPath(c.in); got != c.want {
			t.Errorf("%s: normPath(%q) = %q, want %q (GOOS=%s)", c.name, c.in, got, c.want, runtime.GOOS)
		}
	}

	// The fold decision itself, stated once: folded and unfolded inputs must collide
	// exactly on the case-insensitive platforms and only there.
	same := normPath("ml/F.py") == normPath("ml/f.py")
	if same != fold {
		t.Errorf("case collision on %s: got %v, want %v", runtime.GOOS, same, fold)
	}

	// filepath.Clean is part of the contract: ./a/../b canonicalizes before comparison.
	if got, want := normPath("./ml/../ml/f.py"), "ml/f.py"; got != want {
		t.Errorf("normPath cleaning: got %q, want %q", got, want)
	}
}

// TestMigrationAddsVerdictSource opens the same database twice — migrations must
// upgrade in place and be idempotent — then round-trips one verdict per source and
// TestProbeWriteLockIsHonestAndLeavesNothingBehind.
//
// The probe exists because opening the database proves nothing about writing to it, and "the
// store opened fine" was being printed while every hook that needed to record something failed.
//
// Both of the obvious implementations were false: `BEGIN IMMEDIATE` on top of db.Begin() errors
// on every call including on an idle database, and a read-only transaction takes no write lock
// at all, so it reports green for a database batten cannot write to. Only an attempted WRITE
// measures the thing. This test pins the two properties that catch both mistakes: an idle store
// must probe clean, and the probe must not leave its row behind.
func TestProbeWriteLockIsHonestAndLeavesNothingBehind(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.ProbeWriteLock(); err != nil {
		t.Fatalf("an idle store must probe clean; a probe that always reports contention "+
			"reports nothing at all: %v", err)
	}
	// Twice: the first probe must leave the connection usable, not stuck in a transaction.
	if err := s.ProbeWriteLock(); err != nil {
		t.Fatalf("the second probe failed, so the first left the connection in a transaction: %v", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM events WHERE hook = 'batten.doctor.probe'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the probe left %d row(s) behind; diagnosing the log must not write to the log", n)
	}

	// And the store still works normally afterwards — busy_timeout restored, no lingering state.
	if err := s.LogEvent("", "", "after-probe", []byte("{}")); err != nil {
		t.Errorf("the store is unusable after a probe: %v", err)
	}
}

// asserts LatestVerdictBySource separates the agent's claim from batten's evidence.
func TestMigrationAddsVerdictSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batten.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s, err = Open(path) // second open replays migrate() against an already-migrated file
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s.Close()

	r, err := s.EnsureRun("p", "U-1", "")
	if err != nil {
		t.Fatalf("EnsureRun: %v", err)
	}

	if err := s.SaveVerdict(Verdict{
		RunID:    r.RunID,
		Gate:     "verify",
		CheckID:  "claim",
		Result:   "ok",
		Evidence: []string{"agent says so"},
		// Source left empty on purpose: SaveVerdict must default it to "agent".
	}, true); err != nil {
		t.Fatalf("SaveVerdict (empty source): %v", err)
	}
	if err := s.SaveVerdict(Verdict{
		RunID:    r.RunID,
		Gate:     "verify",
		CheckID:  "checked",
		Result:   "ok",
		Evidence: []string{"go test ./...: ok"},
		Source:   "batten",
	}, true); err != nil {
		t.Fatalf("SaveVerdict (batten): %v", err)
	}

	agent, err := s.LatestVerdictBySource(r.RunID, "", "agent")
	if err != nil {
		t.Fatalf("LatestVerdictBySource(agent): %v", err)
	}
	if agent.Source != "agent" || agent.CheckID != "claim" {
		t.Errorf("agent verdict: got source=%q check_id=%q, want source=%q check_id=%q",
			agent.Source, agent.CheckID, "agent", "claim")
	}
	if len(agent.Evidence) != 1 || agent.Evidence[0] != "agent says so" {
		t.Errorf("agent evidence round-trip: got %#v", agent.Evidence)
	}

	batten, err := s.LatestVerdictBySource(r.RunID, "", "batten")
	if err != nil {
		t.Fatalf("LatestVerdictBySource(batten): %v", err)
	}
	if batten.Source != "batten" || batten.CheckID != "checked" {
		t.Errorf("batten verdict: got source=%q check_id=%q, want source=%q check_id=%q",
			batten.Source, batten.CheckID, "batten", "checked")
	}
	if len(batten.Evidence) != 1 || batten.Evidence[0] != "go test ./...: ok" {
		t.Errorf("batten evidence round-trip: got %#v", batten.Evidence)
	}
}

// TestSaveVerdictRejectsOkWithoutEvidence is the golden rule under test: an "ok"
// with an empty evidence[] must be refused with ErrNoEvidence when evidence is required.
func TestSaveVerdictRejectsOkWithoutEvidence(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	r, err := s.EnsureRun("p", "U-1", "")
	if err != nil {
		t.Fatalf("EnsureRun: %v", err)
	}

	err = s.SaveVerdict(Verdict{
		RunID:   r.RunID,
		Gate:    "verify",
		CheckID: "empty-ok",
		Result:  "ok",
	}, true)
	if !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("SaveVerdict(ok, no evidence, required): got %v, want ErrNoEvidence", err)
	}
}

// TestWriteSetsByRunKeepsNilDistinct pins the three states the vault note renders from, and
// especially the nil: "nobody recorded a write-set" and "this agent owned nothing" are different
// facts, and returning an empty map for the first would let a caller print the second.
func TestWriteSetsByRunKeepsNilDistinct(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	r, err := s.EnsureRun("p", "U-1", "")
	if err != nil {
		t.Fatalf("EnsureRun: %v", err)
	}

	// State 1 — a run whose fan-out claimed nothing at all.
	ws, err := s.WriteSetsByRun(r.RunID)
	if err != nil {
		t.Fatalf("WriteSetsByRun (no claims): %v", err)
	}
	if ws != nil {
		t.Fatalf("a run with zero claims must return a nil map, got %#v", ws)
	}

	for _, id := range []string{"n-a", "n-b"} {
		if err := s.AddNode(Node{NodeID: id, RunID: r.RunID, Kind: "subagent", Label: id}); err != nil {
			t.Fatalf("AddNode(%s): %v", id, err)
		}
	}
	if err := s.ClaimWriteSet(r.RunID, "n-a", []string{"internal/ml/train.go", "internal/ml/eval.go"}); err != nil {
		t.Fatalf("ClaimWriteSet(n-a): %v", err)
	}

	// States 2 and 3 — claims exist, so a node without them owned nothing rather than went unrecorded.
	ws, err = s.WriteSetsByRun(r.RunID)
	if err != nil {
		t.Fatalf("WriteSetsByRun (with claims): %v", err)
	}
	if ws == nil {
		t.Fatal("a run with claims must return a non-nil map")
	}
	if got, want := len(ws["n-a"]), 2; got != want {
		t.Errorf("n-a write-set: got %d files %v, want %d", got, ws["n-a"], want)
	}
	if got := len(ws["n-b"]); got != 0 {
		t.Errorf("n-b claimed nothing, got %d files %v", got, ws["n-b"])
	}

	// Paths come back normalized and sorted — the note renders them verbatim.
	want := []string{normPath("internal/ml/eval.go"), normPath("internal/ml/train.go")}
	for i, w := range want {
		if ws["n-a"][i] != w {
			t.Errorf("n-a[%d]: got %q, want %q (sorted, normalized)", i, ws["n-a"][i], w)
		}
	}
}

// TestEveryMigrationIsAdditive holds the expand-only rule declared above `migrations`.
//
// The scenario it protects is not hypothetical: the binary the plugin installed and the binary on
// your PATH share one ~/.batten/batten.db, and `batten doctor` warns about version skew between
// them because skew is the normal case. A DROP or a RENAME would break the older one against a file
// the newer one already migrated — inside a hook, which fails open, so the gate would stop
// governing and say nothing.
func TestEveryMigrationIsAdditive(t *testing.T) {
	// The loop in migrate() indexes migrations by version. If these disagree, either the newest
	// migration never runs on an existing database, or migrate() panics on an index out of range —
	// in a hook process, where a panic and silence look the same from the outside.
	if len(migrations) != schemaVersion {
		t.Fatalf("schemaVersion is %d but there are %d migrations: migrate() indexes the slice by "+
			"version, so this either skips a migration or panics out of range",
			schemaVersion, len(migrations))
	}

	// Sub-strings, not a parser: this needs to be obvious to a reader adding migration 12.
	forbidden := []string{"DROP TABLE", "DROP COLUMN", "DROP INDEX", "RENAME TO", "RENAME COLUMN", "DELETE FROM"}
	for i, m := range migrations {
		up := strings.ToUpper(strings.Join(strings.Fields(m), " "))
		for _, f := range forbidden {
			if strings.Contains(up, f) {
				t.Errorf("migration v%d contains %s:\n  %s\n"+
					"    Migrations are expand-only. An older batten sharing this database would "+
					"break against a file a newer one had already changed, in a hook, in silence.\n"+
					"    Contract in a later release, once nothing reads it.", i+1, f, m)
			}
		}
		switch {
		case strings.HasPrefix(up, "ALTER TABLE ") && strings.Contains(up, " ADD COLUMN "):
			// Adding a NOT NULL column with no default fails on a table that already has rows —
			// on the user's database, not on a fresh one, so tests built from scratch never see it.
			if strings.Contains(up, "NOT NULL") && !strings.Contains(up, "DEFAULT") {
				t.Errorf("migration v%d adds a NOT NULL column with no DEFAULT:\n  %s\n"+
					"    It succeeds on an empty database and fails on one with rows.", i+1, m)
			}
		case strings.HasPrefix(up, "CREATE TABLE IF NOT EXISTS "),
			strings.HasPrefix(up, "CREATE INDEX IF NOT EXISTS "),
			strings.HasPrefix(up, "CREATE UNIQUE INDEX IF NOT EXISTS "):
			// Fine, and idempotent — migrate() replays the base schema on every open.
		default:
			t.Errorf("migration v%d is neither an ADD COLUMN nor a CREATE ... IF NOT EXISTS:\n  %s\n"+
				"    If it really is additive, widen this test deliberately and say why.", i+1, m)
		}
	}
}
