package spec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The guard against the tenth instance.
//
// Working the rounds of the improvement plan separately turned up what looked like nine
// unrelated defects and was one failure repeated nine times: batten DECLARES a governance
// capability and does not IMPOSE it. `query_before_read` has no consumer. `diff_from: anchor`
// has no consumer. `models.tiers` says in the schema that it "routes subagents and verifies it
// from the ledger" and routes nothing. `retry_of` has four readers and zero writers.
// `budget.max_iterations` is declared, is DRAWN in the TUI, and is never incremented.
//
// That is precisely the failure mode batten exists to remove from other people's workflows. The
// first line of the README says a rule a document can only ASK for, a hook can IMPOSE. Nine
// times, batten asked.
//
// It also explains how a field test found 52 defects in a project with a green suite: the tests
// verified that the code did what it does. Nothing verified that the spec promised only what the
// code does.
//
// So this file is the mechanism, and it is the one place in the codebase that does to batten
// what batten does to its users: it turns "do not declare what you have not implemented" from
// discipline into a denial. Add a field to batten.yaml's schema and you must either wire it up
// or write it down here, with a reason. What you can no longer do is add it and forget.

// declaredAsFuture is the explicit, deliberate list: fields batten.yaml accepts today whose
// promise batten does not keep. Every entry needs a reason and the plan section that owns it.
//
// "No consumer" is the usual reason and it is not the only one. `Budget.MaxIterations` sat here
// while TWO surfaces read it — MCP returned it and the TUI DREW it as `iters %d/%d` — because
// nothing INCREMENTED the counter or refused to go past the ceiling. Displaying a promise is not
// keeping it; it is the same lie told with more confidence. It came off the list when
// `batten iterate` started counting and `batten phase` started refusing.
//
// This list is DEBT, not a parking lot. It should get shorter. A new entry is a decision to
// publish a promise batten does not keep, and it should be made on purpose and in a review.
var declaredAsFuture = map[string]string{
	"GraphCap.QueryBeforeRead": "declares the agent should consult the code graph before reading " +
		"files; nothing enforces or even checks it (plan §5.3, §8)",
	"Phase.GraphQuery": "same promise at phase level; the fan-out prompt never mentions it " +
		"(plan §5.3, §8)",
	"Models.Tiers": "the schema says it 'routes subagents and verifies it from the ledger'. It " +
		"routes nothing: batten does not spawn subagents, so the routing it promises would have " +
		"to be advice to the orchestrator, which it never emits (plan §8)",
	"Models.Phases":     "same as Tiers, pinned per phase instead of per difficulty (plan §8)",
	"Provenance.Format": "provenance metadata for auditing; no writer, no reader (plan §8)",
	"Phase.When": "documented as a free-form, advisory condition. Advisory is its whole contract, " +
		"so it is honest — but nothing reads it either (plan §8)",
	"Resource.Kind":     "resource contention is declared and never arbitrated (plan §8)",
	"Resource.Probe":    "the command that would report free capacity is never run (plan §8)",
	"Resource.Unit":     "unit of the resource's capacity; unused with the rest of Resource (plan §8)",
	"Resource.Priority": "ordering when capacity is short; nothing serializes on it (plan §8)",
	"Domain.Coverage":   "a coverage floor per domain that no check enforces (plan §8)",
	"Domain.Resources":  "which resources a domain contends for; see Resource.* (plan §8)",
	"Unit.Locator": "how to find a unit's block inside the plan document. `batten init` writes it " +
		"from the backlog's real heading shape, and nothing reads it back (plan §8)",

	// THE TENTH INSTANCE, and this guard found it on the first run it ever made — which is the
	// argument for the guard, made by the guard.
	"ObsidianCap.Export": "declares WHICH exports the vault should write (runs | verdicts | " +
		"canvas). Nothing reads it: export.Run writes the note, the dashboards and the canvas " +
		"unconditionally, so a user who asks for `export: [canvas]` gets all three. `batten init` " +
		"writes the field into the generated batten.yaml, so the promise ships by default (plan §8)",
}

