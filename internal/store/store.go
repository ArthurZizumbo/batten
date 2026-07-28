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
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver; registers as "sqlite" (not "sqlite3")
)

type Store struct{ db *sql.DB }

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
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

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
const schemaVersion = 3

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
}

const runCols = `run_id, project, unit_id, COALESCE(session_id,''), COALESCE(phase,''),
	status, COALESCE(base_sha,''), tokens_spent, imputed_usd_spent, quota_start_5h,
	iterations, started_at, ended_at`

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
		&r.Iterations, &r.StartedAt, &r.EndedAt)
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
	_, err := s.db.Exec(`INSERT OR REPLACE INTO nodes
	   (node_id, run_id, kind, label, domain, status, agent_id, agent_type, cost_usd, started_at)
	   VALUES (?,?,?,?,?,?,?,?,?,?)`,
		n.NodeID, n.RunID, n.Kind, n.Label, n.Domain, n.Status, n.AgentID, n.AgentType, n.CostUSD, n.StartedAt)
	return err
}

func (s *Store) FinishNode(nodeID, status string, cost float64) error {
	t := now()
	_, err := s.db.Exec(`UPDATE nodes SET status=?, ended_at=?, cost_usd=cost_usd+? WHERE node_id=?`,
		status, t, cost, nodeID)
	return err
}

