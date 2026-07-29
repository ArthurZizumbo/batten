package main

// `batten unattended` and `batten iterate` — the two commands that make /batten-night's rules
// mechanical instead of aspirational. See internal/hooks/unattended.go for what each rule becomes.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/hooks"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func cmdUnattended(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "--") {
		return errors.New("unattended: batten unattended <unit> [--off]\n" +
			"  on:  four rules stop being prose — no deleting, no override, no commit, and the\n" +
			"       iteration ceiling is enforced\n" +
			"  off: what a human does in the morning, after reading the report")
	}
	unit, off := args[0], false
	for _, a := range args[1:] {
		switch a {
		case "--off":
			off = true
		default:
			return fmt.Errorf("unattended: unknown flag %q", a)
		}
	}

	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no open run for %s — open one first: batten phase %s <phase>", unit, unit)
	}
	if off {
		if err := st.SetMode(run.RunID, ""); err != nil {
			return err
		}
		fmt.Printf("%s is supervised again. Deletes, overrides and commits are yours to make.\n", unit)
		return nil
	}
	if err := st.SetMode(run.RunID, store.ModeUnattended); err != nil {
		return err
	}
	fmt.Printf("%s is now UNATTENDED. Four rules are mechanisms from here:\n", unit)
	fmt.Println("  1. nothing gets deleted — rm, git reset --hard, git restore, git clean, and")
	fmt.Println("     truncating an existing file are denied. Write them in the morning report.")
	fmt.Printf("  2. the iteration ceiling is counted: batten iterate %s\n", unit)
	fmt.Println("  3. batten override is refused — a human reason needs a human.")
	fmt.Println("  4. a commit is denied, verdicts or no verdicts. A human closes.")
	if max := sp.Budget.MaxIterations; max > 0 {
		fmt.Printf("\nceiling: %d iteration(s), %d used.\n", max, run.Iterations)
	} else {
		// Saying this out loud rather than defaulting to a number batten invented. A ceiling
		// nobody declared is not a ceiling of 3 — it is no ceiling, and the person starting an
		// unsupervised run should learn that now rather than from the bill.
		fmt.Println("\n⚠ budget.max_iterations is NOT set, so rule 2 has no ceiling to enforce.")
		fmt.Println("  The fix loop can go round for as long as the other ceilings allow.")
	}
	fmt.Printf("turn it off when you have read the report: batten unattended %s --off\n", unit)
	return nil
}

// cmdIterate is rule 2's counter.
//
// `budget.max_iterations` was declared in the spec, returned over MCP and DRAWN in the TUI as
// `iters %d/%d` — and nothing ever incremented runs.iterations, so it read 0 all night. This is
// the increment, and the exit code is the part a loop can branch on: non-zero means stop.
func cmdIterate(args []string) error {
	if len(args) < 1 {
		return errors.New("iterate: batten iterate <unit>")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit := args[0]
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no open run for %s", unit)
	}
	max := sp.Budget.MaxIterations

	// Checked BEFORE incrementing, so a loop that ignores the exit code and calls again does not
	// get to walk the counter past the ceiling and out the other side.
	if reached, used, ceiling := hooks.IterationCeiling(run, max); reached {
		return errors.New(hooks.Refusal(hooks.CodeIterationCeiling, fmt.Sprintf(
			"%s has used %d of %d iterations: the ceiling is reached.\n"+
				"Stop. A loop that has failed the same check %d times is not going to pass it on "+
				"the next one; it is going to spend the window.\n"+
				"Report what is still red and let a human decide.", unit, used, ceiling, used)))
	}
	n, err := st.Iterate(run.RunID)
	if err != nil {
		return err
	}
	if max <= 0 {
		fmt.Printf("%s: iteration %d (no budget.max_iterations declared, so nothing stops this)\n", unit, n)
		return nil
	}
	fmt.Printf("%s: iteration %d of %d\n", unit, n, max)
	if n == max {
		fmt.Println("this is the last one. The next `batten iterate` will refuse.")
	}
	return nil
}
