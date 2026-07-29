// Package store is batten's source of truth: the run graph, verdicts, and budget ledger.
//
// SQLite is canonical. The .canvas file, the handoff .md and any OTLP export are
// lossy projections of what lives here.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver; registers as "sqlite" (not "sqlite3")
)

type Store struct {
	db *sql.DB
	// path is where this database lives, kept so callers can EXCLUDE it. batten must never be
	// able to report its own bookkeeping as the user's work — the same rule TargetDigest follows,
	// which learned it the hard way when recording a verdict changed the tree the verdict was
	// about. TargetDigest can only guess at the name (`.batten/`, `batten.db*`); anything holding
	// a Store can just ask.
	path string
}

// Path is where this store's database file is. Its sidecars (-wal, -shm, -journal) sit beside it.
func (s *Store) Path() string { return s.path }

// Open prepares the database for the concurrency situation batten actually lives in:
// several short-lived hook processes, one MCP subprocess, and the CLI, all writing
// the same file.
//
// Two mechanisms, both required:
//   - SetMaxOpenConns(1) serializes writes WITHIN a process. Without it, database/sql's
//     pool opens several connections to the same file and you deadlock against yourself.
//   - WAL + busy_timeout absorbs contention BETWEEN processes.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	for _, p := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("batten: pragma %q: %w", p, err)
		}
	}
	s := &Store{db: db, path: path}
	// Retried, because the failure this most often hits is an antivirus holding the file open
	// for the few milliseconds after it is created — see transient.go. Reporting that as a
	// broken store would put "batten did NOT run" in front of a user whose machine is fine.
	if err := retryTransient(5, s.migrate); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ProbeWriteLock reports whether this process can take the database's write lock right now.
//
// It exists for `batten doctor`, and it diagnoses the failure that is otherwise invisible: when
// another process holds the write lock past busy_timeout, every hook that needs to record
// something fails, batten degrades, and the only symptom the user gets is that the gate stopped
// having opinions. Opening the database succeeds in that state — it is acquiring the WRITE lock
// that does not — so "the store opened fine" is not evidence of a healthy store.
//
// The probe has to WRITE, and the write has to be rolled back.
//
// Two wrong ways to do this, both tried, both silently useless:
//
//   - `db.Exec("BEGIN IMMEDIATE")` on top of db.Begin() fails with "cannot start a transaction
//     within a transaction" on every call, including on a completely idle database. A probe that
//     always reports contention is a probe that reports nothing.
//   - A transaction that only reads. Go's Begin starts a DEFERRED transaction, which takes no
//     write lock until something actually writes — so it succeeds happily while another process
//     holds the lock, and the probe returns a green tick for a database batten cannot write to.
//     That is the exact false-green this whole check exists to catch, committed by the check.
//
// So: open a transaction, insert one row, and roll it back. The row never lands; the attempt is
// the measurement. The short busy_timeout is the point — doctor must report contention, not sit
// through it.
func (s *Store) ProbeWriteLock() error {
	if _, err := s.db.Exec("PRAGMA busy_timeout = 250"); err != nil {
		return err
	}
	// Restore the operational timeout no matter how this returns; a doctor run must not leave
	// the connection twitchier than it found it.
	defer func() { _, _ = s.db.Exec("PRAGMA busy_timeout = 5000") }()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`INSERT INTO events (run_id, node_id, hook, ts, payload) VALUES (?,?,?,?,?)`,
		nil, nil, "batten.doctor.probe", now(), "{}")
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
  run_id       TEXT PRIMARY KEY,
  project      TEXT NOT NULL,
  unit_id      TEXT NOT NULL,          -- US-034
  session_id   TEXT,
  phase        TEXT,                   -- current phase id
  status       TEXT NOT NULL,          -- running|ok|blocked|failed|rolled_back
  base_sha     TEXT,                   -- the anchor: diffs are computed from here
  tokens_spent      INTEGER NOT NULL DEFAULT 0,   -- exact; the only thing we can count for sure
  imputed_usd_spent REAL NOT NULL DEFAULT 0,      -- what it WOULD have cost on the API
  quota_start_5h    REAL,                          -- five_hour_pct when the run opened
  iterations   INTEGER NOT NULL DEFAULT 0,
  started_at   INTEGER NOT NULL,
  ended_at     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_runs_unit ON runs(project, unit_id);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);