func (s *Store) NodeByAgent(agentID string) (*Node, error) {
	row := s.db.QueryRow(`SELECT node_id, run_id, kind, label, COALESCE(domain,''), status,
	   COALESCE(agent_id,''), COALESCE(agent_type,''), cost_usd, started_at, ended_at
	   FROM nodes WHERE agent_id=? ORDER BY started_at DESC, rowid DESC LIMIT 1`, agentID)
	var n Node
	err := row.Scan(&n.NodeID, &n.RunID, &n.Kind, &n.Label, &n.Domain, &n.Status,
		&n.AgentID, &n.AgentType, &n.CostUSD, &n.StartedAt, &n.EndedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) Nodes(runID string) ([]Node, error) {
	rows, err := s.db.Query(`SELECT node_id, run_id, kind, label, COALESCE(domain,''), status,
	   COALESCE(agent_id,''), COALESCE(agent_type,''), cost_usd, started_at, ended_at
	   FROM nodes WHERE run_id=? ORDER BY started_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.NodeID, &n.RunID, &n.Kind, &n.Label, &n.Domain, &n.Status,
			&n.AgentID, &n.AgentType, &n.CostUSD, &n.StartedAt, &n.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
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
func (s *Store) WriteSetOwner(runID, path string) (string, error) {
	var owner string
	err := s.db.QueryRow(`SELECT node_id FROM writesets WHERE run_id=? AND path=?`,
		runID, normPath(path)).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
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
	TS                   int64    `json:"-"`
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
	  (run_id, node_id, gate, check_id, result, evidence_json, why, safe_next_step, requires_confirmation, source, ts)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		v.RunID, v.NodeID, v.Gate, v.CheckID, v.Result, string(ev), v.Why, v.SafeNextStep, rc, src, v.TS)
	return err
}

const verdictCols = `gate, check_id, result, evidence_json, COALESCE(why,''),
	COALESCE(safe_next_step,''), requires_confirmation, COALESCE(source,'agent'), ts`

func scanVerdict(runID string, scan func(...any) error) (*Verdict, error) {
	var v Verdict
	var ev string
	var rc int
	if err := scan(&v.Gate, &v.CheckID, &v.Result, &ev, &v.Why, &v.SafeNextStep, &rc, &v.Source, &v.TS); err != nil {
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
func (s *Store) RecordUsage(us []Usage) (added int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
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
		return 0, err
	}
	defer ins.Close()

	touched := map[string]bool{}
	for _, u := range us {
		if u.TS < fenceFor(u.RunID) {
			continue // predates the run: this usage belongs to the session, not to this run
		}
		res, err := ins.Exec(u.RequestID, u.RunID, nullable(u.NodeID), nullable(u.AgentID),
			u.Model, u.Speed, u.TS, u.InputTokens, u.OutputTokens,
			u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.WebSearches, u.ImputedUSD)
		if err != nil {
			return 0, err
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
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
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
}

// Budget reports every declared ceiling. A ceiling we cannot measure is reported as
// unavailable, never as zero — a budget tool that invents a number is worse than none.
func (s *Store) Budget(runID string, tokensCap int64, usdCap, quotaCap float64) ([]Ceiling, error) {
	r, err := s.Run(runID)
	if err != nil {
		return nil, err
	}
	var cs []Ceiling
	if tokensCap > 0 {
		cs = append(cs, Ceiling{"tokens", float64(r.TokensSpent), float64(tokensCap),
			r.TokensSpent >= tokensCap, true})
	}
	if usdCap > 0 {
		cs = append(cs, Ceiling{"imputed_usd", r.ImputedUSD, usdCap, r.ImputedUSD >= usdCap, true})
	}
	if quotaCap > 0 {
		burned, ok, err := s.QuotaBurned(r)
		if err != nil {
			return nil, err
		}
		cs = append(cs, Ceiling{"quota_pct", burned, quotaCap, ok && burned >= quotaCap, ok})
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
	if r.QuotaStart5h == nil || r.SessionID == "" {
		return 0, false, nil
	}
	snap, err := s.LatestQuota(r.SessionID)
	if err != nil || snap.FiveHourPct == nil {
		return 0, false, nil //nolint:nilerr // absence is not an error; it is "not installed"
	}
	d := *snap.FiveHourPct - *r.QuotaStart5h
	if d < 0 {
		return 0, false, nil // the window rolled over mid-run
	}
	return d, true, nil
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

func (s *Store) LogEvent(runID, nodeID, hook string, payload []byte) error {
	_, err := s.db.Exec(`INSERT INTO events (run_id, node_id, hook, ts, payload) VALUES (?,?,?,?,?)`,
		nullable(runID), nullable(nodeID), hook, now(), string(payload))
	return err
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
	RunID  string
	UnitID string
	NodeID string
}

// WriteSetOwnerAcrossOpenRuns finds who owns a path in any open run other than excludeRun.
// This is what defends the fan-out ACROSS sessions: session B editing a file that session A's
// agent claimed gets stopped, with A's unit named. The per-run writesets PK only defends within
// one run; this widens the check to the whole project's live work.
func (s *Store) WriteSetOwnerAcrossOpenRuns(project, path, excludeRun string) (*CrossOwner, error) {
	row := s.db.QueryRow(`SELECT w.run_id, r.unit_id, w.node_id
	   FROM writesets w JOIN runs r ON r.run_id = w.run_id
	   WHERE r.project=? AND r.status='running' AND w.path=? AND w.run_id != ?
	   LIMIT 1`, project, normPath(path), excludeRun)
	var c CrossOwner
	err := row.Scan(&c.RunID, &c.UnitID, &c.NodeID)
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
	rows, err := s.db.Query(`SELECT `+runCols+` FROM runs r
	   WHERE r.project=? AND r.status='running'
	     AND COALESCE((SELECT MAX(ts) FROM events e WHERE e.run_id=r.run_id), r.started_at) < ?
	   ORDER BY r.started_at`, project, cutoff)
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
}

func (s *Store) MeasureByModel(project string) ([]ModelSpend, error) {
	rows, err := s.db.Query(`SELECT u.model, COUNT(*), SUM(u.input_tokens+u.output_tokens), SUM(u.imputed_usd)
	   FROM usage u JOIN runs r ON r.run_id=u.run_id
	   WHERE (?='' OR r.project=?) GROUP BY u.model ORDER BY SUM(u.imputed_usd) DESC`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelSpend
	for rows.Next() {
		var m ModelSpend
		if err := rows.Scan(&m.Model, &m.Requests, &m.Tokens, &m.ImputedUSD); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
