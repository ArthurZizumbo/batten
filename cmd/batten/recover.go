package main

// `batten recover` — re-anchor a run whose base moved.
//
// Idea from gentle-ai's `scope-changed` status (v2.1.11), which refuses to fail with "corrupt
// authority" and instead writes the exact recovery command the operator should run.
//
// The failure it addresses is ordinary: you rebase, you amend, you pull, a commit lands
// underneath — and the commit a run recorded as its anchor is no longer the commit the work sits
// on. Nothing is broken and nothing is lost, but every surface that reads the anchor is now
// describing a different history, and the stale-target check correctly refuses to call the run
// verified.
//
// A denial that cannot be acted on sends an agent loop hunting. So the denial names this command,
// and this command exists.
//
// WHAT IT DOES NOT DO: it does not clear a verdict, re-open a gate, or make anything pass. It
// moves the anchor and records that it moved. The checks still have to be re-run against the new
// base, and `batten check` is still the only thing that can produce a batten-verified pass —
// a recovery command that quietly re-approved work would be the exact hole batten exists to fill.

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/store"
)

func cmdRecover(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "--") {
		return errors.New("recover: batten recover <unit>")
	}
	unit := args[0]

	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no open run for %s", unit)
	}

	head, err := gitAt(sp.Root, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("recover: %s is not a git repository, so there is no anchor to move "+
			"(and `anchor: git_sha` in your spec cannot be honoured here either)", sp.Root)
	}

	old := run.BaseSHA
	switch {
	case old == "":
		fmt.Printf("%s had no anchor recorded.\n", unit)
	case old == head:
		fmt.Printf("%s is already anchored at %s — nothing to recover.\n", unit, shortSHA(head))
		fmt.Println("If a gate still calls the target stale, the working tree changed rather " +
			"than the history. Run: batten check " + unit)
		return nil
	default:
		// Say what actually happened to the old anchor, because "gone" and "still there but no
		// longer an ancestor" are different accidents with different lessons.
		fmt.Printf("%s was anchored at %s\n", unit, shortSHA(old))
		switch {
		case !commitExists(sp.Root, old):
			fmt.Println("  that commit no longer exists — it was rewritten (a rebase, an amend, " +
				"or a dropped branch).")
		case !isAncestor(sp.Root, old, head):
			fmt.Println("  that commit still exists but is no longer an ancestor of HEAD — " +
				"you are on a different line of history.")
			if base, err := gitAt(sp.Root, "merge-base", old, head); err == nil {
				fmt.Printf("  the two share %s.\n", shortSHA(base))
			}
		default:
			fmt.Println("  that commit is still an ancestor of HEAD — new commits landed on top.")
		}
	}

	if err := st.SetBaseSHA(run.RunID, head); err != nil {
		return err
	}
	// On the record, like every other thing batten does that changes what a gate will decide.
	_ = st.LogDecision(store.Event{
		RunID: run.RunID, Hook: "recover", Decision: store.DecisionAllow,
		Rule: "recover", Reason: "anchor moved " + shortSHA(old) + " -> " + shortSHA(head),
	})

	fmt.Printf("re-anchored %s at %s\n", unit, shortSHA(head))
	fmt.Println("\nThe anchor moved; nothing was approved. The declared checks still have to run " +
		"against this base:")
	fmt.Printf("  batten check %s\n", unit)
	return nil
}

func gitAt(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func commitExists(root, sha string) bool {
	_, err := gitAt(root, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

func isAncestor(root, maybeAncestor, of string) bool {
	cmd := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", maybeAncestor, of)
	return cmd.Run() == nil
}
