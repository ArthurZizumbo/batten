package spec

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// valid is a spec that loads cleanly. Tests mutate one thing at a time from here, so a failure
// always points at the mutation rather than at an unrelated field.
const valid = `
version: 1
project: acme
unit:
  name: US
  pattern: 'US-\d{3}'
artifacts:
  planning: docs/planning/{id}.md
phases:
  - id: plan
  - id: build
    reads: [planning, plan]
    anchor: git_sha
  - id: verify
    gate: qa
  - id: close
    requires_verdict: ok
gates:
  qa:
    checks: ['make test']
    verdict: required
    evidence: required
domains:
  api:
    path: server/
    coverage: 70
budget:
  tokens_per_run: 1000
  on_exceed: warn
`

func load(t *testing.T, body string) (*Spec, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadFrom(dir)
}

func mustLoad(t *testing.T, body string) *Spec {
	t.Helper()
	s, err := load(t, body)
	if err != nil {
		t.Fatalf("expected this spec to load: %v", err)
	}
	return s
}

func TestValidSpecLoads(t *testing.T) {
	s := mustLoad(t, valid)
	if s.Project != "acme" || s.Unit.Name != "US" {
		t.Errorf("got project=%q unit=%q", s.Project, s.Unit.Name)
	}
	if s.Root == "" {
		t.Error("Root must record where the spec was loaded from; artifact paths resolve against it")
	}
	if len(s.Phases) != 4 {
		t.Errorf("got %d phases, want 4", len(s.Phases))
	}
}

// TestGateWithoutEvidenceIsRefused is the single most important test in this package.
//
// A gate that requires a verdict but not evidence permits `result: ok` with an empty evidence[],
// which is the exact failure batten exists to prevent. It is refused at LOAD time — before a run
// exists, before a commit is attempted — because a spec that permits it is not a weaker
// configuration, it is a broken one.
func TestGateWithoutEvidenceIsRefused(t *testing.T) {
	body := strings.Replace(valid, "    evidence: required\n", "", 1)
	_, err := load(t, body)
	if err == nil {
		t.Fatal("a gate requiring a verdict but not evidence must NOT load")
	}
	if !strings.Contains(err.Error(), "evidence: required") {
		t.Errorf("the error must say how to fix it; got %v", err)
	}

	// evidence: optional is the same hole spelled differently.
	body = strings.Replace(valid, "    evidence: required", "    evidence: optional", 1)
	if _, err := load(t, body); err == nil {
		t.Fatal(`evidence: optional must be refused just like a missing one`)
	}
}

// TestArtifactWithoutIDIsRefused: without {id} every unit writes to the same file, silently
// destroying the previous unit's artifact.
func TestArtifactWithoutIDIsRefused(t *testing.T) {
	body := strings.Replace(valid, "docs/planning/{id}.md", "docs/planning.md", 1)
	_, err := load(t, body)
	if err == nil {
		t.Fatal("an artifact path without {id} must be refused")
	}
	if !strings.Contains(err.Error(), "overwrite") {
		t.Errorf("the error should explain the consequence; got %v", err)
	}
}

// TestValidationReportsEveryProblemAtOnce: a first-error-wins validator makes fixing a spec a
// game of whack-a-mole. Someone adopting batten should see the whole list in one pass.
func TestValidationReportsEveryProblemAtOnce(t *testing.T) {
	_, err := load(t, `
version: 2
project: ""
enforcement: sometimes
unit:
  name: ""
  pattern: 'US-(\d{3}'
phases: []
`)
	if err == nil {
		t.Fatal("this spec is wrong in seven ways and must not load")
	}
	msg := err.Error()
	for _, want := range []string{
		"version must be 1",
		"project is required",
		"enforcement must be",
		"unit.name is required",
		"not a valid regexp",
		"at least one phase",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q from the error list:\n%s", want, msg)
		}
	}
}

