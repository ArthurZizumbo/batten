// Package usage counts what a run actually cost.
//
// Claude Code hooks are handed a transcript_path and nothing else: no tokens, no cost. The
// numbers exist only inside the JSONL transcript, so we parse it.
//
// The load-bearing fact — and the entire reason this package is not a twenty-line loop over
// one file — is that SUBAGENTS DO NOT APPEAR IN THE PARENT TRANSCRIPT. They are written to
// <dir>/<sessionId>/subagents/agent-<agentId>.jsonl, and the parent's Agent-tool result carries
// no token totals at all. A parser that reads only transcript_path therefore undercounts a
// fan-out run by however much the fan-out did — and fan-out IS batten's workload. We walk both.
//
// The format is not a public API. Every record is best-effort: a line that does not parse is
// skipped, never fatal, and partial results always beat an error.
package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// record is the slice of a transcript line we price. Fields we do not use are left undecoded
// so a format change in them cannot break ingestion.
type record struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Timestamp string `json:"timestamp"`
	AgentID   string `json:"agentId"`
	Message   struct {
		ID    string     `json:"id"`
		Model string     `json:"model"`
		Usage *usageJSON `json:"usage"`
	} `json:"message"`
}

type usageJSON struct {
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64  `json:"cache_read_input_tokens"`
	Speed               string `json:"speed"`
	InferenceGeo        string `json:"inference_geo"`

	// The 5m/1h split. These are priced differently (1.25x vs 2x input) and must not be
	// collapsed back into CacheCreationTokens.
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`

	ServerToolUse *struct {
		WebSearchRequests int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`

	// usage.iterations[] is DELIBERATELY NOT DECODED. It is a per-inner-turn breakdown whose
	// entries the top-level fields already sum. Adding it would double-count every request.
}

// syntheticModel marks Claude Code's locally-generated messages (API errors and the like).
// They were never a billed request, so they are not a cost.
const syntheticModel = "<synthetic>"

// Parse walks the session transcript AND its subagents/*.jsonl, prices every request, and
// returns rows ready for store.RecordUsage. Rows whose RequestID is in seen are skipped.
// AgentID is set for subagent rows and empty for the parent; the caller maps it to a node.
// UnknownModels lists model ids we had no price for (their rows carry ImputedUSD == 0).
//
// The returned error reports that the PARENT transcript could not be read; rows gathered from
// the subagents are still returned alongside it. A missing subagents directory is not an error
// — most sessions never fan out.
func Parse(transcriptPath, runID string, seen map[string]bool) (rows []store.Usage, unknownModels []string, err error) {
	// Dedup is global across the parent and every subagent, and it is not optional: a real
	// transcript on this machine replays 74 of its 106 requests (multi-block responses and
	// resumes re-emit a line per content block, each carrying the SAME usage object). Summing
	// without dedup over-counted that session by 2.11x.
	//
	// seen belongs to the caller (it comes from store.SeenRequests); we never write to it.
	local := make(map[string]bool, len(seen))
	skip := func(key string) bool {
		if key == "" || seen[key] || local[key] {
			return true
		}
		local[key] = true
		return false
	}

	unknown := map[string]bool{}

	if r, e := parseFile(transcriptPath, runID, "", skip, unknown); e != nil {
		err = e // remember it, but keep going: the subagents are a separate source of truth
	} else {
		rows = append(rows, r...)
	}

	for _, sub := range subagentFiles(transcriptPath) {
		r, e := parseFile(sub.path, runID, sub.agentID, skip, unknown)
		if e != nil {
			continue // one unreadable subagent must not lose the rest of the run
		}
		rows = append(rows, r...)
	}

	for m := range unknown {
		unknownModels = append(unknownModels, m)
	}
	sort.Strings(unknownModels)
	return rows, unknownModels, err
}

// parseFile prices one JSONL file. agentID is "" for the parent transcript.
func parseFile(path, runID, agentID string, skip func(string) bool, unknown map[string]bool) ([]store.Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// bufio.Reader, not bufio.Scanner. A Scanner caps a token at 64KB by default and a real
	// transcript on this machine has lines of 81KB — the Scanner would stop at ErrTooLong and
	// silently drop THE ENTIRE REST OF THE FILE. Undercounting by abandoning the tail is
	// precisely the failure this package exists to prevent, so we accept lines of any length.
	br := bufio.NewReaderSize(f, 256*1024)
	var rows []store.Usage

	for {
		line, err := readLine(br)
		if len(line) > 0 {
			if u, ok := priceRecord(line, runID, agentID, skip, unknown); ok {
				rows = append(rows, u)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A read fault mid-file: keep what we have rather than throwing the run away.
			return rows, nil
		}
	}
	return rows, nil
}

// readLine returns one line with no length ceiling. It returns io.EOF alongside any final
// unterminated line, which a JSONL file being appended to concurrently will genuinely have.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		buf = append(buf, chunk...)
		if err != nil {
			return buf, err
		}
		if !isPrefix {
			return buf, nil
		}
	}
}

