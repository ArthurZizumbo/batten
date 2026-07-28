package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// contentOf pulls the model-facing text out of a tool result. An empty result means the handler
// left Content nil, which is exactly the bug: the SDK then fills it with the whole JSON.
func contentOf(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if res == nil {
		t.Fatal("the handler returned a nil CallToolResult, so the SDK fills `content` with the " +
			"entire serialized output — the model reads a struct where it wanted a sentence")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		t.Fatal("the result carries no text content at all: the model would get nothing")
	}
	return b.String()
}

// TestTheModelGetsProseAndTheClientGetsTheJSON is §4.4.
//
// An MCP result has two halves for two readers: `content` reaches the MODEL, `structuredContent`
// is for the CLIENT to render. batten returned a nil result from every handler, and the go-sdk
// fills what a handler leaves empty — so the full serialized struct went into BOTH, byte for
// byte identical, and the half that costs model tokens was carrying RFC3339 timestamps, arrays
// of nulls and schema-shaped keys to a reader that needed a sentence.
//
// This pins both halves: the summary must be much smaller than the JSON, and the JSON must still
// be there in full for whoever wants to draw it.
func TestTheModelGetsProseAndTheClientGetsTheJSON(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	cases := []struct {
		name string
		call func() (*sdk.CallToolResult, any)
	}{
		{"batten_runs", func() (*sdk.CallToolResult, any) {
			r, o, _ := q.runs(ctx(), nil, runsInput{})
			return r, o
		}},
		{"batten_verdict_status", func() (*sdk.CallToolResult, any) {
			r, o, _ := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
			return r, o
		}},
		{"batten_budget", func() (*sdk.CallToolResult, any) {
			r, o, _ := q.budget(ctx(), nil, budgetInput{Unit: "US-001"})
			return r, o
		}},
		{"batten_spec", func() (*sdk.CallToolResult, any) {
			r, o, _ := q.spec(ctx(), nil, specInput{})
			return r, o
		}},
		{"batten_spec(phase)", func() (*sdk.CallToolResult, any) {
			r, o, _ := q.spec(ctx(), nil, specInput{Phase: "implement"})
			return r, o
		}},
		{"batten_writeset_owner", func() (*sdk.CallToolResult, any) {
			r, o, _ := q.writeSetOwner(ctx(), nil, writeSetInput{Path: "internal/api/handler.go"})
			return r, o
		}},
	}

	for _, c := range cases {
		res, out := c.call()
		content := contentOf(t, res)
		full, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}

		// The structured half must still carry everything: this is a redistribution, not a
		// truncation. Nothing a client could render before has been taken away.
		if len(full) < 100 {
			t.Errorf("%s: the structured output collapsed to %d bytes — the rich half is what "+
				"pays for the compact one", c.name, len(full))
		}

		ratio := float64(len(content)) / float64(len(full))
		t.Logf("%-24s content %4d chars vs structured %5d bytes — %.0f%% smaller",
			c.name, len(content), len(full), 100*(1-ratio))

		if ratio > 0.6 {
			t.Errorf("%s: the model-facing summary is %d chars against %d bytes of JSON (%.0f%%). "+
				"That is not a summary — check the handler is not falling back to the SDK's "+
				"serialize-everything default", c.name, len(content), len(full), 100*ratio)
		}
		// A summary that is prose, not a serialized struct. If it opens with a brace the
		// handler is handing the model JSON by another route.
		if strings.HasPrefix(strings.TrimSpace(content), "{") {
			t.Errorf("%s: the model-facing content is JSON:\n%s", c.name, content)
		}
	}
}

