package main

// `batten report` — what batten saw, without batten having stopped anything.
//
// The adoption problem this exists to solve, measured against the three reference plugins:
//
//	caveman      1 command   -> 65% fewer tokens
//	graphify     3 commands  -> an interactive graph in the browser
//	superpowers  2 commands  -> a live methodology
//	batten       ~8 steps    -> a denied commit
//
// `init` puts every repo in `report` mode on purpose — gates warn, they do not block — and that
// mode had no output of its own. So the first thing batten ever did for a new user was refuse
// something, after eight steps of setup. This is the other order: look at what happened, decide
// later whether you want it enforced.
//
// Two rules the whole file obeys, because this is the most visible surface batten has and the
// easiest place to lose the argument:
//
//   - Never a number we did not measure. A run with no ingested transcript has NOT spent zero
//     tokens; it is unmeasured, and those are opposite facts.
//   - Never an estimate dressed as a measurement. "batten saved you ~$15 in retry tokens" is
//     unknowable — nobody knows what the retry that did not happen would have cost — and it is
//     exactly the claim a sceptical reader takes apart, taking the real numbers down with it.
//     "3 commits denied this week" is checkable, and it is the stronger sentence.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func cmdReport(args []string) error {
	since := 24 * time.Hour
	share := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--since":
			if i+1 >= len(args) {
				return errors.New("report: --since needs a duration, e.g. 168h or 7d")
			}
			d, err := parseSince(args[i+1])
			if err != nil {
				return err
			}
			since, i = d, i+1
		case "--week":
			since = 7 * 24 * time.Hour
		case "--share":
			// A markdown block to paste, instead of network telemetry. batten's job is to
			// audit; a tool that audits and phones home has lost the argument before it starts.
			share = true
		default:
			return fmt.Errorf("report: unknown flag %q", args[i])
		}
	}

	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	cutoff := time.Now().Add(-since).Unix()
	var b strings.Builder
	if share {
		fmt.Fprintf(&b, "**batten** · `%s` · last %s\n\n```\n", sp.Project, humanDuration(since))
	}

	runs, err := st.ListRuns(sp.Project, 50)
	if err != nil {
		return err
	}
	var recent []store.Run
	for _, r := range runs {
		if r.StartedAt >= cutoff {
			recent = append(recent, r)
		}
	}

	fmt.Fprintf(&b, "%s · last %s\n\n", sp.Project, humanDuration(since))
	if len(recent) == 0 {
		fmt.Fprintf(&b, "  no runs in this window. Open one with `batten phase <%s> %s`.\n",
			sp.Unit.Name, firstPhase(sp))
	}
	for _, r := range recent {
		reportRun(&b, sp, st, r)
	}

	reportImpact(&b, st, sp.Project, cutoff, since)

	if share {
		b.WriteString("```\n\nEvery number above is counted, not estimated. " +
			"Generated locally by [batten](https://github.com/ArthurZizumbo/batten) — " +
			"nothing left this machine.\n")
	}
	fmt.Print(b.String())
	return nil
}

