package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/gitx"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// A spec whose closing gate declares checks, so the merge gate has both halves to demand: the
// mechanical pass (batten ran the checks) and somebody's judgement against the criteria.
const worktreeSpec = "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
	"enforcement: enforce\n" +
	"phases:\n  - id: build\n    anchor: git_sha\n  - id: close\n    gate: qa\n    requires_verdict: ok\n" +
	"gates:\n  qa:\n    checks:\n      - \"echo checks ran\"\n"

// worktreeFixture is a git repo with a spec, a database inside it, and the environment `load()`
// reads. Returns the repo dir.
func worktreeFixture(t *testing.T) string {
	t.Helper()
	dir := gitRepoWithSpec(t, worktreeSpec)
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))
	t.Cleanup(func() {
		// Leave no worktrees registered against a repo that is about to be deleted.
		_ = exec.Command("git", "-C", dir, "worktree", "prune").Run()
	})
	return dir
}

func runOK(t *testing.T, dir string, fn func() error) string {
	t.Helper()
	var err error
	out := captureStdout(t, func() { inDir(t, dir, func() { err = fn() }) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return out
}

// TestTheMergeBackIsGatedLikeACommit is the half of §5.4 that is batten's business rather than
// git's. A worktree per unit is a git feature; refusing to integrate one whose unit has no
// verdict is the commit gate applied at the other end of the same rope.
func TestTheMergeBackIsGatedLikeACommit(t *testing.T) {
	dir := worktreeFixture(t)
	runOK(t, dir, func() error { return cmdWorktree([]string{"TASK-9"}) })

	wt := ""
	inDir(t, dir, func() {
		trees, err := gitx.Worktrees(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range trees {
			if !gitx.SameTree(w.Path, dir) {
				wt = w.Path
			}
		}
	})
	if wt == "" {
		t.Fatal("no linked worktree was created")
	}
	defer func() { _ = exec.Command("git", "-C", dir, "worktree", "remove", "--force", wt).Run() }()

	// Real work, committed on the unit's branch.
	if err := os.WriteFile(filepath.Join(wt, "seed.txt"), []byte("TASK-9 did this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "TASK-9"}} {
		if out, err := exec.Command("git", append([]string{"-C", wt}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	// No verdict yet: the merge must be refused, and must say the tree is untouched. A refusal
	// that leaves the caller unsure whether anything moved is worse than no refusal.
	var err error
	out := captureStdout(t, func() {
		inDir(t, dir, func() { err = cmdWorktree([]string{"TASK-9", "--merge"}) })
	})
	if err == nil {
		t.Fatalf("a worktree with no verdict at all was merged:\n%s", out)
	}
	if !strings.Contains(err.Error(), "untouched") {
		t.Errorf("the refusal must say nothing was integrated: %v", err)
	}
	if head := headOf(t, dir); strings.Contains(readFile(t, filepath.Join(dir, "seed.txt")), "TASK-9") {
		t.Fatalf("the refusal still merged the work (HEAD %s)", head)
	}

	// Satisfy the gate the way a real run does: batten runs the checks, and a reviewer cites
	// evidence. Both, from two producers — one is not a gate.
	runOK(t, wt, func() error { return cmdCheck([]string{"TASK-9"}) })
	vf := filepath.Join(t.TempDir(), "v.json")
	if err := os.WriteFile(vf, []byte(`{"gate":"qa","check_id":"criteria","result":"ok",`+
		`"why":"meets AC-1","evidence":["seed.txt: rewritten"],"unit":"TASK-9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runOK(t, wt, func() error { return cmdVerdict([]string{"--file", vf}) })

	out = runOK(t, dir, func() error { return cmdWorktree([]string{"TASK-9", "--merge"}) })
	if !strings.Contains(out, "merged") {
		t.Fatalf("the gate was satisfied and the merge did not happen:\n%s", out)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "seed.txt")), "TASK-9") {
		t.Error("the merge reported success and the work is not in this tree")
	}
}

// TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn.
//
// Found by running the merge, not by reading it. `batten check` fingerprints the working tree it
// ran in — with a worktree per unit that is the UNIT's tree — and the gate was asking from
// whichever tree the caller happened to be standing in. So a perfectly healthy run, checked and
// reviewed minutes earlier, was refused with MOVED BASE: batten was comparing the verdict's
// fingerprint against a different checkout entirely and reporting the difference as history
// moving underneath.
//
// "batten-verified" means verified about THIS. The question has to be asked about the tree the
// answer was given in.
func TestTheGateAsksAboutTheTreeTheVerdictWasMadeIn(t *testing.T) {
	dir := worktreeFixture(t)
	runOK(t, dir, func() error { return cmdWorktree([]string{"TASK-8"}) })

	trees, err := gitx.Worktrees(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt := ""
	for _, w := range trees {
		if !gitx.SameTree(w.Path, dir) {
			wt = w.Path
		}
	}
	if wt == "" {
		t.Fatal("no linked worktree")
	}
	defer func() { _ = exec.Command("git", "-C", dir, "worktree", "remove", "--force", wt).Run() }()

	// Verify inside the unit's tree, which is where the work is.
	runOK(t, wt, func() error { return cmdCheck([]string{"TASK-8"}) })
	vf := filepath.Join(t.TempDir(), "v.json")
	if err := os.WriteFile(vf, []byte(`{"gate":"qa","check_id":"criteria","result":"ok",`+
		`"why":"fine","evidence":["seed.txt"],"unit":"TASK-8"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runOK(t, wt, func() error { return cmdVerdict([]string{"--file", vf}) })

	// Now move the OTHER tree's history, which has nothing to do with this unit.
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "unrelated"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	st, err := store.Open(os.Getenv("BATTEN_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run, err := st.LatestRun("p", "TASK-8")
	if err != nil {
		t.Fatal(err)
	}
	if run.Worktree == "" {
		t.Fatal("the run was never bound to its worktree")
	}

	var gateErr error
	inDir(t, dir, func() {
		sp, st2, err := load()
		if err != nil {
			t.Fatal(err)
		}
		defer st2.Close()
		r, err := st2.LatestRun(sp.Project, "TASK-8")
		if err != nil {
			t.Fatal(err)
		}
		gateErr = gateReadyToClose(sp, st2, r)
	})
	if gateErr != nil && strings.Contains(gateErr.Error(), "MOVED BASE") {
		t.Errorf("the gate compared TASK-8's verdict against a tree the verdict was never about, "+
			"and called an unrelated commit in another worktree a moved base:\n%v", gateErr)
	}
	if gateErr != nil {
		t.Errorf("the gate refused a run that is verified in its own tree: %v", gateErr)
	}
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitx.Output(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}
