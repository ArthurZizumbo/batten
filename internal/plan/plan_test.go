package plan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
)

// backlog is the exact shape the field-test replica generates and `batten init` derives the
// locator from — the format this parser exists to read, not an idealized one.
const backlog = `# Backlog

### US-001 — historia 1

Como usuario quiero la funcionalidad 1 para poder trabajar.

**Criterios de aceptacion**
- criterio A de US-001
- criterio B de US-001

### US-002 — historia 2

Como usuario quiero la funcionalidad 2.

**Criterios de aceptacion**
- criterio A de US-002

## Fase 2

Prose that belongs to a section, not to US-002.

### US-003: rate limit

- [ ] returns 429 over the limit
- [x] the header names the window
`

func TestParseSplitsTheBacklogIntoUnits(t *testing.T) {
	units, err := Parse(backlog, "### {id}", `US-\d{3}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 {
		ids := make([]string, len(units))
		for i, u := range units {
			ids[i] = u.ID
		}
		t.Fatalf("got %d units (%v), want 3", len(units), ids)
	}

	u1 := units[0]
	if u1.ID != "US-001" || u1.Title != "historia 1" || u1.Line != 3 {
		t.Errorf("unit 1 = %+v, want US-001 / historia 1 / line 3", u1)
	}
	if !strings.Contains(u1.Body, "criterio B de US-001") {
		t.Errorf("US-001's block lost its own criteria:\n%s", u1.Body)
	}
	if strings.Contains(u1.Body, "US-002") {
		t.Errorf("US-001's block leaked into US-002's:\n%s", u1.Body)
	}

	// The "## Fase 2" section break ends US-002's block even though no new unit starts there.
	if strings.Contains(units[1].Body, "Prose that belongs to a section") {
		t.Errorf("a same-or-shallower heading must end the unit block:\n%s", units[1].Body)
	}

	if units[2].ID != "US-003" || units[2].Title != "rate limit" {
		t.Errorf("unit 3 = %q %q, want US-003 / rate limit", units[2].ID, units[2].Title)
	}
}

func TestParseCriteria(t *testing.T) {
	units, _ := Parse(backlog, "### {id}", `US-\d{3}`)

	// The replica's shape: bullets under a bold **Criterios de aceptacion** line.
	if got, want := units[0].Criteria(), []string{"criterio A de US-001", "criterio B de US-001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("US-001 criteria = %v, want %v", got, want)
	}

	// The checkbox fallback: no criteria section at all, boxes anywhere in the block.
	if got, want := units[2].Criteria(), []string{"returns 429 over the limit", "the header names the window"}; !reflect.DeepEqual(got, want) {
		t.Errorf("US-003 criteria = %v, want %v", got, want)
	}

	// No section and no boxes yields nil — which consumers must report as "no criteria
	// declared", never as a satisfied empty list.
	solo, _ := Parse("### US-009 — bare\n\njust prose\n", "### {id}", `US-\d{3}`)
	if got := solo[0].Criteria(); got != nil {
		t.Errorf("a unit with no criteria must yield nil, got %v", got)
	}
}

// The locator is a template, not a decoration: a heading that carries the id in a DIFFERENT
// shape must not match, or the locator means nothing.
func TestParseHonorsTheLocatorShape(t *testing.T) {
	doc := "### US-001 — real\n\n#### US-002 — wrong level, must not match\n\nUS-003 in prose must not match\n"
	units, err := Parse(doc, "### {id}", `US-\d{3}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || units[0].ID != "US-001" {
		t.Errorf("units = %v, want exactly US-001 — the locator names ### and nothing else", units)
	}

	// And with no locator declared, any heading level counts (that is the degraded guess).
	loose, _ := Parse(doc, "", `US-\d{3}`)
	if len(loose) != 2 {
		t.Errorf("with no locator, both heading units should match; got %d", len(loose))
	}
}

// A unit.pattern carrying its own capture group must not derail the id extraction — the
// pattern is user input, and positional group indexing would silently read the wrong capture.
func TestParseSurvivesACapturingPattern(t *testing.T) {
	units, err := Parse("### US-007 — with groups\n", "### {id}", `US-(\d{3})`)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || units[0].ID != "US-007" {
		t.Errorf("units = %+v, want US-007", units)
	}
}

func TestLoadResolvesUnitPlanFromTheSpec(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "backlog.md"), []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	y := "version: 1\nproject: p\nunit:\n  name: US\n  pattern: 'US-\\d{3}'\n" +
		"  plan: docs/backlog.md\n  locator: '### {id}'\n" +
		"phases:\n  - id: build\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}

	units, err := Load(sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 {
		t.Errorf("Load found %d units, want 3", len(units))
	}
	if _, ok := Find(units, "US-002"); !ok {
		t.Error("Find(US-002) failed on a backlog that defines it")
	}

	// No plan declared is a DIFFERENT fact from an empty backlog, and gets its own error.
	sp.Unit.Plan = ""
	if _, err := Load(sp); err != ErrNoPlan {
		t.Errorf("Load with no unit.plan = %v, want ErrNoPlan", err)
	}
}
