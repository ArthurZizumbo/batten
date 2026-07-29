package main

// `batten scan-diff` — what the fan-out ACTUALLY touched, against what it said it would.
//
// This is the check that does not depend on reading shell. The Bash write-set guard
// (internal/hooks/bashwrite.go) closes the `sed -i` hole by parsing commands, and parsing commands
// has a hard ceiling: a python script, a Makefile target, a `go run`, a code generator, anything a
// third-party tool does. No shell parser reaches those.
//
// This one does, because it does not look at commands at all. It asks git what changed and asks the
// database who claimed what, and contrasts the two. Deterministic, no heuristics, no false
// positives — which is why plan §5.1 says it should have come FIRST even though it is listed fourth.
//
// It also produces, for free, the number §3.3 wants and nobody has: how much batten's agents
// OVER-declare. S-Bus (arXiv:2605.17076) measured 32–49% over-declaration in automatically
// reconstructed read-sets. batten's write-sets are declared by hand, by a planning agent, and until
// now nothing compared the declaration to the outcome. That number is the evidence that would
// reopen the OCC/MTPO decision (plan §10) — or keep it closed.
//
// WHAT IT REPORTS AND WHAT IT REFUSES TO CONCLUDE:
//
//   - a file changed that nobody claimed → reported. It may be legitimate (the orchestrator's own
//     integration edit) or it may be an agent that went around the fence. batten cannot tell which
//     from the diff alone and does not pretend to.
//   - a file claimed and never touched → over-declaration, counted.
//   - NO claims at all → said plainly, and NOT reported as clean. A run with no write-sets is a
//     planning gap; calling it "0 violations" would be the emptiest possible green tick.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/gitx"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func cmdScanDiff(args []string) error {
	unit, strict := "", false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--strict":
			strict = true
		default:
			if strings.HasPrefix(a, "--") {
				return fmt.Errorf("scan-diff: unknown flag %q", a)
			}
			unit = a
		}
	}

	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	if unit == "" {
		if b, err := gitBranch(); err == nil {
			unit = sp.MatchUnit(b)
		}
	}
	if unit == "" {
		return errors.New("scan-diff: batten scan-diff <unit> [--strict]")
	}
	run, err := st.LatestRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no run recorded for %s", unit)
	}
	if run.BaseSHA == "" {
		// Not measurable, and not "nothing changed". The anchor is what makes the unit's diff a
		// unit's diff rather than "everything since some commit".
		return fmt.Errorf("%s has no anchor recorded, so there is no diff to scan.\n"+
			"The anchor is stamped by the phase declaring `anchor: git_sha`; this run never "+
			"entered it. Nothing here is measurable — that is different from clean", unit)
	}

	// Ask the tree the run actually lives in. With a worktree per unit, the diff is over there.
	root := verdictTreeOf(sp, run)
	changed, err := gitx.ChangedFiles(root, run.BaseSHA, st.Path())
	if err != nil {
		return fmt.Errorf("cannot read %s's diff from %s: %w\n"+
			"This is NOT an empty diff. If the anchor was rewritten by a rebase: batten recover %s",
			unit, shortSHA(run.BaseSHA), err, unit)
	}

	ws, err := st.WriteSetsByRun(run.RunID)
	if err != nil {
		return err
	}
	rep := contrastDiff(changed, ws)
	printScanDiff(run, rep)

	if strict && len(rep.Undeclared) > 0 {
		return fmt.Errorf("%d file(s) changed that no write-set claimed", len(rep.Undeclared))
	}
	return nil
}

// scanReport is the contrast, as data. Kept separate from the printing so the comparison is
// testable without capturing stdout.
type scanReport struct {
	Changed    []string            // the unit's real diff
	Undeclared []string            // changed, claimed by nobody
	Unused     map[string][]string // node -> claimed but never touched
	Owned      map[string][]string // node -> claimed AND touched
	Claims     int                 // total paths claimed
}

// OverDeclared is the fraction of claimed paths that were never touched. -1 when nothing was
// claimed, because 0% over-declaration on zero claims is not a measurement.
func (r scanReport) OverDeclared() float64 {
	if r.Claims == 0 {
		return -1
	}
	unused := 0
	for _, ps := range r.Unused {
		unused += len(ps)
	}
	return float64(unused) / float64(r.Claims)
}

