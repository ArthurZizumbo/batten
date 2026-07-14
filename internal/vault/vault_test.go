package vault

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/arthu/batten/internal/store"
)

// ---------- fixtures ----------

func fixtureRun() *store.Run {
	ended := int64(1_752_500_000)
	return &store.Run{
		RunID:       "US-034-1752400000",
		Project:     "batten",
		UnitID:      "US-034",
		SessionID:   "sess-1",
		Phase:       "verify",
		Status:      "ok",
		BaseSHA:     "abc1234def5678",
		TokensSpent: 1_234_567,
		ImputedUSD:  12.3456789,
		StartedAt:   1_752_400_000,
		EndedAt:     &ended,
	}
}

func fixtureNodes() []store.Node {
	return []store.Node{
		{NodeID: "p-implement", RunID: "r", Kind: "phase", Label: "implement", Status: "ok", StartedAt: 10},
		{NodeID: "n-a", RunID: "r", Kind: "subagent", Label: "ml-agent", Domain: "ml", Status: "ok", StartedAt: 20},
		{NodeID: "n-b", RunID: "r", Kind: "subagent", Label: "api-agent", Domain: "api", Status: "blocked", StartedAt: 30},
	}
}

func fixtureUsage() map[string]store.Usage {
	return map[string]store.Usage{
		"n-a": {NodeID: "n-a", InputTokens: 500_000, OutputTokens: 12_000, ImputedUSD: 8.20},
		// n-b deliberately absent: its usage was never ingested.
	}
}

func fixtureVerdict() *store.Verdict {
	return &store.Verdict{
		Gate: "close", CheckID: "verify", Result: "ok",
		Evidence:     []string{"go build ./... clean", "14 tests pass"},
		Why:          "all domain checks green",
		SafeNextStep: "commit",
	}
}

func newVault(t *testing.T) *Writer {
	t.Helper()
	return New(t.TempDir(), "batten")
}

// readNote splits a run note into its parsed frontmatter and its body, failing the test if the
// frontmatter is not valid YAML.
func readNote(t *testing.T, p string) (map[string]any, string) {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("note does not open with a frontmatter fence:\n%s", s)
	}
	rest := s[len("---\n"):]
	i := strings.Index(rest, "\n---\n")
	if i < 0 {
		t.Fatalf("frontmatter is not terminated:\n%s", s)
	}
	head, body := rest[:i+1], rest[i+len("\n---\n"):]

	var m map[string]any
	if err := yaml.Unmarshal([]byte(head), &m); err != nil {
		t.Fatalf("frontmatter is not valid YAML: %v\n%s", err, head)
	}
	return m, body
}

// ---------- frontmatter ----------

func TestFrontmatterRoundTripsAsTypedYAML(t *testing.T) {
	w := newVault(t)
	r := fixtureRun()
	if err := w.WriteRun(r, fixtureNodes(), nil, fixtureVerdict(), fixtureUsage(), ""); err != nil {
		t.Fatal(err)
	}

	m, _ := readNote(t, w.RunNotePath("US-034"))

	if m["unit"] != "US-034" || m["project"] != "batten" || m["status"] != "ok" {
		t.Errorf("identity properties wrong: %#v", m)
	}
	if m["phase"] != "verify" {
		t.Errorf("phase = %v", m["phase"])
	}
	if m["verdict"] != "ok" {
		t.Errorf("verdict = %v", m["verdict"])
	}
	if m["base_sha"] != "abc1234def5678" {
		t.Errorf("base_sha = %v", m["base_sha"])
	}

	// Numbers must decode as numbers, not as strings: the .base dashboards sort on them.
	if got, ok := m["evidence_count"].(int); !ok || got != 2 {
		t.Errorf("evidence_count = %#v, want int 2", m["evidence_count"])
	}
	if got, ok := m["tokens"].(int); !ok || got != 1_234_567 {
		t.Errorf("tokens = %#v, want int 1234567", m["tokens"])
	}
	got, ok := m["imputed_usd"].(float64)
	if !ok || math.Abs(got-12.3457) > 1e-9 {
		t.Errorf("imputed_usd = %#v, want float 12.3457 (rounded)", m["imputed_usd"])
	}

	// Dates must decode as dates so Obsidian types the property and Bases can sort on it.
	if _, ok := m["started"].(time.Time); !ok {
		t.Errorf("started = %#v (%T), want a YAML timestamp", m["started"], m["started"])
	}
	if _, ok := m["ended"].(time.Time); !ok {
		t.Errorf("ended = %#v (%T), want a YAML timestamp", m["ended"], m["ended"])
	}

	// And it must decode into a typed struct, which is what Obsidian effectively does.
	var typed struct {
		Unit          string    `yaml:"unit"`
		EvidenceCount int       `yaml:"evidence_count"`
		Tokens        int64     `yaml:"tokens"`
		ImputedUSD    float64   `yaml:"imputed_usd"`
		Started       time.Time `yaml:"started"`
		Domains       []string  `yaml:"domains"`
	}
	raw, _ := os.ReadFile(w.RunNotePath("US-034"))
	head := strings.SplitN(string(raw), "---\n", 3)[1]
	if err := yaml.Unmarshal([]byte(head), &typed); err != nil {
		t.Fatalf("frontmatter does not decode into typed properties: %v", err)
	}
	if typed.Tokens != 1_234_567 || typed.EvidenceCount != 2 {
		t.Errorf("typed decode wrong: %+v", typed)
	}
	if typed.Started.UTC().Unix() != r.StartedAt {
		t.Errorf("started = %v, want unix %d", typed.Started, r.StartedAt)
	}
	if len(typed.Domains) != 2 || typed.Domains[0] != "api" || typed.Domains[1] != "ml" {
		t.Errorf("domains = %v, want [api ml]", typed.Domains)
	}
}