// enforcedBySpecValidation names the fields whose consumer IS the spec package: Load refuses a
// batten.yaml that gets them wrong. That is real enforcement — the strongest kind, since it
// happens before anything else runs — but it lives in the one package this guard excludes, on
// the principle that declaring a field is not consuming it. So they are named here, explicitly,
// rather than by relaxing the exclusion and letting every accessor mark its own field alive.
var enforcedBySpecValidation = map[string]string{
	"Spec.Version": "Validate rejects any version but 1",
	"Spec.Enforcement": "Validate rejects anything but enforce|report, and ReportOnly() is what " +
		"turns it into the gate's actual behaviour in hooks.gate()",
}

// TestEveryDeclaredFieldHasAConsumerOrIsDeclaredFuture is the guard itself.
func TestEveryDeclaredFieldHasAConsumerOrIsDeclaredFuture(t *testing.T) {
	root := repoRoot(t)
	names, paths := productionSelectors(t, root)
	declared := declaredFields(reflect.TypeOf(Spec{}), "Spec", map[reflect.Type]bool{})

	var orphans []string
	for _, f := range declared {
		if _, listed := declaredAsFuture[f.key]; listed {
			continue
		}
		if _, validated := enforcedBySpecValidation[f.key]; validated {
			continue
		}
		if f.container {
			// A struct-valued field is not read on its own; its leaves are. Reporting `models:`
			// as unconsumed alongside `models.tiers` says the same thing twice and buries the
			// leaf that is the actual promise.
			continue
		}
		if f.path != "" {
			// Precise: this field's parent hangs off Spec under exactly one name, so production
			// code reaching it must spell out the whole path (sp.Budget.MaxIterations).
			if paths[f.path] {
				continue
			}
		} else if names[f.field] {
			// Permissive: the parent lives inside a map or slice (a Domain, a Phase, a Gate),
			// so the field is reached through a loop variable whose type is not visible without
			// a full type-check. Matching the bare name can be fooled by a same-named field on
			// another type — which makes the guard MISS a dead field, never invent one.
			continue
		}
		orphans = append(orphans, f.key+"  (yaml: "+f.yaml+")")
	}

	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf(`batten.yaml declares %d field(s) that nothing in production reads:

    %s

This is the failure batten exists to eliminate in other people's workflows: declaring a
governance capability and not imposing it. A field in the schema is a promise to the user that
batten does something with it.

Two honest ways out, and no third:

  1. Wire it up — something in production must actually read it.
  2. Add it to declaredAsFuture in %s, with a reason and the plan section
     that owns it. That is a decision to ship a promise batten does not keep yet, so make it
     deliberately and in a review.`,
			len(orphans), strings.Join(orphans, "\n    "), "internal/spec/declared_test.go")
	}
}

// TestDeclaredAsFutureHasNoStaleEntries keeps the list from rotting the other way: a renamed or
// deleted field would sit here forever, quietly documenting a promise that no longer exists.
func TestDeclaredAsFutureHasNoStaleEntries(t *testing.T) {
	declared := map[string]bool{}
	for _, f := range declaredFields(reflect.TypeOf(Spec{}), "Spec", map[reflect.Type]bool{}) {
		declared[f.key] = true
	}
	for key := range declaredAsFuture {
		if !declared[key] {
			t.Errorf("declaredAsFuture names %q, which is not a field of the spec any more. "+
				"Remove the entry.", key)
		}
	}
	for key, why := range declaredAsFuture {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%q is on the list with no reason. An unexplained entry is how a deliberate "+
				"decision decays into an oversight.", key)
		}
	}
}

// A guard for the OTHER end of the list — striking an entry off once its field is wired up — was
// written and then deleted, and the reason is worth keeping so it is not rebuilt.
//
// It flagged an entry as soon as any production selector reached the field, and its first run
// demanded the removal of `Budget.MaxIterations`: mcp.go returns it and the TUI DRAWS it as
// `iters %d/%d`. Both read it. Neither honours it — nothing increments the counter and nothing
// refuses to go past the ceiling. So "has a reader" and "keeps its promise" are different
// questions, and a mechanical check that cannot tell them apart would have demanded the removal
// of the single worst entry on the list. The line above ("nothing reads it YET") was the part
// that was wrong; it now says what the list actually holds.

