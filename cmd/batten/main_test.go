package main

import (
	"encoding/json"
	"io"
	"os"
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