func TestDomainsIsAlwaysAList(t *testing.T) {
	w := newVault(t)
	if err := w.WriteRun(fixtureRun(), nil, nil, fixtureVerdict(), nil, ""); err != nil {
		t.Fatal(err)
	}
	m, _ := readNote(t, w.RunNotePath("US-034"))
	v, ok := m["domains"]
	if !ok {
		t.Fatal("domains property is missing")
	}
	if v == nil {
		t.Errorf("domains = null; an empty list must serialize as [], not null")
	}
}

// ---------- the loud cases ----------

func TestNoVerdictSaysSoLoudly(t *testing.T) {
	w := newVault(t)
	r := fixtureRun()
	r.Status = "running"
	if err := w.WriteRun(r, fixtureNodes(), nil, nil, fixtureUsage(), ""); err != nil {
		t.Fatal(err)
	}
	m, body := readNote(t, w.RunNotePath("US-034"))

	// The property must exist with a known value, so the "blocked verdicts" dashboard can
	// select it. A missing key would make the run invisible in the one view that must show it.
	if m["verdict"] != verdictNone {
		t.Errorf("verdict = %v, want %q", m["verdict"], verdictNone)
	}
	// And evidence_count must be ABSENT, not 0: there is no verdict, so there is nothing whose
	// evidence we counted. Reporting 0 would claim a measurement we never made.
	if v, ok := m["evidence_count"]; ok {
		t.Errorf("evidence_count = %v; it must be omitted when there is no verdict", v)
	}
	if !strings.Contains(body, "DENY") || !strings.Contains(body, "[!danger]") {
		t.Errorf("a run with no verdict must say so loudly; body was:\n%s", body)
	}
	if !strings.Contains(body, "no verdict envelope") {
		t.Errorf("body does not name the missing verdict envelope:\n%s", body)
	}
}

func TestOkWithEmptyEvidenceIsFlagged(t *testing.T) {
	w := newVault(t)
	v := &store.Verdict{Gate: "close", CheckID: "verify", Result: "ok"} // the failure batten exists to kill
	if err := w.WriteRun(fixtureRun(), nil, nil, v, nil, ""); err != nil {
		t.Fatal(err)
	}
	m, body := readNote(t, w.RunNotePath("US-034"))

	if got, ok := m["evidence_count"].(int); !ok || got != 0 {
		t.Errorf("evidence_count = %#v; an existing verdict with no evidence must report 0, loudly", m["evidence_count"])
	}
	if !strings.Contains(body, "[!danger]") || !strings.Contains(body, "EMPTY evidence[]") {
		t.Errorf("result=ok with empty evidence[] must be flagged; body was:\n%s", body)
	}
}

