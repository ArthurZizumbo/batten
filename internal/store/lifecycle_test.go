package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func run(t *testing.T, s *Store, unit, session string) *Run {
	t.Helper()
	r, err := s.EnsureRun("p", unit, session)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestEnsureRunIsIdempotentAndClosingReleasesTheUnit: a second EnsureRun for the same unit must
// return the SAME run, not open a parallel one — every hook calls it, so a fresh run per call
// would scatter one unit's ledger across many.
func TestEnsureRunIsIdempotentAndClosingReleasesTheUnit(t *testing.T) {
	s := open(t)
	a := run(t, s, "US-1", "sess-a")
	b := run(t, s, "US-1", "sess-a")
	if a.RunID != b.RunID {
		t.Fatalf("EnsureRun opened a second run for the same unit: %s vs %s", a.RunID, b.RunID)
	}

	if err := s.CloseRun(a.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	// Once closed, the unit is free: a new EnsureRun starts a genuinely new run.
	c := run(t, s, "US-1", "sess-a")
	if c.RunID == a.RunID {
		t.Error("after CloseRun the next EnsureRun must open a NEW run, not resurrect the closed one")
	}

	// ActiveRun sees only the open one; LatestRun sees the newest regardless of status. `batten
	// show` depends on the second: a unit that just closed must not disappear from view.
	if got, err := s.ActiveRun("p", "US-1"); err != nil || got.RunID != c.RunID {
		t.Errorf("ActiveRun = %v (%v), want the open run %s", got, err, c.RunID)
	}
	if err := s.CloseRun(c.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveRun("p", "US-1"); err == nil {
		t.Error("with every run closed, ActiveRun must report none")
	}
	if got, err := s.LatestRun("p", "US-1"); err != nil || got.RunID != c.RunID {
		t.Errorf("LatestRun = %v (%v), want the closed run %s — a closed unit is still inspectable", got, err, c.RunID)
	}
}

// The write-set fence is a DATABASE constraint, not advice: two nodes cannot own one path.
func TestWriteSetRefusesASecondOwner(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	if err := s.ClaimWriteSet(r.RunID, "n-a", []string{"internal/api/handler.go"}); err != nil {
		t.Fatal(err)
	}
	// Re-claiming your own path is fine — a retry must not deadlock against itself.
	if err := s.ClaimWriteSet(r.RunID, "n-a", []string{"internal/api/handler.go"}); err != nil {
		t.Errorf("a node re-claiming its own path must succeed: %v", err)
	}

	err := s.ClaimWriteSet(r.RunID, "n-b", []string{"internal/api/handler.go"})
	if err == nil {
		t.Fatal("a second node claiming the same path must be refused")
	}
	if !strings.Contains(err.Error(), "n-a") {
		t.Errorf("the collision must name the owner; got %v", err)
	}

	owner, err := s.WriteSetOwner(r.RunID, "", "internal/api/handler.go")
	if err != nil || owner != "n-a" {
		t.Errorf("WriteSetOwner = %q (%v), want n-a", owner, err)
	}
	if owner, _ := s.WriteSetOwner(r.RunID, "", "nobody/owns/this.go"); owner != "" {
		t.Errorf("an unclaimed path has no owner, got %q", owner)
	}

	// A whole claim is one transaction: if any path in it collides, NONE of them are taken.
	// A partial claim would leave an agent believing it owns files it does not.
	if err := s.ClaimWriteSet(r.RunID, "n-c", []string{"fresh/one.go", "internal/api/handler.go"}); err == nil {
		t.Fatal("a claim containing a collision must fail as a whole")
	}
	if owner, _ := s.WriteSetOwner(r.RunID, "", "fresh/one.go"); owner != "" {
		t.Errorf("the failed claim leaked a partial write-set: fresh/one.go is owned by %q", owner)
	}
}

// TestCrossRunGuardOnlyDefendsOpenRuns is the multi-session rule, and the reason CloseRun
// matters: claims are released by closing, because the guard filters on status='running'.
// A run nobody closes keeps its files locked against every other session forever.
func TestCrossRunGuardOnlyDefendsOpenRuns(t *testing.T) {
	s := open(t)
	a := run(t, s, "US-1", "sess-a")
	b := run(t, s, "US-2", "sess-b")

	if err := s.ClaimWriteSet(a.RunID, "n-a", []string{"ml/train.py"}); err != nil {
		t.Fatal(err)
	}

	// Session B is told which unit holds the file, not merely that it is taken.
	owner, err := s.WriteSetOwnerAcrossOpenRuns("p", "ml/train.py", b.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if owner == nil {
		t.Fatal("an open run's claim must be defended against another session")
	}
	if owner.UnitID != "US-1" {
		t.Errorf("cross-run owner = %+v, want unit US-1", owner)
	}

	// Its own claims are not held against it.
	if own, _ := s.WriteSetOwnerAcrossOpenRuns("p", "ml/train.py", a.RunID); own != nil {
		t.Error("a run must not collide with itself across the open-run guard")
	}

	// Closing releases. This is the whole lifecycle argument in one assertion.
	if err := s.CloseRun(a.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	after, err := s.WriteSetOwnerAcrossOpenRuns("p", "ml/train.py", b.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if after != nil {
		t.Errorf("closing a run must release its claims; still held by %+v", after)
	}
}

// TestSessionBindingAndAmbiguity: each session is bound to its own unit, and a session bound to
// nothing must report nothing rather than guess at the only open run.
func TestSessionBindingAndAmbiguity(t *testing.T) {
	s := open(t)
	a := run(t, s, "US-1", "sess-a")
	run(t, s, "US-2", "sess-b")

	got, err := s.RunBySession("p", "sess-a")
	if err != nil || got.UnitID != "US-1" {
		t.Errorf("RunBySession(sess-a) = %v (%v), want US-1", got, err)
	}
	if _, err := s.RunBySession("p", "sess-unknown"); err == nil {
		t.Error("an unbound session must not be silently attached to somebody else's run")
	}

	// AdoptSession only binds a run that has NO session yet — it must never steal one that
	// another session already owns, or two Claude Code windows would fight over one unit.
	if err := s.AdoptSession(a.RunID, "sess-thief"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.RunBySession("p", "sess-a"); err != nil || got.RunID != a.RunID {
		t.Errorf("sess-a still owns its run after another session tried to adopt it; got %v (%v)", got, err)
	}
	if _, err := s.RunBySession("p", "sess-thief"); err == nil {
		t.Error("AdoptSession stole a run that already belonged to another session")
	}

	// On an unbound run it does bind.
	orphan, err := s.EnsureRun("p", "US-3", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AdoptSession(orphan.RunID, "sess-late"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.RunBySession("p", "sess-late"); err != nil || got.RunID != orphan.RunID {
		t.Errorf("an unbound run must accept adoption; got %v (%v)", got, err)
	}

	open, err := s.OpenRuns("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 3 {
		t.Errorf("got %d open runs, want 3 — ambiguity has to be visible to be reported", len(open))
	}
}

func TestPhaseAndAnchorRoundTrip(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	if err := s.SetPhase(r.RunID, "verify"); err != nil {
		t.Fatal(err)
	}
	// The anchor is what every later phase diffs from. Losing it silently would make `diff_from:
	// anchor` diff from nothing.
	if err := s.SetBaseSHA(r.RunID, "8f2a1c9"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Run(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "verify" {
		t.Errorf("phase = %q, want verify", got.Phase)
	}
	if got.BaseSHA != "8f2a1c9" {
		t.Errorf("base_sha = %q, want 8f2a1c9", got.BaseSHA)
	}
}

func TestOverrideIsScopedToItsGateAndRecorded(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	if ok, _ := s.HasOverride(r.RunID, "qa"); ok {
		t.Fatal("no override has been recorded yet")
	}
	if err := s.Override(r.RunID, "qa", "hotfix, incident 4412"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.HasOverride(r.RunID, "qa"); err != nil || !ok {
		t.Errorf("HasOverride(qa) = %v (%v), want true", ok, err)
	}
	// Overriding one gate must not open another: the escape hatch is per gate, by design.
	if ok, _ := s.HasOverride(r.RunID, "security"); ok {
		t.Error("an override on qa must not satisfy the security gate")
	}
}

func TestStaleRunsFindsWhatNobodyClosed(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	// Nothing is stale the moment it opens.
	stale, err := s.StaleRuns("p", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("a run opened just now is not stale; got %d", len(stale))
	}

	// A negative window puts the cutoff in the future, so every open run qualifies. This tests
	// the query, not the clock — StaleRuns(0) would sit exactly on the boundary, where
	// started_at < cutoff is false because they are equal.
	stale, err = s.StaleRuns("p", -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].RunID != r.RunID {
		t.Errorf("StaleRuns(-1h) = %v, want the one open run", stale)
	}

	// A closed run is never stale, however old: staleness is about runs nobody finished, and it
	// matters because an unclosed run holds its write-set claims against every other session.
	if err := s.CloseRun(r.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	stale, _ = s.StaleRuns("p", -time.Hour)
	if len(stale) != 0 {
		t.Errorf("a closed run must not be reported stale; got %v", stale)
	}
}

// TestRecentActivityClearsTheStaleWarning is finding #19. The predicate reads "open >48h with
// no activity", but events.run_id was NULL on every row the hook wrote, so the activity half
// was dead code: a run being worked RIGHT NOW was reported stale purely by its age, and the
// advice ("close or resume them") could never be satisfied by resuming. An attributed event
// must clear it.
func TestRecentActivityClearsTheStaleWarning(t *testing.T) {
	s := open(t)
	worked := run(t, s, "US-1", "sess-a")
	idle := run(t, s, "US-2", "sess-b")

	// Both runs opened three days ago.
	old := time.Now().Add(-72 * time.Hour).Unix()
	for _, r := range []*Run{worked, idle} {
		if _, err := s.db.Exec(`UPDATE runs SET started_at=? WHERE run_id=?`, old, r.RunID); err != nil {
			t.Fatal(err)
		}
	}
	// One of them saw a hook event five minutes ago — the shape record() writes when the
	// session is bound to the run.
	if err := s.LogEvent(worked.RunID, "", "PreToolUse", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	stale, err := s.StaleRuns("p", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].RunID != idle.RunID {
		ids := make([]string, len(stale))
		for i, r := range stale {
			ids[i] = r.UnitID
		}
		t.Errorf("stale = %v, want exactly US-2 — a run with a recent attributed event is being "+
			"worked, not rotting", ids)
	}
}

func TestListRunsAndEventLog(t *testing.T) {
	s := open(t)
	a := run(t, s, "US-1", "sess-a")
	run(t, s, "US-2", "sess-b")

	runs, err := s.ListRuns("p", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("ListRuns returned %d, want 2", len(runs))
	}

	// The event log takes arbitrary payloads and must not fail on a large or odd one — it is
	// written from a hook, where an error would surface as a broken session.
	big := make([]byte, 64*1024)
	for i := range big {
		big[i] = 'x'
	}
	if err := s.LogEvent(a.RunID, "", "PreToolUse", big); err != nil {
		t.Errorf("LogEvent must survive a large payload (it is capped, not rejected): %v", err)
	}
	if err := s.LogEvent(a.RunID, "", "PreToolUse", nil); err != nil {
		t.Errorf("LogEvent with no payload: %v", err)
	}
}

// TestTwoNamesForOneFileAreOneOwner.
//
// The write-set guard is about a FILE; a path is only a name for one. Measured on the machine
// this was written on: a hard link produced two paths that normPath called different and
// os.SameFile called identical. So agent A claims `api/rate.go`, agent B writes
// `api/rate-link.go`, and a path-only guard waves it through while they are one file on disk.
// Directory symlinks do the same thing for free on macOS and Linux (/var vs /private/var).
func TestTwoNamesForOneFileAreOneOwner(t *testing.T) {
	root := t.TempDir()
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	real := filepath.Join(root, "rate.go")
	if err := os.WriteFile(real, []byte("package api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "rate-alias.go")
	if err := os.Link(real, alias); err != nil {
		t.Skipf("this filesystem does not support hard links: %v", err)
	}

	r, err := s.EnsureRun("p", "TASK-1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimWriteSet(r.RunID, "agent-a", []string{"rate.go"}); err != nil {
		t.Fatal(err)
	}

	// Control: the claimed name resolves to its owner. If this fails the assertion below
	// proves nothing.
	if owner, _ := s.WriteSetOwner(r.RunID, root, "rate.go"); owner != "agent-a" {
		t.Fatalf("control failed: the claimed path resolves to %q, want agent-a", owner)
	}

	owner, err := s.WriteSetOwner(r.RunID, root, "rate-alias.go")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "agent-a" {
		t.Errorf("a second name for the SAME file on disk resolved to owner %q. Agent B can "+
			"write agent A's file by spelling it differently, and the disjointness the fan-out "+
			"rests on is gone", owner)
	}
}

// The other half, and the reason the path stays the primary key: the write-set fences files an
// agent is ABOUT to write, and most of them do not exist yet. A file that does not exist has no
// inode to key on, so an identity-only scheme cannot fence the case the guard exists for.
func TestAClaimOnAFileThatDoesNotExistYetStillFences(t *testing.T) {
	root := t.TempDir()
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	r, _ := s.EnsureRun("p", "TASK-1", "s1")
	if err := s.ClaimWriteSet(r.RunID, "agent-a", []string{"not/written/yet.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "not", "written", "yet.go")); err == nil {
		t.Fatal("this test is meaningless if the file exists")
	}

	owner, err := s.WriteSetOwner(r.RunID, root, "not/written/yet.go")
	if err != nil {
		t.Fatal(err)
	}
	if owner != "agent-a" {
		t.Errorf("owner = %q: a claim on a file that does not exist yet must still fence it — "+
			"that is the whole point of declaring a write-set before the fan-out writes", owner)
	}
}

// And an unrelated file that merely exists must not be captured by the identity sweep.
func TestAnUnrelatedFileIsNotCapturedByTheIdentitySweep(t *testing.T) {
	root := t.TempDir()
	s, err := Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, n := range []string{"owned.go", "free.go"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := s.EnsureRun("p", "TASK-1", "s1")
	if err := s.ClaimWriteSet(r.RunID, "agent-a", []string{"owned.go"}); err != nil {
		t.Fatal(err)
	}

	if owner, _ := s.WriteSetOwner(r.RunID, root, "free.go"); owner != "" {
		t.Errorf("an unclaimed file was reported as owned by %q; the guard would deny a "+
			"legitimate write and the fan-out would stall", owner)
	}
}