func TestReferentialIntegrity(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			"phase points at a gate that does not exist",
			strings.Replace(valid, "    gate: qa", "    gate: typo", 1),
			`unknown gate "typo"`,
		},
		{
			// `resources:` and `domains[].resources` were REMOVED (plan_publicacion.md §7), so the referential
			// check that used to live here went with them. What replaced it is the unknown-key
			// report: a spec still carrying the block is told the key is not read, rather than
			// being validated against a promise batten never kept. See
			// TestUnknownKeysLoadAndAreReported, which lists `resources` for exactly that reason.
			"budget.on_exceed no longer accepts downgrade_effort",
			strings.Replace(valid, "  on_exceed: warn", "  on_exceed: downgrade_effort", 1),
			"was removed",
		},
		{
			"duplicate phase id",
			strings.Replace(valid, "  - id: verify", "  - id: build", 1),
			`duplicate phase id "build"`,
		},
		{
			"a phase reads something that is neither an artifact nor a prior phase",
			strings.Replace(valid, "    reads: [planning, plan]", "    reads: [nonesuch]", 1),
			`reads "nonesuch"`,
		},
		{
			"requires_verdict must be ok or warn",
			strings.Replace(valid, "    requires_verdict: ok", "    requires_verdict: yes-please", 1),
			"requires_verdict must be",
		},
		{
			"budget.on_exceed is an enum",
			strings.Replace(valid, "  on_exceed: warn", "  on_exceed: explode", 1),
			"budget.on_exceed must be",
		},
		{
			"a domain without a path owns nothing",
			strings.Replace(valid, "    path: server/\n", "", 1),
			"path is required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.body)
			if err == nil {
				t.Fatalf("expected a validation error mentioning %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error mentioning %q, got: %v", c.want, err)
			}
		})
	}
}