func TestUnmeasuredUsageIsNeverReportedAsZero(t *testing.T) {
	w := newVault(t)
	r := fixtureRun()
	r.TokensSpent, r.ImputedUSD = 0, 0 // no transcript ingested: unknown, not zero
	if err := w.WriteRun(r, fixtureNodes(), nil, fixtureVerdict(), nil, ""); err != nil {
		t.Fatal(err)
	}
	m, body := readNote(t, w.RunNotePath("US-034"))

	if v, ok := m["tokens"]; ok {
		t.Errorf("tokens = %v; an unmeasured run must omit the property, not claim 0", v)
	}
	if v, ok := m["imputed_usd"]; ok {
		t.Errorf("imputed_usd = %v; an unmeasured run must omit the property, not claim 0", v)
	}
	if !strings.Contains(body, "not measured") {
		t.Errorf("body must say usage is not measured:\n%s", body)
	}

	// Per node, too: n-b has no usage row and must not be priced at $0.00.
	if !strings.Contains(body, "| api-agent |") {
		t.Fatalf("fan-out table missing api-agent:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "| api-agent |") && !strings.Contains(line, "not measured") {
			t.Errorf("node with no usage row must read \"not measured\", got: %s", line)
		}
	}
}

func TestMeasuredButUnpricedRunIsNeverValuedAtZero(t *testing.T) {
	w := newVault(t)
	r := fixtureRun()
	r.ImputedUSD = 0 // tokens were counted, but the run's model had no API rate
	if err := w.WriteRun(r, fixtureNodes(), nil, fixtureVerdict(), nil, ""); err != nil {
		t.Fatal(err)
	}
	m, body := readNote(t, w.RunNotePath("US-034"))

	// The tokens ARE measured, so the property stays; the dollars are unknown, so it must not.
	if _, ok := m["tokens"]; !ok {
		t.Errorf("tokens were measured and must be reported, got frontmatter: %#v", m)
	}
	if v, ok := m["imputed_usd"]; ok {
		t.Errorf("imputed_usd = %v; an unpriced run must omit the dollar figure, not claim 0", v)
	}
	// The body must never render "$0.00" for an unpriced run.
	if strings.Contains(body, "$0.00") {
		t.Errorf("unpriced run rendered as $0.00 — pricing the unpriceable as free:\n%s", body)
	}
	if !strings.Contains(body, "not priced") {
		t.Errorf("body must say the cost is not priced:\n%s", body)
	}
}

func TestWriteSetSizeIsUnavailableWhenNotRecorded(t *testing.T) {
	w := newVault(t)
	if err := w.WriteRun(fixtureRun(), fixtureNodes(), nil, fixtureVerdict(), fixtureUsage(), ""); err != nil {
		t.Fatal(err)
	}
	_, body := readNote(t, w.RunNotePath("US-034"))
	if !strings.Contains(body, "not recorded") {
		t.Errorf("an absent write-set must read \"not recorded\", not \"0 files\":\n%s", body)
	}

	// With write-sets supplied, the files appear as inline code — never as wikilinks.
	w.WriteSets = map[string][]string{"n-a": {"internal/ml/train.go", "internal/ml/eval.go"}}
	if err := w.WriteRun(fixtureRun(), fixtureNodes(), nil, fixtureVerdict(), fixtureUsage(), ""); err != nil {
		t.Fatal(err)
	}
	_, body = readNote(t, w.RunNotePath("US-034"))
	if !strings.Contains(body, "2 files") {
		t.Errorf("write-set size not rendered:\n%s", body)
	}
	if !strings.Contains(body, "`internal/ml/train.go`") {
		t.Errorf("touched files must be rendered as inline code:\n%s", body)
	}
	if strings.Contains(body, "[[internal/ml/train.go]]") {
		t.Errorf("touched files must NOT be wikilinked: we cannot know graphify's note names")
	}
}

// ---------- wikilinks ----------

var wikilinkRe = regexp.MustCompile(`!?\[\[([^\]|#]+)`)

func TestNoWikilinkToANoteWeCannotKnowExists(t *testing.T) {
	w := newVault(t)
	// US-033.md does not exist in this vault, and no canvas was emitted. The note must
	// therefore contain no wikilink at all: a link to a note that does not exist plants a
	// phantom node in the graph view and asserts a fact we never established.
	if err := w.WriteRun(fixtureRun(), fixtureNodes(), nil, fixtureVerdict(), fixtureUsage(), ""); err != nil {
		t.Fatal(err)
	}
	_, body := readNote(t, w.RunNotePath("US-034"))
	if strings.Contains(body, "[[") {
		t.Errorf("emitted a wikilink with nothing to link to:\n%s", body)
	}
}

