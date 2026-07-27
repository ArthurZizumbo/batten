package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "batten.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// gatedSpec is the shape that matters: a closing phase that requires a verdict, i.e. a repo
// where a missing verdict really will deny the commit.
func gatedSpec(root string) *spec.Spec {
	return &spec.Spec{
		Version: 1,
		Project: "batten",
		Root:    root,
		Unit:    spec.Unit{Name: "US", Pattern: `US-\d{3}`},
		Phases: []spec.Phase{
			{ID: "build"},
			{ID: "close", Gate: "close", RequiresVerdict: "ok"},
		},
		Gates: map[string]spec.Gate{"close": {Verdict: "required", Evidence: "required"}},
	}
}

// The one that would make batten lie. rate_limits is absent for API-key users and for every
// invocation before the session's first API response. If that were recorded as 0%, a run's
// baseline would say "the whole window is still yours".
func TestNoRateLimitsRecordsNoSnapshot(t *testing.T) {
	st := testStore(t)
	sp := gatedSpec(t.TempDir())
	if _, err := st.EnsureRun(sp.Project, "US-034", ""); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`{"session_id":"s1","model":{"id":"claude-opus-4-8"},
	                "cost":{"total_cost_usd":1.23},"context_window":{"used_percentage":42.1}}`)
	line, err := Run(sp, st, raw)
	if err != nil {
		t.Fatalf("Run must never fail loudly: %v", err)
	}

	if _, err := st.LatestQuota("s1"); err == nil {
		t.Fatal("a payload with no rate_limits recorded a quota snapshot; absent must not become 0%")
	}
	run, err := st.ActiveRun(sp.Project, "US-034")
	if err != nil {
		t.Fatal(err)
	}
	if run.QuotaStart5h != nil {
		t.Fatalf("baseline set from a payload with no quota: %v", *run.QuotaStart5h)
	}
	if strings.Contains(line, "5h") || strings.Contains(line, "7d") {
		t.Fatalf("line claims quota it never received: %q", line)
	}
}

// A window can arrive with a reset time but no percentage. There is still nothing to measure.
func TestWindowWithoutPercentageRecordsNothing(t *testing.T) {
	st := testStore(t)
	sp := gatedSpec(t.TempDir())

	raw := []byte(`{"session_id":"s1","rate_limits":{"five_hour":{"resets_at":1738425600}}}`)
	if _, err := Run(sp, st, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LatestQuota("s1"); err == nil {
		t.Fatal("recorded a snapshot from a window carrying no used_percentage")
	}
}

// Each window is independently optional: five_hour present, seven_day absent must store one
// value and one unknown, not one value and a zero.
func TestPartialRateLimitsKeepsUnknownNil(t *testing.T) {
	st := testStore(t)
	sp := gatedSpec(t.TempDir())

	raw := []byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1738425600}}}`)
	line, err := Run(sp, st, raw)
	if err != nil {
		t.Fatal(err)
	}
	q, err := st.LatestQuota("s1")
	if err != nil {
		t.Fatalf("five_hour was present but no snapshot was stored: %v", err)
	}
	if q.FiveHourPct == nil || *q.FiveHourPct != 23.5 {
		t.Fatalf("five_hour_pct = %v, want 23.5", q.FiveHourPct)
	}
	if q.SevenDayPct != nil {
		t.Fatalf("seven_day was absent but was stored as %v", *q.SevenDayPct)
	}
	if !strings.Contains(line, "5h 24%") && !strings.Contains(line, "5h 23%") {
		t.Fatalf("line should show the 5h window: %q", line)
	}
	if strings.Contains(line, "7d") {
		t.Fatalf("line invented a 7d window: %q", line)
	}
}

// A run opens before the session's first statusline invocation, so both the session and the
// quota baseline have to be back-filled here or per-run deltas are impossible.
func TestRunAdoptsSessionAndSetsBaseline(t *testing.T) {
	st := testStore(t)
	sp := gatedSpec(t.TempDir())
	r, err := st.EnsureRun(sp.Project, "US-034", "") // opened with no session, as the CLI does
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPhase(r.RunID, "build"); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`{"session_id":"s1","rate_limits":{"five_hour":{"used_percentage":23.5},
	                "seven_day":{"used_percentage":41.2}}}`)
	line, err := Run(sp, st, raw)
	if err != nil {
		t.Fatal(err)
	}

	got, err := st.Run(r.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s1" {
		t.Fatalf("session not adopted: %q", got.SessionID)
	}
	if got.QuotaStart5h == nil || *got.QuotaStart5h != 23.5 {
		t.Fatalf("baseline = %v, want 23.5", got.QuotaStart5h)
	}
	for _, want := range []string{"US-034", "build", "5h 24%", "7d 41%"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

// The most useful thing batten can say mid-session.
func TestNoVerdictSaysCommitWillBeDenied(t *testing.T) {
	st := testStore(t)
	sp := gatedSpec(t.TempDir())
	if _, err := st.EnsureRun(sp.Project, "US-034", ""); err != nil {
		t.Fatal(err)
	}

	line, err := Run(sp, st, []byte(`{"session_id":"s1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "DENIED") {
		t.Fatalf("a gated run with no verdict must warn that the commit is denied: %q", line)
	}
}

