package main

// `batten worktree` — closing the loop batten opened in three separate messages.
//
// The strongest argument for this is not the literature. It is that batten ALREADY prescribes it
// and does not do it: three different denials tell the user to use a worktree per unit, and then
// leave them to run the git by hand. Diagnosing a problem, naming the fix and refusing to perform
// it is the same gap between declared and done as a dead spec field, wearing different clothes.
//
// TWO SCOPE DECISIONS, both deliberate:
//
// PER UNIT, NOT PER SUBAGENT. Isolating the fanned-out agents of ONE unit from each other breaks
// the fan-out: they work on disjoint write-sets of the SAME tree — that is the premise of the
// design — and giving each its own tree means none sees the others' work, plus N merges for one
// work item. The isolation that is needed is between CONCURRENT UNITS, which is where the
// conflict is real and is what those three messages already say.
//
// BATTEN DOES NOT ORCHESTRATE. It does not launch subagents; Claude Code's Dynamic Workflows do,
// and they already have `isolation: "worktree"`. So this command creates and REGISTERS the tree
// and gates the way back in. It never decides who works where.
//
// The gate on the way back is the part that is batten's business and nobody else's: a worktree
// whose unit does not have both verdicts does not get merged. It is the commit gate, applied at
// the other end of the same rope.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/gitx"
	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func cmdWorktree(args []string) error {
	var unit, path, branch, from string
	var merge, remove, list bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--merge":
			merge = true
		case "--remove":
			remove = true
		case "--list":
			list = true
		case "--path", "--branch", "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("worktree: %s needs a value", a)
			}
			switch a {
			case "--path":
				path = args[i+1]
			case "--branch":
				branch = args[i+1]
			case "--from":
				from = args[i+1]
			}
			i++
		default:
			if strings.HasPrefix(a, "--") {
				return fmt.Errorf("worktree: unknown flag %q", a)
			}
			unit = a
		}
	}

	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	if !gitx.IsRepo(sp.Root) {
		return fmt.Errorf("worktree: %s is not a git repository, so there is nothing to branch a "+
			"tree from", sp.Root)
	}
	if list {
		return worktreeList(sp, st)
	}
	if unit == "" {
		return errors.New("worktree: batten worktree <unit> [--path p] [--branch b] [--from ref]\n" +
			"                batten worktree <unit> --merge     integrate it (gated)\n" +
			"                batten worktree <unit> --remove    take the tree down\n" +
			"                batten worktree --list")
	}
	if merge && remove {
		return errors.New("worktree: --merge and --remove do different things; run them in that order")
	}
	// Same rigor a phase id gets: creating a tree also opens a run, and a typo'd unit would
	// open a phantom one with exit 0 (#21). Merge/remove operate on what exists and resolve
	// by name anyway.
	if !merge && !remove && !sp.ValidUnitID(unit) {
		return fmt.Errorf("worktree: %q does not match unit.pattern %q — no tree was created", unit, sp.Unit.Pattern)
	}

	// Every git-level operation here writes into the area SHARED by all worktrees, so it is taken
	// under the one lock that is actually shared. See gitx.Lock for why the location matters.
	release, err := gitx.Lock(sp.Root)
	if err != nil {
		return err
	}
	defer release()

	switch {
	case merge:
		return worktreeMerge(sp, st, unit)
	case remove:
		return worktreeRemove(sp, st, unit)
	}
	return worktreeAdd(sp, st, unit, path, branch, from)
}

// specRootIn maps this repo's batten.yaml directory onto another working tree, so a spec that
// lives in a subdirectory is still found in the same place inside the new tree.
func specRootIn(sp *spec.Spec, tree string) string {
	top, err := gitx.TopLevel(sp.Root)
	if err != nil {
		return tree
	}
	rel, err := filepath.Rel(top, sp.Root)
	if err != nil || rel == "." {
		return tree
	}
	return filepath.Join(tree, rel)
}