type declaredField struct {
	key   string // "GraphCap.QueryBeforeRead" — how declaredAsFuture names it
	field string // "QueryBeforeRead" — the bare Go selector
	path  string // "Graph.QueryBeforeRead" — the two-level path, when the parent is a plain
	//              struct field of its owner. Empty when the parent lives in a map or slice.
	yaml      string // "query_before_read" — what the user writes in batten.yaml
	container bool   // holds further declared fields rather than a value of its own
}

// declaredFields walks the spec's type graph and returns every field a batten.yaml can set.
// Unexported fields and `yaml:"-"` are skipped: they are not part of the declared surface.
//
// owner is the Go field name this struct hangs off (Budget, Graph, Unit…), or "" when it was
// reached through a map or a slice. It is what makes the precise path possible: a Budget is
// reachable only as `<something>.Budget`, so `.Budget.MaxIterations` is unambiguous, while a
// Domain arrives as a range variable and cannot be qualified without a full type-check.
func declaredFields(t reflect.Type, name string, seen map[reflect.Type]bool) []declaredField {
	return declaredFieldsFrom(t, name, "", seen)
}

func declaredFieldsFrom(t reflect.Type, name, owner string, seen map[reflect.Type]bool) []declaredField {
	indirect := false
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
		indirect = true
	}
	if indirect {
		owner = "" // reached through a container: the base expression is a loop variable
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return nil
	}
	seen[t] = true

	var out []declaredField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported: not declarable
		}
		tag := f.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		yamlName := strings.Split(tag, ",")[0]
		if yamlName == "" {
			yamlName = strings.ToLower(f.Name)
		}
		path := ""
		if owner != "" {
			path = owner + "." + f.Name
		}
		out = append(out, declaredField{
			key:       name + "." + f.Name,
			field:     f.Name,
			path:      path,
			yaml:      yamlName,
			container: holdsDeclaredFields(f.Type),
		})
		out = append(out, declaredFieldsFrom(f.Type, typeName(f.Type, f.Name), f.Name, seen)...)
	}
	return out
}

// holdsDeclaredFields reports whether a field is a container of further declared fields
// (Budget, map[string]Domain, []Phase) rather than a value the user sets directly.
func holdsDeclaredFields(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

func typeName(t reflect.Type, fallback string) string {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if n := t.Name(); n != "" {
		return n
	}
	return fallback
}

// productionSelectors returns every field name selected anywhere in production Go code.
//
// It parses rather than greps, on purpose: a grep counts `// models.tiers routes subagents` in a
// comment, and the YAML template `batten init` prints, as consumers. Those are the two places a
// dead field is MOST likely to appear, because writing the promise down is exactly what happened
// instead of implementing it.
//
// It returns two views. names is every selector name seen. paths is every two-segment selector
// chain (`x.Budget.MaxIterations` yields "Budget.MaxIterations"), which is what makes the check
// exact for the top-level config blocks — a Budget is only ever reached as `.Budget`.
//
// Known limit, stated rather than hidden: for the types that live inside maps and slices —
// Domain, Phase, Gate, Resource — the field arrives through a range variable, so only the bare
// name is available and a same-named field on an unrelated type will satisfy it. That makes the
// guard permissive there, never strict: it can miss a dead field, it cannot invent one. Getting
// it exact needs a full type-check of the module, which costs a dependency this repo does not
// otherwise carry.
func productionSelectors(t *testing.T, root string) (names, paths map[string]bool) {
	t.Helper()
	names, paths = map[string]bool{}, map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "graphify-out", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		// The spec package DECLARES the fields; declaring is not consuming. Constructors,
		// validators and accessors here would otherwise mark every field alive and the guard
		// would pass on an entirely dead schema.
		if filepath.Dir(p) == filepath.Join(root, "internal", "spec") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0) // 0: comments dropped, which is the point
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			s, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			names[s.Sel.Name] = true
			if inner, ok := s.X.(*ast.SelectorExpr); ok {
				paths[inner.Sel.Name+"."+s.Sel.Name] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(names) == 0 {
		t.Fatalf("found no production selectors under %s — the guard would pass vacuously", root)
	}
	return names, paths
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // internal/spec
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("could not locate the repo root from %s: %v", wd, err)
	}
	return root
}

// ---------- the other half: the unattended run's rules ----------