func TestVerdictWithEvidenceIsShown(t *testing.T) {
	st := testStore(t)
	sp := gatedSpec(t.TempDir())
	r, err := st.EnsureRun(sp.Project, "US-034", "s1")
	if err != nil {
		t.Fatal(err)
	}
	err = st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "close", CheckID: "tests", Result: "ok",
		Evidence: []string{"go test ./...: 12 passed"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	line, err := Run(sp, st, []byte(`{"session_id":"s1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "verdict ok (1 ev)") {
		t.Fatalf("line %q should report the verdict and its evidence count", line)
	}
	if strings.Contains(line, "DENIED") {
		t.Fatalf("line %q threatens a denial for a run that has a verdict", line)
	}
}

// A broken status line degrades the terminal on every turn, so nothing here may return an error.
func TestRunNeverFailsLoudly(t *testing.T) {
	st := testStore(t)
	for _, raw := range [][]byte{[]byte("not json at all"), []byte(""), []byte(`{"session_id":123}`)} {
		line, err := Run(nil, st, raw)
		if err != nil {
			t.Fatalf("Run(%q) returned an error: %v", raw, err)
		}
		if line == "" {
			t.Fatalf("Run(%q) returned an empty line", raw)
		}
	}
}

// sp == nil: cwd is not a batten repo. The quota is still account-global and still worth
// sampling, but nothing may be claimed about a unit.
func TestNilSpecStillSamplesQuota(t *testing.T) {
	st := testStore(t)
	raw := []byte(`{"session_id":"s9","rate_limits":{"five_hour":{"used_percentage":7.5}}}`)
	line, err := Run(nil, st, raw)
	if err != nil {
		t.Fatal(err)
	}
	q, err := st.LatestQuota("s9")
	if err != nil || q.FiveHourPct == nil || *q.FiveHourPct != 7.5 {
		t.Fatalf("quota not sampled outside a batten repo: %v %v", q, err)
	}
	if !strings.Contains(line, "5h 7.5%") {
		t.Fatalf("line %q should still carry the quota", line)
	}
}

// ---------- install ----------

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s is not valid JSON after we wrote it: %v\n%s", path, err, b)
	}
	return m
}

func TestInstalledReportsAbsence(t *testing.T) {
	present, existing, err := Installed(t.TempDir())
	if err != nil || present || existing != "" {
		t.Fatalf("Installed on a bare dir = (%v, %q, %v), want (false, \"\", nil)", present, existing, err)
	}
}

func TestInstallPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "permissions": {
    "allow": ["Bash(go test:*)"],
    "deny": []
  },
  "env": {
    "FOO": "bar"
  },
  "model": "claude-opus-4-8"
}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Install(dir, `C:\bin\batten.exe`, false); err != nil {
		t.Fatalf("Install: %v", err)
	}

	m := readJSON(t, path)
	if _, ok := m["permissions"]; !ok {
		t.Fatal("Install dropped the permissions key")
	}
	if _, ok := m["env"]; !ok {
		t.Fatal("Install dropped the env key")
	}
	if m["model"] != "claude-opus-4-8" {
		t.Fatalf("Install mangled the model key: %v", m["model"])
	}

	// The values we did not touch must survive byte-for-byte, not just semantically.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `"allow": ["Bash(go test:*)"]`) {
		t.Fatalf("Install reformatted a value it does not own:\n%s", after)
	}

	present, existing, err := Installed(dir)
	if err != nil || !present {
		t.Fatalf("Installed after Install = (%v, %v)", present, err)
	}
	if !IsBatten(existing) {
		t.Fatalf("installed command is not batten's: %q", existing)
	}
	if !strings.Contains(existing, "statusline") {
		t.Fatalf("installed command does not invoke the statusline subcommand: %q", existing)
	}
}

func TestInstallCreatesSettingsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := Install(dir, "/usr/local/bin/batten", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	m := readJSON(t, settingsPath(dir))
	sl, ok := m["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine not written: %v", m)
	}
	if sl["type"] != "command" {
		t.Fatalf("statusLine.type = %v, want command", sl["type"])
	}
	if sl["command"] != "/usr/local/bin/batten statusline" {
		t.Fatalf("statusLine.command = %v", sl["command"])
	}
}

// A path with spaces must be quoted or the shell runs the wrong thing. This is the normal case
// on Windows, which is a first-class target.
func TestInstallQuotesPathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	if err := Install(dir, `C:\Program Files\batten\batten.exe`, false); err != nil {
		t.Fatal(err)
	}
	_, existing, err := Installed(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(existing, `"C:\Program Files\batten\batten.exe"`) {
		t.Fatalf("path with spaces was not quoted: %q", existing)
	}
}

func TestInstallRefusesToClobberWithoutChain(t *testing.T) {
	dir := t.TempDir()
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "statusLine": {
    "type": "command",
    "command": "my-fancy-prompt --color"
  }
}
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Install(dir, "batten", false)
	if err == nil {
		t.Fatal("Install clobbered an existing statusLine instead of refusing")
	}
	if !strings.Contains(err.Error(), "my-fancy-prompt") {
		t.Fatalf("the refusal must name what is already there: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("a refused Install still modified the file:\n%s", after)
	}
	if _, err := os.Stat(chainPath(dir)); err == nil {
		t.Fatal("a refused Install wrote a chain sidecar")
	}
}

func TestInstallChainWrapsExisting(t *testing.T) {
	dir := t.TempDir()
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := `{"statusLine":{"type":"command","command":"my-fancy-prompt --color"}}`
	if err := os.WriteFile(path, []byte(prev), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Install(dir, "batten", true); err != nil {
		t.Fatalf("Install(chain): %v", err)
	}
	_, existing, err := Installed(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !IsBatten(existing) {
		t.Fatalf("statusLine is not batten's after chaining: %q", existing)
	}
	chained, err := chainedCommand(dir)
	if err != nil {
		t.Fatal(err)
	}
	if chained != "my-fancy-prompt --color" {
		t.Fatalf("the displaced command was not preserved: %q", chained)
	}
}

// Re-installing must be safe. Above all it must not chain batten to batten, which would fork a
// new batten on every redraw.
func TestInstallIsIdempotentAndNeverSelfChains(t *testing.T) {
	dir := t.TempDir()
	if err := Install(dir, "batten", false); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, "batten", true); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if c, _ := chainedCommand(dir); c != "" {
		t.Fatalf("batten chained itself: %q", c)
	}
	m := readJSON(t, settingsPath(dir))
	if _, ok := m["statusLine"]; !ok {
		t.Fatal("statusLine missing after re-install")
	}
}

// The whole point of chaining: the user keeps their status line, and gains ours.
func TestRunAppendsChainedOutput(t *testing.T) {
	dir := t.TempDir()
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// `echo` is a builtin of both cmd.exe and sh, so this exercises the real shell on either.
	if err := os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"echo chained-ok"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, "batten", true); err != nil {
		t.Fatal(err)
	}

	st := testStore(t)
	sp := gatedSpec(dir)
	line, err := Run(sp, st, []byte(`{"session_id":"s1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "chained-ok") {
		t.Fatalf("chained command's output missing from %q", line)
	}
	if !strings.HasPrefix(line, "batten") {
		t.Fatalf("batten's own segment must come first: %q", line)
	}
}

func TestInstallRejectsUnparseableSettings(t *testing.T) {
	dir := t.TempDir()
	path := settingsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "{ this is not json"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(dir, "batten", true); err == nil {
		t.Fatal("Install rewrote a settings.json it could not parse")
	}
	after, _ := os.ReadFile(path)
	if string(after) != broken {
		t.Fatalf("the unparseable file was modified: %s", after)
	}
}

func TestUntilResetNeverGoesNegative(t *testing.T) {
	past := int64(1)
	if got := untilReset(&past); got != "" {
		t.Fatalf("untilReset(past) = %q, want \"\"", got)
	}
	if got := untilReset(nil); got != "" {
		t.Fatalf("untilReset(nil) = %q, want \"\"", got)
	}
}
