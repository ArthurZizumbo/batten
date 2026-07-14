package usage

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arthu/batten/internal/store"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPrice(t *testing.T) {
	// One request's worth of every bucket, so a mistake in any single multiplier shows up.
	const k = 1000

	cases := []struct {
		name        string
		model       string
		speed       string
		in, out     int64
		cw5m, cw1h  int64
		cacheRead   int64
		webSearches int64
		geoUS       bool
		want        float64
		wantOK      bool
	}{
		{
			// (1000*5 + 1000*5*1.25 + 1000*5*2 + 1000*5*0.1 + 1000*25) / 1e6
			name: "opus standard, every bucket", model: "claude-opus-4-8", speed: "standard",
			in: k, out: k, cw5m: k, cw1h: k, cacheRead: k,
			want: 0.046750, wantOK: true,
		},
		{
			// Fast is Opus-only and doubles both rates: 10 in / 50 out. The cache multipliers
			// stack on top of the FAST input price, so the whole request is exactly 2x.
			name: "opus fast doubles, cache multipliers stack", model: "claude-opus-4-8", speed: "fast",
			in: k, out: k, cw5m: k, cw1h: k, cacheRead: k,
			want: 0.093500, wantOK: true,
		},
		{
			// The premium is Opus-only. A "fast" Haiku must not invent a doubled rate.
			name: "fast on non-opus falls back to standard", model: "claude-haiku-4-5", speed: "fast",
			in: 1_000_000, out: 0,
			want: 1.0, wantOK: true,
		},
		{
			name: "haiku standard rate", model: "claude-haiku-4-5", speed: "standard",
			in: 1_000_000, out: 1_000_000,
			want: 6.0, wantOK: true, // 1 + 5
		},
		{
			// Model ids carry date suffixes; matching is by prefix.
			name: "date-suffixed id still matches", model: "claude-haiku-4-5-20251001", speed: "standard",
			in:   1_000_000,
			want: 1.0, wantOK: true,
		},
		{
			name: "fable is a premium model", model: "claude-fable-5", speed: "standard",
			in: 1_000_000, out: 1_000_000,
			want: 60.0, wantOK: true, // 10 + 50
		},
		{
			name: "sonnet 4-6 is not sonnet 5", model: "claude-sonnet-4-6", speed: "standard",
			in: 1_000_000, out: 1_000_000,
			want: 18.0, wantOK: true, // 3 + 15
		},
		{
			// 1.1x on everything, tokens and server tools alike.
			name: "us geo premium", model: "claude-opus-4-8", speed: "standard",
			in: k, out: k, cw5m: k, cw1h: k, cacheRead: k, geoUS: true,
			want: 0.046750 * 1.1, wantOK: true,
		},
		{
			// $10 per 1,000 searches, on top of the tokens.
			name: "web search is billed per request", model: "claude-opus-4-8", speed: "standard",
			in: 1_000_000, webSearches: 3,
			want: 5.03, wantOK: true,
		},
		{
			// The whole point: an unpriced model is reported as unpriced, not as free.
			name: "unknown model is not free, it is unknown", model: "gpt-5-turbo", speed: "standard",
			in: 1_000_000, out: 1_000_000,
			want: 0, wantOK: false,
		},
		{
			name: "empty model id is unknown", model: "", speed: "standard",
			in:   1_000_000,
			want: 0, wantOK: false,
		},
		{
			name: "zero usage costs zero", model: "claude-opus-4-8", speed: "standard",
			want: 0, wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Price(c.model, c.speed, c.in, c.out, c.cw5m, c.cw1h, c.cacheRead, c.webSearches, c.geoUS)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !approx(got, c.want) {
				t.Fatalf("usd = %.9f, want %.9f", got, c.want)
			}
		})
	}
}

