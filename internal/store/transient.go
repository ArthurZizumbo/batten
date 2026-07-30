package store

// Transient failures, told apart from real ones.
//
// batten keeps a local SQLite database, and on Windows that file lives in a world where other
// processes touch it without being asked. Windows Defender and corporate antivirus intercept and
// inspect a database file — and, worse, the transaction journal — in the microsecond after it is
// created or renamed, and the open fails with ERROR_SHARING_VIOLATION (32) or ERROR_ACCESS_DENIED
// (5). SQLite surfaces those as SQLITE_BUSY or SQLITE_IOERR.
//
// Treating that as a broken database is wrong twice over. It is wrong about the world — nothing
// is damaged and the next attempt a few milliseconds later succeeds — and it is expensive here,
// because batten announces loudly when it cannot run. A scan that lasted 30ms would put
// "batten did NOT run for this tool call" in front of a user whose machine is perfectly healthy,
// and a warning that cries wolf is a warning people learn to skip past.
//
// Idea from gentle-ai's lock-contention reclassification (v2.2.0-rc.1): classify contention as
// transient and retry it, and only report what survives.
//
// WHAT IS DELIBERATELY NOT RETRIED: anything that is not on this list. A corrupt database, a
// missing directory, a read-only volume, a schema error — those are real, and retrying them just
// delays the truth by a few hundred milliseconds. The point is to be precise, not patient.

import (
	"strings"
	"time"
)

// transientPatterns are the substrings that mean "try again", matched case-insensitively.
// Written as text because the driver is pure Go and does not export the numeric codes.
var transientPatterns = []string{
	"database is locked", // SQLITE_BUSY
	"database table is locked",
	"sqlite_busy",
	"sqlite_ioerr",
	"disk i/o error",    // what an interrupted read surfaces as
	"sharing violation", // ERROR_SHARING_VIOLATION (32): an antivirus has the file open
	"access is denied",  // ERROR_ACCESS_DENIED (5), same cause on a different code path
	"used by another process",
	"temporarily unavailable",
}

// IsTransient reports whether an error is worth retrying rather than reporting.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range transientPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// retryTransient runs fn until it succeeds, fails for a reason worth reporting, or runs out of
// attempts. The backoff is exponential and short: the failures it exists for last milliseconds,
// and every one of these sits on the fast path of a hook that must not make the session wait.
//
// Total worst case with the defaults is under a second — long enough to outlast a virus scanner
// opening a file, short enough that a genuinely locked database still reports promptly.
func retryTransient(attempts int, fn func() error) error {
	delay := 20 * time.Millisecond
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil || !IsTransient(err) {
			return err
		}
		if i == attempts-1 {
			break
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}
