// Command batten governs an agentic workflow declared in batten.yaml.
//
//	batten init                 generate batten.yaml (optionally from a prose workflow doc)
//	batten doctor               validate the spec and report which capabilities are live
//	batten hook <event>         read a Claude Code hook payload on stdin, decide on stdout
//	batten verdict              record a verdict envelope (rejects ok with empty evidence)
//	batten claim <agent> <files...>   declare a subagent's write-set
//	batten runs | show | canvas  inspect the run graph
//	batten budget | override    the governor, and its audited escape hatch
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arthu/batten/internal/canvas"
	"github.com/arthu/batten/internal/discovery"
	"github.com/arthu/batten/internal/export"
	"github.com/arthu/batten/internal/hooks"
	"github.com/arthu/batten/internal/mcp"
	"github.com/arthu/batten/internal/scan"
	"github.com/arthu/batten/internal/spec"
	"github.com/arthu/batten/internal/statusline"
	"github.com/arthu/batten/internal/store"
	"github.com/arthu/batten/internal/tui"
	"github.com/arthu/batten/internal/usage"
)

var version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "hook":
		err = cmdHook(os.Args[2:])
	case "init":
		err = cmdInit(os.Args[2:])
	case "doctor":
		err = cmdDoctor()
	case "verdict":
		err = cmdVerdict(os.Args[2:])
	case "claim":
		err = cmdClaim(os.Args[2:])
	case "runs":
		err = cmdRuns()
	case "show":
		err = cmdShow(os.Args[2:])
	case "canvas":
		err = cmdCanvas(os.Args[2:])
	case "budget":
		err = cmdBudget(os.Args[2:])
	case "override":
		err = cmdOverride(os.Args[2:])
	case "phase":
		err = cmdPhase(os.Args[2:])
	case "mcp":
		err = cmdMCP()
	case "tui":
		err = cmdTUI()
	case "statusline":
		err = cmdStatusline(os.Args[2:])
	case "ingest":
		err = cmdIngest(os.Args[2:])
	case "measure":
		err = cmdMeasure()
	case "close":
		err = cmdClose(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "hook-debug":
		err = cmdHookDebug(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("batten", version)
	default:
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "batten:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `batten — procedural memory as data

  batten init [--from <prose-doc>]   generate batten.yaml
  batten doctor                      validate spec, report live capabilities
  batten hook <event>                hook entrypoint (stdin JSON -> stdout JSON)
  batten phase <unit> <phase>        advance a unit to a phase (records the anchor SHA)
  batten claim <agent-id> <file>...  declare a subagent's write-set
  batten verdict [--file v.json]     record a verdict envelope
  batten runs                        list runs
  batten show <unit>                 run detail
  batten canvas <unit> [--out p]     emit the run DAG as JSON Canvas
  batten budget [<unit>]             governor status (tokens / imputed $ / quota)
  batten override <unit> --reason    audited escape from the gate
  batten tui                         review runs in a terminal UI
  batten mcp                         MCP server (exposes the run graph to the agent)
  batten statusline [--install]      status line + the only subscription-quota sensor
  batten ingest <unit> --transcript  price a transcript's tokens into the run ledger
`)
}

// ---------- wiring ----------

// dbPath keeps state where it survives a plugin update. ${CLAUDE_PLUGIN_ROOT} is
// wiped on every update — the docs say so explicitly — so state must never live there.
func dbPath() string {
	if d := os.Getenv("BATTEN_DB"); d != "" {
		return d
	}
	if d := os.Getenv("CLAUDE_PLUGIN_DATA"); d != "" {
		return filepath.Join(d, "batten.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "batten.db"
	}
	return filepath.Join(home, ".batten", "batten.db")
}

func load() (*spec.Spec, *store.Store, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	sp, err := spec.LoadFrom(cwd)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(dbPath())
	if err != nil {
		return nil, nil, err
	}
	return sp, st, nil
}

// ---------- hook ----------

func cmdHook(args []string) (retErr error) {
	// A hook runs on every tool call. A panic here — a nil deref, a bad type assertion on a
	// payload shape we didn't expect — would paint a Go stack trace across the user's session.
	// Principle #2: degrade, never break. Swallow it and exit clean; the tool call proceeds.
	defer func() {
		if r := recover(); r != nil {
			retErr = nil
		}
	}()

	if len(args) < 1 {
		return errors.New("hook: need an event name")
	}
	event := args[0]

	raw, err := hooks.ReadInput(os.Stdin)
	if err != nil {
		return err
	}

	// A hook must never break a session. If batten is not configured here, or anything
	// goes wrong, exit clean and silent: we govern where we were invited, nowhere else.
	sp, st, err := loadForHook(raw)
	if err != nil {
		return nil
	}
	defer st.Close()

	h := &hooks.Handler{Spec: sp, Store: st, TapPath: tapPath()}
	out, err := h.Dispatch(event, raw)
	if err != nil || out == nil {
		return nil
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

// loadForHook resolves the spec from the hook's own cwd, not the process cwd —
// a hook process may be started anywhere.
func loadForHook(raw []byte) (*spec.Spec, *store.Store, error) {
	var probe struct {
		CWD string `json:"cwd"`
	}
	_ = json.Unmarshal(raw, &probe)
	dir := probe.CWD
	if dir == "" {
		dir, _ = os.Getwd()
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(dbPath())
	if err != nil {
		return nil, nil, err
	}
	return sp, st, nil
}

// ---------- verdict ----------

func cmdVerdict(args []string) error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	var unit, file, gate string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--file":
			i++
			file = args[i]
		case "--unit":
			i++
			unit = args[i]
		case "--gate":
			i++
			gate = args[i]
		}
	}

	var raw []byte
	if file != "" {
		raw, err = os.ReadFile(file)
	} else {
		raw, err = hooks.ReadInput(os.Stdin)
	}
	if err != nil {
		return err
	}

	var v store.Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("verdict must be a JSON envelope {check_id, result, evidence[], why, "+
			"safe_next_step, requires_confirmation}: %w", err)
	}
	if unit == "" {
		unit = sp.MatchUnit(v.CheckID)
	}
	if unit == "" {
		if b, err := gitBranch(); err == nil {
			unit = sp.MatchUnit(b)
		}
	}
	if unit == "" {
		return fmt.Errorf("cannot tell which %s this verdict is for; pass --unit", sp.Unit.Name)
	}

	run, err := st.EnsureRun(sp.Project, unit, "")
	if err != nil {
		return err
	}
	if gate == "" {
		if c, ok := sp.ClosingPhase(); ok && c.Gate != "" {
			gate = c.Gate
		} else {
			for _, p := range sp.Phases {
				if p.Gate != "" {
					gate = p.Gate
				}
			}
		}
	}
	v.RunID, v.Gate = run.RunID, gate

	required := true
	if g, ok := sp.Gates[gate]; ok {
		required = g.EvidenceRequired()
	}
	if err := st.SaveVerdict(v, required); err != nil {
		return err
	}
	fmt.Printf("verdict recorded: %s %s=%s (%d evidence)\n", unit, gate, v.Result, len(v.Evidence))
	if v.Result != "ok" {
		fmt.Printf("the close gate will deny a commit while this stands at %q\n", v.Result)
	}
	// The moment the vault most wants to reflect reality: the gate state just changed.
	if sp.Capabilities.Obsidian.Vault != "" {
		if res, err := export.Run(sp, st, unit); err == nil && res.RunNotePath != "" {
			fmt.Printf("updated run note %s\n", res.RunNotePath)
		}
	}
	return nil
}

// ---------- write-set ----------

func cmdClaim(args []string) error {
	if len(args) < 2 {
		return errors.New("claim: batten claim <agent-id> <file>...")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	agent := args[0]
	node, err := st.NodeByAgent(agent)
	if err != nil {
		return fmt.Errorf("no subagent %q on record; the SubagentStart hook creates it", agent)
	}
	files := make([]string, 0, len(args)-1)
	for _, f := range args[1:] {
		if rel, err := filepath.Rel(sp.Root, f); err == nil && !strings.HasPrefix(rel, "..") {
			f = rel
		}
		files = append(files, filepath.ToSlash(f))
	}
	if err := st.ClaimWriteSet(node.RunID, node.NodeID, files); err != nil {
		return err
	}
	fmt.Printf("%s owns %d file(s); any other agent writing them is now denied\n", agent, len(files))
	return nil
}

// ---------- phase ----------

func cmdPhase(args []string) error {
	if len(args) < 2 {
		return errors.New("phase: batten phase <unit> <phase>")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit, phaseID := args[0], args[1]
	ph, ok := sp.Phase(phaseID)
	if !ok {
		return fmt.Errorf("no phase %q in %s", phaseID, spec.Filename)
	}
	run, err := st.EnsureRun(sp.Project, unit, "")
	if err != nil {
		return err
	}
	if err := st.SetPhase(run.RunID, phaseID); err != nil {
		return err
	}
	// Tag the run with whether compression is live, so `batten measure` can compare later.
	if sp.Capabilities.CompressionEnabled() && sp.Capabilities.Compression.Measure {
		_ = st.SetHeadroom(run.RunID, headroomAlive())
	}
	_ = st.AddNode(store.Node{
		NodeID: "p-" + phaseID, RunID: run.RunID, Kind: "phase", Label: phaseID, Status: "running",
	})

	// The anchor. Every later phase diffs from here, not from HEAD~N.
	if ph.Anchor == "git_sha" && run.BaseSHA == "" {
		sha, err := gitSHA()
		if err == nil {
			_ = st.SetBaseSHA(run.RunID, sha)
			fmt.Printf("anchor: %s base SHA = %s\n", unit, sha)
		}
	}
	fmt.Printf("%s -> phase %s\n", unit, phaseID)
	if ph.Gate != "" {
		fmt.Printf("this phase must emit a verdict for gate %q (evidence required)\n", ph.Gate)
	}
	return nil
}

// ---------- inspection ----------

func cmdRuns() error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()
	runs, err := st.ListRuns(sp.Project, 50)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("no runs yet")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "UNIT\tSTATUS\tPHASE\tVERDICT\tTOKENS\tIMPUTED")
	for _, r := range runs {
		verdict := "—"
		if v, err := st.LatestVerdict(r.RunID, ""); err == nil {
			verdict = fmt.Sprintf("%s(%d)", v.Result, len(v.Evidence))
		}
		tok, usd := "—", "—"
		if r.TokensSpent > 0 {
			tok = humanTokens(r.TokensSpent)
			usd = fmt.Sprintf("$%.2f", r.ImputedUSD)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.UnitID, r.Status, r.Phase, verdict, tok, usd)
	}
	return w.Flush()
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func cmdShow(args []string) error {
	if len(args) < 1 {
		return errors.New("show: batten show <unit>")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()
	run, err := st.ActiveRun(sp.Project, args[0])
	if err != nil {
		return fmt.Errorf("no active run for %s", args[0])
	}
	fmt.Printf("%s  run=%s  status=%s  phase=%s  base=%s\n",
		run.UnitID, run.RunID, run.Status, run.Phase, run.BaseSHA)
	if run.TokensSpent > 0 {
		fmt.Printf("usage: %s tokens, $%.2f imputed (what this would have cost on the API)\n",
			humanTokens(run.TokensSpent), run.ImputedUSD)
	}
	nodes, _ := st.Nodes(run.RunID)
	for _, n := range nodes {
		ws, _ := st.WriteSet(run.RunID, n.NodeID)
		fmt.Printf("  [%s] %-24s %-8s", n.Kind, n.Label, n.Status)
		if len(ws) > 0 {
			fmt.Printf(" owns %d file(s)", len(ws))
		}
		fmt.Println()
	}
	if v, err := st.LatestVerdict(run.RunID, ""); err == nil {
		fmt.Printf("\nverdict %s=%s: %s\n", v.Gate, v.Result, v.Why)
		for _, e := range v.Evidence {
			fmt.Println("  -", e)
		}
		if len(v.Evidence) == 0 {
			fmt.Println("  (no evidence — this cannot be an approval)")
		}
	} else {
		fmt.Println("\nno verdict: the close gate will deny a commit")
	}
	return nil
}

func cmdCanvas(args []string) error {
	if len(args) < 1 {
		return errors.New("canvas: batten canvas <unit> [--out <path>]")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit := args[0]
	out := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			out = args[i+1]
		}
	}

	// --out is the escape hatch for "just give me the canvas here"; without it, export.Run
	// does the full thing (canvas + vault note + dashboards) — the same path the Stop hook fires.
	if out != "" {
		run, err := st.ActiveRun(sp.Project, unit)
		if err != nil {
			return fmt.Errorf("no active run for %s", unit)
		}
		nodes, _ := st.Nodes(run.RunID)
		edges, _ := st.Edges(run.RunID)
		v, _ := st.LatestVerdict(run.RunID, "")
		c := canvas.Render(run, nodes, edges, v)
		if err := c.WriteFile(out); err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d nodes, %d edges)\n", out, len(c.Nodes), len(c.Edges))
		return nil
	}

	res, err := export.Run(sp, st, unit)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d nodes, %d edges)\n", res.CanvasPath, res.Nodes, res.Edges)
	if res.RunNotePath != "" {
		fmt.Printf("wrote run note %s — open the vault in Obsidian\n", res.RunNotePath)
	}
	return nil
}

// ---------- measure: does headroom actually save tokens in OUR fan-out? ----------

func cmdMeasure() error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()
	groups, err := st.MeasureByHeadroom(sp.Project)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Println("no finished runs with usage yet — nothing to compare")
		return nil
	}
	fmt.Println("headroom effect on THIS project's runs (imputed, not billed):")
	var with, without *store.MeasureGroup
	for i := range groups {
		g := groups[i]
		note := ""
		// Never present a 2-run sample as a conclusion: units differ in size, so the noise
		// swamps the signal. Say "insufficient" rather than imply a finding.
		if g.Runs < 3 {
			note = "  (insufficient — need ≥3 runs to compare meaningfully)"
		}
		fmt.Printf("  %-18s %d run(s): %s tokens, $%.2f imputed (mean)%s\n",
			g.Label, g.Runs, humanTokens(int64(g.MeanTokens)), g.MeanUSD, note)
		if g.Label == "with headroom" {
			with = &groups[i]
		}
		if g.Label == "without headroom" {
			without = &groups[i]
		}
	}
	if with != nil && without != nil && with.Runs >= 3 && without.Runs >= 3 && without.MeanTokens > 0 {
		delta := (1 - with.MeanTokens/without.MeanTokens) * 100
		fmt.Printf("\n  → with headroom used %.1f%% %s tokens on average\n",
			abs(delta), map[bool]string{true: "fewer", false: "more"}[delta >= 0])
		fmt.Println("  (still noisy — runs are not identical work; treat as directional, not exact)")
	}
	return nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ---------- close: end a run so its claims release (R1) ----------

func cmdClose(args []string) error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit, status := "", "ok"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--status":
			if i+1 < len(args) {
				status = args[i+1]
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "--") {
				unit = args[i]
			}
		}
	}
	if unit == "" {
		if b, err := gitBranch(); err == nil {
			unit = sp.MatchUnit(b)
		}
	}
	if unit == "" {
		return errors.New("close: batten close <unit> [--status ok|failed|rolled_back]")
	}
	switch status {
	case "ok", "failed", "rolled_back":
	default:
		return fmt.Errorf("--status must be ok|failed|rolled_back, got %q", status)
	}
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no open run for %s", unit)
	}

	// Closing as ok obeys the same rule as the commit gate: no clean close without a verdict
	// (or an override). failed/rolled_back close freely — a run that went wrong should always
	// be closeable, or its claims would be stuck forever.
	if status == "ok" {
		if err := gateReadyToClose(sp, st, run); err != nil {
			return err
		}
	}
	if err := st.CloseRun(run.RunID, status); err != nil {
		return err
	}
	fmt.Printf("%s closed (%s). Write-set claims released — files it held are free again.\n", unit, status)
	if sp.Capabilities.Obsidian.Vault != "" {
		_, _ = export.Run(sp, st, unit) // note now reflects the final state
	}
	return nil
}

// gateReadyToClose enforces the close precondition (mirrors the commit gate) so `batten close`
// and a gated `git commit` cannot disagree about what "done" means.
func gateReadyToClose(sp *spec.Spec, st *store.Store, run *store.Run) error {
	closing, ok := sp.ClosingPhase()
	if !ok {
		return nil
	}
	if o, _ := st.HasOverride(run.RunID, closing.Gate); o {
		return nil
	}
	v, err := st.LatestVerdict(run.RunID, closing.Gate)
	if err != nil || v.Result != "ok" || len(v.Evidence) == 0 {
		return fmt.Errorf("cannot close %s as ok: needs a verdict with result=ok and cited evidence "+
			"(run `batten check %s`, or the verify phase). Use --status failed to close a run that went wrong",
			run.UnitID, run.UnitID)
	}
	return nil
}

// ---------- check: generate evidence by running the gate's own checks (R2) ----------

func cmdCheck(args []string) error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit, gateName := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--gate":
			if i+1 < len(args) {
				gateName = args[i+1]
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "--") {
				unit = args[i]
			}
		}
	}
	if unit == "" {
		if b, err := gitBranch(); err == nil {
			unit = sp.MatchUnit(b)
		}
	}
	if unit == "" {
		return errors.New("check: batten check <unit> [--gate <name>]")
	}
	if gateName == "" {
		if c, ok := sp.ClosingPhase(); ok && c.Gate != "" {
			gateName = c.Gate
		} else {
			for _, p := range sp.Phases {
				if p.Gate != "" {
					gateName = p.Gate
				}
			}
		}
	}
	gate, ok := sp.Gates[gateName]
	if !ok {
		return fmt.Errorf("no gate %q in %s", gateName, spec.Filename)
	}
	if len(gate.Checks) == 0 {
		return fmt.Errorf("gate %q declares no checks to run", gateName)
	}
	run, err := st.EnsureRun(sp.Project, unit, "")
	if err != nil {
		return err
	}

	// Run each check for real. This is the whole point: the evidence is GENERATED, not a string
	// the model typed. "verified" now means batten watched it pass.
	var evidence []string
	allPass := true
	for _, c := range gate.Checks {
		out, code, took := runCheck(sp.Root, c, 5*time.Minute)
		if code == 0 {
			evidence = append(evidence, fmt.Sprintf("%s: PASS (exit 0, %s)", c, took))
			fmt.Printf("  ✓ %s\n", c)
		} else {
			allPass = false
			tail := lastLines(out, 8)
			evidence = append(evidence, fmt.Sprintf("%s: FAIL (exit %d)\n%s", c, code, tail))
			fmt.Printf("  ✗ %s (exit %d)\n%s\n", c, code, indent(tail))
		}
	}

	result := "blocked"
	why := "one or more gate checks failed"
	next := "fix the failures, then run batten check again"
	if allPass {
		result, why, next = "ok", "all gate checks passed (batten ran them)", "add your acceptance-criteria judgment, then close"
	}
	v := store.Verdict{
		RunID: run.RunID, Gate: gateName, CheckID: unit + "-" + gateName + "-batten",
		Result: result, Evidence: evidence, Why: why, SafeNextStep: next, Source: "batten",
	}
	if err := st.SaveVerdict(v, gate.EvidenceRequired()); err != nil {
		return err
	}
	fmt.Printf("\n%s: %s (batten-verified). %s\n", unit, strings.ToUpper(result), why)
	if result != "ok" {
		fmt.Println("the commit gate will deny until this passes.")
	}
	return nil
}

// runCheck executes one gate command via the OS shell, capturing combined output. A per-command
// timeout keeps a hung test from wedging the gate.
func runCheck(dir, command string, timeout time.Duration) (out string, code int, took string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = dir
	start := time.Now()
	b, err := cmd.CombinedOutput()
	took = time.Since(start).Round(time.Millisecond).String()
	if ctx.Err() == context.DeadlineExceeded {
		return string(b), 124, took + " TIMED OUT"
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(b), ee.ExitCode(), took
		}
		return err.Error(), 1, took
	}
	return string(b), 0, took
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func indent(s string) string {
	if s == "" {
		return ""
	}
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// ---------- hook-debug: resolve the agent_id question (E0 step 2) ----------

// tapPath is where hooks dump their raw payload while a tap is active. The whole point is
// E0's load-bearing unknown: does a PreToolUse fired INSIDE a subagent carry agent_id?
// We answer it by capturing the real thing rather than guessing.
func tapPath() string {
	dir := os.Getenv("CLAUDE_PLUGIN_DATA")
	if dir == "" {
		dir = filepath.Dir(dbPath())
	}
	return filepath.Join(dir, "hook-taps.jsonl")
}

func tapFlagPath() string { return tapPath() + ".on" }

func cmdHookDebug(args []string) error {
	if len(args) == 0 {
		return errors.New("hook-debug: --tap | --off | --show")
	}
	switch args[0] {
	case "--tap":
		if err := os.MkdirAll(filepath.Dir(tapFlagPath()), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(tapFlagPath(), []byte("on"), 0o644); err != nil {
			return err
		}
		fmt.Printf("tap ON — every hook payload now appends to %s\n", tapPath())
		fmt.Println("now launch a subagent that edits a file, then run: batten hook-debug --show")
		return nil
	case "--off":
		_ = os.Remove(tapFlagPath())
		fmt.Println("tap OFF")
		return nil
	case "--show":
		b, err := os.ReadFile(tapPath())
		if err != nil {
			return fmt.Errorf("no taps captured yet (%v)", err)
		}
		// Summarize the one thing we care about: which PreToolUse payloads carried agent_id.
		var withAgent, withoutAgent int
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if m["hook_event_name"] != "PreToolUse" {
				continue
			}
			if id, ok := m["agent_id"].(string); ok && id != "" {
				withAgent++
			} else {
				withoutAgent++
			}
		}
		fmt.Print(string(b))
		fmt.Printf("\n--- PreToolUse payloads: %d WITH agent_id, %d without ---\n", withAgent, withoutAgent)
		if withAgent > 0 {
			fmt.Println("VERDICT: subagent hooks DO carry agent_id — the write-set guard works as designed.")
		} else if withoutAgent > 0 {
			fmt.Println("VERDICT: no agent_id seen — the guard runs in advisory mode (warns, cannot hard-deny per-agent).")
		}
		return nil
	}
	return fmt.Errorf("hook-debug: unknown flag %q", args[0])
}

// ---------- mcp / tui ----------

func cmdMCP() error {
	// The MCP server speaks a protocol on stdout, so a missing spec must not print anything
	// there. Resolve quietly; serve an empty graph if this is not a batten repo.
	cwd, _ := os.Getwd()
	sp, _ := spec.LoadFrom(cwd)
	st, err := store.Open(dbPath())
	if err != nil {
		return err
	}
	defer st.Close()
	return mcp.Serve(sp, st)
}

func cmdTUI() error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()
	return tui.Run(sp, st)
}

// ---------- statusline: the quota sensor ----------

func cmdStatusline(args []string) error {
	// Installation path: `batten statusline --install` wires this binary as the project's
	// statusLine. A plugin cannot register it automatically — it lives in settings.json.
	for i := 0; i < len(args); i++ {
		if args[i] == "--install" {
			return installStatusline(args[i+1:])
		}
	}

	// Sensor path: read the payload, snapshot the quota, print the line. This must never
	// fail loudly — a broken status line paints the terminal, so errors become a plain label.
	cwd, _ := os.Getwd()
	sp, _ := spec.LoadFrom(cwd)
	st, err := store.Open(dbPath())
	if err != nil {
		fmt.Print("batten")
		return nil
	}
	defer st.Close()
	raw, _ := hooks.ReadInput(os.Stdin)
	line, _ := statusline.Run(sp, st, raw)
	fmt.Print(line)
	return nil
}

func installStatusline(args []string) error {
	chain := false
	for _, a := range args {
		if a == "--chain" {
			chain = true
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		self = "batten"
	}
	if present, existing, _ := statusline.Installed(cwd); present && !statusline.IsBatten(existing) && !chain {
		return fmt.Errorf("a statusLine is already configured (%q).\n"+
			"Re-run with --chain to have batten wrap it, or edit .claude/settings.json yourself", existing)
	}
	if err := statusline.Install(cwd, self, chain); err != nil {
		return err
	}
	fmt.Println("statusline installed in .claude/settings.json — it now samples your subscription quota")
	return nil
}

// ---------- ingest: price a transcript into the ledger ----------

// cmdIngest walks a session transcript (and its subagents) and records what it cost. This is
// the critical-path-free accounting: hooks call it async on Stop, and it can be re-run by hand.
func cmdIngest(args []string) error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit, transcript := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--transcript":
			if i+1 < len(args) {
				transcript = args[i+1]
				i++ // consume the value, or it gets re-read as the unit on the next turn
			}
		default:
			if !strings.HasPrefix(args[i], "--") {
				unit = args[i]
			}
		}
	}
	if unit == "" {
		if b, err := gitBranch(); err == nil {
			unit = sp.MatchUnit(b)
		}
	}
	if unit == "" || transcript == "" {
		return errors.New("ingest: batten ingest <unit> --transcript <path>")
	}
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no active run for %s", unit)
	}

	seen, _ := st.SeenRequests(run.RunID)
	rows, unknown, err := usage.Parse(transcript, run.RunID, seen)
	if err != nil {
		return err
	}
	// Attribute each row to a node when its agent is on record; the parent's rows stay unattributed.
	for i := range rows {
		if rows[i].AgentID != "" {
			if n, err := st.NodeByAgent(rows[i].AgentID); err == nil {
				rows[i].NodeID = n.NodeID
			}
		}
	}
	added, err := st.RecordUsage(rows)
	if err != nil {
		return err
	}
	r, _ := st.Run(run.RunID)
	fmt.Printf("%s: +%d requests, %s tokens total, $%.2f imputed\n",
		unit, added, humanTokens(r.TokensSpent), r.ImputedUSD)
	if len(unknown) > 0 {
		// Never silently price an unknown model as zero-value: name it, so the number is honest.
		fmt.Printf("  unpriced models (counted as $0, tokens still exact): %s\n", strings.Join(unknown, ", "))
	}
	return nil
}

func cmdBudget(args []string) error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			unit = a
		}
	}
	runs, err := st.ListRuns(sp.Project, 50)
	if err != nil {
		return err
	}
	b := sp.Budget
	shown := false
	for _, r := range runs {
		if unit != "" && r.UnitID != unit {
			continue
		}
		if unit == "" && r.Status != "running" {
			continue
		}
		shown = true
		fmt.Printf("%s  %s tokens, $%.2f imputed\n", r.UnitID, humanTokens(r.TokensSpent), r.ImputedUSD)

		if !b.Set() {
			fmt.Println("  no ceilings declared in budget:")
			continue
		}
		cs, err := st.Budget(r.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
		if err != nil {
			return err
		}
		for _, c := range cs {
			mark := "·"
			if c.Exceeded {
				mark = "!"
			}
			switch {
			case !c.Available:
				// Never invent a number. An unmeasurable ceiling is an unenforced ceiling,
				// and the user deserves to know which.
				fmt.Printf("  %s %-12s NOT MEASURABLE — install the statusline (`batten statusline --install`)\n",
					mark, c.Kind)
			case c.Kind == "tokens":
				fmt.Printf("  %s %-12s %s / %s  %s\n", mark, c.Kind,
					humanTokens(int64(c.Spent)), humanTokens(int64(c.Cap)), bar(c.Spent/c.Cap))
			case c.Kind == "imputed_usd":
				fmt.Printf("  %s %-12s $%.2f / $%.2f  %s\n", mark, c.Kind, c.Spent, c.Cap, bar(c.Spent/c.Cap))
			case c.Kind == "quota_pct":
				fmt.Printf("  %s %-12s %.1f%% / %.1f%% of the rolling 5h window  %s\n",
					mark, c.Kind, c.Spent, c.Cap, bar(c.Spent/c.Cap))
			}
			if c.Exceeded {
				fmt.Printf("      OVER — on_exceed=%s\n", b.OnExceed)
			}
		}
	}
	if !shown {
		fmt.Println("no open runs")
	}
	return nil
}

func bar(frac float64) string {
	const w = 12
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	f := int(frac * w)
	return "[" + strings.Repeat("=", f) + strings.Repeat(".", w-f) + "]"
}

func cmdOverride(args []string) error {
	if len(args) < 1 {
		return errors.New("override: batten override <unit> --reason \"...\"")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit, reason := args[0], ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--reason" && i+1 < len(args) {
			reason = args[i+1]
		}
	}
	// A gate with no escape gets the plugin uninstalled. An unlogged escape defeats
	// the gate. So: an escape that costs a sentence, and lands in the audit log.
	if strings.TrimSpace(reason) == "" {
		return errors.New("--reason is required: an override with no stated reason is just a disabled gate")
	}
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no active run for %s", unit)
	}
	gate := ""
	if c, ok := sp.ClosingPhase(); ok {
		gate = c.Gate
	}
	if gate == "" {
		gate = "*"
	}
	if err := st.Override(run.RunID, gate, reason); err != nil {
		return err
	}
	fmt.Printf("override recorded for %s (gate %s): %s\n", unit, gate, reason)
	fmt.Println("the gate will now allow the commit. This override is in the audit log.")
	return nil
}

// ---------- doctor ----------

func cmdDoctor() error {
	cwd, _ := os.Getwd()
	path, err := spec.Find(cwd)
	if err != nil {
		fmt.Printf("✗ no %s found. Run: batten init\n", spec.Filename)
		return nil
	}
	sp, err := spec.Load(path)
	if err != nil {
		fmt.Printf("✗ %v\n", err)
		return nil
	}
	fmt.Printf("✓ %s — project %q, unit %q, %d phases, %d domains\n",
		path, sp.Project, sp.Unit.Name, len(sp.Phases), len(sp.Domains))

	if c, ok := sp.ClosingPhase(); ok {
		fmt.Printf("✓ close gate: phase %q requires verdict %q on gate %q\n",
			c.ID, c.RequiresVerdict, c.Gate)
	} else {
		fmt.Println("⚠ no phase sets requires_verdict — nothing gates a commit")
	}
	if sp.ReportOnly() {
		fmt.Println("● enforcement: REPORT — gates WARN, they do not block yet. " +
			"Set enforcement: enforce (or remove it) when the team trusts the gates.")
	} else {
		fmt.Println("✓ enforcement: enforce — gates block")
	}

	st, err := store.Open(dbPath())
	if err != nil {
		fmt.Printf("✗ store: %v\n", err)
		return nil
	}
	defer st.Close()
	fmt.Printf("✓ store: %s\n", dbPath())

	// Optional capabilities degrade; report what is actually live rather than what is declared.
	if sp.Capabilities.GraphEnabled() {
		report("graph", sp.Capabilities.Graph.Provider, have(sp.Capabilities.Graph.Provider))
		if sp.Capabilities.Graph.Lessons {
			fmt.Println("  ⚠ graph.lessons is on; it overlaps engram's job. Prefer lessons: false")
		}
		graphStaleness(sp) // a stale code graph gives wrong answers silently; warn loudly
		if sp.Capabilities.Obsidian.Vault != "" {
			fmt.Printf("  → for one graph of all three memories, export graphify into the same vault:\n"+
				"    graphify . --obsidian --obsidian-dir %s\n", expandHome(sp.Capabilities.Obsidian.Vault))
		}
	}
	if sp.Capabilities.Memory.Provider != "" && sp.Capabilities.Memory.Provider != "none" {
		fmt.Printf("· memory: %s (via MCP; batten does not store episodic memory)\n",
			sp.Capabilities.Memory.Provider)
	}
	if sp.Capabilities.CompressionEnabled() {
		report("compression", sp.Capabilities.Compression.Provider, have(sp.Capabilities.Compression.Provider))
		if sp.Capabilities.Compression.Provider == "headroom" {
			if headroomAlive() {
				fmt.Println("  ✓ headroom proxy responding on :8787")
			} else {
				fmt.Println("  ⚠ headroom proxy not responding on :8787 — no compression is happening. " +
					"Start it: headroom init claude")
			}
		}
		if sp.Capabilities.Compression.Memory {
			fmt.Println("  ⚠ compression.memory is on; it duplicates engram. Prefer memory: false")
		}
		if sp.Capabilities.Compression.Measure {
			fmt.Println("  → runs are tagged by headroom on/off; compare with: batten measure")
		} else {
			fmt.Println("  ⚠ compression.measure is off; you are trusting the README instead of your own numbers")
		}
	}
	if v := sp.Capabilities.Obsidian.Vault; v != "" {
		p := expandHome(v)
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("✓ obsidian vault: %s\n", p)
		} else {
			fmt.Printf("⚠ obsidian vault not found: %s (canvas falls back to .batten/)\n", p)
		}
	}
	for name, d := range sp.Domains {
		if d.Rules != "" {
			if _, err := os.Stat(filepath.Join(sp.Root, d.Rules)); err != nil {
				fmt.Printf("⚠ domain %q: rules file missing: %s\n", name, d.Rules)
			}
		}
	}

	// A skill or agent the spec names but the machine does not have is the classic silent
	// failure: it surfaces at 3am inside an unattended run, not here where it is cheap to fix.
	if problems, err := discovery.Validate(sp, sp.Root); err == nil {
		for _, p := range problems {
			hint := ""
			if p.Hint != "" {
				hint = fmt.Sprintf(" (did you mean %q?)", p.Hint)
			}
			fmt.Printf("⚠ %s references %q, which is not installed%s\n", p.Where, p.Ref, hint)
		}
	}

	// The quota ceiling is unenforceable without the statusline, so say plainly whether it is live.
	if sp.Budget.QuotaPctPerRun > 0 {
		if present, existing, _ := statusline.Installed(sp.Root); present && statusline.IsBatten(existing) {
			fmt.Println("✓ statusline installed — the quota ceiling is enforced")
		} else {
			fmt.Println("⚠ budget.quota_pct_per_run is set but the statusline is not installed — that " +
				"ceiling is NOT enforced. Run: batten statusline --install")
		}
	}

	// A run nobody closed keeps its write-set claims alive and muddies session attribution.
	// Surface the stale ones so they don't rot: 48h with no event means abandoned or forgotten.
	if stale, err := st.StaleRuns(sp.Project, 48*time.Hour); err == nil && len(stale) > 0 {
		fmt.Printf("⚠ %d run(s) open >48h with no activity — close or resume them:\n", len(stale))
		for _, r := range stale {
			fmt.Printf("    %s (phase %s): batten close %s [--status ok|failed]\n", r.UnitID, r.Phase, r.UnitID)
		}
	}
	return nil
}

func report(kind, provider string, ok bool) {
	if ok {
		fmt.Printf("✓ %s: %s (on PATH)\n", kind, provider)
	} else {
		fmt.Printf("⚠ %s: %s declared but not on PATH — batten degrades gracefully\n", kind, provider)
	}
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// graphStaleness compares the code graph's age against HEAD. A graph N commits behind answers
// "what already exists?" with yesterday's code — a silent way to mislead the plan phase.
func graphStaleness(sp *spec.Spec) {
	gj := filepath.Join(sp.Root, "graphify-out", "graph.json")
	fi, err := os.Stat(gj)
	if err != nil {
		fmt.Println("  ⚠ no graphify-out/graph.json yet — run: graphify .")
		return
	}
	out, err := exec.Command("git", "-C", sp.Root, "log", "-1", "--format=%ct").Output()
	if err != nil {
		return
	}
	var headUnix int64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &headUnix)
	if headUnix > fi.ModTime().Unix()+3600 { // more than an hour behind HEAD
		fmt.Println("  ⚠ the code graph is older than HEAD — it may answer with stale code.")
		fmt.Println("    refresh: graphify . --update   (or once: graphify hook install)")
	} else {
		fmt.Println("  ✓ code graph is current")
	}
}

// headroomAlive probes the local compression proxy without blocking doctor for long.
func headroomAlive() bool {
	c := http.Client{Timeout: 250 * time.Millisecond}
	resp, err := c.Get("http://127.0.0.1:8787/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

// ---------- init ----------

func cmdInit(args []string) error {
	scanJSON, from := false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scan-json":
			scanJSON = true
		case "--from":
			if i+1 < len(args) {
				from = args[i+1]
			}
		}
	}
	cwd, _ := os.Getwd()

	facts, err := scan.Scan(cwd)
	if err != nil {
		return err
	}

	// --scan-json feeds the facts to /batten-init, which adds the judgment a heuristic can't:
	// mining invariants from AGENTS.md, migrating from a prose doc (--from), naming gates.
	if scanJSON {
		b, _ := json.MarshalIndent(facts, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	dst := filepath.Join(cwd, spec.Filename)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists — remove it or edit it by hand", spec.Filename)
	}
	if err := os.WriteFile(dst, []byte(facts.ToYAML()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s — a working draft in report mode (gates warn, don't block).\n", spec.Filename)
	fmt.Printf("  project=%q unit=%q, %d domain(s) detected", facts.Project, facts.UnitName, len(facts.Domains))
	if facts.Graphify {
		fmt.Print(", graphify found")
	}
	if facts.Engram {
		fmt.Print(", engram found")
	}
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  1. fill the invariants (the TODOs) — the highest-value part of the file")
	if from != "" {
		fmt.Printf("  2. reconcile against your prose workflow: %s\n", from)
	}
	fmt.Println("  2. run: batten doctor")
	fmt.Println("  3. flip enforcement: enforce when you trust the gates")
	fmt.Println("For a richer draft (invariants mined from AGENTS.md, migration from a prose doc),")
	fmt.Println("run /batten-init inside Claude Code instead — it uses `batten init --scan-json`.")
	return nil
}

// ---------- helpers ----------

func gitSHA() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--short=7", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

func gitBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
