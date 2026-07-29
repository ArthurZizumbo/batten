package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The guard for the hole the OTHER guard cannot see.
//
// internal/spec/declared_test.go turns "do not declare what you have not implemented" into a
// failing test — for FIELDS of the spec. `retry_of` slipped past it for the whole life of the
// project because it is not a field: it is a VALUE of the `edges.rel` column. Five surfaces read
// it (`batten pr` twice — the retry badge and the dotted Mermaid edge — the canvas, the vault's
// Relations list, MCP's `retries` counter and the TUI's cross-edges) and nothing in the binary
// ever wrote one. The one property `batten pr` advertises as impossible for a plan diagram —
// showing a retry — rested on a row that only ever existed because a test inserted it by hand.
//
// Two lessons are baked in here rather than written down somewhere:
//
//  1. A field is not the only kind of promise. A column's vocabulary is one too.
//  2. Building a NEW reader on a value nothing writes makes it more dead, not less, while
//     looking exactly like progress — the canvas HTML and `batten pr` both shipped in the block
//     before this one and both read `retry_of`.
//
// So: every relation any production surface branches on must have a producer somewhere in
// production, or be registered below with the reason.

// relsReadWithoutProducer is the deliberate list, same contract as declaredAsFuture: an entry is
// a decision to ship a surface that can render a relation batten cannot produce.
//
// The guard found both entries it started with on its first run — the same thing that happened
// to the field guard — and one of them was fixed on the spot rather than listed: `depends_on` now
// chains a run's phases, which is what made the run graph an actual graph instead of a row of
// unconnected boxes.
var relsReadWithoutProducer = map[string]string{
	"rollback": "the canvas gives it a colour and batten has no rollback operation at all. " +
		"`runs.status` can be 'rolled_back', but nothing ever writes the EDGE, and there is no " +
		"command that would. Listed rather than deleted because the renderer is correct for the " +
		"day one exists; the tool description that promised rollbacks over MCP was removed, since " +
		"that one was read by the model as a fact about the data (plan §9, lifecycle)",
}

// TestEveryEdgeRelationReadHasAProducer is the guard.
func TestEveryEdgeRelationReadHasAProducer(t *testing.T) {
	root := repoRoot(t)
	read, written := edgeRelations(t, root)

	if len(read) == 0 {
		t.Fatal("found no code branching on an edge relation — the guard would pass vacuously")
	}
	if !written["spawn"] {
		t.Fatal("no producer for `spawn` was found at all; the AST scan is not seeing AddEdge calls")
	}

	var orphans []string
	for rel := range read {
		if written[rel] || relsReadWithoutProducer[rel] != "" {
			continue
		}
		orphans = append(orphans, rel)
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf(`%d edge relation(s) are READ by a surface and WRITTEN by nothing:

    %s

This is how `+"`retry_of`"+` survived: five readers, zero producers, and a headline feature resting on
a row no run ever creates. A renderer that can draw a relation batten cannot record is a promise
in exactly the sense internal/spec/declared_test.go exists to prevent — it is just not a field,
so that guard cannot see it.

  1. Write it: some production path must call AddEdge with this relation.
  2. Or register it in relsReadWithoutProducer, with the reason and the plan section.`,
			len(orphans), strings.Join(orphans, "\n    "))
	}

	for rel := range relsReadWithoutProducer {
		if written[rel] {
			t.Errorf("relsReadWithoutProducer still lists %q, which now has a producer. Strike it off.", rel)
		}
	}
}

// edgeRelations parses production Go and returns the relations that are branched on and the ones
// that are inserted.
//
// PRODUCERS are exact: the fourth argument of an AddEdge call, when it is a literal.
//
// READERS are heuristic, and the heuristic is stated rather than hidden. A relation is "read"
// when a string literal is compared against — or switched on through — either a selector named
// `Rel` (`e.Rel == "spawn"`) or a plain identifier named `rel` (canvas.go's
// `relColor(rel string)` switches on its parameter). That covers all six readers in the tree
// today. It can MISS a reader written some other way, which keeps the guard from inventing work;
// it cannot invent one, because a bare literal in a struct tag or a doc comment matches neither
// form — and both of those are exactly where a dead relation likes to hide (mcp.go's jsonschema
// tag lists all five relations and consumes none of them).
func edgeRelations(t *testing.T, root string) (read, written map[string]bool) {
	t.Helper()
	read, written = map[string]bool{}, map[string]bool{}
	fset := token.NewFileSet()

	isRelExpr := func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.SelectorExpr:
			return v.Sel.Name == "Rel"
		case *ast.Ident:
			return v.Name == "rel"
		}
		return false
	}
	lit := func(e ast.Expr) (string, bool) {
		b, ok := e.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(b.Value)
		return s, err == nil && s != ""
	}

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
		f, perr := parser.ParseFile(fset, p, nil, 0) // comments dropped, which is the point
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "AddEdge" && len(v.Args) == 4 {
					if s, ok := lit(v.Args[3]); ok {
						written[s] = true
					}
				}
			case *ast.BinaryExpr:
				if v.Op != token.EQL && v.Op != token.NEQ {
					return true
				}
				if isRelExpr(v.X) {
					if s, ok := lit(v.Y); ok {
						read[s] = true
					}
				}
				if isRelExpr(v.Y) {
					if s, ok := lit(v.X); ok {
						read[s] = true
					}
				}
			case *ast.SwitchStmt:
				if v.Tag == nil || !isRelExpr(v.Tag) {
					return true
				}
				for _, stmt := range v.Body.List {
					cc, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, e := range cc.List {
						if s, ok := lit(e); ok {
							read[s] = true
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return read, written
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // internal/store
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("could not locate the repo root from %s: %v", wd, err)
	}
	return root
}