// The summary is worthless if it drops the fact the tool exists to deliver. batten_verdict_status
// answers exactly one question — would `git commit` be denied right now — and the answer plus its
// remedy must survive the compaction.
func TestTheVerdictSummaryKeepsTheAnswerAndTheRemedy(t *testing.T) {
	q := fixture(t)
	seed(t, q)

	res, out, err := q.verdictStatus(ctx(), nil, verdictInput{Unit: "US-001"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.CommitDenied {
		t.Fatalf("control failed: the seeded run has no verdict, so a commit must be denied — " +
			"the assertions below would prove nothing")
	}
	content := contentOf(t, res)

	for _, want := range []string{"US-001", "DENIED"} {
		if !strings.Contains(content, want) {
			t.Errorf("the summary lost %q, which is the answer itself:\n%s", want, content)
		}
	}
	// The remedy is the difference between a status tool and a useful one.
	if !strings.Contains(content, "Fix:") {
		t.Errorf("the summary states the denial without the concrete next step:\n%s", content)
	}
}

// §4.5: batten_spec answers for ONE phase. The spec was always available; nothing about it ever
// changed with the phase, so an agent about to fan out and an agent about to face a gate got the
// same document and had to orient themselves.
func TestSpecNarrowsToOnePhase(t *testing.T) {
	q := fixture(t)

	_, whole, err := q.spec(ctx(), nil, specInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Phases) < 3 {
		t.Fatalf("control failed: the fixture must declare several phases, got %d", len(whole.Phases))
	}

	res, out, err := q.spec(ctx(), nil, specInput{Phase: "implement"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ScopedToPhase != "implement" {
		t.Errorf("scoped_to_phase = %q, want implement", out.ScopedToPhase)
	}
	if len(out.Phases) != 1 || out.Phases[0].ID != "implement" {
		t.Fatalf("expected only the implement phase, got %+v", out.Phases)
	}
	// implement fans out, so its domains and their invariants are exactly what its agents need.
	if len(out.Domains) == 0 {
		t.Error("a fan-out phase must keep its domains: their invariants ride into every agent's prompt")
	}
	// It declares no gate, so no gate belongs in the answer.
	if len(out.Gates) != 0 {
		t.Errorf("implement declares no gate, so none should come back: %+v", out.Gates)
	}

	content := contentOf(t, res)
	if !strings.Contains(content, "never widen a public signature") {
		t.Errorf("the invariant a distracted agent would break is missing from the summary:\n%s", content)
	}

	// The gate phase gets its gate, and does NOT get the fan-out's domains.
	_, gated, err := q.spec(ctx(), nil, specInput{Phase: "verify"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gated.Gates) != 1 || gated.Gates[0].Name != "close" {
		t.Errorf("the verify phase must come back with its gate, got %+v", gated.Gates)
	}
	if len(gated.Domains) != 0 {
		t.Errorf("verify does not fan out, so domain invariants have no agent to ride into: %+v", gated.Domains)
	}
}

// A phase the spec does not declare must say so. Silently returning the whole document would let
// a typo look like a successful narrowing.
func TestAnUnknownPhaseIsReportedRatherThanIgnored(t *testing.T) {
	q := fixture(t)

	_, out, err := q.spec(ctx(), nil, specInput{Phase: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ScopedToPhase != "" {
		t.Errorf("nothing was narrowed, so scoped_to_phase must stay empty; got %q", out.ScopedToPhase)
	}
	if !strings.Contains(out.Note, "nonexistent") {
		t.Errorf("the note must name the phase that does not exist: %q", out.Note)
	}
	if len(out.Phases) < 3 {
		t.Errorf("an unrecognised phase should fall back to the whole spec, not to an empty one")
	}
}

// Principle #1 reaches the summaries too. A run nobody ingested a transcript for has NOT spent
// zero tokens, and the compact rendering is exactly where a "0" would be easiest to slip in.
func TestTheSummaryNeverInventsAZero(t *testing.T) {
	q := fixture(t)
	// A run nobody has ingested a transcript for — NOT the seeded one, which has usage rows.
	if _, err := q.st.EnsureRun("batten", "US-777", "sess-none"); err != nil {
		t.Fatal(err)
	}

	res, out, err := q.runs(ctx(), nil, runsInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 1 || out.Runs[0].Tokens != 0 {
		t.Fatalf("control failed: this run must have no measured usage, got %+v", out.Runs)
	}
	content := contentOf(t, res)
	if !strings.Contains(content, "NOT MEASURED") {
		t.Errorf("the seeded run has no ingested usage, so the summary must say so rather than "+
			"print a zero it never measured:\n%s", content)
	}
	if strings.Contains(content, "0 tokens") {
		t.Errorf("the summary reports 0 tokens for an unmeasured run:\n%s", content)
	}
}