// reportRun renders one run the way someone skims it: outcome first, then who did what.
func reportRun(b *strings.Builder, sp *spec.Spec, st *store.Store, r store.Run) {
	fmt.Fprintf(b, "  %-10s %-9s", r.UnitID, r.Status)

	nodes, _ := st.Nodes(r.RunID)
	var phases, subs []store.Node
	for _, n := range nodes {
		switch n.Kind {
		case "phase":
			phases = append(phases, n)
		case "subagent":
			subs = append(subs, n)
		}
	}
	fmt.Fprintf(b, "%d phase(s) · %d subagent(s)", len(phases), len(subs))
	if r.BaseSHA != "" {
		fmt.Fprintf(b, " · anchor %s", shortSHA(r.BaseSHA))
	}
	b.WriteString("\n")

	// Per-subagent lines: what it owned and what it cost. Both are things no other tool in the
	// ecosystem can print, because nothing else records the write-set claims.
	ws, _ := st.WriteSetsByRun(r.RunID)
	usg, _ := st.UsageByNode(r.RunID)
	sort.Slice(subs, func(i, j int) bool { return subs[i].StartedAt < subs[j].StartedAt })
	for _, n := range subs {
		// Domain first — it is the fan-out axis and the thing worth reading. Then the agent id,
		// which is at least distinctive. The agent TYPE last: five subagents all labelled
		// "general-purpose" tell the reader nothing about which is which.
		label := n.Domain
		if label == "" {
			label = n.AgentID
		}
		if label == "" {
			label = n.Label
		}
		fmt.Fprintf(b, "          %-16s %-22s", label, filesPhrase(ws[n.NodeID]))
		if u, ok := usg[n.NodeID]; ok && totalTokens(u) > 0 {
			fmt.Fprintf(b, "  %s tokens", humanTokens(totalTokens(u)))
		} else {
			// The unmeasured case, said plainly. A blank column here would read as "free".
			b.WriteString("  usage not measured")
		}
		b.WriteString("\n")
	}

	// The verdict line is the one that says whether this run could actually land.
	rv, rErr := st.LatestVerdictNotBySource(r.RunID, "", "batten")
	bv, bErr := st.LatestVerdictBySource(r.RunID, "", "batten")
	switch {
	case rErr != nil && bErr != nil:
		b.WriteString("          no verdict — the gate would have denied this commit\n")
	case bErr == nil && bv.Result != "ok":
		fmt.Fprintf(b, "          checks ran and FAILED: %s\n", firstLineOf(bv.Why))
		for _, e := range bv.Evidence {
			fmt.Fprintf(b, "            %s\n", firstLineOf(e))
		}
	case rErr != nil:
		b.WriteString("          checks ran, but nobody judged the work against its criteria\n")
	case rv.Result != "ok":
		fmt.Fprintf(b, "          reviewer said %s: %s\n", rv.Result, firstLineOf(rv.Why))
	case bErr != nil:
		fmt.Fprintf(b, "          reviewed (%d evidence), but the declared checks were never run\n",
			len(rv.Evidence))
	default:
		fmt.Fprintf(b, "          verified: checks RAN + criteria judged (%d evidence)\n",
			len(rv.Evidence))
	}

	// Spend, with the same honesty the ledger uses everywhere else.
	switch {
	case r.TokensSpent > 0 && r.ImputedUSD > 0:
		fmt.Fprintf(b, "          %s tokens · $%.2f imputed (never billed)\n",
			humanTokens(r.TokensSpent), r.ImputedUSD)
	case r.TokensSpent > 0:
		fmt.Fprintf(b, "          %s tokens · imputed cost not priced\n", humanTokens(r.TokensSpent))
	default:
		b.WriteString("          usage NOT MEASURED for this run — not zero, unknown\n")
	}
	b.WriteString("\n")
}

