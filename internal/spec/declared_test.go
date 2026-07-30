package spec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
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
// promise batten does not keep. Every entry needs a reason.
//
// "No consumer" is the usual reason and it is not the only one. `Budget.MaxIterations` sat here
// while TWO surfaces read it — MCP returned it and the TUI DREW it as `iters %d/%d` — because
// nothing INCREMENTED the counter or refused to go past the ceiling. Displaying a promise is not
// keeping it; it is the same lie told with more confidence. It came off the list when
// `batten iterate` started counting and `batten phase` started refusing.
//
// It is EMPTY, and that is the state it was built to reach. Sixteen entries, then seven, now none:
// every field batten.yaml accepts has something in production that reads it.
//
// The seven went out by both exits, which is the point — "wire it up" was never the only honest
// answer:
//
//   - Phase.When and Domain.Coverage were WIRED. Both are advisory by contract, and advisory is
//     not the same as unread: the phase briefing at SessionStart now prints the condition and the
//     floors to the agent standing in the phase, which is the only reader such a field could have.
//     Domain.Coverage carries an exit condition rather than a promise — if two review cycles pass
//     with no verdict citing a floor, it comes out of the spec.
//   - Resource.{Kind,Probe,Unit,Priority} and Domain.Resources were REMOVED. The schema said in as
//     many words that "the orchestrator runs it BEFORE launching and queues", and batten does not
//     orchestrate — the same argument that removed models.{tiers,phases}. Four fields promising
//     serialization that nothing serializes was the largest single lie the spec told. Note the
//     honest wrinkle in Domain.Resources: it DID have a reader, the spec package's own referential
//     check, and that is exactly what this guard means by "declaring is not consuming".
//
// Before them, models.{tiers,phases} and provenance.format left the same way, and Unit.Locator and
// ObsidianCap.Export left by being implemented.
//
// Keep it empty. A new entry is a decision to publish a promise batten does not keep, and this list
// is DEBT, not a parking lot — it should only ever get shorter, and it has nowhere shorter to go.
var declaredAsFuture = map[string]string{}

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

// ---------- the third half: pointers into documents ----------

// TestNoCommentPointsAtADocumentThatIsNotHere.
//
// The rule, and it is one line: a tracked file may not cite a section of a document a clone does
// not receive.
//
// It exists because this project has now made the same mistake four times. Comments cited bare
// section numbers of a planning document; that document was retired; the numbers stayed. At best
// the reader had nowhere to go. At worst — and this is what makes it a defect rather than
// untidiness — the document that REPLACED it had its own sections 7, 4.1 and 9 about completely
// different things, so a bare number silently re-pointed itself at unrelated prose and whoever
// followed it came away confidently misinformed. A dead link stops you; a live wrong one does not.
//
// The fourth time was the working documents leaving the tree: thirty-five comments cited them by
// section, and every one had to become the reason it was standing in for. That is the honest end
// state anyway. A comment that says WHY needs no pointer; one that only points is not a comment.
//
// Deliberately not a whitelist of known documents. The check is whether the cited file is in the
// tree, so a document added tomorrow is citable the moment it is committed and stops being citable
// the moment it is not — which is the property that keeps rotting, and the only one worth
// mechanising.
//
// The examples in this comment are spelled "section N" rather than with the sigil, the same
// discipline as the masked pattern in TestNoPrivateProjectTokensAreTracked: a guard whose own
// prose trips it teaches whoever hits that failure to add an exclusion, and the exclusion is what
// blinds it. This file is IN scope, like every other.
func TestNoCommentPointsAtADocumentThatIsNotHere(t *testing.T) {
	root := repoRoot(t)
	checked := 0

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
		switch {
		case strings.HasSuffix(p, ".go"), strings.HasSuffix(p, ".sh"), strings.HasSuffix(p, ".ps1"):
		default:
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		checked++
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)

		for _, loc := range docRefRe.FindAllStringSubmatchIndex(string(src), -1) {
			doc := string(src[loc[2]:loc[3]])
			sec := string(src[loc[4]:loc[5]])
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")

			// Anywhere in the tree, because a doc can legitimately live in docs/, at the root, or
			// beside the code it describes. What matters is that a clone receives it.
			if !fileIsTracked(root, doc) {
				t.Errorf("%s:%d cites %s section %s, and git does not track %s.\n"+
					"    A clone does not get that document, so the pointer is dead on arrival — or\n"+
					"    worse, re-points itself at whatever takes that section number next.\n"+
					"    State the reason inline instead: a comment that says WHY needs no pointer.",
					rel, line, doc, sec, doc)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("read no source files at all; this guard would pass vacuously")
	}
}

// fileIsTracked reports whether git tracks a file with this base name anywhere in the repository.
//
// git, not the filesystem, and that distinction is the entire guard. The first version walked the
// working tree and passed happily on the author's machine, where the retired documents are still
// sitting on disk — untracked, invisible to every clone, and therefore exactly the dead pointers
// this test exists to catch. It reported green while the property was false for everyone else.
//
// The question a reference has to answer is "does a clone receive this", and only the index knows.
func fileIsTracked(root, name string) bool {
	git, err := exec.LookPath("git")
	if err != nil {
		return true // cannot tell; do not invent a failure
	}
	out, err := exec.Command(git, "-C", root, "ls-files", "--", "*/"+name, name).Output()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}

// docRefRe matches a citation of a named markdown document by section: the file name, a space,
// the section sigil, the number. Both halves are required — a bare section number has nothing to
// check, and a bare filename is a mention rather than a pointer.
//
// No literal example is written above, and that is not fussiness: the first version spelled one
// out and this guard failed on its own documentation, which is the third time a guard in this
// repository has tripped over its own prose. The other two masked their pattern; this one omits
// it. The lesson each time is the same — the fix a reader reaches for when a guard fails on
// itself is an exclusion, and the exclusion is what blinds it.
var docRefRe = regexp.MustCompile(`([A-Za-z0-9_.-]+\.md) §(\d+(?:\.\d+)?)`)

// planHeadingRe matches the plan's numbered headings: `## 4. ...` and `### 4.2 — ...`.
var planHeadingRe = regexp.MustCompile(`(?m)^#{2,3} (\d+(?:\.\d+)?)[ .]`)

// sectionRefRe matches a section reference. The lookbehind Go lacks is done by the caller, which
// checks the bytes immediately before the match.
var sectionRefRe = regexp.MustCompile(`§(\d+(?:\.\d+)?)`)

// ---------- the other half: the unattended run's rules ----------

// The same idea applied to /batten-night, which is the most dangerous command batten ships.
//
// Its four absolute rules used to be 112 lines of prose asking the model to behave — the loudest
// possible example of batten not taking its own medicine, since its whole thesis is that a rule a
// document can only ask for, a hook can impose. All four are mechanisms now, and this
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
