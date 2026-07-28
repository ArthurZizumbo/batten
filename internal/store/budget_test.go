package store

import (
	"testing"
)

func usage(runID, reqID string, ts int64, in, out, cacheRead int64, usd float64) Usage {
	return Usage{
		RequestID: reqID, RunID: runID, NodeID: "n-a", Model: "opus", TS: ts,
		InputTokens: in, OutputTokens: out, CacheRead: cacheRead, ImputedUSD: usd,
	}
}

// TestUsageIngestionIsIdempotent: transcripts get replayed on resume and a retried request
// appears twice. Without dedup, re-parsing one transcript inflates the ledger every time, and a
// budget that grows when you re-read a file is a budget nobody can act on.
func TestUsageIngestionIsIdempotent(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	rows := []Usage{
		usage(r.RunID, "req-1", r.StartedAt, 1000, 500, 200, 0.25),
		usage(r.RunID, "req-2", r.StartedAt, 300, 100, 0, 0.05),
	}
	added, err := s.RecordUsage(rows)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("first ingestion added %d rows, want 2", added)
	}

	// The same transcript, read again.
	again, err := s.RecordUsage(rows)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Errorf("re-ingesting the same requests added %d rows; the ledger must be idempotent", again)
	}

	got, err := s.Run(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	// 1000+500+200 + 300+100 = 2100. Cache reads count: cheap is not free, and they are real
	// context the model processed.
	if got.TokensSpent != 2100 {
		t.Errorf("tokens_spent = %d, want 2100 (cache reads included)", got.TokensSpent)
	}
	if got.ImputedUSD < 0.29 || got.ImputedUSD > 0.31 {
		t.Errorf("imputed_usd = %v, want ~0.30", got.ImputedUSD)
	}
}

// TestUsageBeforeTheRunIsNotTheRunsFault: the time fence. Tokens spent before a run opened
// belong to the session, not to that run — charging them to the run makes every unit look
// expensive in proportion to how long the session had been running before it started.
func TestUsageBeforeTheRunIsNotTheRunsFault(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")

	added, err := s.RecordUsage([]Usage{
		usage(r.RunID, "old", 1, 9_000_000, 0, 0, 99.0),         // the epoch: long before the run
		usage(r.RunID, "mine", r.StartedAt, 1000, 500, 0, 0.10), // inside the run's lifetime
	})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added %d rows, want 1 — the pre-run row must be fenced out", added)
	}
	got, _ := s.Run(r.RunID)
	if got.TokensSpent != 1500 {
		t.Errorf("tokens_spent = %d, want 1500; pre-run usage leaked into the run", got.TokensSpent)
	}
}

// TestBudgetNeverInventsANumber is principle #1 under test. A ceiling batten cannot measure is
// reported as unavailable — never as zero, which would read as "plenty of room left".
func TestBudgetNeverInventsANumber(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")
	if _, err := s.RecordUsage([]Usage{usage(r.RunID, "req-1", r.StartedAt, 1000, 500, 200, 0.25)}); err != nil {
		t.Fatal(err)
	}

	// quota_pct is only observable through the statusline. None has been installed here, so it
	// must report itself unmeasurable rather than 0%.
	ceilings, err := s.Budget(r.RunID, 1_000_000, 8.0, 15.0)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]Ceiling{}
	for _, c := range ceilings {
		byKind[c.Kind] = c
	}
	if len(byKind) != 3 {
		t.Fatalf("got %d ceilings, want 3 (tokens, imputed_usd, quota_pct)", len(byKind))
	}

	tok := byKind["tokens"]
	if !tok.Available || tok.Spent != 1700 {
		t.Errorf("tokens ceiling = %+v, want available with spent=1700", tok)
	}
	if tok.Exceeded {
		t.Error("1700 does not exceed 1e6")
	}

	q := byKind["quota_pct"]
	if q.Available {
		t.Error("no statusline is installed, so quota_pct CANNOT be measured and must say so")
	}
	if q.Spent != 0 || q.Exceeded {
		// Spent may be zero-valued, but the contract is that Available=false makes it meaningless
		// and it must never be treated as enforced.
		if q.Exceeded {
			t.Error("an unmeasurable ceiling must never report itself exceeded")
		}
	}

	// A ceiling that was not declared is not reported at all.
	only, err := s.Budget(r.RunID, 1_000_000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Kind != "tokens" {
		t.Errorf("with one ceiling declared, Budget reported %+v", only)
	}
}