// reportImpact is the closing block: what batten actually stopped.
//
// Counted, never estimated, and never extrapolated into money. It is deliberately capable of
// printing all zeros — "batten denied nothing this week" is a real and useful answer, and a
// report that can only ever look impressive is not a report.
func reportImpact(b *strings.Builder, st *store.Store, project string, cutoff int64, since time.Duration) {
	counts, err := st.CountDecisions(project, cutoff)
	if err != nil {
		return
	}
	first, _ := st.FirstDecisionAt(project)
	if first == 0 {
		b.WriteString("  batten has not recorded a decision yet, so there is nothing to count.\n" +
			"  It starts counting from the first tool call it sees.\n")
		return
	}

	byRule, byRuleAdvised := map[string]int{}, map[string]int{}
	var denied, advised, whileReporting int
	for _, c := range counts {
		if c.Enforcement == "report" && c.Decision != store.DecisionAllow {
			whileReporting += c.N
		}
		switch c.Decision {
		case store.DecisionDeny:
			denied += c.N
			byRule[c.Rule] += c.N
		case store.DecisionAdvise:
			advised += c.N
			byRuleAdvised[c.Rule] += c.N
		}
	}

	fmt.Fprintf(b, "  what batten stopped, last %s\n", humanDuration(since))
	fmt.Fprintf(b, "    %d commit(s) denied", byRule[store.RuleVerdictGate])
	if byRule[store.RuleVerdictGate] > 0 {
		b.WriteString("   (no verdict, empty evidence, or checks not run)")
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "    %d write-set collision(s) stopped\n", byRule[store.RuleWriteSet])
	fmt.Fprintf(b, "    %d run(s) stopped on budget\n", byRule[store.RuleBudget])
	// The unsupervised run's own line. This is the number nobody else in the ecosystem can
	// produce, because it takes a tool that was PRESENT at 3am and said no — "4 destructive
	// commands refused while nobody was watching" is a different kind of statement from "the
	// model behaved", and it is the difference between a rule and a request.
	if n := byRule[store.RuleUnattended]; n > 0 {
		fmt.Fprintf(b, "    %d action(s) refused during an unattended run   "+
			"(deletes, and commits before a human read the report)\n", n)
	}
	fmt.Fprintf(b, "    %d warning(s) issued without blocking\n", advised)
	if denied == 0 && advised == 0 {
		b.WriteString("    nothing was stopped in this window. That is a result, not an empty report.\n")
	}

	// What got through while the gates were only warning.
	//
	// `enforcement: report` is batten's honest off-switch, and it is what `init` writes by
	// default — so this is not an edge case, it is the state most adopters are in. A kill switch
	// is only worth having if you can find out what happened while it was off, and that is the
	// half batten was missing: without it, "we ran in report mode for three weeks" has no record
	// of what that cost.
	if whileReporting > 0 {
		fmt.Fprintf(b, "\n    ⚠ %d of these were WARNINGS ONLY: enforcement is `report`, so the "+
			"gate did not block them.\n", whileReporting)
		fmt.Fprintf(b, "      Those %d tool call(s) went through. Set `enforcement: enforce` in "+
			"batten.yaml when you trust what you see above.\n", whileReporting)
	}

	// The honesty line, and it is not optional. Without it "3 commits denied" reads as a
	// lifetime total, when batten may have been counting since Tuesday.
	if first > cutoff {
		fmt.Fprintf(b, "    counting since %s — anything before that was never recorded.\n",
			time.Unix(first, 0).Format("2006-01-02 15:04"))
	}
}

func firstPhase(sp *spec.Spec) string {
	if len(sp.Phases) > 0 {
		return sp.Phases[0].ID
	}
	return "build"
}

// totalTokens counts every bucket, cache reads included. They are cheap, not free, and leaving
// them out is how a ledger quietly under-reports the thing it exists to report.
func totalTokens(u store.Usage) int64 {
	return u.InputTokens + u.OutputTokens + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead
}

// shortSHA trims an anchor to the length a human reads. The vault has its own copy; this one is
// here rather than exported because the two surfaces should be free to disagree about length.
func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func filesPhrase(files []string) string {
	switch len(files) {
	case 0:
		// Nobody claimed a write-set. Not "0 files": one is a fact about the work, the other is
		// a fact about the recording, and they are not the same thing.
		return "write-set not recorded"
	case 1:
		return "1 file"
	}
	return fmt.Sprintf("%d files", len(files))
}

// parseSince accepts Go durations plus the "7d" spelling, which is what anyone reaches for.
func parseSince(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		var days float64
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%g", &days); err == nil {
			return time.Duration(days * 24 * float64(time.Hour)), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("report: %q is not a duration (try 24h, 7d, 168h)", s)
	}
	return d, nil
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	case d >= 2*time.Hour:
		return fmt.Sprintf("%.0f hours", d.Hours())
	}
	return d.String()
}