// The 5m and 1h cache-write buckets are priced differently (1.25x vs 2x input). Collapsing
// them into a single "cache write" number is a real and tempting bug, so pin the difference.
func TestPriceCacheWriteBucketsAreNotInterchangeable(t *testing.T) {
	const n = 1_000_000
	w5, ok5 := Price("claude-opus-4-8", "standard", 0, 0, n, 0, 0, 0, false)
	w1, ok1 := Price("claude-opus-4-8", "standard", 0, 0, 0, n, 0, 0, false)
	rd, okR := Price("claude-opus-4-8", "standard", 0, 0, 0, 0, n, 0, false)
	if !ok5 || !ok1 || !okR {
		t.Fatal("opus must be priceable")
	}
	if !approx(w5, 6.25) { // 5 * 1.25
		t.Errorf("5m cache write = %.6f, want 6.25", w5)
	}
	if !approx(w1, 10.0) { // 5 * 2
		t.Errorf("1h cache write = %.6f, want 10.00", w1)
	}
	if !approx(rd, 0.5) { // 5 * 0.1
		t.Errorf("cache read = %.6f, want 0.50", rd)
	}
	if approx(w5, w1) {
		t.Error("5m and 1h cache writes priced identically: the split was collapsed")
	}
}

// Sonnet 5 reprices on 2026-09-01. Pricing keys off the RECORD's timestamp, so a transcript
// written before the change must still price at the old rate after it — otherwise every
// historical run silently reprices itself when the calendar rolls over.
func TestPriceAtSonnet5Repricing(t *testing.T) {
	day := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	cases := []struct {
		when string
		want float64 // 1M in + 1M out
	}{
		{"2026-07-14T00:00:00Z", 12.0}, // 2 + 10
		{"2026-08-31T23:59:59Z", 12.0}, // last moment of the old rate
		{"2026-09-01T00:00:00Z", 18.0}, // 3 + 15
		{"2027-01-01T00:00:00Z", 18.0},
	}
	for _, c := range cases {
		t.Run(c.when, func(t *testing.T) {
			got, ok := priceAt("claude-sonnet-5-20260514", "standard",
				1_000_000, 1_000_000, 0, 0, 0, 0, false, day(c.when))
			if !ok {
				t.Fatal("sonnet-5 must be priceable")
			}
			if !approx(got, c.want) {
				t.Fatalf("usd = %.4f, want %.4f", got, c.want)
			}
		})
	}
}

// ---------- Parse ----------

// assistantLine builds one transcript record. iterations[] is included on purpose: it is a
// per-inner-turn breakdown the top-level fields ALREADY sum, and adding it would double-count.
func assistantLine(reqID, model, ts string, in, out, cw5m, cw1h, read int64) string {
	return fmt.Sprintf(`{"type":"assistant","requestId":%q,"timestamp":%q,`+
		`"message":{"id":"msg_%s","model":%q,"usage":{`+
		`"input_tokens":%d,"output_tokens":%d,`+
		`"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,`+
		`"service_tier":"standard","speed":"standard","inference_geo":"not_available",`+
		`"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},`+
		`"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d},`+
		`"iterations":[{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,`+
		`"cache_creation_input_tokens":%d,"type":"message"}]}}}`,
		reqID, ts, reqID, model, in, out, cw5m+cw1h, read, cw5m, cw1h, in, out, read, cw5m+cw1h)
}