func TestOverBudgetTripsOnTheMeasurableCeilings(t *testing.T) {
	s := open(t)
	r := run(t, s, "US-1", "sess-a")
	if _, err := s.RecordUsage([]Usage{usage(r.RunID, "req-1", r.StartedAt, 1000, 500, 200, 0.25)}); err != nil {
		t.Fatal(err)
	}

	over, _, err := s.OverBudget(r.RunID, 1_000_000, 8.0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if over {
		t.Error("1700 tokens / $0.25 is not over a 1M / $8 budget")
	}

	over, cs, err := s.OverBudget(r.RunID, 10, 8.0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !over {
		t.Fatal("1700 tokens exceeds a ceiling of 10")
	}
	var named bool
	for _, c := range cs {
		if c.Kind == "tokens" && c.Exceeded {
			named = true
		}
	}
	if !named {
		t.Errorf("OverBudget must say WHICH ceiling broke; got %+v", cs)
	}

	// An unmeasurable ceiling can never be the thing that blocks a commit: batten would be
	// blocking on a number it does not have.
	over, _, err = s.OverBudget(r.RunID, 0, 0, 0.001)
	if err != nil {
		t.Fatal(err)
	}
	if over {
		t.Error("quota_pct is unmeasurable without a statusline; it must not trip the budget")
	}
}

// The quota is account-global, so a run's share is a DELTA between the sample taken when it
// opened and the newest one — not the absolute percentage, which includes everything else the
// account did today.
func TestQuotaIsMeasuredAsADeltaFromTheRunsBaseline(t *testing.T) {
	s := open(t)

	pct := func(f float64) *float64 { return &f }
	if err := s.SaveQuota(QuotaSnapshot{SessionID: "sess-a", TS: 100, FiveHourPct: pct(20)}); err != nil {
		t.Fatal(err)
	}
	// The run opens after the account has already burned 20%.
	r := run(t, s, "US-1", "sess-a")
	if r.QuotaStart5h == nil {
		t.Fatal("a run opened with a quota sample available must anchor its baseline")
	}
	if *r.QuotaStart5h != 20 {
		t.Errorf("baseline = %v, want 20", *r.QuotaStart5h)
	}

	if err := s.SaveQuota(QuotaSnapshot{SessionID: "sess-a", TS: 200, FiveHourPct: pct(35)}); err != nil {
		t.Fatal(err)
	}
	burned, ok, err := s.QuotaBurned(r)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("with a baseline and a newer sample, the burn IS measurable")
	}
	if burned != 15 {
		t.Errorf("burned = %v, want 15 (35 now - 20 at open), not the absolute 35", burned)
	}

	// Without a baseline there is nothing to subtract from, and it must say so.
	noBase := &Run{RunID: "x", SessionID: "sess-a"}
	if _, ok, _ := s.QuotaBurned(noBase); ok {
		t.Error("a run with no baseline cannot report a burn")
	}
}

// TestMeasureRefusesToConcludeFromTooFewRuns: the headroom/code-graph comparison. With fewer
// than three runs on a side, the honest answer is "insufficient data", not a number that will
// be quoted back as fact.
func TestMeasureRefusesToConcludeFromTooFewRuns(t *testing.T) {
	s := open(t)

	mk := func(unit string, graph bool, tokens int64) {
		r, err := s.EnsureRun("p", unit, "sess-a")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetCodeGraph(r.RunID, graph); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordUsage([]Usage{usage(r.RunID, "req-"+unit, r.StartedAt, tokens, 0, 0, 0)}); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseRun(r.RunID, "ok"); err != nil {
			t.Fatal(err)
		}
	}

	mk("U-1", true, 1000)
	mk("U-2", false, 2000)

	groups, err := s.MeasureByCodeGraph("p")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Runs >= 3 {
			t.Fatalf("only two runs were seeded, but a group claims n=%d", g.Runs)
		}
	}

	// Enough runs on both sides and the groups become populated. The caller decides what to
	// print; the store's job is to report n honestly so it CAN decide.
	for i, u := range []string{"U-3", "U-4", "U-5", "U-6"} {
		mk(u, i%2 == 0, int64(1000*(i+1)))
	}
	groups, err = s.MeasureByCodeGraph("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) == 0 {
		t.Fatal("with six closed runs there is something to group")
	}
	var total int
	for _, g := range groups {
		total += g.Runs
	}
	if total != 6 {
		t.Errorf("groups account for %d runs, want 6", total)
	}
}

