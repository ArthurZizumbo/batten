package store

import (
	"errors"
	"testing"
	"time"
)

// TestTransientIsToldApartFromBroken.
//
// On Windows a virus scanner opens the database file in the microsecond after it is created, and
// SQLite reports SQLITE_BUSY or a sharing violation. Nothing is damaged; the next attempt a few
// milliseconds later works. Treating it as a broken database would put "batten did NOT run for
// this tool call" in front of a user whose machine is perfectly healthy — and §4.3 made that
// warning loud on purpose, so crying wolf with it is expensive.
//
// Being precise matters more than being patient: retrying a corrupt file, a read-only volume or
// a schema error just delays the truth.
func TestTransientIsToldApartFromBroken(t *testing.T) {
	transient := []string{
		"database is locked",
		"database table is locked (SQLITE_BUSY)",
		"disk I/O error",
		"The process cannot access the file because it is being used by another process.",
		"CreateFile: sharing violation",
		"open batten.db: Access is denied.",
	}
	for _, msg := range transient {
		if !IsTransient(errors.New(msg)) {
			t.Errorf("%q should be retried, not reported", msg)
		}
	}

	permanent := []string{
		"file is not a database",
		"no such table: runs",
		"attempt to write a readonly database",
		"no such file or directory",
		"UNIQUE constraint failed: writesets.path",
	}
	for _, msg := range permanent {
		if IsTransient(errors.New(msg)) {
			t.Errorf("%q is a real failure; retrying it only delays the truth", msg)
		}
	}
	if IsTransient(nil) {
		t.Error("nil is not a failure at all")
	}
}

func TestRetryGivesUpOnRealFailuresImmediately(t *testing.T) {
	calls := 0
	start := time.Now()
	err := retryTransient(5, func() error {
		calls++
		return errors.New("file is not a database")
	})
	if err == nil {
		t.Fatal("a real failure must still be returned")
	}
	if calls != 1 {
		t.Errorf("a permanent error was retried %d times; it should be reported at once", calls)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Error("a permanent failure should not have waited through any backoff")
	}
}

func TestRetrySucceedsOnceTheContentionClears(t *testing.T) {
	calls := 0
	err := retryTransient(5, func() error {
		calls++
		if calls < 3 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("contention that cleared should have succeeded: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// And it must give up eventually, or a genuinely locked database hangs the hook instead of
// reporting — which is the failure §4.3 exists to make visible.
func TestRetryStopsRatherThanHanging(t *testing.T) {
	calls := 0
	start := time.Now()
	err := retryTransient(4, func() error {
		calls++
		return errors.New("database is locked")
	})
	if err == nil {
		t.Fatal("contention that never clears must be reported")
	}
	if calls != 4 {
		t.Errorf("calls = %d, want 4", calls)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the backoff took %v; this sits on the fast path of a hook", d)
	}
}