func worktreeAdd(sp *spec.Spec, st *store.Store, unit, path, branch, from string) error {
	top, err := gitx.TopLevel(sp.Root)
	if err != nil {
		return err
	}
	if branch == "" {
		branch = unit
	}
	if path == "" {
		// A SIBLING of the repository, never a directory inside it. A worktree checked out under
		// the repo shows up in `git status` as an untracked mountain and lands in every diff scope
		// the unit computes — including the one `diff_from: anchor` reports.
		path = filepath.Join(filepath.Dir(top), filepath.Base(top)+"-"+unit)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// Already there? Say so and re-register rather than failing. This command will be run twice
	// by anyone resuming work, and the second run should be an answer, not an error.
	if existing := worktreeFor(sp, abs); existing != nil {
		fmt.Printf("%s already has a worktree at %s (branch %s)\n", unit, existing.Path, existing.Branch)
		return registerWorktree(sp, st, unit, existing.Path, "")
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("worktree: %s already exists and is not a worktree of this repository.\n"+
			"Pick another location with --path, or remove it yourself — batten does not delete "+
			"directories it did not create", abs)
	}

	base := from
	if base == "" {
		base = "HEAD"
	}
	// Resolve the base BEFORE creating anything: this commit is the unit's anchor, and reading it
	// after the fact would read the new branch's HEAD, which is the same thing today and not the
	// same thing after the first commit.
	baseSHA, err := gitx.Output(sp.Root, "rev-parse", "--short=7", base)
	if err != nil {
		return fmt.Errorf("worktree: cannot resolve %q to a commit: %w", base, err)
	}

	add := []string{"worktree", "add"}
	if branchExists(sp.Root, branch) {
		fmt.Printf("branch %s already exists — checking it out rather than starting it over\n", branch)
		add = append(add, abs, branch)
	} else {
		add = append(add, "-b", branch, abs, base)
	}
	if out, err := gitRun(top, add...); err != nil {
		return fmt.Errorf("worktree: git worktree add failed: %v\n%s", err, out)
	}

	fmt.Printf("%s -> worktree %s (branch %s, from %s)\n", unit, abs, branch, baseSHA)
	if err := registerWorktree(sp, st, unit, abs, baseSHA); err != nil {
		return err
	}

	// The warning has to be written down before starting, not after somebody hits it. graphify
	// commits a graph.json of about a megabyte; two branches that touch code conflict on it every
	// time, and worktrees turn "two branches touching code" from the rare case into the normal
	// one. doctor already checks for the union merge driver — with this, that check stops being a
	// courtesy.
	if sp.Capabilities.GraphEnabled() && !mergeDriverRegistered(sp.Root) {
		fmt.Println("\n⚠ the graphify merge driver is NOT registered in this repository.")
		fmt.Println("  graph.json is ~1 MB and regenerated on both branches, so the first merge back")
		fmt.Println("  will conflict on it. With worktrees that is the normal case, not the rare one.")
		fmt.Println("  Fix it before you have two: batten doctor")
	}
	fmt.Printf("\nwork there:   cd %s\n", abs)
	fmt.Printf("bring it back: batten worktree %s --merge   (denied unless the gate is satisfied)\n", unit)
	return nil
}

// registerWorktree binds the run to the tree, and anchors it where the tree branched.
//
// The anchor changing meaning is the second thing to write down: every worktree has its
// own HEAD, so `diff_from: anchor` becomes MORE correct in this model, not less — a unit's anchor
// is the point its tree diverged, which is exactly the diff a reviewer should be looking at.
func registerWorktree(sp *spec.Spec, st *store.Store, unit, tree, baseSHA string) error {
	run, err := st.EnsureRun(sp.Project, unit, "")
	if err != nil {
		return err
	}
	if err := st.SetWorktree(run.RunID, specRootIn(sp, tree)); err != nil {
		return err
	}
	if baseSHA != "" && run.BaseSHA == "" {
		if err := st.SetBaseSHA(run.RunID, baseSHA); err != nil {
			return err
		}
		fmt.Printf("anchor: %s base SHA = %s (where its tree diverged)\n", unit, baseSHA)
	}
	return nil
}

// worktreeMerge is the gate at the other end of the rope.
func worktreeMerge(sp *spec.Spec, st *store.Store, unit string) error {
	run, err := st.LatestRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no run recorded for %s", unit)
	}
	wt := worktreeOfRun(sp, run)
	if wt == nil {
		return fmt.Errorf("worktree: %s has no worktree registered. If you made one by hand, "+
			"register it with `batten worktree %s --path <dir>`", unit, unit)
	}
	here, _ := gitx.TopLevel(sp.Root)
	if gitx.SameTree(here, wt.Path) {
		return fmt.Errorf("worktree: you are standing IN %s's worktree. Merge from the tree you "+
			"want to merge INTO — a branch cannot integrate itself", unit)
	}
	if wt.Branch == "" {
		return fmt.Errorf("worktree: %s's tree at %s has a detached HEAD; there is no branch to "+
			"merge", unit, wt.Path)
	}

	// THE GATE. Exactly the condition the commit hook applies, so `batten worktree --merge` and a
	// gated `git commit` cannot disagree about what done means. This is the one decision in this
	// file that is batten's rather than git's.
	if err := gateReadyToClose(sp, st, run); err != nil {
		return fmt.Errorf("refusing to merge %s: %w\n"+
			"The worktree is untouched — nothing was integrated and nothing was lost", unit, err)
	}

	// Uncommitted work in EITHER tree, refused before anything moves. Merging out of a dirty
	// source silently leaves that work behind on a branch nobody will look at again; merging into
	// a dirty target mixes somebody else's half-finished edit into the integration commit.
	for _, t := range []struct{ what, root string }{{unit + "'s worktree", wt.Path}, {"this tree", here}} {
		dirty, what, derr := gitx.IsDirty(t.root, st.Path())
		if derr != nil {
			return fmt.Errorf("worktree: cannot read the state of %s: %w", t.root, derr)
		}
		if dirty {
			return fmt.Errorf("refusing to merge: %s has uncommitted work.\n  %s\n"+
				"Commit it or stash it — a merge would leave it behind without saying so", t.what, what)
		}
	}

	out, err := gitRun(here, "merge", "--no-ff", wt.Branch, "-m",
		fmt.Sprintf("Merge %s (%s) — batten-verified", unit, wt.Branch))
	if err != nil {
		return fmt.Errorf("worktree: the merge did not complete: %v\n%s\n"+
			"Resolve it here; batten's gate already passed, so this is git's conflict, not a verdict",
			err, out)
	}
	fmt.Printf("merged %s into %s\n", wt.Branch, currentBranch(here))
	fmt.Printf("the tree is still at %s — take it down with: batten worktree %s --remove\n", wt.Path, unit)
	return nil
}

