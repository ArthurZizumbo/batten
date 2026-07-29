package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// The TUI is a review surface: its whole job is to show the operator what actually happened.
// These tests drive it with synthetic messages and assert on the rendered string, so "does it
// display what we think it displays" is a question with an answer rather than something only a
// human squinting at a terminal could tell you.

func fixture(t *testing.T) (*Model, *store.Store, *store.Run) {
	t.Helper()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: US\n  pattern: 'US-\\d{3}'\n" +
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
		"gates:\n  qa:\n    checks: ['go test ./...']\n    verdict: required\n    evidence: required\n" +
		"budget:\n  tokens_per_run: 1000000\n  imputed_usd_per_run: 8.0\n  quota_pct_per_run: 15\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	r, err := st.EnsureRun("p", "US-034", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPhase(r.RunID, "close")
	for _, n := range []store.Node{
		{NodeID: "p-build", RunID: r.RunID, Kind: "phase", Label: "build", Status: "ok"},
		{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "backend", Domain: "backend", Status: "ok"},
		{NodeID: "n-b", RunID: r.RunID, Kind: "subagent", Label: "frontend", Domain: "frontend", Status: "failed"},
	} {
		if err := st.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.AddEdge(r.RunID, "p-build", "n-a", "spawn")
	_ = st.AddEdge(r.RunID, "p-build", "n-b", "spawn")
	_ = st.ClaimWriteSet(r.RunID, "n-a", []string{"backend/api.py"})

	m := New(sp, st)
	m.Init()
	// A real terminal size; without one the model falls back to defaults and the panes differ.
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, st, r
}

// strip removes ANSI escape sequences so assertions are about content, not colour.
func strip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestTheListShowsTheRunAndItsState — the first thing an operator looks at.
func TestTheListShowsTheRunAndItsState(t *testing.T) {
	m, _, _ := fixture(t)
	out := strip(m.render())

	if !strings.Contains(out, "US-034") {
		t.Errorf("the unit is not on screen:\n%s", out)
	}
	if !strings.Contains(out, "close") {
		t.Errorf("the current phase is not shown:\n%s", out)
	}
}

// TestAMissingVerdictIsVisibleBeforeTheCommitIsAttempted.
//
// The close gate will deny a commit without a verdict. If the review surface does not say so,
// the operator finds out at the commit instead of here, which is the whole point of having a
// review surface.
func TestAMissingVerdictIsVisibleBeforeTheCommitIsAttempted(t *testing.T) {
	m, st, r := fixture(t)
	out := strip(m.render())

	low := strings.ToLower(out)
	if !strings.Contains(low, "verdict") {
		t.Errorf("a run with no verdict must say something about the gate:\n%s", out)
	}

	// With an ok verdict carrying evidence, the surface must stop warning.
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"go test ./...: PASS (exit 0)"}, Source: "batten",
	}, true); err != nil {
		t.Fatal(err)
	}
	m.reload()
	out2 := strip(m.render())
	if !strings.Contains(out2, "ok") {
		t.Errorf("the passing verdict is not reflected:\n%s", out2)
	}
	// The evidence is the point of the envelope — it must be readable, not just counted.
	if !strings.Contains(out2, "PASS") {
		t.Errorf("the verdict's evidence is not shown; a count alone cannot be checked:\n%s", out2)
	}
}

// TestTheFanOutAndItsFailureAreVisible: a failed subagent must not look like a healthy one.
func TestTheFanOutAndItsFailureAreVisible(t *testing.T) {
	m, _, _ := fixture(t)
	out := strip(m.render())

	for _, want := range []string{"backend", "frontend"} {
		if !strings.Contains(out, want) {
			t.Errorf("fanned-out agent %q is missing from the detail pane:\n%s", want, out)
		}
	}
	// glyph() is how status reaches the screen once colour is stripped; ok and failed must not
	// collapse to the same character.
	if glyph("ok") == glyph("failed") {
		t.Errorf("ok and failed share the glyph %q — a red run would read as a green one", glyph("ok"))
	}
}