// TestAnUnmeasuredRunIsNotAZeroSpendRun is principle #1 as a test.
//
// Ceiling.Available was hardcoded true for tokens and dollars, so a run nobody had ingested a
// transcript for reported `0 tokens, $0.00` — presented with a progress bar, beside a ceiling,
// as a measured number. That is not a rounding problem: it is the tool stating a fact it does
// not have. And it was the DEFAULT path, because the flow opens the run, the work happens, and
// nothing ingests anything unless someone remembers to.
func TestAnUnmeasuredRunIsNotAZeroSpendRun(t *testing.T) {
	s := open(t)
	run := run(t, s, "US-1", "sess-1")

	cs, err := s.Budget(run.RunID, 1_000_000, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("want a tokens and an imputed_usd ceiling, got %+v", cs)
	}
	for _, c := range cs {
		if c.Available {
			t.Errorf("%s reports as measured on a run with no usage row at all; "+
				"unmeasured must never render as a number", c.Kind)
		}
		if c.Exceeded {
			t.Errorf("%s cannot be exceeded when it cannot be measured", c.Kind)
		}
		if c.Reason == "" {
			t.Errorf("%s is unavailable with no reason, so no surface can tell the user "+
				"what to do about it", c.Kind)
		}
	}

	// One measured request flips it, and only then is the number real.
	if _, err := s.RecordUsage([]Usage{{
		RequestID: "r1", RunID: run.RunID, Model: "claude-opus-4-5",
		TS: run.StartedAt + 1, InputTokens: 500,
	}}); err != nil {
		t.Fatal(err)
	}
	cs, err = s.Budget(run.RunID, 1_000_000, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if !c.Available {
			t.Errorf("%s is still unmeasurable after a usage row landed: %s", c.Kind, c.Reason)
		}
	}
}

// TestTheFenceReportsWhatItKeptOut. Discarding the session's earlier history is correct — a
// run must not inherit 30 hours it did not spend. Discarding it SILENTLY and then printing a
// total is not: the caller has no way to distinguish "nothing was spent" from "everything was
// thrown away", and those need opposite responses.
func TestTheFenceReportsWhatItKeptOut(t *testing.T) {
	s := open(t)
	run := run(t, s, "US-1", "sess-1")

	added, fenced, err := s.RecordUsageFenced([]Usage{
		{RequestID: "old-1", RunID: run.RunID, Model: "m", TS: run.StartedAt - 600,
			InputTokens: 1000, OutputTokens: 200, ImputedUSD: 0.30},
		{RequestID: "old-2", RunID: run.RunID, Model: "m", TS: run.StartedAt - 300,
			InputTokens: 500, ImputedUSD: 0.10},
		{RequestID: "new-1", RunID: run.RunID, Model: "m", TS: run.StartedAt + 5,
			InputTokens: 42, ImputedUSD: 0.01},
	})
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1 — only the row inside the run's life counts", added)
	}
	if fenced.Requests != 2 {
		t.Errorf("fenced.Requests = %d, want 2", fenced.Requests)
	}
	if fenced.Tokens != 1700 {
		t.Errorf("fenced.Tokens = %d, want 1700 — the caller cannot report what it cannot see",
			fenced.Tokens)
	}
	if fenced.Earliest != run.StartedAt-600 {
		t.Errorf("fenced.Earliest = %d, want the oldest excluded row", fenced.Earliest)
	}
}
