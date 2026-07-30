package hooks

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// TestBashWriteTargetsFindsTheWaysAroundTheGuard.
//
// The bypass, reproduced: `Edit` on a claimed file is denied, and the same write through `sed -i`
// went through in silence. Every entry below is a way an agent can write a file without the tool
// batten watches.
func TestBashWriteTargetsFindsTheWaysAroundTheGuard(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"sed -i 's/a/b/' ml/train.py", []string{"ml/train.py"}},
		{"sed -i.bak -e 's/a/b/' a.go b.go", []string{"a.go", "b.go"}},
		{"sed --in-place 's/x/y/' api/routes.go", []string{"api/routes.go"}},
		{"echo hi > notes.md", []string{"notes.md"}},
		{"echo hi >notes.md", []string{"notes.md"}},        // glued, no space
		{"echo hi >> notes.md", []string{"notes.md"}},      // an append is still a write
		{"go build 2> errors.log", []string{"errors.log"}}, // a descriptor prefix
		{"cat x | tee api/routes.go", []string{"api/routes.go"}},
		{"cat x | tee -a a.txt b.txt", []string{"a.txt", "b.txt"}},
		{"mv old.go api/routes.go", []string{"api/routes.go"}},
		{"cp -r src/ dist/", []string{"dist/"}},
		{"dd if=/dev/zero of=disk.img bs=1M", []string{"disk.img"}},
		{"patch -p0 api/routes.go", []string{"api/routes.go"}},
		{"truncate -s 0 log.txt", []string{"log.txt"}},
		// Compound commands: each half is read.
		{"go test ./... && sed -i 's/a/b/' ml/train.py", []string{"ml/train.py"}},
		{"make build; echo done > out.txt", []string{"out.txt"}},
		// Env assignments are not the program.
		{"CGO_ENABLED=0 sed -i 's/a/b/' x.go", []string{"x.go"}},
	}
	for _, c := range cases {
		got := pathsOf(BashWriteTargets(c.cmd))
		if !sameSet(got, c.want) {
			t.Errorf("BashWriteTargets(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// The other half, and the reason this ships as a warning rather than a denial: a false positive
// here would block a legitimate agent on the critical path of every tool call.
func TestBashWriteTargetsStaysQuietOnReads(t *testing.T) {
	for _, cmd := range []string{
		"go test ./...",
		"sed 's/a/b/' file.go",  // no -i: a filter, writes nothing
		"sed -n '1,5p' file.go", // -n is not -i
		"cat a.txt | head -5",
		"grep -rn 'foo > bar' .", // the > is inside a quoted argument
		"echo 'x > y'",
		"npm run build 2>&1",        // a descriptor dup, not a file
		"go test ./... > /dev/null", // the bit bucket is not a file anyone owns
		"echo $OUT > $TARGET",       // unexpanded variables: batten does not know the path
		"git diff --name-only main",
		"mv only-one-arg", // no destination to name
	} {
		if got := BashWriteTargets(cmd); len(got) != 0 {
			t.Errorf("BashWriteTargets(%q) = %v, want none — a false positive here blocks real work",
				cmd, pathsOf(got))
		}
	}
}

// TestTheSedBypassIsSeenAtLast is the whole point, end to end. The same file, the same owner, the
// same session: denied through Edit and — until now — completely silent through the shell.
//
// What makes the silence worse than an ordinary fail-open: batten is not confused here. It named
// the owner in a denial one tool call earlier. It was not "I cannot determine blame", it was
// "I did not look".
func TestTheSedBypassIsSeenAtLast(t *testing.T) {
	h, root := guardFixture(t)
	r, err := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	owner := store.AgentNodeID(r.RunID, "agent-a")
	if err := h.Store.AddNode(store.Node{
		NodeID: owner, RunID: r.RunID, Kind: "subagent", Label: "a", AgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.ClaimWriteSet(r.RunID, owner, []string{"ml/train.py"}); err != nil {
		t.Fatal(err)
	}

	// The control first: through Edit, this is a hard denial.
	in := writeInputFor(root, "ml/train.py")
	in.AgentID = "agent-b"
	if err := h.Store.AddNode(store.Node{
		NodeID: store.AgentNodeID(r.RunID, "agent-b"), RunID: r.RunID,
		Kind: "subagent", Label: "b", AgentID: "agent-b",
	}); err != nil {
		t.Fatal(err)
	}
	out, _ := h.writeSetGuard(in, filepath.Join(root, "ml/train.py"))
	if decision(out) != "deny" {
		t.Fatalf("fixture is wrong: Edit on a claimed file was %q", decision(out))
	}

	// And now the same write, through the shell.
	bin := bashInputFor("sed -i 's/lr=0.1/lr=0.01/' ml/train.py", root)
	bin.AgentID = "agent-b"
	got := h.bashWriteGuard(bin, "sed -i 's/lr=0.1/lr=0.01/' ml/train.py")
	if got == nil {
		t.Fatal("`sed -i` on another agent's file went through in silence — the write-set guard " +
			"is one shell command away from being optional")
	}
	// ADVISORY, not a denial. That is deliberate for one cycle: this is a heuristic reading of
	// shell feeding a hard block on the critical path, and it gets measured before it gets teeth.
	if d := decision(got); d != "warn" {
		t.Errorf("the Bash write check is %q; it ships as a warning for one cycle so its false "+
			"positives can be counted first", d)
	}
	if !strings.Contains(reasonOf(got), "ml/train.py") || !strings.Contains(reasonOf(got), "sed -i") {
		t.Errorf("the warning does not name the file and how it would be written:\n%s", reasonOf(got))
	}
	if got.Rule != store.RuleBashWrite {
		t.Errorf("the warning is filed as %q, so `batten report` cannot count it separately — "+
			"and counting it is the entire purpose of the advisory cycle", got.Rule)
	}

	// The owner writing its OWN file through the shell must not be warned at.
	own := bashInputFor("sed -i 's/a/b/' ml/train.py", root)
	own.AgentID = "agent-a"
	if out := h.bashWriteGuard(own, "sed -i 's/a/b/' ml/train.py"); out != nil {
		t.Errorf("the file's owner was warned about writing it:\n%s", reasonOf(out))
	}
}

// TestBashPathsResolveAgainstTheCallersCwd: without it the check names
// the wrong file. A shell command says `sed -i s/a/b/ train.py` and means "relative to where this
// call is running"; resolving that against the repo root points at a file that does not exist,
// and a guard that warns about the wrong file is worse than one that says nothing.
func TestBashPathsResolveAgainstTheCallersCwd(t *testing.T) {
	h, root := guardFixture(t)
	r, _ := h.Store.EnsureRun("p", "TASK-1", "sess-a")
	owner := store.AgentNodeID(r.RunID, "agent-a")
	_ = h.Store.AddNode(store.Node{NodeID: owner, RunID: r.RunID, Kind: "subagent", AgentID: "agent-a"})
	_ = h.Store.ClaimWriteSet(r.RunID, owner, []string{"ml/train.py"})

	// The agent is standing in ml/ and names the file relatively.
	in := bashInputFor("sed -i 's/a/b/' train.py", filepath.Join(root, "ml"))
	in.AgentID = "agent-b"
	_ = h.Store.AddNode(store.Node{
		NodeID: store.AgentNodeID(r.RunID, "agent-b"), RunID: r.RunID,
		Kind: "subagent", AgentID: "agent-b",
	})
	if out := h.bashWriteGuard(in, "sed -i 's/a/b/' train.py"); out == nil {
		t.Fatal("a relative path was resolved against the repo root instead of the caller's cwd, " +
			"so the check looked for a file nobody owns")
	}

	// And a path outside the repo is nobody's business.
	out := bashInputFor("sed -i 's/a/b/' /etc/hosts", root)
	out.AgentID = "agent-b"
	if o := h.bashWriteGuard(out, "sed -i 's/a/b/' /etc/hosts"); o != nil {
		t.Errorf("a path outside the repository produced a warning:\n%s", reasonOf(o))
	}
}

func pathsOf(ts []BashTarget) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Path)
	}
	return out
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