// TestAnUnmeasurableCeilingSaysWhyRatherThanShowingZero is principle #1 on screen.
//
// quota_pct can only be sampled by the statusline. A bar sitting at 0% would read as "plenty of
// room"; the operator has to see that nothing is being measured, AND what to do about it.
func TestAnUnmeasurableCeilingSaysWhyRatherThanShowingZero(t *testing.T) {
	c := store.Ceiling{Kind: "quota_pct", Cap: 15, Available: false}
	line := strip(ceilingLine(c))

	if !strings.Contains(line, "n/a") {
		t.Errorf("an unmeasurable ceiling must read n/a, not a number: %q", line)
	}
	if !strings.Contains(line, "batten statusline") {
		t.Errorf("a bare n/a reads as a bug in batten; the line must name the fix: %q", line)
	}
	if strings.Contains(line, "0.0%") {
		t.Errorf("an unmeasurable ceiling must never render a figure: %q", line)
	}

	// A measurable one does show its numbers (rendered by the shared internal/render form).
	ok := strip(ceilingLine(store.Ceiling{Kind: "tokens", Cap: 1_000_000, Spent: 250_000, Available: true}))
	if !strings.Contains(ok, "250.0k") || !strings.Contains(ok, "1.0M") {
		t.Errorf("a measurable ceiling must show spent and cap: %q", ok)
	}
}

// binding picks the ceiling closest to its cap — the one that will actually stop the run. An
// unmeasurable ceiling can never be it, because batten would be reporting a limit it cannot see.
func TestBindingCeilingIsTheClosestMeasurableOne(t *testing.T) {
	cs := []store.Ceiling{
		{Kind: "tokens", Cap: 1_000_000, Spent: 100_000, Available: true}, // 10%
		{Kind: "imputed_usd", Cap: 8, Spent: 6, Available: true},          // 75%
		{Kind: "quota_pct", Cap: 15, Spent: 14.9, Available: false},       // 99% but UNSEEN
	}
	got, ok := binding(cs)
	if !ok {
		t.Fatal("two ceilings are measurable; one of them binds")
	}
	if got.Kind != "imputed_usd" {
		t.Errorf("binding = %q, want imputed_usd (75%%) — quota is nearer its cap but unmeasurable", got.Kind)
	}

	if _, ok := binding([]store.Ceiling{{Kind: "quota_pct", Cap: 15, Available: false}}); ok {
		t.Error("with nothing measurable, nothing binds")
	}
	if _, ok := binding(nil); ok {
		t.Error("no ceilings declared means nothing binds")
	}
	// A declared-but-zero cap is not a ceiling of zero, it is an absent ceiling.
	if _, ok := binding([]store.Ceiling{{Kind: "tokens", Cap: 0, Spent: 5, Available: true}}); ok {
		t.Error("a cap of zero must not bind; it would read as instantly exceeded")
	}

	if got := firstUnmeasurable(cs); got.Kind != "quota_pct" {
		t.Errorf("firstUnmeasurable = %q, want quota_pct", got.Kind)
	}
	if got := firstUnmeasurable(cs[:2]); got.Kind != "" {
		t.Error("with everything measurable there is nothing to report as unmeasurable")
	}
}

// TestScrollWindowAlwaysContainsTheSelection: the cursor must never leave the screen, at any
// terminal height — including absurd ones.
func TestScrollWindowAlwaysContainsTheSelection(t *testing.T) {
	m, st, _ := fixture(t)
	for i := 2; i <= 30; i++ {
		if _, err := st.EnsureRun("p", "US-0"+string(rune('0'+i%10))+string(rune('0'+i/10)), "s"); err != nil {
			t.Fatal(err)
		}
	}
	m.reload()

	for _, h := range []int{6, 12, 24, 40, 200} {
		m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		for _, sel := range []int{0, 1, len(m.runs) / 2, len(m.runs) - 1} {
			if sel < 0 || sel >= len(m.runs) {
				continue
			}
			m.sel = sel
			lo, hi := m.window()
			if lo < 0 || hi > len(m.runs) || lo > hi {
				t.Fatalf("h=%d sel=%d: window [%d,%d) is out of bounds for %d runs", h, sel, lo, hi, len(m.runs))
			}
			if sel < lo || sel >= hi {
				t.Errorf("h=%d: selection %d is outside the visible window [%d,%d)", h, sel, lo, hi)
			}
		}
	}
}

// TestNavigationAndQuitDoNotPanic drives the real key handling. The TUI has never been opened by
// a second person, so the floor is: it must survive being used.
func TestNavigationAndQuitDoNotPanic(t *testing.T) {
	m, st, _ := fixture(t)
	for i := 0; i < 5; i++ {
		_, _ = st.EnsureRun("p", "US-10"+string(rune('0'+i)), "s")
	}
	m.reload()

	key := func(s string) tea.Msg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }
	for _, k := range []string{"j", "j", "j", "k", "g", "G", "r"} {
		m.Update(key(k))
		if out := m.render(); out == "" {
			t.Fatalf("render came back empty after %q", k)
		}
	}

	// Down past the end and up past the start must clamp rather than index out of range.
	for i := 0; i < 50; i++ {
		m.Update(key("j"))
	}
	if m.sel >= len(m.runs) {
		t.Errorf("selection ran past the end: sel=%d of %d", m.sel, len(m.runs))
	}
	for i := 0; i < 50; i++ {
		m.Update(key("k"))
	}
	if m.sel < 0 {
		t.Errorf("selection ran before the start: sel=%d", m.sel)
	}
}