// TestDomainForPicksTheMostSpecificPath: with nested domains, the deepest match owns the file.
// The write-set guard resolves blame through this, so a wrong answer blames the wrong agent.
func TestDomainForPicksTheMostSpecificPath(t *testing.T) {
	s := mustLoad(t, `
version: 1
project: p
unit: {name: T, pattern: 'T-\d+'}
phases: [{id: build}]
domains:
  app:
    path: src/
    exclude: [src/vendor/]
  api:
    path: src/api/
`)
	cases := []struct{ file, want string }{
		{"src/api/handler.go", "api"}, // deepest match wins over the shorter prefix
		{"src/ui/button.tsx", "app"},  // only the shallow domain matches
		{"src/vendor/lib.go", ""},     // excluded, even though src/ would match
		{"docs/readme.md", ""},        // no domain owns it
		{"srcfoo/thing.go", ""},       // prefix match must respect the path boundary
		// A domain declared as src/api/ owns what is INSIDE it. The path src/api itself is not
		// inside it, so it falls to the enclosing domain — which is what you want if a file
		// happens to be named that.
		{"src/api", "app"},
	}
	for _, c := range cases {
		got, ok := s.DomainFor(c.file)
		if c.want == "" {
			if ok {
				t.Errorf("DomainFor(%q) = %q, want no owner", c.file, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("DomainFor(%q) = %q (ok=%v), want %q", c.file, got, ok, c.want)
		}
	}

	// Backslash handling is platform-specific, and deliberately so. filepath.ToSlash only
	// converts on Windows, where a backslash IS the separator and the hook payload carries
	// them. On Linux a backslash is a legal character in a filename, so converting it would
	// invent a path the user never wrote — the current behaviour is correct on both, and the
	// expectation has to be computed per platform rather than asserted from the machine that
	// happened to author the test.
	got, ok := s.DomainFor(`src\api\handler.go`)
	if runtime.GOOS == "windows" {
		if !ok || got != "api" {
			t.Errorf(`on Windows a backslash path must resolve: DomainFor = %q (ok=%v), want "api"`, got, ok)
		}
	} else if ok {
		t.Errorf(`on %s, src\api\handler.go is one filename containing backslashes, not a path; `+
			`DomainFor claimed it belongs to %q`, runtime.GOOS, got)
	}
}

func TestMatchUnitExtractsFromArbitraryText(t *testing.T) {
	s := mustLoad(t, valid)
	cases := []struct{ in, want string }{
		{"feature/US-034-rate-limit", "US-034"},
		{"fix US-007 and move on", "US-007"},
		{"US-12", ""}, // pattern demands 3 digits
		{"main", ""},  // no unit here
		{"", ""},      //
	}
	for _, c := range cases {
		if got := s.MatchUnit(c.in); got != c.want {
			t.Errorf("MatchUnit(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// MatchUnit compiles lazily when the regexp was not cached by Validate — a Spec built by
	// hand rather than loaded must still work rather than silently matching nothing.
	bare := &Spec{Unit: Unit{Pattern: `TCK-\d+`}}
	if got := bare.MatchUnit("branch/TCK-9"); got != "TCK-9" {
		t.Errorf("hand-built spec: MatchUnit = %q, want TCK-9", got)
	}
	broken := &Spec{Unit: Unit{Pattern: `TCK-(\d+`}}
	if got := broken.MatchUnit("TCK-9"); got != "" {
		t.Errorf("an uncompilable pattern must yield no match, got %q", got)
	}
}

func TestFindWalksUpToTheRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "server", "internal", "handlers")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Find(deep)
	if err != nil {
		t.Fatalf("Find from a nested directory: %v", err)
	}
	if want := filepath.Join(root, Filename); got != want {
		t.Errorf("Find = %q, want %q", got, want)
	}

	// No spec anywhere up the tree is an error naming the file, not a nil spec.
	if _, err := Find(t.TempDir()); err == nil {
		t.Error("Find must fail when no spec exists in any parent")
	} else if !strings.Contains(err.Error(), Filename) {
		t.Errorf("the error should name %s; got %v", Filename, err)
	}
}

func TestArtifactSubstitutesTheUnitID(t *testing.T) {
	s := mustLoad(t, valid)
	p, ok := s.Artifact("planning", "US-034")
	if !ok {
		t.Fatal("planning artifact is declared")
	}
	if !strings.HasSuffix(filepath.ToSlash(p), "docs/planning/US-034.md") {
		t.Errorf("artifact path = %q", p)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("artifact paths resolve against Root, got a relative %q", p)
	}
	if _, ok := s.Artifact("nonesuch", "US-034"); ok {
		t.Error("an undeclared artifact kind must report not-found")
	}
}

func TestClosingPhaseAndLookups(t *testing.T) {
	s := mustLoad(t, valid)
	c, ok := s.ClosingPhase()
	if !ok || c.ID != "close" {
		t.Fatalf("ClosingPhase = %+v (ok=%v), want the close phase", c, ok)
	}
	if _, ok := s.Phase("build"); !ok {
		t.Error("Phase(build) should be found")
	}
	if _, ok := s.Phase("nope"); ok {
		t.Error("Phase(nope) should not be found")
	}

	// A spec with no requires_verdict gates nothing, and must say so rather than guessing.
	nogate := mustLoad(t, `
version: 1
project: p
unit: {name: T, pattern: 'T-\d+'}
phases: [{id: build}]
`)
	if _, ok := nogate.ClosingPhase(); ok {
		t.Error("no phase requires a verdict; ClosingPhase must report none")
	}
}

func TestDefaultsAndPredicates(t *testing.T) {
	s := mustLoad(t, valid)
	if s.ReportOnly() {
		t.Error("enforcement is unset, which means enforce — not report")
	}
	if !s.Budget.Set() {
		t.Error("tokens_per_run: 1000 is a declared ceiling")
	}
	if (Budget{}).Set() {
		t.Error("a budget with no ceilings must report unset, not zero-valued")
	}
	if s.Capabilities.GraphEnabled() {
		t.Error("no graph provider is declared")
	}
	if (Capabilities{Graph: GraphCap{Provider: "none"}}).GraphEnabled() {
		t.Error(`provider "none" is explicitly disabled, not enabled`)
	}
	if !(Capabilities{Graph: GraphCap{Provider: "graphify"}}).GraphEnabled() {
		t.Error("graphify is a provider")
	}
	if !(Capabilities{Compression: CompressionCap{Provider: "headroom"}}).CompressionEnabled() {
		t.Error("headroom is a compression provider")
	}
	if !(Gate{Evidence: "required"}).EvidenceRequired() {
		t.Error("evidence: required means required")
	}

	report := mustLoad(t, strings.Replace(valid, "project: acme", "project: acme\nenforcement: report", 1))
	if !report.ReportOnly() {
		t.Error("enforcement: report must be report-only")
	}
}

func TestMalformedYAMLNamesTheFile(t *testing.T) {
	_, err := load(t, "version: 1\nproject: [unclosed\n")
	if err == nil {
		t.Fatal("malformed YAML must not load")
	}
	if !strings.Contains(err.Error(), Filename) {
		t.Errorf("the error must name the file that failed; got %v", err)
	}
}

// A key the struct does not have must LOAD (a spec written for a newer batten has to work on an
// older one) and must be REPORTED (a key that is declared and never read is the failure this tool
// exists to eliminate).
func TestUnknownKeysLoadAndAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	stale := valid + "\nprovenance:\n  format: '{id} @ {git_sha7}'\nmodels:\n  tiers:\n    moderate: sonnet\n"
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("an unknown key must not stop the spec from loading: %v", err)
	}
	got := UnknownKeys(path)
	want := []string{"models", "provenance"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("UnknownKeys = %v, want %v (sorted, so doctor's output is diffable)", got, want)
	}

	// The migration path for `resources:`, removed in plan_publicacion.md §7. A spec that still carries it must
	// keep LOADING — batten does not brick a repo over a key it stopped reading — and must be told
	// the key is dead. Silence here would be the removal's own version of the failure the removal
	// was for: the user goes on believing the block does something.
	withResources := filepath.Join(dir, "resources.yaml")
	if err := os.WriteFile(withResources, []byte(valid+
		"\nresources:\n  gpu:\n    kind: exclusive_pool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(withResources); err != nil {
		t.Fatalf("a spec carrying the removed `resources:` block must still load: %v", err)
	}
	if got := UnknownKeys(withResources); len(got) != 1 || got[0] != "resources" {
		t.Errorf("UnknownKeys = %v, want [resources]: the block was removed because nothing "+
			"arbitrated it, and a user still declaring it has to be told", got)
	}

	clean := filepath.Join(dir, "clean.yaml")
	if err := os.WriteFile(clean, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := UnknownKeys(clean); len(got) > 0 {
		t.Errorf("a clean spec must report nothing; got %v", got)
	}
	if got := UnknownKeys(filepath.Join(dir, "does-not-exist.yaml")); got != nil {
		t.Errorf("a missing file is not an unknown key; got %v", got)
	}
}

// Every spec THIS repo ships. `provenance.format` and `models.*` were removed from the struct and
// from batten.schema.json in one commit and left behind in batten.yaml and two of the examples —
// so the schema rejected the project's own spec, CI's schema job went red, and `batten doctor`
// said everything was fine. The published schema and the shipped examples are the first thing a
// new user meets; they cannot disagree.
func TestTheSpecsThisRepoShipsHaveNoDeadKeys(t *testing.T) {
	root := repoRoot(t)
	paths := []string{filepath.Join(root, Filename)}
	found, err := filepath.Glob(filepath.Join(root, "examples", "*", Filename))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, found...)
	if len(paths) < 2 {
		t.Fatal("no examples found; this test is looking in the wrong place")
	}

	for _, p := range paths {
		if unknown := UnknownKeys(p); len(unknown) > 0 {
			rel, _ := filepath.Rel(root, p)
			t.Errorf("%s declares keys batten does not read: %s — batten.schema.json rejects them, "+
				"so an editor calls this file invalid while `batten doctor` calls it valid",
				filepath.ToSlash(rel), strings.Join(unknown, ", "))
		}
	}
}

// Finding #1 from the field test, which shipped as a Known gap: `enforcment: report` (one letter
// missing) made doctor print a green "enforcement: enforce — gates block" and exit 0. The spec
// loads either way; what changes is whether anyone is told. A typo in the key that decides whether
// gates BLOCK is the most expensive silent default in the file.
func TestATypoInAKeyIsNamed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	typo := strings.Replace(valid, "project: acme", "project: acme\nenforcment: report", 1)
	if err := os.WriteFile(path, []byte(typo), 0o644); err != nil {
		t.Fatal(err)
	}

	sp, err := Load(path)
	if err != nil {
		t.Fatalf("the spec must still load: %v", err)
	}
	if sp.ReportOnly() {
		t.Fatal("the misspelled key must NOT take effect — that is the whole danger")
	}
	got := UnknownKeys(path)
	if len(got) != 1 || got[0] != "enforcment" {
		t.Errorf("UnknownKeys = %v, want [enforcment]: the key that decides whether gates block "+
			"cannot be ignored in silence", got)
	}
}