func contrastDiff(changed []string, ws map[string][]string) scanReport {
	rep := scanReport{
		Changed: changed,
		Unused:  map[string][]string{},
		Owned:   map[string][]string{},
	}
	// Owner index. Compared through the same normalisation the guard uses, so a claim written
	// `./api/x.go` and a diff line reading `api/x.go` are the same file here too.
	owner := map[string]string{}
	for node, paths := range ws {
		for _, p := range paths {
			owner[normalizeScanPath(p)] = node
			rep.Claims++
		}
	}
	touched := map[string]bool{}
	for _, c := range changed {
		key := normalizeScanPath(c)
		touched[key] = true
		if n, ok := owner[key]; ok {
			rep.Owned[n] = append(rep.Owned[n], c)
		} else {
			rep.Undeclared = append(rep.Undeclared, c)
		}
	}
	for node, paths := range ws {
		for _, p := range paths {
			if !touched[normalizeScanPath(p)] {
				rep.Unused[node] = append(rep.Unused[node], p)
			}
		}
	}
	for _, m := range []map[string][]string{rep.Unused, rep.Owned} {
		for k := range m {
			sort.Strings(m[k])
		}
	}
	sort.Strings(rep.Undeclared)
	return rep
}

func normalizeScanPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	p = strings.TrimPrefix(p, "./")
	return strings.TrimSuffix(p, "/")
}

// printScanDiff renders the contrast. Every branch that could print a reassuring number without
// having measured one says so instead.
func printScanDiff(run *store.Run, rep scanReport) {
	nameOf := store.DisplayNodeID
	fmt.Printf("%s  anchor %s  %d file(s) changed\n", run.UnitID, shortSHA(run.BaseSHA), len(rep.Changed))

	if rep.Claims == 0 {
		fmt.Println("\nno write-set was declared for this run, so there is nothing to contrast.")
		fmt.Println("That is a planning gap, not a clean result: a fan-out with no declared")
		fmt.Println("write-sets is exactly the arrangement the guard exists to make impossible.")
		fmt.Println("Declare them with `batten claim <agent-id> <files...>` as each agent starts.")
		return
	}

	if len(rep.Owned) > 0 {
		fmt.Println("\nclaimed and touched:")
		nodes := make([]string, 0, len(rep.Owned))
		for n := range rep.Owned {
			nodes = append(nodes, n)
		}
		sort.Strings(nodes)
		for _, n := range nodes {
			fmt.Printf("  %-20s %s\n", nameOf(n), strings.Join(rep.Owned[n], ", "))
		}
	}

	if len(rep.Undeclared) > 0 {
		fmt.Printf("\n⚠ %d file(s) changed that NO write-set claimed:\n", len(rep.Undeclared))
		for _, p := range rep.Undeclared {
			fmt.Printf("    %s\n", p)
		}
		fmt.Println("  This is what a shell command, a code generator or a third-party tool leaves")
		fmt.Println("  behind — the writes no parser of commands can see. batten cannot tell from a")
		fmt.Println("  diff whether these were the orchestrator integrating or an agent going around")
		fmt.Println("  the fence, and it will not guess: look at them.")
	} else {
		fmt.Println("\n✓ every changed file was claimed by someone.")
	}

	if over := rep.OverDeclared(); over >= 0 {
		unused := 0
		for _, ps := range rep.Unused {
			unused += len(ps)
		}
		fmt.Printf("\nover-declaration: %d of %d claimed path(s) were never touched (%.0f%%)\n",
			unused, rep.Claims, over*100)
		if unused > 0 {
			nodes := make([]string, 0, len(rep.Unused))
			for n := range rep.Unused {
				nodes = append(nodes, n)
			}
			sort.Strings(nodes)
			for _, n := range nodes {
				fmt.Printf("  %-20s %s\n", nameOf(n), strings.Join(rep.Unused[n], ", "))
			}
		}
		// The comparison this number exists for. Reported as a comparison, not as a verdict:
		// one run is not a measurement, and saying so is the difference between evidence and a
		// number that flatters whoever printed it.
		fmt.Println("  (S-Bus measured 32–49% over-declaration in automatically reconstructed")
		fmt.Println("   read-sets. This is ONE run of hand-declared write-sets — a data point,")
		fmt.Println("   not a rate. Plan §3.3 wants the rate across many.)")
	}
}
