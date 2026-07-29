package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// TestDBPathIsAlwaysUnderHome pins the decision that cost two bugs in E0.
//
// State must NOT live under ${CLAUDE_PLUGIN_DATA}: hook processes have that variable set and the
// user's terminal does not, so an env-dependent path splits the state into two databases — the
// TUI reports "no runs" while the hooks happily write runs somewhere else. ${CLAUDE_PLUGIN_ROOT}
// is forbidden for a different reason: it is wiped on every plugin update.
func TestDBPathIsAlwaysUnderHome(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", filepath.Join(t.TempDir(), "plugin-data"))
	t.Setenv("CLAUDE_PLUGIN_ROOT", filepath.Join(t.TempDir(), "plugin-root"))
	t.Setenv("BATTEN_DB", "")

	got := dbPath()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	want := filepath.Join(home, ".batten", "batten.db")
	if got != want {
		t.Errorf("dbPath() = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"plugin-data", "plugin-root"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("dbPath() leaked %s into the path: %q", forbidden, got)
		}
	}

	// BATTEN_DB is the one supported override — tests and CI depend on it.
	custom := filepath.Join(t.TempDir(), "custom.db")
	t.Setenv("BATTEN_DB", custom)
	if got := dbPath(); got != custom {
		t.Errorf("BATTEN_DB override ignored: got %q, want %q", got, custom)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{1_400_000, "1.4M"},
	}
	for _, c := range cases {
		if got := humanTokens(c.in); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMeasureNeverPricesTheUnpriceableAsFree is finding #32's render half. A model with no
// published rate showed as `$0.00` under "spend by model" — byte-identical to a measured row
// that genuinely rounds to zero. The two facts are opposites and must not share a rendering.
func TestMeasureNeverPricesTheUnpriceableAsFree(t *testing.T) {
	unpriced := store.ModelSpend{Model: "claude-opus-4-9", Requests: 2, Tokens: 57_900, UnpricedRequests: 2}
	if got := measurePrice(unpriced); strings.Contains(got, "$0.00") || !strings.Contains(got, "UNPRICED") {
		t.Errorf("fully unpriced row rendered %q; must say UNPRICED and never $0.00", got)
	}

	// A partially unpriced row is a floor, not a total. A skeptical reader doing the
	// arithmetic must not be able to mistake it for a complete figure.
	mixed := store.ModelSpend{Model: "m", Requests: 3, ImputedUSD: 0.39, UnpricedRequests: 1}
	if got := measurePrice(mixed); !strings.HasPrefix(got, "≥") || !strings.Contains(got, "unpriced") {
		t.Errorf("partially unpriced row rendered %q; must read as a floor (≥) and name the gap", got)
	}

	// And the control: measured-and-tiny genuinely is $0.00. The fix must not take that away.
	priced := store.ModelSpend{Model: "haiku", Requests: 1, ImputedUSD: 0.0000008}
	if got := measurePrice(priced); got != "$0.00" {
		t.Errorf("measured row rendered %q, want $0.00 — a real measurement that rounds down", got)
	}
}

const partialPricingSpec = "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
	"phases:\n  - id: build\n    anchor: git_sha\n" +
	"budget:\n  tokens_per_run: 3000000\n  imputed_usd_per_run: 2.0\n  on_exceed: warn\n"

const lifecycleSpec = "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
	"phases:\n  - id: build\n    anchor: git_sha\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
	"gates:\n  qa:\n    checks: ['git --version']\n    verdict: required\n    evidence: required\n"

// TestCheckAndVerdictRefuseToForkAClosedUnit is finding #12. `batten check` on a closed unit
// silently INSERTed a second run — phase NULL, anchor NULL, status running — and exit 0 made
// it look right; `batten show` then displayed only the empty fork. Opening a run is `batten
// phase`'s job alone: the other verbs operate on the run that is open, or say there is none.
func TestCheckAndVerdictRefuseToForkAClosedUnit(t *testing.T) {
	dir := gitRepoWithSpec(t, lifecycleSpec)
	db := filepath.Join(dir, "state.db")
	t.Setenv("BATTEN_DB", db)

	inDir(t, dir, func() {
		_ = captureStdout(t, func() { _ = cmdPhase([]string{"TASK-1", "build"}) })

		st, err := store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		r, err := st.ActiveRun("p", "TASK-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CloseRun(r.RunID, "ok"); err != nil {
			t.Fatal(err)
		}
		st.Close()

		var checkErr error
		_ = captureStdout(t, func() { checkErr = cmdCheck([]string{"TASK-1"}) })
		if checkErr == nil || !strings.Contains(checkErr.Error(), "no open run") {
			t.Errorf("check on a closed unit must refuse and say why; got err=%v", checkErr)
		}

		vf := filepath.Join(dir, "v.json")
		if err := os.WriteFile(vf, []byte(`{"check_id":"qa","result":"ok","evidence":["x"],"why":"y"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		var verdictErr error
		_ = captureStdout(t, func() { verdictErr = cmdVerdict([]string{"--unit", "TASK-1", "--file", vf}) })
		if verdictErr == nil || !strings.Contains(verdictErr.Error(), "no open run") {
			t.Errorf("verdict on a closed unit must refuse and say why; got err=%v", verdictErr)
		}

		// The real damage was the silent fork: exactly one run may exist afterwards.
		st, err = store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		runs, err := st.ListRuns("p", 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != 1 {
			t.Errorf("%d runs exist for one unit — a read verb forked a new one", len(runs))
		}
	})
}

// TestAUnitIdThatDoesNotMatchThePatternIsRejected is finding #21. `batten phase FOO-9 build`
// opened a run with exit 0 while the same command hard-rejects a phase that does not exist —
// and there is no way to delete the phantom afterwards. The pattern is anchored whole-string:
// with `US-\d{3}`, US-0001 must not slide through as its US-000 prefix.
func TestAUnitIdThatDoesNotMatchThePatternIsRejected(t *testing.T) {
	spec3 := "version: 1\nproject: p\nunit:\n  name: US\n  pattern: 'US-\\d{3}'\n" +
		"phases:\n  - id: build\n    anchor: git_sha\n"
	dir := gitRepoWithSpec(t, spec3)
	db := filepath.Join(dir, "state.db")
	t.Setenv("BATTEN_DB", db)

	inDir(t, dir, func() {
		for _, bad := range []string{"FOO-9", "US-0001", "us-001", "US-1"} {
			var err error
			_ = captureStdout(t, func() { err = cmdPhase([]string{bad, "build"}) })
			if err == nil || !strings.Contains(err.Error(), "unit.pattern") {
				t.Errorf("cmdPhase(%q) = %v, want a refusal naming unit.pattern", bad, err)
			}
		}
		var err error
		_ = captureStdout(t, func() { err = cmdPhase([]string{"US-001", "build"}) })
		if err != nil {
			t.Fatalf("the control must pass: a valid id opens its run; got %v", err)
		}

		st, e := store.Open(db)
		if e != nil {
			t.Fatal(e)
		}
		defer st.Close()
		runs, e := st.ListRuns("p", 50)
		if e != nil {
			t.Fatal(e)
		}
		if len(runs) != 1 || runs[0].UnitID != "US-001" {
			t.Errorf("runs = %v, want exactly US-001 — a rejected id must leave no phantom behind", runs)
		}
	})
}

// TestShowRunFlagSelectsTheExactRun is finding #23: `batten show <unit> --run <id>` discarded
// the flag and its value, printed the unit's LATEST run and exited 0 — even for an id that
// does not exist. A flag that is accepted and ignored shows the user a run they did not ask
// for and tells them it succeeded.
func TestShowRunFlagSelectsTheExactRun(t *testing.T) {
	dir := gitRepoWithSpec(t, diffFromSpec)
	db := filepath.Join(dir, "state.db")
	t.Setenv("BATTEN_DB", db)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CloseRun(first.RunID, "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureRun("p", "TASK-1", "sess-a"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	inDir(t, dir, func() {
		out := captureStdout(t, func() {
			if err := cmdShow([]string{"TASK-1", "--run", first.RunID}); err != nil {
				t.Errorf("show --run with a real id: %v", err)
			}
		})
		if !strings.Contains(out, first.RunID) {
			t.Errorf("show --run %s printed some other run:\n%s", first.RunID, out)
		}

		var bogusErr error
		_ = captureStdout(t, func() { bogusErr = cmdShow([]string{"TASK-1", "--run", "no-such-run"}) })
		if bogusErr == nil {
			t.Error("an id that does not exist must be an error, not the latest run with exit 0")
		}

		var flagErr error
		_ = captureStdout(t, func() { flagErr = cmdShow([]string{"TASK-1", "--totally-bogus"}) })
		if flagErr == nil {
			t.Error("an unknown flag must fail loudly, not vanish")
		}
	})
}

// TestAnOverrideIsVisibleOnEveryReadingSurface is finding #10. After `batten override`, the
// hook allows the commit — it checks the override before anything else — while `batten show`
// kept printing "the close gate will deny a commit" (the literal opposite of the truth),
// `batten runs` showed no trace, and the canvas contained no 'override' string. The statusline
// and MCP read the same store and say it correctly; the CLI surfaces were simply not asking.
func TestAnOverrideIsVisibleOnEveryReadingSurface(t *testing.T) {
	dir := gitRepoWithSpec(t, diffFromSpec)
	db := filepath.Join(dir, "state.db")
	t.Setenv("BATTEN_DB", db)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.EnsureRun("p", "TASK-1", "sess-x")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Override(r.RunID, "*", "hotfix for the Friday demo"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	inDir(t, dir, func() {
		show := captureStdout(t, func() { _ = cmdShow([]string{"TASK-1"}) })
		if strings.Contains(show, "will deny a commit") {
			t.Errorf("show promises a denial the hook will not deliver:\n%s", show)
		}
		if !strings.Contains(show, "overridden") || !strings.Contains(show, "Friday demo") {
			t.Errorf("show must name the override and its reason:\n%s", show)
		}

		runsOut := captureStdout(t, func() { _ = cmdRuns() })
		if !strings.Contains(runsOut, "overridden") {
			t.Errorf("runs shows no trace of the override:\n%s", runsOut)
		}

		cv := filepath.Join(dir, "t.canvas")
		_ = captureStdout(t, func() { _ = cmdCanvas([]string{"TASK-1", "--out", cv}) })
		raw, err := os.ReadFile(cv)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "override") {
			t.Errorf("the canvas draws the gate with no trace of the override that opened it")
		}
	})
}

// TestPartiallyPricedSpendReadsAsAFloorEverywhere is finding #33 end to end. With 38% of a
// run's tokens on a model with no published rate, `batten budget`, `batten runs` and
// `batten show` all presented $0.39 as a completed measurement. The dollars for that share
// do not exist: the figure is a floor, and a skeptical reader doing the arithmetic must
// reach that conclusion from the output alone.
func TestPartiallyPricedSpendReadsAsAFloorEverywhere(t *testing.T) {
	dir := gitRepoWithSpec(t, partialPricingSpec)
	db := filepath.Join(dir, "state.db")
	t.Setenv("BATTEN_DB", db)

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.EnsureRun("p", "TASK-1", "sess-x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordUsage([]store.Usage{
		{RequestID: "req-1", RunID: r.RunID, Model: "priced", TS: r.StartedAt,
			InputTokens: 100_000, OutputTokens: 30_000, ImputedUSD: 0.39},
		{RequestID: "req-2", RunID: r.RunID, Model: "unpriced-future-model", TS: r.StartedAt,
			InputTokens: 70_000, OutputTokens: 10_000},
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	inDir(t, dir, func() {
		budget := captureStdout(t, func() { _ = cmdBudget(nil) })
		for _, want := range []string{"≥$0.39", "floor", "38%"} {
			if !strings.Contains(budget, want) {
				t.Errorf("budget: %q missing — the partial figure reads as a total:\n%s", want, budget)
			}
		}
		runs := captureStdout(t, func() { _ = cmdRuns() })
		if !strings.Contains(runs, "≥$0.39") || !strings.Contains(runs, "floor") {
			t.Errorf("runs must mark the figure as a floor and explain the mark:\n%s", runs)
		}
		show := captureStdout(t, func() { _ = cmdShow([]string{"TASK-1"}) })
		if !strings.Contains(show, "floor, not a total") {
			t.Errorf("show presents the partial figure as a measurement:\n%s", show)
		}
	})
}

// modelMatches decides whether `batten show` flags a routing deviation. A false alarm here
// trains people to ignore the column, so aliases must resolve: a spec saying "opus" is satisfied
// by a run that actually used "claude-opus-4-20250514".
func TestModelMatchesToleratesAliases(t *testing.T) {
	cases := []struct {
		declared string
		ran      []string
		want     bool
		why      string
	}{
		// The caller only reaches this with a declared model AND at least one recorded run
		// (main.go: `if ran := models[n.NodeID]; len(ran) > 0` guards it, and d.Model != ""),
		// so those two empty cases are unreachable and are not asserted here.
		{"opus", []string{"claude-opus-4-20250514"}, true, "an alias must match the full model id"},
		{"sonnet", []string{"claude-sonnet-4-5"}, true, "same, for sonnet"},
		{"opus", []string{"claude-haiku-4-5"}, false, "a genuinely different model is a deviation"},
		{"opus", []string{"claude-haiku-4-5", "claude-opus-4"}, true, "one matching run is enough"},
		{"haiku", []string{"claude-haiku-4-5"}, true, "the cheapest tier resolves like the others"},
	}
	for _, c := range cases {
		if got := modelMatches(c.declared, c.ran); got != c.want {
			t.Errorf("modelMatches(%q, %v) = %v, want %v — %s", c.declared, c.ran, got, c.want, c.why)
		}
	}
}

func TestFirstLineOfAndLastLines(t *testing.T) {
	// Multi-line evidence is shown as its first line plus an ellipsis. The ellipsis is the
	// point: a reader must be able to tell a one-line piece of evidence from a truncated one,
	// or a citation that continues "…but 3 tests failed" reads as an unqualified pass.
	if got := firstLineOf("one\ntwo\nthree"); got != "one …" {
		t.Errorf("firstLineOf = %q, want %q", got, "one …")
	}
	if got := firstLineOf("single line"); got != "single line" {
		t.Errorf("a single line must not gain an ellipsis; got %q", got)
	}
	if got := firstLineOf(""); got != "" {
		t.Errorf("firstLineOf(empty) = %q", got)
	}

	// lastLines is what a failed check cites as evidence. Losing the tail would cite the wrong
	// part of the output — the failure is at the end, not the beginning.
	got := lastLines("a\nb\nc\nd\ne", 2)
	if !strings.Contains(got, "e") || !strings.Contains(got, "d") {
		t.Errorf("lastLines kept %q, want the final two lines", got)
	}
	if strings.Contains(got, "a") {
		t.Errorf("lastLines returned the head instead of the tail: %q", got)
	}
	if got := lastLines("only", 5); got != "only" {
		t.Errorf("asking for more lines than exist must return them all; got %q", got)
	}
}

func TestBarNeverOverflowsItsTrack(t *testing.T) {
	for _, frac := range []float64{-1, 0, 0.5, 1, 2} {
		b := bar(frac)
		if b == "" {
			t.Errorf("bar(%v) is empty", frac)
		}
		// An over-budget run passes frac > 1. The bar must clamp rather than print a line that
		// wraps the terminal.
		if len([]rune(b)) > 40 {
			t.Errorf("bar(%v) is %d runes wide; it must clamp", frac, len([]rune(b)))
		}
	}
}

func TestAbs(t *testing.T) {
	if abs(-2.5) != 2.5 || abs(2.5) != 2.5 || abs(0) != 0 {
		t.Error("abs is wrong")
	}
}

// runCheck is how `batten check` turns a declared command into evidence. Its contract: report the
// real exit code, capture output, and never hang forever.
func TestRunCheckReportsTheRealExitCode(t *testing.T) {
	dir := t.TempDir()

	out, code, took := runCheck(dir, "exit 0", 10*time.Second)
	if code != 0 {
		t.Errorf("a passing command reported exit %d (output %q)", code, out)
	}
	if took == "" {
		t.Error("runCheck must report how long it took; that is part of the evidence")
	}

	// A failing command must NOT be reported as passing — this is the whole point of batten
	// check: evidence generated, not asserted.
	if _, code, _ := runCheck(dir, "exit 3", 10*time.Second); code == 0 {
		t.Error("a failing command was reported as exit 0")
	}

	// A command that does not exist is a failure, not a pass.
	if _, code, _ := runCheck(dir, "this-command-does-not-exist-anywhere", 10*time.Second); code == 0 {
		t.Error("a missing command must not report success")
	}
}

func TestExpandHome(t *testing.T) {
	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Errorf("an absolute path must survive unchanged; got %q", got)
	}
	if got := expandHome(""); got != "" {
		t.Errorf("empty stays empty; got %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := expandHome("~/vault"); !strings.HasPrefix(got, home) || strings.Contains(got, "~") {
		t.Errorf("~ did not expand: %q", got)
	}
}

// runHook drives the real hook entry point end to end — stdin in, stdout captured — because the
// thing under test IS the entry point's silence, and a test that called Dispatch directly would
// step over every path that produces it.
func runHook(t *testing.T, event, payload string) string {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()

	go func() { _, _ = inW.WriteString(payload); inW.Close() }()

	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(outR); done <- string(b) }()

	if err := cmdHook([]string{event}); err != nil {
		t.Fatalf("cmdHook returned an error, which would surface as a broken hook: %v", err)
	}
	outW.Close()
	return <-done
}

// TestABrokenStoreSaysSoInsteadOfPassingInSilence is §4.3: fail open, but never quietly.
//
// The hook entry point funnelled every failure into `return nil` — no spec, unreadable store,
// dispatch error, recovered panic. Exit 0 with no output is also what an ALLOW looks like, so a
// batten whose gate was completely down was indistinguishable from a batten that had just
// approved the commit. The user concludes the gate is working. It is not.
//
// It stays fail-OPEN on purpose. The likeliest cause is a busy SQLite file, and denying every
// tool call while another process holds the write lock would brick the session.
func TestABrokenStoreSaysSoInsteadOfPassingInSilence(t *testing.T) {
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: verify\n    gate: qa\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory where the database file should be: store.Open cannot open it, and it fails the
	// same way a locked or corrupt database does — before a Handler exists to report anything.
	broken := filepath.Join(t.TempDir(), "batten.db")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BATTEN_DB", broken)

	payload := `{"session_id":"s","cwd":` + jsonString(dir) +
		`,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m \"x\""}}`

	out := runHook(t, "PreToolUse", payload)
	if strings.TrimSpace(out) == "" {
		t.Fatal("the store could not be opened, so the commit gate did not run at all — and the " +
			"hook said nothing, which the user reads as the gate approving")
	}
	for _, want := range []string{"did NOT run", "cause", "batten doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning must say batten did not run, why, and what to do. Missing %q from:\n%s", want, out)
		}
	}
	// Fail OPEN: a warning, never a denial. Denying on a busy database would deny every tool call.
	if strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("a degraded batten must not deny — SQLITE_BUSY would then brick the session:\n%s", out)
	}
	// And it must reach the human, not only the model's context.
	if !strings.Contains(out, "systemMessage") {
		t.Errorf("the warning never reaches the user's screen:\n%s", out)
	}
}

// The other half, and the reason this is not simply "warn on every failure": a plugin installed
// globally fires its hooks in every repo the user opens. Where there is no batten.yaml, nobody
// asked to be governed and silence is the correct answer — batten governs where it was invited.
func TestNoBattenYamlStaysCompletelySilent(t *testing.T) {
	dir := t.TempDir() // no batten.yaml anywhere above it
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	payload := `{"session_id":"s","cwd":` + jsonString(dir) +
		`,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m \"x\""}}`

	if out := runHook(t, "PreToolUse", payload); strings.TrimSpace(out) != "" {
		t.Errorf("batten spoke up in a repo that never asked for it:\n%s", out)
	}
}

// A malformed batten.yaml is the opposite case: this repo DID ask to be governed, and the gate
// is down because its own config does not load. Silence there hides a broken installation.
func TestAnUnloadableSpecIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte("version: 1\nproject:\n  - not a string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	payload := `{"session_id":"s","cwd":` + jsonString(dir) +
		`,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git commit -m \"x\""}}`

	out := runHook(t, "PreToolUse", payload)
	if strings.TrimSpace(out) == "" {
		t.Fatal("batten.yaml exists and does not load, so no gate ran — and nothing said so")
	}
	if !strings.Contains(out, "batten.yaml") {
		t.Errorf("the warning must name the file that failed to load:\n%s", out)
	}
}

// Recording events is not enforcing them. Losing a SubagentStart costs a node in the graph; it
// does not let an unverified commit through, so it must not put a warning in front of the user
// on every tool call. The noise budget belongs to the events where silence means approval.
func TestOnlyTheEnforcingEventWarnsWhenDegraded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"),
		[]byte("version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\nphases:\n  - id: build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(t.TempDir(), "batten.db")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BATTEN_DB", broken)

	payload := `{"session_id":"s","cwd":` + jsonString(dir) + `,"hook_event_name":"SubagentStart","agent_id":"a1"}`
	if out := runHook(t, "SubagentStart", payload); strings.TrimSpace(out) != "" {
		t.Errorf("a recording event warned about a degraded store; that fires on every subagent "+
			"and trains the user to ignore the warning that matters:\n%s", out)
	}
}

// jsonString quotes s as a JSON string, so Windows backslashes in a temp path do not turn the
// payload into invalid JSON — which would make the hook fail for the wrong reason.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// captureStdout runs fn with stdout redirected and returns everything it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	os.Stdout = old
	w.Close()
	return <-done
}

// writeSpec drops a batten.yaml into a fresh dir and returns the dir.
func writeSpec(t *testing.T, y string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// inDir runs fn with the process cwd set to dir, because doctor diagnoses the repo you are in.
func inDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	fn()
}

// TestDoctorReportsEverythingInOnePass is field-test finding #60.
//
// doctor returned at the first fatal, so a repo with two problems reported one, you fixed it,
// reran, and met the next. People give up on the third round trip — and the whole point of the
// command is to be the first thing you run when something is already broken.
func TestDoctorReportsEverythingInOnePass(t *testing.T) {
	dir := writeSpec(t, "version: 1\nproject:\n  - this is not a string\n") // spec: fatal
	broken := filepath.Join(t.TempDir(), "batten.db")                       // store: also fatal
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BATTEN_DB", broken)

	var err error
	out := captureStdout(t, func() { inDir(t, dir, func() { err = cmdDoctor() }) })

	if err == nil {
		t.Error("a repo whose spec does not load must exit non-zero, or CI goes green on it")
	}
	if !strings.Contains(out, "batten.yaml") {
		t.Errorf("the spec problem is missing:\n%s", out)
	}
	if !strings.Contains(out, "store") {
		t.Errorf("doctor stopped at the spec and never reached the store, so the second problem "+
			"only surfaces on the NEXT run. One diagnosis per invocation is how people give up:\n%s", out)
	}
}

// TestDoctorCatchesAChecksCommandItCannotRun is field-test finding #58.
//
// `checks:` is copied verbatim from the build files of whoever ran `batten init`, so a command
// the team's machine has and yours does not is the normal way this breaks. It does not fail
// visibly: `batten check` reports exit 1 with "not recognized" buried in the evidence of a
// BLOCKED verdict, and the reader concludes the code is at fault.
func TestDoctorCatchesAChecksCommandItCannotRun(t *testing.T) {
	dir := writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: verify\n    gate: qa\n  - id: close\n    gate: qa\n    requires_verdict: ok\n"+
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n"+
		"    checks: ['definitely-not-a-real-binary-xyz test ./...']\n")
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	out := captureStdout(t, func() { inDir(t, dir, func() { _ = cmdDoctor() }) })

	if !strings.Contains(out, "definitely-not-a-real-binary-xyz") {
		t.Errorf("the gate declares a check this machine cannot run and doctor did not say so:\n%s", out)
	}
	if !strings.Contains(out, "cannot be run here") {
		t.Errorf("the warning must say the command cannot be run, not merely mention it:\n%s", out)
	}
}

// The other half: a check that CAN run must not be reported. A doctor that cries wolf about a
// working configuration gets ignored, which costs more than the check was worth.
func TestDoctorStaysQuietAboutChecksThatResolve(t *testing.T) {
	dir := writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: verify\n    gate: qa\n  - id: close\n    gate: qa\n    requires_verdict: ok\n"+
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n"+
		"    checks: ['go build ./...']\n") // go is on PATH: these tests are running under it
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	out := captureStdout(t, func() { inDir(t, dir, func() { _ = cmdDoctor() }) })

	if strings.Contains(out, "cannot be run here") {
		t.Errorf("doctor flagged a check that resolves perfectly well:\n%s", out)
	}
}

// TestDoctorCatchesAnAnchorTheRepoCannotRecord came out of the proyecto_ui replica.
//
// `batten init` writes `anchor: git_sha` into the spec of a directory that is not a git repo,
// `batten phase` then records an empty base_sha, and nothing says so — while `diff_from: anchor`
// is documented as reading exactly that value.
func TestDoctorCatchesAnAnchorTheRepoCannotRecord(t *testing.T) {
	dir := writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: build\n    anchor: git_sha\n  - id: close\n    requires_verdict: ok\n")
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	out := captureStdout(t, func() { inDir(t, dir, func() { _ = cmdDoctor() }) })

	if !strings.Contains(out, "not a git repository") {
		t.Errorf("a phase declares an anchor the repo cannot produce and doctor was silent:\n%s", out)
	}
	if !strings.Contains(out, "build") {
		t.Errorf("the warning must name the phase that declares it:\n%s", out)
	}
}

// ---------- `diff_from: anchor`, and the phase chain ----------

// gitRepoWithSpec makes a real one-commit git repository holding a batten.yaml, because the two
// things under test here — the anchor and the diff it scopes — do not exist without git.
func gitRepoWithSpec(t *testing.T, y string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is not usable here (%v): %s", err, out)
		}
	}
	return dir
}

const diffFromSpec = "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
	"phases:\n  - id: build\n    anchor: git_sha\n  - id: verify\n    diff_from: anchor\n" +
	"  - id: close\n    requires_verdict: ok\n"

// TestEnteringAPhaseSaysWhatItsDiffScopeIs.
//
// `diff_from: anchor` is documented as "operate only on the unit's diff" and had no consumer at
// all: the phase behaved exactly like a phase without it. Nothing computed the diff, nothing
// printed the range, and a verify agent had no way to know what it was supposed to be looking at
// short of reading the database.
func TestEnteringAPhaseSaysWhatItsDiffScopeIs(t *testing.T) {
	dir := gitRepoWithSpec(t, diffFromSpec)
	// INSIDE the repo, and not called batten.db: that is the shape that exposed the exclusion
	// bug, and it is the shape the field-test replica actually uses.
	t.Setenv("BATTEN_DB", filepath.Join(dir, "state.db"))

	inDir(t, dir, func() {
		_ = captureStdout(t, func() { _ = cmdPhase([]string{"TASK-1", "build"}) })
		// The unit does its work: one tracked edit and one new file, neither committed — which is
		// the normal state of a unit that is being verified.
		if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "added.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() { _ = cmdPhase([]string{"TASK-1", "verify"}) })

		if !strings.Contains(out, "scope:") {
			t.Fatalf("a phase declaring `diff_from: anchor` said nothing about its scope:\n%s", out)
		}
		for _, f := range []string{"seed.txt", "added.go"} {
			if !strings.Contains(out, f) {
				t.Errorf("the scope omits %s — uncommitted work IS the unit's diff:\n%s", f, out)
			}
		}
		// batten's own ledger is not the unit's work. This was only found by pointing the thing
		// at a real database and reading the output: the exclusion matched the conventional
		// names (`.batten/`, `batten.db*`) and BATTEN_DB points wherever the user says, so a run
		// whose database was called `state.db` reported state.db, state.db-wal and state.db-shm
		// as three files the unit had changed. The database's location is known — it must be
		// asked for, not guessed at.
		for _, sidecar := range []string{"state.db", "state.db-wal", "state.db-shm"} {
			if strings.Contains(out, sidecar) {
				t.Errorf("batten's own ledger (%s) is being reported as the unit's work:\n%s",
					sidecar, out)
			}
		}
	})
}

// The silent half, which is field-test #529: skip the anchor-bearing phase and every later phase
// carries on as though its scope were real. An empty base with no warning is the worst shape —
// it reads as "no changes" rather than "I do not know".
func TestAPhaseThatDiffsFromAMissingAnchorSaysSo(t *testing.T) {
	dir := gitRepoWithSpec(t, diffFromSpec)
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	inDir(t, dir, func() {
		out := captureStdout(t, func() { _ = cmdPhase([]string{"TASK-1", "verify"}) })
		if !strings.Contains(out, "no anchor") {
			t.Fatalf("verify declares `diff_from: anchor` with no anchor recorded and batten was "+
				"silent:\n%s", out)
		}
		if !strings.Contains(out, "build") {
			t.Errorf("the warning must name the phase that records the anchor:\n%s", out)
		}
	})
}

// TestEnteringAPhaseChainsItToThePrevious. The run graph called itself a DAG and had no edge
// between any two phases: every surface drew a row of unconnected boxes with subagents hanging
// off them. `depends_on` was the second relation the canvas could colour and nothing could ever
// produce.
func TestEnteringAPhaseChainsItToThePrevious(t *testing.T) {
	dir := gitRepoWithSpec(t, diffFromSpec)
	db := filepath.Join(t.TempDir(), "batten.db")
	t.Setenv("BATTEN_DB", db)

	inDir(t, dir, func() {
		for _, p := range []string{"build", "verify", "close"} {
			_ = captureStdout(t, func() { _ = cmdPhase([]string{"TASK-1", p}) })
		}
	})

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run, err := st.LatestRun("p", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	edges, err := st.Edges(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{ // dependent -> dependency
		store.PhaseNodeID(run.RunID, "verify"): store.PhaseNodeID(run.RunID, "build"),
		store.PhaseNodeID(run.RunID, "close"):  store.PhaseNodeID(run.RunID, "verify"),
	}
	got := map[string]string{}
	for _, e := range edges {
		if e.Rel == "depends_on" {
			got[e.Src] = e.Dst
		}
	}
	if len(got) != len(want) {
		t.Fatalf("depends_on edges = %v, want %v", got, want)
	}
	for src, dst := range want {
		if got[src] != dst {
			t.Errorf("%s depends_on %q, want %q", store.DisplayNodeID(src),
				store.DisplayNodeID(got[src]), store.DisplayNodeID(dst))
		}
	}
}

// TestDoctorCrossesQueryBeforeReadWithReality is plan §5.3 step 2.
//
// `query_before_read: true` is now an instruction batten injects into every fanned-out agent's
// orientation. An instruction to consult a tool that is not installed is WORSE than no instruction:
// the agent either burns a turn discovering that, or reports having consulted something it could
// not reach. Same shape as every other check here — declaration crossed with reality.
func TestDoctorCrossesQueryBeforeReadWithReality(t *testing.T) {
	dir := writeSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		// A provider name that is certainly not on anybody's PATH, so the test does not depend on
		// whether graphify happens to be installed on the machine running it.
		"capabilities:\n  graph: { provider: graphify-definitely-not-installed, query_before_read: true }\n"+
		"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n")
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))

	out := captureStdout(t, func() { inDir(t, dir, func() { _ = cmdDoctor() }) })

	if !strings.Contains(out, "query_before_read") {
		t.Fatalf("doctor said nothing about query_before_read with the provider absent:\n%s", out)
	}
	if !strings.Contains(out, "NOT on PATH") {
		t.Errorf("the warning does not say the provider is missing:\n%s", out)
	}
	// And it must offer the honest alternative, not just complain.
	if !strings.Contains(out, "query_before_read: false") {
		t.Errorf("the warning does not offer turning the promise off:\n%s", out)
	}
}

func TestCommandHeadsFindsEveryExecutableInACompoundCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"go test ./...", []string{"go"}},
		{"go build ./... && go test ./...", []string{"go", "go"}},
		{"npm run lint; npm test", []string{"npm", "npm"}},
		{"CGO_ENABLED=0 go build ./...", []string{"go"}}, // env assignment is not the program
		{"./scripts/check.sh", []string{"./scripts/check.sh"}},
		{"make lint | tee out.txt", []string{"make", "tee"}},
	}
	for _, c := range cases {
		got := commandHeads(c.in)
		if len(got) != len(c.want) {
			t.Errorf("commandHeads(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("commandHeads(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// gateReadyToClose is the same decision the commit hook makes, reached from the CLI. It must
// agree with the hook, or `batten close` tells the user they are fine and the commit is denied.
func TestGateReadyToCloseAgreesWithTheHook(t *testing.T) {
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: verify\n    gate: qa\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
		"gates:\n  qa:\n    verdict: required\n    evidence: required\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	r, err := st.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}

	if err := gateReadyToClose(sp, st, r); err == nil {
		t.Error("with no verdict at all, the gate is not ready to close")
	}

	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa", Result: "blocked",
		Evidence: []string{"3 tests failing"}, Why: "the suite is red",
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := gateReadyToClose(sp, st, r); err == nil {
		t.Error("a blocked verdict must not permit a close")
	}

	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"go test ./...: PASS"},
	}, true); err != nil {
		t.Fatal(err)
	}
	if err := gateReadyToClose(sp, st, r); err != nil {
		t.Errorf("an ok verdict with evidence must permit the close: %v", err)
	}

	// An override is the audited escape hatch, and it must work from here too.
	r2, _ := st.EnsureRun("p", "TASK-2", "sess-b")
	if err := gateReadyToClose(sp, st, r2); err == nil {
		t.Fatal("TASK-2 has no verdict")
	}
	if err := st.Override(r2.RunID, "qa", "incident 4412"); err != nil {
		t.Fatal(err)
	}
	if err := gateReadyToClose(sp, st, r2); err != nil {
		t.Errorf("a recorded override must open the gate: %v", err)
	}
}

// codeGraphFresh feeds both the doctor warning and the run tagging that `batten measure` compares.
// A missing graph must read as absent, not as fresh — tagging runs "graph: yes" when there is no
// graph would poison the measurement it exists to support.
func TestCodeGraphFreshReportsAbsenceHonestly(t *testing.T) {
	dir := t.TempDir()
	sp := &spec.Spec{Root: dir}

	exists, fresh := codeGraphFresh(sp)
	if exists || fresh {
		t.Errorf("no graph exists; got exists=%v fresh=%v", exists, fresh)
	}

	if err := os.MkdirAll(filepath.Join(dir, "graphify-out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "graphify-out", "graph.json"), []byte(`{"nodes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	exists, fresh = codeGraphFresh(sp)
	if !exists {
		t.Error("a graph.json on disk must be reported as existing")
	}
	// With no git history to compare against, an existing graph counts as fresh rather than
	// nagging about staleness it cannot establish.
	if !fresh {
		t.Error("with no git history there is nothing to be stale against")
	}
}

// TestUsageListsEveryCommandTheGateTellsYouToRun.
//
// `check`, `close` and `measure` were absent from --help while the commit gate's own denial
// said "Run: batten check <unit>". A user reads that, types `batten --help`, does not find the
// command, and reasonably concludes the denial is a bug in batten rather than an instruction.
func TestUsageListsEveryCommandTheGateTellsYouToRun(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	printUsage()
	w.Close()
	os.Stderr = old

	var sb strings.Builder
	if _, err := io.Copy(&sb, r); err != nil {
		t.Fatal(err)
	}
	help := sb.String()

	for _, cmd := range []string{"check", "close", "measure", "init", "doctor", "verdict", "phase"} {
		if !strings.Contains(help, "batten "+cmd) {
			t.Errorf("--help does not list %q, which the CLI accepts and the gate's messages cite", cmd)
		}
	}
}