func TestPrevUnitIsLinkedOnlyOnceItsNoteExists(t *testing.T) {
	w := newVault(t)

	// Materialize the previous unit's note and the canvas the caller claims to have emitted.
	if err := os.MkdirAll(w.runsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.RunNotePath("US-033"), []byte("---\nunit: US-033\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CanvasPath("US-034"), []byte(`{"nodes":[],"edges":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := w.WriteRun(fixtureRun(), fixtureNodes(), nil, fixtureVerdict(), fixtureUsage(),
		w.CanvasRel("US-034")); err != nil {
		t.Fatal(err)
	}
	_, body := readNote(t, w.RunNotePath("US-034"))

	if !strings.Contains(body, "[[US-033]]") {
		t.Errorf("previous unit note exists but was not linked:\n%s", body)
	}
	if !strings.Contains(body, "![[US-034.canvas]]") {
		t.Errorf("canvas not embedded:\n%s", body)
	}

	// Every wikilink in the note must resolve to a file that is actually there.
	for _, m := range wikilinkRe.FindAllStringSubmatch(body, -1) {
		target := strings.TrimSpace(m[1])
		candidates := []string{
			filepath.Join(w.runsDir(), target),
			filepath.Join(w.runsDir(), target+".md"),
		}
		found := false
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				found = true
			}
		}
		if !found {
			t.Errorf("wikilink [[%s]] resolves to nothing in %s", target, w.runsDir())
		}
	}
}

func TestPrevUnitCandidates(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"US-034", []string{"US-033", "US-33"}}, // width is unknowable; existence on disk decides
		{"E7-US-010", []string{"E7-US-009", "E7-US-9"}},
		{"US-100", []string{"US-099", "US-99"}},
		{"US-001", nil}, // nothing precedes the first unit
		{"US-1", nil},
		{"backlog", nil}, // no numeric suffix: nothing to derive, so nothing is linked
		{"", nil},
	}
	for _, c := range cases {
		got := prevUnitCandidates(c.in)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("prevUnitCandidates(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---------- bases ----------

func TestWriteBasesIsIdempotent(t *testing.T) {
	w := newVault(t)
	if err := w.WriteBases(); err != nil {
		t.Fatal(err)
	}
	first := snapshotDir(t, w.projectDir())

	if err := w.WriteBases(); err != nil {
		t.Fatal(err)
	}
	second := snapshotDir(t, w.projectDir())

	if len(first) != 3 {
		t.Fatalf("want 3 dashboards, got %d: %v", len(first), keys(first))
	}
	for name, b1 := range first {
		b2, ok := second[name]
		if !ok {
			t.Errorf("%s vanished on the second write", name)
			continue
		}
		if b1 != b2 {
			t.Errorf("%s is not byte-identical across two writes", name)
		}
	}
}

func TestBasesAreValidAndScopedToTheProject(t *testing.T) {
	w := newVault(t)
	if err := w.WriteBases(); err != nil {
		t.Fatal(err)
	}
	files := snapshotDir(t, w.projectDir())

	allowedTop := map[string]bool{"filters": true, "formulas": true, "properties": true, "summaries": true, "views": true}

	for name, content := range files {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(content), &m); err != nil {
			t.Fatalf("%s is not valid YAML: %v\n%s", name, err, content)
		}
		for k := range m {
			if !allowedTop[k] {
				t.Errorf("%s: unknown top-level key %q", name, k)
			}
		}
		if f, ok := m["filters"]; ok {
			checkFilterNode(t, name, f)
		} else {
			t.Errorf("%s has no filters: it would list every note in the vault", name)
		}
		// A vault can hold several projects. A dashboard that forgets to scope itself would
		// show another project's runs as if they were ours.
		if !strings.Contains(content, `project == "batten"`) {
			t.Errorf("%s is not scoped to the project:\n%s", name, content)
		}

		views, ok := m["views"].([]any)
		if !ok || len(views) == 0 {
			t.Fatalf("%s has no views", name)
		}
		v0, _ := views[0].(map[string]any)
		switch v0["type"] {
		case "table", "cards", "list":
		default:
			t.Errorf("%s: view type %v is not table|cards|list", name, v0["type"])
		}
		if v0["name"] == nil || v0["name"] == "" {
			t.Errorf("%s: view has no name", name)
		}
	}

	// The cost dashboard must exclude unmeasured runs rather than render them as $0.00.
	if !strings.Contains(files["imputed-cost-by-unit.base"], "imputed_usd > 0") {
		t.Errorf("the cost dashboard must filter out runs whose usage was never measured:\n%s",
			files["imputed-cost-by-unit.base"])
	}
	// The attention dashboard must catch runs with NO verdict, not just blocked ones.
	blocked := files["blocked-verdicts.base"]
	if !strings.Contains(blocked, `verdict == "none"`) || !strings.Contains(blocked, `verdict == "blocked"`) {
		t.Errorf("blocked-verdicts must select both blocked and verdict-less runs:\n%s", blocked)
	}
}

// checkFilterNode enforces the Bases filter grammar: a node is either an expression string or
// an object with EXACTLY ONE key (and|or|not) whose value is a list of nodes.
func checkFilterNode(t *testing.T, where string, n any) {
	t.Helper()
	switch x := n.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			t.Errorf("%s: empty filter expression", where)
		}
	case map[string]any:
		if len(x) != 1 {
			t.Errorf("%s: filter object has %d keys, must have exactly one", where, len(x))
		}
		for k, v := range x {
			if k != "and" && k != "or" && k != "not" {
				t.Errorf("%s: unknown filter operator %q", where, k)
			}
			list, ok := v.([]any)
			if !ok {
				t.Errorf("%s: %q is %T, want a list", where, k, v)
				continue
			}
			for _, child := range list {
				checkFilterNode(t, where+"."+k, child)
			}
		}
	default:
		t.Errorf("%s: filter node is %T, want a string or a one-key object", where, n)
	}
}