CREATE TABLE IF NOT EXISTS nodes (
  node_id    TEXT PRIMARY KEY,
  run_id     TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,            -- phase|subagent|gate|tool
  label      TEXT NOT NULL,
  domain     TEXT,                     -- the fan-out axis this node owns
  status     TEXT NOT NULL,            -- running|ok|blocked|failed
  agent_id   TEXT,                     -- from SubagentStart/Stop
  agent_type TEXT,
  cost_usd   REAL NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL,
  ended_at   INTEGER,
  attempt    INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_nodes_run ON nodes(run_id);
CREATE INDEX IF NOT EXISTS idx_nodes_agent ON nodes(agent_id);

-- Typed, multi-relation edges. This is what a flat OTel trace cannot express
-- (one parent edge + untyped links) and why SQLite is canonical, not a cache.
CREATE TABLE IF NOT EXISTS edges (
  run_id   TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  src_node TEXT NOT NULL,
  dst_node TEXT NOT NULL,
  rel      TEXT NOT NULL,              -- spawn|depends_on|retry_of|supersedes|rollback
  PRIMARY KEY (src_node, dst_node, rel)
);

-- A subagent's declared write-set. The write-set guard reads this to deny
-- agent B writing a file owned by agent A.
CREATE TABLE IF NOT EXISTS writesets (
  run_id   TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  node_id  TEXT NOT NULL,
  path     TEXT NOT NULL,              -- repo-relative, slash-normalized
  PRIMARY KEY (run_id, path)           -- one owner per file, enforced by the DB itself
);
CREATE INDEX IF NOT EXISTS idx_ws_node ON writesets(node_id);

CREATE TABLE IF NOT EXISTS verdicts (
  verdict_id  INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id      TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  node_id     TEXT,
  gate        TEXT NOT NULL,
  check_id    TEXT NOT NULL,
  result      TEXT NOT NULL,           -- ok|warn|blocked
  evidence_json TEXT NOT NULL DEFAULT '[]',
  why         TEXT,
  safe_next_step TEXT,
  requires_confirmation INTEGER NOT NULL DEFAULT 0,
  ts          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_verdicts_run ON verdicts(run_id, result);

-- Append-only. events + budget_ledger together ARE the replay log. No Temporal.
CREATE TABLE IF NOT EXISTS events (
  event_id  INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id    TEXT,
  node_id   TEXT,
  hook      TEXT NOT NULL,
  ts        INTEGER NOT NULL,
  payload   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_run ON events(run_id, event_id);

CREATE TABLE IF NOT EXISTS budget_ledger (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id    TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  node_id   TEXT,
  ts        INTEGER NOT NULL,
  delta_usd REAL NOT NULL,
  tokens_in  INTEGER NOT NULL DEFAULT 0,
  tokens_out INTEGER NOT NULL DEFAULT 0,
  reason    TEXT
);

-- One row per (request, run). request_id is the dedup key: transcripts get replayed on
-- resume, and a retried request appears twice. INSERT OR IGNORE + this PK makes ingestion
-- idempotent, so re-parsing a transcript can never double-count.
CREATE TABLE IF NOT EXISTS usage (
  request_id  TEXT NOT NULL,
  run_id      TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
  node_id     TEXT,
  agent_id    TEXT,
  model       TEXT NOT NULL,
  speed       TEXT NOT NULL DEFAULT 'standard',
  ts          INTEGER NOT NULL,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  cache_write_5m      INTEGER NOT NULL DEFAULT 0,
  cache_write_1h      INTEGER NOT NULL DEFAULT 0,
  cache_read          INTEGER NOT NULL DEFAULT 0,
  web_searches        INTEGER NOT NULL DEFAULT 0,
  imputed_usd REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (request_id, run_id)
);
CREATE INDEX IF NOT EXISTS idx_usage_run ON usage(run_id);
CREATE INDEX IF NOT EXISTS idx_usage_node ON usage(node_id);

-- Subscription quota, sampled by "batten statusline" (the ONLY local surface that exposes
-- it -- hooks never see it). Account-global, so a run's share is a DELTA between the first
-- and last sample taken while it was open.
CREATE TABLE IF NOT EXISTS quota_snapshots (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id   TEXT NOT NULL,
  ts           INTEGER NOT NULL,
  five_hour_pct   REAL,
  five_hour_reset INTEGER,
  seven_day_pct   REAL,
  seven_day_reset INTEGER
);
CREATE INDEX IF NOT EXISTS idx_quota_session ON quota_snapshots(session_id, ts);

-- Audited escapes. A gate with no escape hatch gets the plugin uninstalled;
-- an unlogged escape defeats the gate. So: escape, but on the record.
CREATE TABLE IF NOT EXISTS overrides (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id  TEXT NOT NULL,
  gate    TEXT NOT NULL,
  reason  TEXT NOT NULL,
  ts      INTEGER NOT NULL
);
`

// schemaVersion is bumped whenever an additive migration is added below. The base schema
// (CREATE TABLE IF NOT EXISTS ...) always runs; versioned steps run once, in order, so a
// database created by an older batten upgrades in place instead of needing a rebuild.
const schemaVersion = 11

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	var have int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&have); err != nil {
		return err
	}
	migrations := []string{
		// v1: tag each run with whether the headroom compression proxy was live, so
		// `batten measure` can compare with/without instead of trusting a vendor README.
		`ALTER TABLE runs ADD COLUMN headroom INTEGER`, // NULL = unknown, 0 = off, 1 = on
		// v2: record who produced a verdict. 'agent' is a claim; 'batten' is evidence batten
		// generated by running the gate's own checks — the difference between "I verified it"
		// and "it was verified".
		`ALTER TABLE verdicts ADD COLUMN source TEXT NOT NULL DEFAULT 'agent'`,
		// v3: tag each run with whether a FRESH code graph (graphify) existed at run open,
		// so `batten measure` can compare with/without — the same admitted-but-measured
		// treatment headroom gets. NULL = unknown, 0 = absent/stale, 1 = fresh.
		`ALTER TABLE runs ADD COLUMN code_graph INTEGER`,
		// v4-v6: what batten DECIDED, not just what it was asked.
		//
		// The events table has called itself a replay log since the beginning and could not
		// replay anything: it recorded the INCOMING payload, once, BEFORE dispatch. So the one
		// fact worth keeping — whether batten allowed, denied or warned, and why — was never
		// written down. batten could not answer "how many commits did you deny this week"
		// because it had never noticed denying one.
		//
		// Three columns rather than one: the decision, the human reason, and the rule that
		// produced it. The rule is what lets a report say "2 without a verdict, 1 with red
		// checks" instead of deriving categories by pattern-matching English later.
		`ALTER TABLE events ADD COLUMN decision TEXT`, // allow | deny | advise
		`ALTER TABLE events ADD COLUMN reason TEXT`,
		`ALTER TABLE events ADD COLUMN rule TEXT`, // verdict_gate | budget | write_set | degraded
		// v7: the state of the working tree a verdict was made ABOUT.
		//
		// `batten check` proved the declared checks passed, and recorded no trace of WHAT they
		// passed against. A formatter running between the check and the commit leaves the
		// verdict saying batten-verified about a tree that no longer exists. Empty means the
		// digest was not measurable (not a git repo) — never "nothing changed".
		`ALTER TABLE verdicts ADD COLUMN target_digest TEXT`,
		// v8: the enforcement mode in force when the decision was taken.
		//
		// `enforcement: report` is batten's honest off-switch: gates warn instead of blocking,
		// and it is what `init` writes by default. What was missing is the other half of a kill
		// switch worth having — knowing what got through while it was off. Without this column
		// the events all look alike, and "we ran in report mode for three weeks" has no record
		// of what that cost.
		`ALTER TABLE events ADD COLUMN enforcement TEXT`,
		// v9: which WORKING TREE this run lives in.
		//
		// Everything batten knew about a file was its repo-relative path, which is the same
		// string in every worktree cut from the same repository. So two units on two branches,
		// each in its own tree, each legitimately editing `api/handler.go`, looked to the
		// cross-run guard exactly like two sessions fighting over one file — and it denied both.
		// batten told users to use a worktree per unit in three separate messages and then
		// punished them for it.
		//
		// Empty means "not recorded" and is treated as "might be the same tree", which keeps the
		// old behaviour for every run written before this column existed. Never as "a different
		// tree": that would turn an unknown into a licence.
		`ALTER TABLE runs ADD COLUMN worktree TEXT`,
		// v10: whether this run is happening with nobody watching.
		//
		// /batten-night's four absolute rules — never delete, never override, do not commit, honour
		// the iteration ceiling — were 112 lines of markdown asking the model to behave, in the most
		// dangerous command batten ships. That is the exact category of rule the README's first line
		// says a hook can impose and a document can only request, and it was the one place batten
		// had not taken its own medicine.
		//
		// One flag on the run turns all four into denials, with no new orchestration: batten still
		// does not run the loop, it just stops being the only participant that cannot say no.
		`ALTER TABLE runs ADD COLUMN mode TEXT`,
		// v11: acceptance criteria as data (plan §7, ítem 21).
		//
		// "Criteria" appeared ten times in the codebase's prose and zero times as data:
		// evidence was a flat []string and nothing could say WHICH criterion a piece of
		// evidence covered. This table is seeded from the unit's block in unit.plan when a
		// phase opens, and a verdict's evidence marks rows covered by citing `AC-<idx>:`.
		// The format of the verdict envelope does not change — the citation is a string
		// prefix, because #27 showed what handing objects to string fields does.
		`CREATE TABLE IF NOT EXISTS criteria (
		  run_id   TEXT NOT NULL,
		  unit_id  TEXT NOT NULL,
		  idx      INTEGER NOT NULL,
		  text     TEXT NOT NULL,
		  status   TEXT NOT NULL DEFAULT 'open',
		  evidence TEXT,
		  PRIMARY KEY (run_id, idx)
		)`,
	}
	for i := have; i < schemaVersion; i++ {
		if _, err := s.db.Exec(migrations[i]); err != nil {
			// A duplicate-column error means an older path already added it; tolerate and move on.
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("migration v%d: %w", i+1, err)
			}
		}
	}
	_, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion))
	return err
}

func now() int64 { return time.Now().Unix() }

// ---------- runs ----------

type Run struct {
	RunID        string
	Project      string
	UnitID       string
	SessionID    string
	Phase        string
	Status       string
	BaseSHA      string
	TokensSpent  int64
	ImputedUSD   float64
	QuotaStart5h *float64 // five_hour_pct when the run opened; nil if statusline is not installed
	Iterations   int
	StartedAt    int64
	EndedAt      *int64
	// Worktree is the root of the working tree this run is bound to. Empty means unrecorded,
	// which is deliberately NOT the same as "the main tree" — see the v9 migration.
	Worktree string
	// Mode is "unattended" while /batten-night owns this run, empty otherwise.
	Mode string
	// UnpricedTokens is the share of TokensSpent on models with no published rate. Those
	// tokens contributed $0 to ImputedUSD not because they were free but because no price
	// exists — so whenever this is > 0, ImputedUSD is a FLOOR, and every surface that prints
	// it must say so. It rides on the Run because #33 happened surface by surface: each one
	// rendered the dollar figure on its own and none of them could see the gap.
	UnpricedTokens int64
}

// ModeUnattended is the one mode there is. A run carrying it is being driven with nobody awake,
// and four things batten would otherwise allow become denials.
const ModeUnattended = "unattended"

// Unattended reports whether this run is running with nobody watching.
func (r *Run) Unattended() bool { return r != nil && r.Mode == ModeUnattended }

const runCols = `run_id, project, unit_id, COALESCE(session_id,''), COALESCE(phase,''),
	status, COALESCE(base_sha,''), tokens_spent, imputed_usd_spent, quota_start_5h,
	iterations, started_at, ended_at, COALESCE(worktree,''), COALESCE(mode,''),
	(SELECT COALESCE(SUM(u.input_tokens+u.output_tokens+u.cache_write_5m+u.cache_write_1h+u.cache_read),0)
	 FROM usage u WHERE u.run_id = runs.run_id AND u.imputed_usd=0
	 AND (u.input_tokens+u.output_tokens+u.cache_write_5m+u.cache_write_1h+u.cache_read)>0)`

// EnsureRun returns the open run for a unit, creating it if absent. Idempotent:
// hooks fire in any order and may race, so this must never duplicate a run.
func (s *Store) EnsureRun(project, unitID, sessionID string) (*Run, error) {
	r, err := s.ActiveRun(project, unitID)
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	id := newRunID(unitID)
	// Anchor the quota baseline at birth: a run's share of the 5h window is the delta
	// from here, since the window itself is account-global and shared with everything else.
	var q any
	if sessionID != "" {
		if snap, err := s.LatestQuota(sessionID); err == nil && snap.FiveHourPct != nil {
			q = *snap.FiveHourPct
		}
	}
	_, err = s.db.Exec(`INSERT INTO runs (run_id, project, unit_id, session_id, status, quota_start_5h, started_at)
	                    VALUES (?,?,?,?,'running',?,?)`, id, project, unitID, sessionID, q, now())
	if err != nil {
		return nil, err
	}
	return s.ActiveRun(project, unitID)
}

// newRunID builds a run id that stays unique even when the clock does not move.
//
// This was `unitID + time.Now().UnixNano()`, which is unique only if two calls never land in the
// same tick. On Windows the system clock's granularity is coarse — often half a millisecond or
// worse — so closing a run and opening the next one for the same unit produced the SAME
// nanosecond and collided on the primary key. EnsureRun then returned an error, and EnsureRun is
// on the hook path, where an error is a broken session. Windows is this tool's primary target,
// which makes a clock-resolution assumption exactly the wrong thing to depend on.
//
// The timestamp stays because it makes ids sortable and legible when reading the database by
// hand; the random suffix is what actually guarantees uniqueness.
func newRunID(unitID string) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is close to impossible; degrade to the timestamp alone rather
		// than refuse to open a run, and let the primary key catch the remaining case.
		return fmt.Sprintf("%s-%d", unitID, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%d-%s", unitID, time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

func (s *Store) ActiveRun(project, unitID string) (*Run, error) {
	row := s.db.QueryRow(`SELECT `+runCols+`
	   FROM runs WHERE project=? AND unit_id=? AND status='running' ORDER BY started_at DESC, rowid DESC LIMIT 1`,
		project, unitID)
	return scanRun(row.Scan)
}

func (s *Store) Run(runID string) (*Run, error) {
	row := s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE run_id=?`, runID)
	return scanRun(row.Scan)
}

// LatestRun is ActiveRun without the status filter: the unit's most recent run, open or not.
// Inspection commands want this — a run you just closed is the one you most want to look at.
func (s *Store) LatestRun(project, unitID string) (*Run, error) {
	row := s.db.QueryRow(`SELECT `+runCols+`
	   FROM runs WHERE project=? AND unit_id=? ORDER BY started_at DESC, rowid DESC LIMIT 1`,
		project, unitID)
	return scanRun(row.Scan)
}

func scanRun(scan func(...any) error) (*Run, error) {
	var r Run
	err := scan(&r.RunID, &r.Project, &r.UnitID, &r.SessionID, &r.Phase, &r.Status,
		&r.BaseSHA, &r.TokensSpent, &r.ImputedUSD, &r.QuotaStart5h,
		&r.Iterations, &r.StartedAt, &r.EndedAt, &r.Worktree, &r.Mode, &r.UnpricedTokens)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListRuns(project string, limit int) ([]Run, error) {
	rows, err := s.db.Query(`SELECT `+runCols+`
	   FROM runs WHERE (?='' OR project=?) ORDER BY started_at DESC, rowid DESC LIMIT ?`, project, project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) SetPhase(runID, phase string) error {
	_, err := s.db.Exec(`UPDATE runs SET phase=? WHERE run_id=?`, phase, runID)
	return err
}

// SetMode turns unattended mode on or off for a run. Passing "" turns it off, which is what a
// human closing the run in the morning does.
func (s *Store) SetMode(runID, mode string) error {
	_, err := s.db.Exec(`UPDATE runs SET mode=? WHERE run_id=?`, mode, runID)
	return err
}

// Iterate increments a run's iteration counter and returns the new value.
//
// `budget.max_iterations` was declared in the spec, returned over MCP, and DRAWN in the TUI as
// `iters %d/%d` — and runs.iterations was 0 forever, because nothing anywhere incremented it. The
// only brake an unsupervised loop had against spending the whole window was a sentence in a
// markdown file. This is the counter finally existing.
func (s *Store) Iterate(runID string) (int, error) {
	if _, err := s.db.Exec(`UPDATE runs SET iterations = iterations + 1 WHERE run_id=?`, runID); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRow(`SELECT iterations FROM runs WHERE run_id=?`, runID).Scan(&n)
	return n, err
}

// UnattendedOpenRun returns any open run in the project running unattended, or nil.
//
// Project-wide rather than per-session on purpose. The destruction guard has to answer while a
// tool call is in flight, and attribution can fail — no agent_id, an ambiguous session, a
// subshell. Asking "is anything here unattended right now" is the question whose WRONG answer is
// cheap: a needless denial of `rm` costs the loop a line in its report, and a needless allow costs
// work that nobody recovers, in a run nobody was watching. That asymmetry is rule 1's entire
// argument, applied to the guard that enforces rule 1.
func (s *Store) UnattendedOpenRun(project string) (*Run, error) {
	row := s.db.QueryRow(`SELECT `+runCols+`
	   FROM runs WHERE project=? AND status='running' AND mode=?
	   ORDER BY started_at DESC, rowid DESC LIMIT 1`, project, ModeUnattended)
	r, err := scanRun(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// SetWorktree records which working tree a run lives in. Idempotent and cheap: it is called from
// `batten phase` and `batten worktree`, both of which already know where they are standing.
func (s *Store) SetWorktree(runID, root string) error {
	_, err := s.db.Exec(`UPDATE runs SET worktree=? WHERE run_id=?`, root, runID)
	return err
}

func (s *Store) SetBaseSHA(runID, sha string) error {
	_, err := s.db.Exec(`UPDATE runs SET base_sha=? WHERE run_id=?`, sha, runID)
	return err
}

func (s *Store) CloseRun(runID, status string) error {
	t := now()
	if _, err := s.db.Exec(`UPDATE runs SET status=?, ended_at=? WHERE run_id=?`, status, t, runID); err != nil {
		return err
	}
	// A closed run has no phase still running. Nothing used to write ended_at on a phase node,
	// so every phase read `running` forever — on `batten show`, on the canvas and in the TUI,
	// in the same frame whose header said the run finished ok. Scoped by run_id: node ids are
	// per-run now, but this UPDATE must not depend on that to be safe.
	_, err := s.db.Exec(
		`UPDATE nodes SET status=?, ended_at=? WHERE run_id=? AND kind='phase' AND ended_at IS NULL`,
		status, t, runID)
	return err
}

// ---------- nodes & edges ----------

type Node struct {
	NodeID    string
	RunID     string
	Kind      string
	Label     string
	Domain    string
	Status    string
	AgentID   string
	AgentType string
	CostUSD   float64
	StartedAt int64
	EndedAt   *int64
	// Attempt is 1 for the first agent to take a piece of work and N+1 for the one that took it
	// over after attempt N ended failed. It is what makes two same-domain cards distinguishable
	// on a surface: without it a reviewer looking at a red `ml` and a green `ml` cannot tell
	// which one is the retry, only that there are two.
	Attempt int
}

const nodeCols = `node_id, run_id, kind, label, COALESCE(domain,''), status,
	COALESCE(agent_id,''), COALESCE(agent_type,''), cost_usd, started_at, ended_at, attempt`

func scanNode(scan func(...any) error) (*Node, error) {
	var n Node
	err := scan(&n.NodeID, &n.RunID, &n.Kind, &n.Label, &n.Domain, &n.Status,
		&n.AgentID, &n.AgentType, &n.CostUSD, &n.StartedAt, &n.EndedAt, &n.Attempt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// PhaseNodeID and AgentNodeID are the only sanctioned ways to name a node.
//
// node_id is a PRIMARY KEY and AddNode is an INSERT OR REPLACE, so an id that does not carry
// its run is not an identifier — it is a collision waiting for a second work item. A phase
// named "build" was once literally one row for the whole database: the second unit to enter
// build took the row out of the first one's run, and that run's canvas collapsed to a bare
// header while its subagents were left pointing at a parent that had moved. Two units open at
// once is the headline use case, so this has to be structural rather than remembered.
func PhaseNodeID(runID, phaseID string) string { return runID + ":p-" + phaseID }

// AgentNodeID scopes subagent nodes the same way. Agent ids are only unique within the session
// that minted them, and two runs fanning out over the same domains reuse them freely.
func AgentNodeID(runID, agentID string) string { return runID + ":n-" + agentID }

// DisplayNodeID strips the run scope and the kind prefix, leaving the name a human typed or a
// subagent was launched under. Internal ids are for joins; showing one to an agent in a denial
// invites it to pass that id back to `batten claim`, where it is not recognised.
func DisplayNodeID(nodeID string) string {
	if i := strings.LastIndex(nodeID, ":"); i >= 0 {
		nodeID = nodeID[i+1:]
	}
	if len(nodeID) > 2 && (nodeID[:2] == "n-" || nodeID[:2] == "p-") {
		nodeID = nodeID[2:]
	}
	return nodeID
}

func (s *Store) AddNode(n Node) error {
	if n.StartedAt == 0 {
		n.StartedAt = now()
	}
	if n.Status == "" {
		n.Status = "running"
	}
	if n.Attempt == 0 {
		n.Attempt = 1
	}
	// attempt is written explicitly because this is INSERT OR REPLACE: leaving it to the column
	// default would silently reset a retry back to attempt 1 the next time anything re-upserted
	// the row.
	_, err := s.db.Exec(`INSERT OR REPLACE INTO nodes
	   (node_id, run_id, kind, label, domain, status, agent_id, agent_type, cost_usd, started_at, attempt)
	   VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		n.NodeID, n.RunID, n.Kind, n.Label, n.Domain, n.Status, n.AgentID, n.AgentType,
		n.CostUSD, n.StartedAt, n.Attempt)
	return err
}

func (s *Store) FinishNode(nodeID, status string, cost float64) error {
	t := now()
	_, err := s.db.Exec(`UPDATE nodes SET status=?, ended_at=?, cost_usd=cost_usd+? WHERE node_id=?`,
		status, t, cost, nodeID)
	return err
}

func (s *Store) NodeByAgent(agentID string) (*Node, error) {
	row := s.db.QueryRow(`SELECT `+nodeCols+`
	   FROM nodes WHERE agent_id=? ORDER BY started_at DESC, rowid DESC LIMIT 1`, agentID)
	return scanNode(row.Scan)
}

func (s *Store) Nodes(runID string) ([]Node, error) {
	rows, err := s.db.Query(`SELECT `+nodeCols+` FROM nodes WHERE run_id=? ORDER BY started_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// LastUnretriedFailure finds the attempt that a subagent starting NOW would be retrying, or
// sql.ErrNoRows when there is none.
//
// This is the whole producer side of `edges.rel = retry_of`, which had five readers and no
// writer: `batten pr` counts retries for its badge and draws the dotted edge, the canvas colours
// it orange, the vault lists it under Relations, MCP reports `retries: N`, and the TUI hangs it
// off the node. Every one of those was reading a row nothing ever inserted, and the headline
// claim of `batten pr` — that the DAG shows what a plan diagram cannot — rests entirely on this.
//
// A retry is: a FINISHED subagent, in this run, that ended `failed`, whose logical identity
// matches the new one, and which nothing has retried yet.
//
// Each of those four clauses is load-bearing:
//
//   - finished and failed, ended at or before the new agent starts. This is the clause that
//     keeps a FAN-OUT from reading as a pile of retries: two agents on the same domain running
//     side by side are the normal case, and the earlier one is still `running` with a NULL
//     ended_at when the later one starts, so it cannot match.
//   - identity is the DOMAIN when the agent has one, because the domain is the unit of work the
//     fan-out divides; agent_type is the fallback for an agent outside the declared domains.
//   - not already retried, or a run that failed once and succeeded on the second try would mark
//     every later same-domain agent as a third attempt at work that is long finished.
//   - scoped to the run, never to the phase: the build → verify → fix → re-verify loop retries a
//     domain in a DIFFERENT phase from the one that failed, and that is the retry most worth
//     drawing.
//
// What it inherits and cannot fix here: `failed` comes from SubagentStop's reading of the final
// assistant message, which is a heuristic. A false `failed` produces a false retry edge. The
// status is the same one every other surface already trusts, so this adds a reader of it, not a
// new guess.
func (s *Store) LastUnretriedFailure(runID, domain, agentType string, notAfter int64) (*Node, error) {
	q := `SELECT ` + nodeCols + ` FROM nodes
	   WHERE run_id=? AND kind='subagent' AND status='failed'
	     AND ended_at IS NOT NULL AND ended_at<=?
	     AND NOT EXISTS (SELECT 1 FROM edges e
	                     WHERE e.run_id=nodes.run_id AND e.dst_node=nodes.node_id AND e.rel='retry_of')`
	args := []any{runID, notAfter}
	if domain != "" {
		q += ` AND domain=?`
		args = append(args, domain)
	} else {
		// Both sides must be domainless. A node that carries a domain is identified by it, and
		// pairing it with a domainless node on agent_type alone would cross the fan-out axis.
		q += ` AND COALESCE(domain,'')='' AND agent_type=?`
		args = append(args, agentType)
	}
	q += ` ORDER BY ended_at DESC, rowid DESC LIMIT 1`
	return scanNode(s.db.QueryRow(q, args...).Scan)
}

type Edge struct{ Src, Dst, Rel string }

func (s *Store) AddEdge(runID, src, dst, rel string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO edges (run_id, src_node, dst_node, rel) VALUES (?,?,?,?)`,
		runID, src, dst, rel)
	return err
}

func (s *Store) Edges(runID string) ([]Edge, error) {
	rows, err := s.db.Query(`SELECT src_node, dst_node, rel FROM edges WHERE run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.Src, &e.Dst, &e.Rel); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- write-sets ----------

// ClaimWriteSet assigns files to a node. The PRIMARY KEY (run_id, path) means the
// database itself refuses a second owner for a file — the disjointness rule is not
// advice, it is a constraint.
func (s *Store) ClaimWriteSet(runID, nodeID string, paths []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		p = normPath(p)
		var owner string
		err := tx.QueryRow(`SELECT node_id FROM writesets WHERE run_id=? AND path=?`, runID, p).Scan(&owner)
		if err == nil && owner != nodeID {
			return fmt.Errorf("write-set collision: %s is already owned by %s", p, owner)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO writesets (run_id, node_id, path) VALUES (?,?,?)`,
			runID, nodeID, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WriteSetOwner returns the node owning a path in a run, or "" if unclaimed.
//
// root is the directory the run's relative paths hang off. Pass "" to skip the identity check
// below and compare paths only.
//
// TWO LOOKUPS, because a path is a NAME and the guard is about a FILE.
//
// The first is the exact normalised path, which is what the PRIMARY KEY enforces. The second
// exists because two different names can be the same file, and measuring beat guessing here:
// on this Windows machine, a hard link produced two paths that normPath called different and
// os.SameFile called identical. Agent A claims `api/rate.go`, agent B writes `api/rate-link.go`,
// and the guard waves it through while they are one file on disk. Directory symlinks do the
// same thing for free on macOS and Linux — /var against /private/var is the canonical case.
//
// AND WHY THE PATH STAYS PRIMARY, which is the part a device+inode design has to answer: the
// write-set fences files an agent is ABOUT to write, and most of them do not exist yet. A file
// that does not exist has no inode to key on. Measured, not assumed: os.Stat on an unwritten
// path fails, so an identity-only scheme cannot fence the very case the guard exists for.
//
// So identity is a SECOND chance to catch a collision, never the first — and it costs one stat
// per existing claim, only when the path lookup missed and the target actually exists.
func (s *Store) WriteSetOwner(runID, root, path string) (string, error) {
	var owner string
	err := s.db.QueryRow(`SELECT node_id FROM writesets WHERE run_id=? AND path=?`,
		runID, normPath(path)).Scan(&owner)
	if err == nil {
		return owner, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if root == "" {
		return "", nil
	}
	return s.ownerBySameFile(runID, root, path)
}

// ownerBySameFile asks the operating system whether the target is any of the run's claimed
// files under a different name.
func (s *Store) ownerBySameFile(runID, root, path string) (string, error) {
	target, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		// It does not exist, so it cannot be an alias of something that does. This is the
		// common case — the guard runs before the write — and it exits here for free.
		return "", nil
	}

	rows, err := s.db.Query(`SELECT node_id, path FROM writesets WHERE run_id=?`, runID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var node, claimed string
		if err := rows.Scan(&node, &claimed); err != nil {
			return "", err
		}
		fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(claimed)))
		if err != nil {
			continue // claimed but not yet written: nothing to compare
		}
		if os.SameFile(target, fi) {
			return node, nil
		}
	}
	return "", rows.Err()
}

func (s *Store) WriteSet(runID, nodeID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM writesets WHERE run_id=? AND node_id=? ORDER BY path`, runID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// WriteSetsByRun returns every claim in a run, keyed by owning node — one query instead of one
// per node, the same shape as UsageByNode.
//
// A run with NO claims at all returns a nil map, and that nil is load-bearing: it is the
// difference between "the write-set was never recorded" and "this agent owned nothing", and a
// caller rendering the second as the first would be inventing a fact. An empty non-nil map would
// erase that distinction, so this returns nil rather than map[string][]string{}.
func (s *Store) WriteSetsByRun(runID string) (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT node_id, path FROM writesets WHERE run_id=? ORDER BY node_id, path`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out map[string][]string
	for rows.Next() {
		var node, path string
		if err := rows.Scan(&node, &path); err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[node] = append(out[node], path)
	}
	return out, rows.Err()
}

// normPath canonicalizes a repo-relative path for write-set comparison. On Windows AND macOS
// it also case-folds: NTFS and default APFS are both case-insensitive, so ml/F.py and ml/f.py
// are the SAME file there, and a guard that treated them as two would let an agent cross the
// fence just by changing the casing. A Mac with a case-sensitive volume loses nothing that
// matters: folding errs toward DETECTING collisions, and two paths differing only by case is
// a practice that breaks on every default macOS/Windows checkout anyway. Linux stays exact.
func normPath(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}

// ---------- verdicts ----------

// Verdict is the envelope. The shape is engram's mem_doctor envelope, not invented here.
type Verdict struct {
	RunID                string   `json:"-"`
	NodeID               string   `json:"-"`
	Gate                 string   `json:"-"`
	CheckID              string   `json:"check_id"`
	Result               string   `json:"result"` // ok|warn|blocked
	Evidence             []string `json:"evidence"`
	Why                  string   `json:"why"`
	SafeNextStep         string   `json:"safe_next_step"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
	Source               string   `json:"-"` // "agent" (a claim) | "batten" (evidence batten generated)
	// TargetDigest fingerprints the working tree this verdict was made about. Empty means it
	// could not be measured here, which is NOT the same as "unchanged".
	TargetDigest string `json:"-"`
	TS           int64  `json:"-"`
}

// ErrNoEvidence is the golden rule, mechanized: an approval with nothing to point at
// is not an approval. This is the single thing batten exists to make impossible.
var ErrNoEvidence = errors.New(
	"result \"ok\" with an empty evidence[] is not allowed: an approval must cite something " +
		"(command output, test counts, a criterion verified). Without evidence the result is \"blocked\"")

func (s *Store) SaveVerdict(v Verdict, evidenceRequired bool) error {
	if evidenceRequired && v.Result == "ok" && len(v.Evidence) == 0 {
		return ErrNoEvidence
	}
	ev, err := json.Marshal(v.Evidence)
	if err != nil {
		return err
	}
	if v.TS == 0 {
		v.TS = now()
	}
	rc := 0
	if v.RequiresConfirmation {
		rc = 1
	}
	src := v.Source
	if src == "" {
		src = "agent"
	}
	_, err = s.db.Exec(`INSERT INTO verdicts
	  (run_id, node_id, gate, check_id, result, evidence_json, why, safe_next_step, requires_confirmation, source, ts, target_digest)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.RunID, v.NodeID, v.Gate, v.CheckID, v.Result, string(ev), v.Why, v.SafeNextStep, rc, src, v.TS,
		nullable(v.TargetDigest))
	if err != nil {
		return err
	}
	// An approving verdict covers the criteria its evidence CITES — `AC-3: ...` marks
	// criterion 3. Only an ok result covers anything: a blocked verdict naming AC-3 is
	// describing what failed, and marking it covered would invert its meaning. Best-effort
	// by contract: the verdict is already saved, and a citation to a criterion this run
	// never seeded simply covers nothing.
	if v.Result == "ok" {
		for _, e := range v.Evidence {
			if m := citationRe.FindStringSubmatch(e); m != nil {
				idx, _ := strconv.Atoi(m[1])
				_, _ = s.db.Exec(`UPDATE criteria SET status='covered', evidence=?
				   WHERE run_id=? AND idx=?`, firstLineStr(e), v.RunID, idx)
			}
		}
	}
	return err
}

// citationRe is the evidence-to-criterion link: an evidence item that BEGINS with `AC-<n>`
// cites acceptance criterion n. A string prefix on the existing []string, deliberately —
// finding #27 showed what handing objects to string fields does, so the envelope's shape
// does not change.
var citationRe = regexp.MustCompile(`^\s*AC-(\d+)\b`)

func firstLineStr(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Criterion is one acceptance criterion of a run, seeded from the unit's block in unit.plan.
type Criterion struct {
	RunID    string
	UnitID   string
	Idx      int // 1-based: the n in AC-n
	Text     string
	Status   string // open | covered
	Evidence string // first line of the evidence item that covered it
}

// StatusCovered is the one non-default criterion state. `open` is the default, and it means
// exactly "no approving evidence has cited this yet" — never "failed".
const StatusCovered = "covered"

// SeedCriteria records a run's acceptance criteria, once: re-seeding an already-seeded run is
// a no-op, so statuses survive phase changes — every `batten phase` call passes through here.
func (s *Store) SeedCriteria(runID, unitID string, texts []string) error {
	if len(texts) == 0 {
		return nil
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM criteria WHERE run_id=?`, runID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, t := range texts {
		if _, err := tx.Exec(`INSERT INTO criteria (run_id, unit_id, idx, text, status)
		   VALUES (?,?,?,?,'open')`, runID, unitID, i+1, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Criteria lists a run's acceptance criteria in index order. Empty means none were seeded —
// the unit has no criteria in the plan document, or no plan document at all — which every
// surface must report as "no criteria declared", never as a fully-satisfied empty list.
func (s *Store) Criteria(runID string) ([]Criterion, error) {
	rows, err := s.db.Query(`SELECT run_id, unit_id, idx, text, status, COALESCE(evidence,'')
	   FROM criteria WHERE run_id=? ORDER BY idx`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Criterion
	for rows.Next() {
		var c Criterion
		if err := rows.Scan(&c.RunID, &c.UnitID, &c.Idx, &c.Text, &c.Status, &c.Evidence); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const verdictCols = `gate, check_id, result, evidence_json, COALESCE(why,''),
	COALESCE(safe_next_step,''), requires_confirmation, COALESCE(source,'agent'), ts,
	COALESCE(target_digest,'')`

func scanVerdict(runID string, scan func(...any) error) (*Verdict, error) {
	var v Verdict
	var ev string
	var rc int
	if err := scan(&v.Gate, &v.CheckID, &v.Result, &ev, &v.Why, &v.SafeNextStep, &rc, &v.Source, &v.TS,
		&v.TargetDigest); err != nil {
		return nil, err
	}
	v.RunID = runID
	v.RequiresConfirmation = rc == 1
	_ = json.Unmarshal([]byte(ev), &v.Evidence)
	return &v, nil
}

// LatestVerdict returns the most recent verdict for a run's gate (any source).
func (s *Store) LatestVerdict(runID, gate string) (*Verdict, error) {
	row := s.db.QueryRow(`SELECT `+verdictCols+`
	   FROM verdicts WHERE run_id=? AND (?='' OR gate=?) ORDER BY ts DESC, verdict_id DESC LIMIT 1`,
		runID, gate, gate)
	return scanVerdict(runID, row.Scan)
}

// LatestVerdictBySource returns the most recent verdict from a specific producer. The commit
// gate uses this to demand a batten-generated verdict (real check output), not just the
// agent's claim — that is what makes "verified" mean verified.
func (s *Store) LatestVerdictBySource(runID, gate, source string) (*Verdict, error) {
	row := s.db.QueryRow(`SELECT `+verdictCols+`
	   FROM verdicts WHERE run_id=? AND (?='' OR gate=?) AND source=? ORDER BY ts DESC, verdict_id DESC LIMIT 1`,
		runID, gate, gate, source)
	return scanVerdict(runID, row.Scan)
}

// LatestVerdictNotBySource is the other half of the pair. `batten check` writes its own
// source='batten' verdict, and that row is both the newest verdict AND a batten-verified one —
// so it used to satisfy both of the gate's conditions by itself, and `batten check` alone could
// close a unit on an empty diff with nothing having judged the acceptance criteria. The two
// conditions only mean something if they come from two different producers: the machine says
// the checks ran, the reviewer says the work is right.
func (s *Store) LatestVerdictNotBySource(runID, gate, source string) (*Verdict, error) {
	row := s.db.QueryRow(`SELECT `+verdictCols+`
	   FROM verdicts WHERE run_id=? AND (?='' OR gate=?) AND source<>? ORDER BY ts DESC, verdict_id DESC LIMIT 1`,
		runID, gate, gate, source)
	return scanVerdict(runID, row.Scan)
}

// ---------- usage & budget ----------

// Usage is one API request's token buckets, already priced.
type Usage struct {
	RequestID    string
	RunID        string
	NodeID       string
	AgentID      string
	Model        string
	Speed        string
	TS           int64
	InputTokens  int64
	OutputTokens int64
	CacheWrite5m int64
	CacheWrite1h int64
	CacheRead    int64
	WebSearches  int64
	ImputedUSD   float64
}

// Tokens is every bucket that counts against a token ceiling. Cache reads are included:
// they are cheap, not free, and they are real context the model processed.
func (u Usage) Tokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead
}

// RecordUsage ingests parsed transcript rows. Idempotent by (request_id, run_id): a
// transcript can be re-parsed any number of times without double-counting, which matters
// because resumes and retries genuinely do replay lines.
//
// Time-fenced per run: a session transcript spans the session's WHOLE life, but a run that
// adopts the session must only own the usage that happened while it was open. Without the
// fence, a run opened in hour 30 of a long session inherits 30 hours of history — found live
// in E0 when a fresh demo run showed 176M tokens it never spent.
// Fenced is what the time fence kept out: usage that is real, was parsed and priced, and
// belongs to the session rather than to this run. It is returned rather than dropped because
// silently discarding the whole transcript and then printing "+0 requests" reads as "this run
// cost nothing", which is the one thing a budget tool must never say when it does not know.
type Fenced struct {
	Requests   int
	Tokens     int64
	ImputedUSD float64
	Earliest   int64 // ts of the oldest row kept out; 0 when nothing was
}

func (s *Store) RecordUsage(us []Usage) (added int, err error) {
	added, _, err = s.RecordUsageFenced(us)
	return added, err
}

// RecordUsageFenced is RecordUsage plus an account of what the fence excluded.
func (s *Store) RecordUsageFenced(us []Usage) (added int, out Fenced, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, out, err
	}
	defer tx.Rollback()

	startedAt := map[string]int64{}
	fenceFor := func(runID string) int64 {
		if v, ok := startedAt[runID]; ok {
			return v
		}
		var v int64
		_ = tx.QueryRow(`SELECT started_at FROM runs WHERE run_id=?`, runID).Scan(&v)
		startedAt[runID] = v
		return v
	}

	ins, err := tx.Prepare(`INSERT OR IGNORE INTO usage
	  (request_id, run_id, node_id, agent_id, model, speed, ts,
	   input_tokens, output_tokens, cache_write_5m, cache_write_1h, cache_read, web_searches, imputed_usd)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, out, err
	}
	defer ins.Close()

	touched := map[string]bool{}
	for _, u := range us {
		if u.TS < fenceFor(u.RunID) {
			// Predates the run: this usage belongs to the session, not to this run. Counted
			// so the caller can say so out loud instead of reporting an unmeasured zero.
			out.Requests++
			out.Tokens += u.Tokens()
			out.ImputedUSD += u.ImputedUSD
			if out.Earliest == 0 || u.TS < out.Earliest {
				out.Earliest = u.TS
			}
			continue
		}
		res, err := ins.Exec(u.RequestID, u.RunID, nullable(u.NodeID), nullable(u.AgentID),
			u.Model, u.Speed, u.TS, u.InputTokens, u.OutputTokens,
			u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.WebSearches, u.ImputedUSD)
		if err != nil {
			return 0, out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
			touched[u.RunID] = true
		}
	}
	// Recompute run totals from the usage table rather than incrementing, so the totals
	// can never drift away from their own ledger.
	for runID := range touched {
		if _, err := tx.Exec(`UPDATE runs SET
		    tokens_spent = (SELECT COALESCE(SUM(input_tokens+output_tokens+cache_write_5m+cache_write_1h+cache_read),0)
		                    FROM usage WHERE run_id=?),
		    imputed_usd_spent = (SELECT COALESCE(SUM(imputed_usd),0) FROM usage WHERE run_id=?)
		    WHERE run_id=?`, runID, runID, runID); err != nil {
			return 0, out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, out, err
	}
	return added, out, nil
}

// UsageByNode totals a run's usage per node, for the TUI and the canvas.
func (s *Store) UsageByNode(runID string) (map[string]Usage, error) {
	rows, err := s.db.Query(`SELECT COALESCE(node_id,''),
	   SUM(input_tokens), SUM(output_tokens), SUM(cache_write_5m), SUM(cache_write_1h),
	   SUM(cache_read), SUM(web_searches), SUM(imputed_usd)
	   FROM usage WHERE run_id=? GROUP BY node_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Usage{}
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.NodeID, &u.InputTokens, &u.OutputTokens, &u.CacheWrite5m,
			&u.CacheWrite1h, &u.CacheRead, &u.WebSearches, &u.ImputedUSD); err != nil {
			return nil, err
		}
		u.RunID = runID
		out[u.NodeID] = u
	}
	return out, rows.Err()
}

// SeenRequests returns the request ids already ingested for a run, so a parser can skip
// re-pricing what it has already priced.
func (s *Store) SeenRequests(runID string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT request_id FROM usage WHERE run_id=?`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	return seen, rows.Err()
}

// Ceiling is one budget limit and where the run stands against it.
type Ceiling struct {
	Kind      string // tokens | imputed_usd | quota_pct
	Spent     float64
	Cap       float64
	Exceeded  bool
	Available bool // false => we cannot measure this; it is NOT enforced, and we say so
	// Reason says WHY it is unmeasurable, in the user's terms. Every unavailable ceiling used
	// to print "install the statusline", which is the remedy for exactly one of the causes and
	// misdirects for the others.
	Reason string
	// UnpricedTokens and TotalTokens qualify the imputed_usd kind only: when UnpricedTokens
	// is > 0, Spent is missing the dollars for exactly that volume and is a floor, not a total.
	UnpricedTokens int64
	TotalTokens    int64
}

// tokenReason and the constants below are the causes a ceiling can be unmeasurable. They are
// spelled as remedies because that is what the reader needs.
const (
	reasonNoUsage    = "no usage has been measured for this run — run `batten ingest <unit> --transcript <path>`"
	reasonNoStatus   = "install `batten statusline` — it is the only local surface that samples the quota"
	reasonNoBaseline = "this run opened before the statusline was installed, so it has no baseline to subtract"
	reasonRolledOver = "the 5-hour window rolled over mid-run, so the delta is not a share of one window"
	reasonAmbiguous  = "more than one run is open in this session, so the quota delta cannot be attributed to one"
)

// HasUsage reports whether any usage row was ever recorded for this run. This is the
// difference between "this run spent nothing" and "nobody has measured this run", and
// batten is required to tell them apart rather than print a zero for both.
func (s *Store) HasUsage(runID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM usage WHERE run_id=?`, runID).Scan(&n)
	return n > 0, err
}

// Budget reports every declared ceiling. A ceiling we cannot measure is reported as
// unavailable, never as zero — a budget tool that invents a number is worse than none.
func (s *Store) Budget(runID string, tokensCap int64, usdCap, quotaCap float64) ([]Ceiling, error) {
	r, err := s.Run(runID)
	if err != nil {
		return nil, err
	}
	// A run with no usage row has not spent zero — it has not been measured. Reporting the
	// former for the latter is the exact failure principle #1 exists to prevent, and it was
	// the DEFAULT path: the flow opens the run, the work happens, and nothing ingests a
	// transcript unless someone remembers to.
	measured, err := s.HasUsage(runID)
	if err != nil {
		return nil, err
	}

	var cs []Ceiling
	if tokensCap > 0 {
		c := Ceiling{Kind: "tokens", Spent: float64(r.TokensSpent), Cap: float64(tokensCap),
			Exceeded: measured && r.TokensSpent >= tokensCap, Available: measured}
		if !measured {
			c.Reason = reasonNoUsage
		}
		cs = append(cs, c)
	}
	if usdCap > 0 {
		c := Ceiling{Kind: "imputed_usd", Spent: r.ImputedUSD, Cap: usdCap,
			Exceeded: measured && r.ImputedUSD >= usdCap, Available: measured,
			UnpricedTokens: r.UnpricedTokens, TotalTokens: r.TokensSpent}
		if !measured {
			c.Reason = reasonNoUsage
		}
		cs = append(cs, c)
	}
	if quotaCap > 0 {
		burned, ok, why, err := s.quotaBurned(r)
		if err != nil {
			return nil, err
		}
		cs = append(cs, Ceiling{Kind: "quota_pct", Spent: burned, Cap: quotaCap,
			Exceeded: ok && burned >= quotaCap, Available: ok, Reason: why})
	}
	return cs, nil
}

// QuotaBurned is the share of the rolling 5-hour window this run consumed: the delta
// between the baseline taken when it opened and the newest sample. Only `batten statusline`
// can supply either, so this is unavailable (not zero) when it is not installed.
//
// The window resets on a rolling basis; a negative delta means it rolled over mid-run,
// which we report as unmeasurable rather than as a bogus number.
func (s *Store) QuotaBurned(r *Run) (pct float64, ok bool, err error) {
	pct, ok, _, err = s.quotaBurned(r)
	return pct, ok, err
}

// quotaBurned also returns WHY it could not measure. The causes are genuinely different and
// so are their remedies: an uninstalled statusline, a run that predates its installation, a
// window that rolled over, and an ambiguous session are four different problems. Printing
// "install the statusline" for all four sends three of those users to fix something that is
// already fine — and the field test caught exactly that, with doctor reporting the statusline
// installed on the same screen where budget told the user to install it.
func (s *Store) quotaBurned(r *Run) (pct float64, ok bool, reason string, err error) {
	if r.SessionID == "" {
		return 0, false, reasonAmbiguous, nil
	}
	snap, err := s.LatestQuota(r.SessionID)
	if err != nil || snap.FiveHourPct == nil {
		//nolint:nilerr // absence is not an error; it is "not installed"
		return 0, false, reasonNoStatus, nil
	}
	// The statusline IS sampling — this run simply opened before it started, so there is no
	// baseline to subtract. Naming that is the difference between a fixable state and a
	// confusing one: the next run will measure fine, and nothing needs installing.
	if r.QuotaStart5h == nil {
		return 0, false, reasonNoBaseline, nil
	}
	d := *snap.FiveHourPct - *r.QuotaStart5h
	if d < 0 {
		return 0, false, reasonRolledOver, nil
	}
	return d, true, "", nil
}

// OverBudget reports whether any measurable ceiling is blown, and which.
func (s *Store) OverBudget(runID string, tokensCap int64, usdCap, quotaCap float64) (bool, []Ceiling, error) {
	cs, err := s.Budget(runID, tokensCap, usdCap, quotaCap)
	if err != nil {
		return false, nil, err
	}
	for _, c := range cs {
		if c.Exceeded {
			return true, cs, nil
		}
	}
	return false, cs, nil
}

// ---------- quota snapshots ----------

type QuotaSnapshot struct {
	SessionID     string
	TS            int64
	FiveHourPct   *float64
	FiveHourReset *int64
	SevenDayPct   *float64
	SevenDayReset *int64
}

func (s *Store) SaveQuota(q QuotaSnapshot) error {
	if q.TS == 0 {
		q.TS = now()
	}
	_, err := s.db.Exec(`INSERT INTO quota_snapshots
	  (session_id, ts, five_hour_pct, five_hour_reset, seven_day_pct, seven_day_reset)
	  VALUES (?,?,?,?,?,?)`,
		q.SessionID, q.TS, q.FiveHourPct, q.FiveHourReset, q.SevenDayPct, q.SevenDayReset)
	return err
}

func (s *Store) LatestQuota(sessionID string) (*QuotaSnapshot, error) {
	row := s.db.QueryRow(`SELECT session_id, ts, five_hour_pct, five_hour_reset,
	   seven_day_pct, seven_day_reset
	   FROM quota_snapshots WHERE session_id=? ORDER BY ts DESC, id DESC LIMIT 1`, sessionID)
	var q QuotaSnapshot
	err := row.Scan(&q.SessionID, &q.TS, &q.FiveHourPct, &q.FiveHourReset,
		&q.SevenDayPct, &q.SevenDayReset)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// SetQuotaBaseline back-fills a run's baseline once the first snapshot arrives. Runs
// often open before the first statusline invocation of a session.
func (s *Store) SetQuotaBaseline(runID string, pct float64) error {
	_, err := s.db.Exec(`UPDATE runs SET quota_start_5h=? WHERE run_id=? AND quota_start_5h IS NULL`,
		pct, runID)
	return err
}

// AdoptSession attaches a session to a run that was created without one (e.g. from the CLI).
func (s *Store) AdoptSession(runID, sessionID string) error {
	_, err := s.db.Exec(`UPDATE runs SET session_id=? WHERE run_id=? AND COALESCE(session_id,'')=''`,
		sessionID, runID)
	return err
}

// ---------- events & overrides ----------

// Decisions are the vocabulary of the audit log. They are what batten DID, which until now it
// never recorded — only what it was asked.
const (
	DecisionAllow  = "allow"  // batten had no objection (or no opinion at all)
	DecisionDeny   = "deny"   // the tool call was refused
	DecisionAdvise = "advise" // the call proceeded, with a warning the user can see
)

// Rules name which wedge produced a decision, so a report can group denials by cause without
// pattern-matching the English of the reason.
const (
	RuleVerdictGate = "verdict_gate"
	RuleBudget      = "budget"
	RuleWriteSet    = "write_set"
	RuleDegraded    = "degraded"   // batten could not run at all and said so
	RuleUnattended  = "unattended" // a rule of the unsupervised run, now a mechanism
	RuleBashWrite   = "bash_write" // a shell command writing a file it does not own (advisory)
)

// Event is one row of the replay log: what arrived, and what batten decided about it.
type Event struct {
	RunID    string
	NodeID   string
	Hook     string
	Payload  []byte
	Decision string // allow | deny | advise; empty for a bare record
	Reason   string
	Rule     string
	// Enforcement is the mode in force when this was decided: "enforce" or "report". It is what
	// lets a report say what got through while the gates were only warning.
	Enforcement string
	TS          int64
}

// LogEvent records an event with no decision attached. Kept for callers that are only
// journalling an arrival; anything that DECIDES something should use LogDecision.
func (s *Store) LogEvent(runID, nodeID, hook string, payload []byte) error {
	return s.LogDecision(Event{RunID: runID, NodeID: nodeID, Hook: hook, Payload: payload})
}

// LogDecision writes one replay-log row. Written AFTER the dispatch, which is the whole point:
// the row that said what came in was useless for answering what batten did about it.
func (s *Store) LogDecision(e Event) error {
	if e.TS == 0 {
		e.TS = now()
	}
	_, err := s.db.Exec(`INSERT INTO events
	  (run_id, node_id, hook, ts, payload, decision, reason, rule, enforcement)
	  VALUES (?,?,?,?,?,?,?,?,?)`,
		nullable(e.RunID), nullable(e.NodeID), e.Hook, e.TS, string(e.Payload),
		nullable(e.Decision), nullable(e.Reason), nullable(e.Rule), nullable(e.Enforcement))
	return err
}

// DecisionCount is one row of "what batten actually stopped", grouped the way a human would
// ask for it.
type DecisionCount struct {
	Decision    string
	Rule        string
	Enforcement string
	N           int
}

// CountDecisions reports what batten decided since a point in time.
//
// It counts rows batten WROTE, so it can only ever undercount — decisions taken before the
// decision column existed are simply absent, and the report says so rather than presenting a
// short history as a complete one.
func (s *Store) CountDecisions(project string, since int64) ([]DecisionCount, error) {
	// Events carry no project of their own; they are attributed through their run. Rows with no
	// run (an unattributable commit, a degraded hook) belong to whoever is asking: they happened
	// in this working tree, which is the only place batten was running.
	rows, err := s.db.Query(`
	  SELECT e.decision, COALESCE(e.rule,''), COALESCE(e.enforcement,''), COUNT(*)
	    FROM events e
	    LEFT JOIN runs r ON r.run_id = e.run_id
	   WHERE e.decision IS NOT NULL
	     AND e.ts >= ?
	     AND (e.run_id IS NULL OR r.project = ?)
	   GROUP BY e.decision, e.rule, e.enforcement`, since, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DecisionCount
	for rows.Next() {
		var c DecisionCount
		if err := rows.Scan(&c.Decision, &c.Rule, &c.Enforcement, &c.N); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FirstDecisionAt returns when batten first recorded a decision for this project, or 0 if it
// never has. A counter without this is a lie by omission: "3 commits denied" reads as a
// lifetime total, and batten only started counting when the column was added.
func (s *Store) FirstDecisionAt(project string) (int64, error) {
	var ts sql.NullInt64
	err := s.db.QueryRow(`
	  SELECT MIN(e.ts) FROM events e
	    LEFT JOIN runs r ON r.run_id = e.run_id
	   WHERE e.decision IS NOT NULL AND (e.run_id IS NULL OR r.project = ?)`, project).Scan(&ts)
	if err != nil || !ts.Valid {
		return 0, err
	}
	return ts.Int64, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) Override(runID, gate, reason string) error {
	_, err := s.db.Exec(`INSERT INTO overrides (run_id, gate, reason, ts) VALUES (?,?,?,?)`,
		runID, gate, reason, now())
	return err
}

// HasOverride reports whether a run's gate was explicitly overridden. The escape
// hatch exists so the gate does not get the plugin uninstalled — but it is on the record.
func (s *Store) HasOverride(runID, gate string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM overrides WHERE run_id=? AND (gate=? OR gate='*')`,
		runID, gate).Scan(&n)
	return n > 0, err
}

// OverrideDetail is what an override put on the record: which gate it opened, the human
// reason, and when.
type OverrideDetail struct {
	Gate   string
	Reason string
	TS     int64
}

// OverrideFor returns the newest override covering a run's gate, or nil. HasOverride answers
// the gate's question — is it open? — and nothing more; the reading surfaces need the reason
// and the timestamp, because a run whose gate is open by override while `batten show` still
// says "the close gate will deny a commit" states the opposite of the truth (#10). An escape
// hatch that is auditable only by opening SQLite by hand is not on the record in any sense
// that matters.
func (s *Store) OverrideFor(runID, gate string) (*OverrideDetail, error) {
	row := s.db.QueryRow(`SELECT gate, reason, ts FROM overrides WHERE run_id=? AND (gate=? OR gate='*')
	   ORDER BY ts DESC, rowid DESC LIMIT 1`, runID, gate)
	var o OverrideDetail
	err := row.Scan(&o.Gate, &o.Reason, &o.TS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ---------- headroom measurement ----------

// SetHeadroom records whether the compression proxy was live for a run. Called once at run
// creation; NULL stays NULL (unknown) if never set, which `batten measure` treats as its own
// bucket rather than lumping it with "off".
func (s *Store) SetHeadroom(runID string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE runs SET headroom=? WHERE run_id=? AND headroom IS NULL`, v, runID)
	return err
}

// MeasureGroup is the aggregate for one headroom state across finished runs.
type MeasureGroup struct {
	Label      string // "with headroom" | "without headroom" | "unknown"
	Runs       int
	MeanTokens float64
	MeanUSD    float64
}

// measureByFlag groups finished runs by a boolean run column. Reports means only; with too
// few runs per group the caller should say "insufficient", because comparing units that
// differ in size is noisy — batten must not pretend a 2-run sample is a conclusion.
// The column is interpolated, so it MUST come from the fixed set below, never from input.
func (s *Store) measureByFlag(project, column, name string) ([]MeasureGroup, error) {
	switch column {
	case "headroom", "code_graph":
	default:
		return nil, fmt.Errorf("measureByFlag: unknown column %q", column)
	}
	rows, err := s.db.Query(`SELECT
	    CASE `+column+` WHEN 1 THEN 'with `+name+`' WHEN 0 THEN 'without `+name+`' ELSE 'unknown' END,
	    COUNT(*), AVG(tokens_spent), AVG(imputed_usd_spent)
	  FROM runs
	  WHERE (?='' OR project=?) AND status IN ('ok','blocked','failed','rolled_back') AND tokens_spent > 0
	  GROUP BY `+column, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MeasureGroup
	for rows.Next() {
		var g MeasureGroup
		if err := rows.Scan(&g.Label, &g.Runs, &g.MeanTokens, &g.MeanUSD); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) MeasureByHeadroom(project string) ([]MeasureGroup, error) {
	return s.measureByFlag(project, "headroom", "headroom")
}

// MeasureByCodeGraph is the graphify counterpart: does a fresh code graph actually reduce
// the orientation cost of a run? Same rule — measured on YOUR runs, not taken on faith.
func (s *Store) MeasureByCodeGraph(project string) ([]MeasureGroup, error) {
	return s.measureByFlag(project, "code_graph", "code graph")
}

// SetCodeGraph records whether a fresh code graph existed when the run opened. Write-once
// like SetHeadroom: NULL stays NULL if never set.
func (s *Store) SetCodeGraph(runID string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE runs SET code_graph=? WHERE run_id=? AND code_graph IS NULL`, v, runID)
	return err
}

// ---------- multi-session: which session owns which run ----------

// RunBySession returns the open run bound to a session, if any. This is the anchor of
// multi-session correctness: when two Claude Code sessions work the same repo, each finds ITS
// unit by its own session id rather than guessing from a shared branch.
func (s *Store) RunBySession(project, sessionID string) (*Run, error) {
	if sessionID == "" {
		return nil, sql.ErrNoRows
	}
	row := s.db.QueryRow(`SELECT `+runCols+`
	   FROM runs WHERE project=? AND session_id=? AND status='running'
	   ORDER BY started_at DESC, rowid DESC LIMIT 1`, project, sessionID)
	return scanRun(row.Scan)
}

// OpenRuns lists every running run in a project — used to detect ambiguity (2+ open) and to
// search write-sets across sessions.
func (s *Store) OpenRuns(project string) ([]Run, error) {
	rows, err := s.db.Query(`SELECT `+runCols+`
	   FROM runs WHERE project=? AND status='running' ORDER BY started_at`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CrossOwner is a file's owner found in some OTHER open run of the project.
type CrossOwner struct {
	RunID    string
	UnitID   string
	NodeID   string
	Worktree string
}

// WriteSetOwnerAcrossOpenRuns finds who owns a path in any open run other than excludeRun.
// This is what defends the fan-out ACROSS sessions: session B editing a file that session A's
// agent claimed gets stopped, with A's unit named. The per-run writesets PK only defends within
// one run; this widens the check to the whole project's live work.
//
// Callers get the owner's WORKING TREE too, because a path is only a shared resource if the two
// runs are standing in the same one. See sameTreeAsCaller at the call site: batten spent three
// separate messages telling people to use a worktree per unit and then denied both of them for
// touching `api/handler.go`, which in two worktrees is two different files.
func (s *Store) WriteSetOwnerAcrossOpenRuns(project, path, excludeRun string) (*CrossOwner, error) {
	row := s.db.QueryRow(`SELECT w.run_id, r.unit_id, w.node_id, COALESCE(r.worktree,'')
	   FROM writesets w JOIN runs r ON r.run_id = w.run_id
	   WHERE r.project=? AND r.status='running' AND w.path=? AND w.run_id != ?
	   LIMIT 1`, project, normPath(path), excludeRun)
	var c CrossOwner
	err := row.Scan(&c.RunID, &c.UnitID, &c.NodeID, &c.Worktree)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ---------- run lifecycle helpers ----------

// StaleRuns lists runs still 'running' whose newest event is older than maxAge. A run nobody
// closed keeps its write-set claims alive and muddies session attribution, so doctor surfaces
// these rather than letting them rot silently.
func (s *Store) StaleRuns(project string, maxAge time.Duration) ([]Run, error) {
	cutoff := now() - int64(maxAge.Seconds())
	rows, err := s.db.Query(`SELECT `+runCols+` FROM runs
	   WHERE runs.project=? AND runs.status='running'
	     AND COALESCE((SELECT MAX(ts) FROM events e WHERE e.run_id=runs.run_id), runs.started_at) < ?
	   ORDER BY runs.started_at`, project, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ModelsByNode reports which model(s) each node actually ran on, from the ledger. This is how
// batten VERIFIES model routing: the spec declares a tier per domain, and this says what really
// happened — declared haiku, ran opus shows up as a discrepancy, not a silent overspend.
func (s *Store) ModelsByNode(runID string) (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT COALESCE(node_id,''), model, SUM(input_tokens+output_tokens) t
	   FROM usage WHERE run_id=? GROUP BY node_id, model ORDER BY node_id, t DESC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var node, model string
		var t int64
		if err := rows.Scan(&node, &model, &t); err != nil {
			return nil, err
		}
		out[node] = append(out[node], model)
	}
	return out, rows.Err()
}

// ModelSpend is per-model usage across a project's runs, for `batten measure`.
type ModelSpend struct {
	Model      string
	Requests   int
	Tokens     int64
	ImputedUSD float64
	// UnpricedRequests counts requests whose model had no published rate. Their tokens are
	// exact; their dollars are unknown — not zero. The renderer needs the split so it never
	// prints a hard $0.00 for a price that was never computed.
	UnpricedRequests int
}

func (s *Store) MeasureByModel(project string) ([]ModelSpend, error) {
	// Tokens sum all five buckets — same definition as runs.tokens_spent (RecordUsageFenced)
	// and batten.schema.json. Counting only input+output here made this table disagree with
	// `batten runs` over the same rows by whatever factor the cache traffic dictated.
	rows, err := s.db.Query(`SELECT u.model, COUNT(*),
	   SUM(u.input_tokens+u.output_tokens+u.cache_write_5m+u.cache_write_1h+u.cache_read),
	   SUM(u.imputed_usd),
	   SUM(CASE WHEN u.imputed_usd=0
	            AND (u.input_tokens+u.output_tokens+u.cache_write_5m+u.cache_write_1h+u.cache_read)>0
	       THEN 1 ELSE 0 END)
	   FROM usage u JOIN runs r ON r.run_id=u.run_id
	   WHERE (?='' OR r.project=?) GROUP BY u.model ORDER BY SUM(u.imputed_usd) DESC`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelSpend
	for rows.Next() {
		var m ModelSpend
		if err := rows.Scan(&m.Model, &m.Requests, &m.Tokens, &m.ImputedUSD, &m.UnpricedRequests); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