// TestAnEmptyProjectRendersSomethingUseful: first run, nothing recorded. It must not render a
// blank screen that reads as a crash.
func TestAnEmptyProjectRendersSomethingUseful(t *testing.T) {
	dir := t.TempDir()
	y := "version: 1\nproject: empty\nunit:\n  name: US\n  pattern: 'US-\\d{3}'\nphases:\n  - id: build\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, _ := spec.LoadFrom(dir)
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := New(sp, st)
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := strip(m.render())
	if strings.TrimSpace(out) == "" {
		t.Fatal("an empty project rendered a blank screen, which reads as a crash")
	}
	if _, hi := m.window(); hi != 0 {
		t.Errorf("no runs, but the window claims %d rows", hi)
	}
}

// TestHeaderCountsItsRunsGrammatically is finding #48: the title bar read '1 runs' while the
// same package already shipped the plural() helper that knows better.
func TestHeaderCountsItsRunsGrammatically(t *testing.T) {
	if got := headerLine("p", 1); !strings.Contains(got, "1 run") || strings.Contains(got, "1 runs") {
		t.Errorf("headerLine(p, 1) = %q, want '1 run'", got)
	}
	if got := headerLine("p", 2); !strings.Contains(got, "2 runs") {
		t.Errorf("headerLine(p, 2) = %q, want '2 runs'", got)
	}
}

func TestFormattingHelpers(t *testing.T) {
	// Token rendering moved to internal/render — this package's private copy had drifted to
	// its own precision rules, which is exactly how one surface came to disagree with the
	// next about the same number (#36). render_test owns the canonical cases now.
	// 12 characters, git's unambiguous-enough length — not the 7 a commit line shows.
	if got := shortSHA("abc1234def5678901"); got != "abc1234def56" {
		t.Errorf("shortSHA = %q, want abc1234def56", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("a short sha must survive unchanged, got %q", got)
	}
	if plural(1, "file") == plural(2, "file") {
		t.Error("plural must distinguish one from many")
	}
	// The bar must never exceed its width, whatever fraction it is handed — an over-budget run
	// passes a fraction above 1 and a wrapped bar would break the pane.
	for _, f := range []float64{-1, 0, 0.5, 1, 3} {
		if n := len([]rune(strip(bar(f, 12)))); n != 12 {
			t.Errorf("bar(%v, 12) rendered %d cells, want exactly 12", f, n)
		}
	}
	// wrapIndent must not lose content.
	long := strings.Repeat("word ", 40)
	w := wrapIndent(long, 30, "  ")
	if !strings.Contains(strings.ReplaceAll(w, "\n", " "), "word") {
		t.Error("wrapIndent dropped the text")
	}
}

// TestTheTUIShowsBothVerdictsAndNamesTheMissingHalf.
//
// The gate needs two verdicts from two producers: batten's, proving the declared checks were
// RUN, and a reviewer's, judging the work against its acceptance criteria. The TUI loaded only
// the newest row, so `batten check` — which always writes last — hid the reviewer's evidence
// behind its own check output, and a screen showing one green verdict read as "approved" when
// only half the rule was satisfied.
func TestTheTUIShowsBothVerdictsAndNamesTheMissingHalf(t *testing.T) {
	m, st, r := fixture(t)

	// Only the reviewer has spoken: the mechanical half must be called out as missing.
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"AC-1 covered by TestExport"}, Source: "agent",
	}, true); err != nil {
		t.Fatal(err)
	}
	m.reload()
	out := strip(m.render())
	if !strings.Contains(out, "AC-1") {
		t.Errorf("the reviewer's evidence is not shown:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "no batten-verified pass") {
		t.Errorf("with no batten verdict the screen must say the checks were never run:\n%s", out)
	}

	// Now batten's own check lands. It must ADD to the screen, not replace what was there.
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa-batten", Result: "ok",
		Evidence: []string{"go test ./...: PASS (exit 0)"}, Source: "batten",
	}, true); err != nil {
		t.Fatal(err)
	}
	m.reload()
	out = strip(m.render())
	for _, want := range []string{"AC-1", "PASS"} {
		if !strings.Contains(out, want) {
			t.Errorf("after `batten check` the screen lost %q — one verdict is hiding the other:\n%s",
				want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "no batten-verified pass") {
		t.Errorf("the missing-half warning survived the verdict that satisfies it:\n%s", out)
	}
}