func worktreeRemove(sp *spec.Spec, st *store.Store, unit string) error {
	run, err := st.LatestRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no run recorded for %s", unit)
	}
	wt := worktreeOfRun(sp, run)
	if wt == nil {
		return fmt.Errorf("worktree: %s has no worktree registered", unit)
	}
	here, _ := gitx.TopLevel(sp.Root)
	if gitx.SameTree(here, wt.Path) {
		return fmt.Errorf("worktree: you are standing in it. cd out of %s first", wt.Path)
	}
	// git refuses a dirty worktree on its own, and that refusal is correct — it is left in place
	// rather than forced, because --force here would be batten deleting work nobody asked it to.
	if out, err := gitRun(here, "worktree", "remove", wt.Path); err != nil {
		return fmt.Errorf("worktree: git would not remove %s: %v\n%s", wt.Path, err, out)
	}
	if err := st.SetWorktree(run.RunID, ""); err != nil {
		return err
	}
	fmt.Printf("removed %s. The branch %s is untouched — batten does not delete branches.\n",
		wt.Path, wt.Branch)
	return nil
}

func worktreeList(sp *spec.Spec, st *store.Store) error {
	trees, err := gitx.Worktrees(sp.Root)
	if err != nil {
		return err
	}
	byTree := map[string]store.Run{}
	if runs, err := st.OpenRuns(sp.Project); err == nil {
		for _, r := range runs {
			if r.Worktree != "" {
				byTree[filepath.Clean(r.Worktree)] = r
			}
		}
	}
	for i, w := range trees {
		who := "—"
		for tree, r := range byTree {
			if gitx.SameTree(specRootIn(sp, w.Path), tree) {
				who = r.UnitID
			}
		}
		kind := "worktree"
		if i == 0 {
			kind = "main" // `git worktree list` always puts the main tree first
		}
		fmt.Printf("%-8s %-40s %-20s %s\n", kind, w.Path, w.Branch, who)
	}
	return nil
}

// worktreeOfRun finds the live git worktree a run is registered against, or nil. It resolves
// through git rather than trusting the recorded string, so a tree somebody removed by hand
// reports as absent instead of as a path that no longer exists.
func worktreeOfRun(sp *spec.Spec, run *store.Run) *gitx.Worktree {
	if run.Worktree == "" {
		return nil
	}
	trees, err := gitx.Worktrees(sp.Root)
	if err != nil {
		return nil
	}
	for i := range trees {
		if gitx.SameTree(specRootIn(sp, trees[i].Path), run.Worktree) {
			return &trees[i]
		}
	}
	return nil
}

func worktreeFor(sp *spec.Spec, abs string) *gitx.Worktree {
	trees, err := gitx.Worktrees(sp.Root)
	if err != nil {
		return nil
	}
	for i := range trees {
		if gitx.SameTree(trees[i].Path, abs) {
			return &trees[i]
		}
	}
	return nil
}

func branchExists(root, branch string) bool {
	_, err := gitx.Output(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func currentBranch(root string) string {
	b, err := gitx.Output(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "HEAD"
	}
	return b
}

// mergeDriverRegistered reports whether the union merge driver graphify needs is configured.
func mergeDriverRegistered(root string) bool {
	_, err := gitx.Output(root, "config", "--get", "merge.graphjson.driver")
	return err == nil
}

// gitRun runs a git subcommand and returns its combined output, which is what a failed merge
// needs: git says the useful part on stderr.
func gitRun(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