// priceRecord decodes and prices one line. ok == false means "not a billed request" — a
// malformed line, a non-assistant record, a synthetic message, or one already counted.
func priceRecord(line []byte, runID, agentID string, skip func(string) bool, unknown map[string]bool) (store.Usage, bool) {
	line = trimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return store.Usage{}, false
	}

	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return store.Usage{}, false // the format is not a public API: skip, never fatal
	}
	if rec.Type != "assistant" || rec.Message.Usage == nil {
		return store.Usage{}, false
	}
	if rec.Message.Model == syntheticModel {
		return store.Usage{}, false
	}

	// The dedup key is requestId. If a future format drops it, fall back to the message id
	// (also unique per API response, and shared by every content block of one response) rather
	// than emitting a row with an empty key: store's PK is (request_id, run_id), so blank keys
	// would collide with each other and INSERT OR IGNORE would silently keep only one.
	key := rec.RequestID
	if key == "" && rec.Message.ID != "" {
		key = "msg:" + rec.Message.ID
	}
	if skip(key) {
		return store.Usage{}, false
	}

	u := rec.Message.Usage

	// Price at the record's own timestamp, not now, so the Sonnet 5 repricing cannot rewrite
	// history under us. A record with no usable timestamp is priced at today's rate — the only
	// honest option left — and stamped with ingest time rather than 1970.
	at := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
		at = t.UTC()
	}

	cw5m, cw1h := cacheSplit(u)

	var webSearches int64
	if u.ServerToolUse != nil {
		webSearches = u.ServerToolUse.WebSearchRequests
	}

	speed := u.Speed
	if speed == "" {
		speed = "standard"
	}

	usd, ok := priceAt(rec.Message.Model, speed,
		u.InputTokens, u.OutputTokens, cw5m, cw1h, u.CacheReadTokens,
		webSearches, u.InferenceGeo == "us", at)
	if !ok {
		// The tokens are still real and still recorded; only the price is unknown. Say so
		// instead of folding a zero into the total as if it were free.
		m := rec.Message.Model
		if m == "" {
			m = "(unset)"
		}
		unknown[m] = true
	}

	return store.Usage{
		RequestID:    key,
		RunID:        runID,
		AgentID:      agentID, // NodeID is left to the caller, which owns the agent->node map
		Model:        rec.Message.Model,
		Speed:        speed,
		TS:           at.Unix(),
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CacheWrite5m: cw5m,
		CacheWrite1h: cw1h,
		CacheRead:    u.CacheReadTokens,
		WebSearches:  webSearches,
		ImputedUSD:   usd,
	}, true
}

// cacheSplit resolves the 5m/1h cache-write buckets, which are priced differently.
//
// The invariant (verified against a real transcript: 495/495 records) is
// cache_creation.5m + cache_creation.1h == cache_creation_input_tokens. When the split is
// absent, or does not account for the whole total because a future TTL bucket was added that
// we do not know about, the remainder is attributed to the 5m bucket. That keeps the TOKEN
// count exact — never silently dropped — while pricing the unknown remainder at the cheaper
// of the two known write rates instead of inventing a rate for a bucket we cannot see.
func cacheSplit(u *usageJSON) (cw5m, cw1h int64) {
	if u.CacheCreation != nil {
		cw5m, cw1h = u.CacheCreation.Ephemeral5m, u.CacheCreation.Ephemeral1h
	}
	if rem := u.CacheCreationTokens - (cw5m + cw1h); rem > 0 {
		cw5m += rem
	}
	return cw5m, cw1h
}

type subagentFile struct {
	path    string
	agentID string
}

// subagentFiles finds the transcripts of every subagent this session spawned.
//
// Layout: <dir>/<sessionId>/subagents/agent-<agentId>.jsonl, beside a .meta.json we do not
// need (the agent id in the FILENAME matches the agentId inside every record — verified across
// 264 records — and the caller maps that id to a node itself).
//
// We walk the whole session directory rather than reading one fixed path so that a nested
// fan-out (spawnDepth > 1) is still counted if the layout ever nests. Anything found twice is
// harmless: the requestId dedup is global across every file in one Parse.
func subagentFiles(transcriptPath string) []subagentFile {
	if transcriptPath == "" {
		return nil
	}
	dir := filepath.Dir(transcriptPath)
	session := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	root := filepath.Join(dir, session)

	var out []subagentFile
	// A session that never fanned out has no such directory at all. That is the common case,
	// not an error.
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it, keep the rest
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		// Only files that actually live under a "subagents" directory. The session directory
		// also holds tool-results/ and workflows/, which are not billed transcripts.
		if filepath.Base(filepath.Dir(p)) != "subagents" {
			return nil
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		if id == "" {
			return nil
		}
		out = append(out, subagentFile{path: p, agentID: id})
		return nil
	})

	// Deterministic order, so re-parsing produces rows in a stable sequence.
	sort.Slice(out, func(i, j int) bool { return out[i].agentID < out[j].agentID })
	return out
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 {
		c := b[len(b)-1]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}