// fixture lays out a session exactly as Claude Code does: <session>.jsonl beside a
// <session>/subagents/ directory.
func fixture(t *testing.T) (transcript string) {
	t.Helper()
	dir := t.TempDir()
	session := "859c1c51-0832-46b6-8681-5d1feec047e2"
	transcript = filepath.Join(dir, session+".jsonl")

	parent := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"go"}}`,
		assistantLine("req_1", "claude-opus-4-8", "2026-07-14T20:51:45.997Z", 10, 20, 100, 0, 1000),
		// The SAME request replayed as a second content block, carrying an identical usage
		// object. Real transcripts do this constantly; counting it twice inflates the run.
		assistantLine("req_1", "claude-opus-4-8", "2026-07-14T20:51:45.997Z", 10, 20, 100, 0, 1000),
		assistantLine("req_2", "claude-fable-5", "2026-07-14T20:52:00.000Z", 5, 7, 0, 200, 0),
		// Never a billed request.
		assistantLine("req_syn", "<synthetic>", "2026-07-14T20:52:01.000Z", 999, 999, 999, 999, 999),
		`{"type":"assistant","requestId":"req_broken"`, // truncated JSON: skip, never fatal
		``,
		`not json at all`,
		assistantLine("req_unk", "claude-zzz-9", "2026-07-14T20:53:00.000Z", 1, 2, 0, 0, 0),
		`{"type":"system","subtype":"info"}`,
	}, "\n") + "\n"

	if err := os.WriteFile(transcript, []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}

	subs := filepath.Join(dir, session, "subagents")
	if err := os.MkdirAll(subs, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two fanned-out subagents. These tokens appear NOWHERE in the parent transcript.
	writeFile(t, filepath.Join(subs, "agent-a14454d0598f4b001.jsonl"),
		assistantLine("req_s1", "claude-opus-4-8", "2026-07-14T20:54:00.000Z", 1, 2, 0, 500, 50)+"\n")
	writeFile(t, filepath.Join(subs, "agent-a14454d0598f4b001.meta.json"),
		`{"agentType":"general-purpose","description":"Research","toolUseId":"toolu_1","spawnDepth":1}`)
	writeFile(t, filepath.Join(subs, "agent-a4f58350b0608d313.jsonl"),
		assistantLine("req_s2", "claude-opus-4-8", "2026-07-14T20:55:00.000Z", 3, 4, 300, 0, 60)+"\n")

	// Sibling directories that are NOT billed transcripts. A greedy walk would count these.
	tr := filepath.Join(dir, session, "tool-results")
	if err := os.MkdirAll(tr, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tr, "agent-decoy.jsonl"),
		assistantLine("req_decoy", "claude-opus-4-8", "2026-07-14T20:56:00.000Z", 7777, 7777, 0, 0, 0)+"\n")

	return transcript
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func byReq(rows []store.Usage) map[string]store.Usage {
	m := map[string]store.Usage{}
	for _, r := range rows {
		m[r.RequestID] = r
	}
	return m
}

// THE test this package exists for: a parser that reads only transcript_path misses every
// subagent, and batten's whole workload is fan-out.
func TestParseCountsSubagentTokens(t *testing.T) {
	rows, unknown, err := Parse(fixture(t), "run-1", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := byReq(rows)
	for _, want := range []string{"req_s1", "req_s2"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("subagent request %s was not counted: rows=%v", want, keys(got))
		}
	}

	s1 := got["req_s1"]
	if s1.AgentID != "a14454d0598f4b001" {
		t.Errorf("subagent row AgentID = %q, want the id from the filename", s1.AgentID)
	}
	if s1.NodeID != "" {
		t.Errorf("NodeID = %q, want empty: the caller owns the agent->node mapping", s1.NodeID)
	}
	if s1.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", s1.RunID)
	}
	// 1 in + 2 out + 500 1h-write + 50 read
	if s1.CacheWrite1h != 500 || s1.CacheRead != 50 || s1.Tokens() != 553 {
		t.Errorf("subagent tokens wrong: %+v", s1)
	}

	// The parent's own rows must still be there, and must carry no agent id.
	if p := got["req_1"]; p.AgentID != "" {
		t.Errorf("parent row AgentID = %q, want empty", p.AgentID)
	}

	// The decoy under tool-results/ is not a billed transcript.
	if _, ok := got["req_decoy"]; ok {
		t.Error("counted a file outside subagents/: tool-results/ is not billed usage")
	}

	if len(unknown) != 1 || unknown[0] != "claude-zzz-9" {
		t.Errorf("unknownModels = %v, want [claude-zzz-9]", unknown)
	}

	// A run whose subagent tokens went uncounted would report this much less.
	var total int64
	for _, r := range rows {
		total += r.Tokens()
	}
	subagentTokens := got["req_s1"].Tokens() + got["req_s2"].Tokens()
	if subagentTokens == 0 || subagentTokens >= total {
		t.Fatalf("subagent tokens = %d of %d total: implausible", subagentTokens, total)
	}
	t.Logf("subagents contributed %d of %d tokens (%.0f%%)",
		subagentTokens, total, 100*float64(subagentTokens)/float64(total))
}

func TestParseDedupsAndSkips(t *testing.T) {
	rows, _, err := Parse(fixture(t), "run-1", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// req_1 is present twice in the file with an identical usage object.
	n := 0
	for _, r := range rows {
		if r.RequestID == "req_1" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("req_1 counted %d times, want 1: replayed lines must be deduped", n)
	}

	got := byReq(rows)
	if _, ok := got["req_syn"]; ok {
		t.Error("counted a <synthetic> record: it was never a billed request")
	}
	if _, ok := got["req_broken"]; ok {
		t.Error("counted a malformed line")
	}

	// iterations[] is already summed into the top-level fields. If it were added, input would
	// read 20 instead of 10.
	if r := got["req_1"]; r.InputTokens != 10 || r.OutputTokens != 20 {
		t.Errorf("req_1 = %d in / %d out, want 10/20: iterations[] must not be added to the top-level totals", r.InputTokens, r.OutputTokens)
	}

	// The 5m/1h split must survive parsing.
	if r := got["req_1"]; r.CacheWrite5m != 100 || r.CacheWrite1h != 0 {
		t.Errorf("req_1 cache split = 5m:%d 1h:%d, want 5m:100 1h:0", r.CacheWrite5m, r.CacheWrite1h)
	}
	if r := got["req_2"]; r.CacheWrite5m != 0 || r.CacheWrite1h != 200 {
		t.Errorf("req_2 cache split = 5m:%d 1h:%d, want 5m:0 1h:200", r.CacheWrite5m, r.CacheWrite1h)
	}

	// An unpriced model still yields its TOKENS; only the price is unknown.
	unk := got["req_unk"]
	if unk.ImputedUSD != 0 {
		t.Errorf("unpriced model got a price: %v", unk.ImputedUSD)
	}
	if unk.InputTokens != 1 || unk.OutputTokens != 2 {
		t.Errorf("unpriced model lost its tokens: %+v", unk)
	}

	// Timestamps come from the record, not from ingest time. Derive the expected epoch rather
	// than hardcoding one: a hand-computed constant is just another invented number.
	want, err := time.Parse(time.RFC3339, "2026-07-14T20:51:45.997Z")
	if err != nil {
		t.Fatal(err)
	}
	if r := got["req_1"]; r.TS != want.Unix() {
		t.Errorf("TS = %d, want the record's own timestamp (%d)", r.TS, want.Unix())
	}
}

func TestParseHonoursSeenAndDoesNotMutateIt(t *testing.T) {
	seen := map[string]bool{"req_2": true, "req_s1": true}
	rows, _, err := Parse(fixture(t), "run-1", seen)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := byReq(rows)
	if _, ok := got["req_2"]; ok {
		t.Error("re-emitted an already-ingested parent request")
	}
	if _, ok := got["req_s1"]; ok {
		t.Error("re-emitted an already-ingested SUBAGENT request")
	}
	if _, ok := got["req_1"]; !ok {
		t.Error("dropped a request that was not in seen")
	}
	// seen belongs to the caller (it comes from store.SeenRequests).
	if len(seen) != 2 {
		t.Errorf("Parse mutated the caller's seen map: %v", seen)
	}
}

// A default bufio.Scanner caps a token at 64KB and stops the scan at ErrTooLong, silently
// dropping the rest of the file. Real transcripts on this machine carry 81KB lines, so this
// pins that a record AFTER an oversized line is still counted.
func TestParseSurvivesOversizedLines(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess.jsonl")

	// A 200KB line, well past the Scanner's default ceiling.
	huge := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 200_000) + `"}}`
	content := strings.Join([]string{
		huge,
		assistantLine("req_after", "claude-opus-4-8", "2026-07-14T21:00:00.000Z", 11, 22, 0, 0, 0),
	}, "\n") + "\n"
	writeFile(t, transcript, content)

	rows, _, err := Parse(transcript, "run-1", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := byReq(rows)["req_after"]; !ok {
		t.Fatal("the record after a 200KB line was dropped: the reader truncated the file")
	}
}

// A session that never fanned out has no subagents directory. That is the common case.
func TestParseWithoutSubagentsDir(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess.jsonl")
	writeFile(t, transcript,
		assistantLine("req_1", "claude-opus-4-8", "2026-07-14T21:00:00.000Z", 1, 2, 0, 0, 0)+"\n")

	rows, unknown, err := Parse(transcript, "run-1", nil)
	if err != nil {
		t.Fatalf("a missing subagents dir is not an error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if len(unknown) != 0 {
		t.Errorf("unknownModels = %v, want none", unknown)
	}
}

// A missing parent transcript is reported, but any subagent rows are still returned: partial
// results beat an error.
func TestParseMissingTranscriptReportsButKeepsSubagents(t *testing.T) {
	rows, _, err := Parse(filepath.Join(t.TempDir(), "nope.jsonl"), "run-1", nil)
	if err == nil {
		t.Error("an unreadable parent transcript should be reported")
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}

	// Now the same, but with subagents present on disk beside the missing parent.
	dir := t.TempDir()
	subs := filepath.Join(dir, "sess", "subagents")
	if err := os.MkdirAll(subs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subs, "agent-abc.jsonl"),
		assistantLine("req_s", "claude-opus-4-8", "2026-07-14T21:00:00.000Z", 1, 2, 0, 0, 0)+"\n")

	rows, _, err = Parse(filepath.Join(dir, "sess.jsonl"), "run-1", nil)
	if err == nil {
		t.Error("the missing parent should still be reported")
	}
	if len(rows) != 1 || rows[0].AgentID != "abc" {
		t.Fatalf("subagent rows were lost when the parent was unreadable: %+v", rows)
	}
}

func TestParseEmptyPath(t *testing.T) {
	rows, _, err := Parse("", "run-1", nil)
	if err == nil {
		t.Error("an empty transcript path should be reported")
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

// A cache_creation object that does not account for the whole cache_creation_input_tokens
// total (a future TTL bucket we do not know about) must not lose the tokens.
func TestCacheSplitNeverDropsTokens(t *testing.T) {
	u := &usageJSON{CacheCreationTokens: 1000}
	u.CacheCreation = nil
	cw5m, cw1h := cacheSplit(u)
	if cw5m+cw1h != 1000 {
		t.Errorf("absent split lost tokens: 5m=%d 1h=%d, want 1000 total", cw5m, cw1h)
	}

	// 300 accounted for by a known bucket, 700 in a bucket we cannot see.
	v := &usageJSON{CacheCreationTokens: 1000}
	v.CacheCreation = &struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	}{Ephemeral1h: 300}
	cw5m, cw1h = cacheSplit(v)
	if cw5m+cw1h != 1000 || cw1h != 300 {
		t.Errorf("unaccounted remainder lost: 5m=%d 1h=%d, want 700/300", cw5m, cw1h)
	}
}

// TestParseRealTranscript runs the parser against an actual Claude Code transcript. The JSONL
// format is not a public API, so a fixture only ever proves we agree with ourselves; this is
// the check that we still agree with reality. Point BATTEN_TEST_TRANSCRIPT at a real
// ~/.claude/projects/<slug>/<sessionId>.jsonl to run it.
func TestParseRealTranscript(t *testing.T) {
	path := os.Getenv("BATTEN_TEST_TRANSCRIPT")
	if path == "" {
		t.Skip("set BATTEN_TEST_TRANSCRIPT to a real transcript to run this")
	}

	rows, unknown, err := Parse(path, "run-real", nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows parsed from a real transcript")
	}

	var parentTok, subTok, parentUSD, subUSD = int64(0), int64(0), 0.0, 0.0
	var nParent, nSub int
	agents := map[string]bool{}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.RequestID] {
			t.Fatalf("Parse emitted requestId %s twice: dedup is broken", r.RequestID)
		}
		seen[r.RequestID] = true

		if r.AgentID == "" {
			nParent++
			parentTok += r.Tokens()
			parentUSD += r.ImputedUSD
			continue
		}
		nSub++
		subTok += r.Tokens()
		subUSD += r.ImputedUSD
		agents[r.AgentID] = true
	}

	t.Logf("parent   : %3d requests %12d tokens  $%8.2f", nParent, parentTok, parentUSD)
	t.Logf("subagents: %3d requests %12d tokens  $%8.2f  (%d agents)", nSub, subTok, subUSD, len(agents))
	t.Logf("TOTAL    : %3d requests %12d tokens  $%8.2f", len(rows), parentTok+subTok, parentUSD+subUSD)
	if len(unknown) > 0 {
		t.Logf("unpriced models: %v", unknown)
	}
	if subTok > 0 {
		t.Logf("a transcript-only parser would have MISSED %.1f%% of this run",
			100*float64(subTok)/float64(parentTok+subTok))
	}
}

func keys(m map[string]store.Usage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