// The same idea applied to /batten-night, which is the most dangerous command batten ships.
//
// Its four absolute rules used to be 112 lines of prose asking the model to behave — the loudest
// possible example of batten not taking its own medicine, since its whole thesis is that a rule a
// document can only ask for, a hook can impose. All four are mechanisms now (plan §5.6), and this
// file is what keeps them that way: a rule may be listed as mechanical only if the identifier its
// mechanism is built on is actually USED in production, not merely declared. That is the exact
// distinction the rest of this file exists to enforce, applied to the enforcement itself.
//
// A FIFTH rule still cannot be added to the command by writing it down: it needs a mechanism here,
// or a conscious entry on the prose list below.
var nightRulesMechanical = map[string]string{
	"1": "CodeUnattendedDelete",   // PreToolUse/Bash denies rm, reset --hard, restore, clean, truncation
	"2": "CodeIterationCeiling",   // `batten iterate` counts; `batten phase` refuses past the ceiling
	"3": "CodeUnattendedOverride", // `batten override` refuses while mode='unattended'
	"4": "CodeUnattendedCommit",   // the verdict gate refuses a commit, verdicts or no verdicts
}

// nightRulesStillProse is the escape hatch, and it is EMPTY. An entry here is a decision to ship
// an absolute rule for an unsupervised run that nothing enforces.
var nightRulesStillProse = map[string]string{}

var nightRuleRe = regexp.MustCompile(`(?m)^\*\*(\d+)\.\s+(.+?)\*\*`)

func TestEveryUnattendedRuleIsMechanicalOrRegisteredAsProse(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "plugin", "claude-code", "commands", "batten-night.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the unattended-run command is not in this tree: %v", err)
	}

	found := nightRuleRe.FindAllStringSubmatch(string(b), -1)
	if len(found) == 0 {
		t.Fatalf("could not parse any numbered absolute rule out of %s — either the command "+
			"changed shape or this guard is now blind to it", path)
	}

	used := productionIdentUses(t, root)
	for _, m := range found {
		num, text := m[1], strings.TrimSpace(m[2])
		ident, mechanical := nightRulesMechanical[num]
		if _, prose := nightRulesStillProse[num]; !mechanical && !prose {
			t.Errorf(`unattended rule %s (%q) is not accounted for.

/batten-night is the most dangerous command batten ships. A new absolute rule may not simply be
written down: either give it a mechanism and name that mechanism's identifier in
nightRulesMechanical, or register it in nightRulesStillProse with the mechanism it is waiting for.`,
				num, text)
			continue
		}
		// Declared is not implemented — the lesson of this entire file. A rule listed as
		// mechanical whose identifier is only DEFINED and never referenced is prose with a
		// constant next to it.
		if mechanical && used[ident] < 2 {
			t.Errorf(`unattended rule %s (%q) is listed as mechanical via %s, but that identifier
is used %d time(s) in production — a definition with no caller. The rule is still prose.`,
				num, text, ident, used[ident])
		}
	}
	for num := range nightRulesMechanical {
		if !ruleStated(found, num) {
			t.Errorf("nightRulesMechanical claims rule %s, which the command no longer states. "+
				"Remove it, or the list stops describing anything.", num)
		}
	}
	for num := range nightRulesStillProse {
		if !ruleStated(found, num) {
			t.Errorf("nightRulesStillProse registers rule %s, which the command no longer states. "+
				"Remove it, or the list stops describing anything.", num)
		}
		if _, both := nightRulesMechanical[num]; both {
			t.Errorf("rule %s is on both lists; it cannot be a mechanism and prose at once", num)
		}
	}
}

func ruleStated(found [][]string, num string) bool {
	for _, m := range found {
		if m[1] == num {
			return true
		}
	}
	return false
}

// productionIdentUses counts every identifier occurrence in production Go, so a constant that is
// declared and never referenced can be told apart from one that governs something. The
// declaration itself counts as one, which is why the threshold above is two.
func productionIdentUses(t *testing.T, root string) map[string]int {
	t.Helper()
	uses := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "graphify-out", "node_modules", "vendor", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				uses[v.Name]++
			case *ast.SelectorExpr:
				uses[v.Sel.Name]++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return uses
}