// ---------- paths ----------

func TestPathsAreAbsoluteAndCannotEscapeTheVault(t *testing.T) {
	w := New(filepath.Join(t.TempDir(), "vault"), "batten")

	note := w.RunNotePath("US-034")
	if !filepath.IsAbs(note) {
		t.Errorf("RunNotePath is not absolute: %s", note)
	}
	if want := filepath.Join(w.Root, "batten", "batten", "runs", "US-034.md"); note != want {
		t.Errorf("RunNotePath = %s, want %s", note, want)
	}
	if canvas := w.CanvasPath("US-034"); canvas != strings.TrimSuffix(note, ".md")+".canvas" {
		t.Errorf("CanvasPath = %s, does not sit beside the note", canvas)
	}
	if rel := w.CanvasRel("US-034"); rel != "batten/batten/runs/US-034.canvas" {
		t.Errorf("CanvasRel = %s (must be vault-relative and slash-separated)", rel)
	}

	// A hostile unit id must not be able to write outside the runs folder.
	for _, evil := range []string{"../../etc/passwd", "..", "a/b", `C:\Windows\system32`} {
		p := w.RunNotePath(evil)
		if dir := filepath.Dir(p); dir != w.runsDir() {
			t.Errorf("unit id %q escaped the runs folder: %s", evil, p)
		}
	}
}

func TestNoVaultConfigured(t *testing.T) {
	w := New("", "batten")
	if err := w.WriteRun(fixtureRun(), nil, nil, nil, nil, ""); !errors.Is(err, ErrNoVault) {
		t.Errorf("WriteRun with no root = %v, want ErrNoVault", err)
	}
	if err := w.WriteBases(); !errors.Is(err, ErrNoVault) {
		t.Errorf("WriteBases with no root = %v, want ErrNoVault", err)
	}
}

// TestSpawnEdgesAreNotRepeated: the fan-out table already shows spawn, so only the typed
// relations a flat trace cannot express (retry_of, rollback, ...) get their own section.
func TestOnlyNonSpawnEdgesGetASection(t *testing.T) {
	w := newVault(t)
	edges := []store.Edge{
		{Src: "p-implement", Dst: "n-a", Rel: "spawn"},
		{Src: "n-b", Dst: "n-a", Rel: "retry_of"},
	}
	if err := w.WriteRun(fixtureRun(), fixtureNodes(), edges, fixtureVerdict(), fixtureUsage(), ""); err != nil {
		t.Fatal(err)
	}
	_, body := readNote(t, w.RunNotePath("US-034"))
	if !strings.Contains(body, "`retry_of`: api-agent → ml-agent") {
		t.Errorf("typed relation not surfaced:\n%s", body)
	}
	if strings.Contains(body, "`spawn`") {
		t.Errorf("spawn is already visible as the fan-out; it must not be repeated:\n%s", body)
	}
}

// ---------- helpers ----------

func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".base") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
