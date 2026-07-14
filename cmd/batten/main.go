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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/arthu/batten/internal/canvas"
	"github.com/arthu/batten/internal/discovery"
	"github.com/arthu/batten/internal/hooks"
	"github.com/arthu/batten/internal/mcp"
	"github.com/arthu/batten/internal/spec"
	"github.com/arthu/batten/internal/statusline"
	"github.com/arthu/batten/internal/store"
	"github.com/arthu/batten/internal/tui"
	"github.com/arthu/batten/internal/usage"
	"github.com/arthu/batten/internal/vault"
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

func cmdHook(args []string) error {
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

	h := &hooks.Handler{Spec: sp, Store: st}
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
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no active run for %s", unit)
	}
	nodes, err := st.Nodes(run.RunID)
	if err != nil {
		return err
	}
	edges, err := st.Edges(run.RunID)
	if err != nil {
		return err
	}
	v, _ := st.LatestVerdict(run.RunID, "")

	// A configured vault owns the canvas location: it must sit where the run note embeds it
	// (runs/<unit>.canvas), so the two stay colocated instead of the wikilink resolving by luck.
	var w *vault.Writer
	if vlt := sp.Capabilities.Obsidian.Vault; vlt != "" && out == "" {
		w = vault.New(expandHome(vlt), sp.Project)
		out = w.CanvasPath(unit)
	}
	if out == "" {
		out = filepath.Join(sp.Root, ".batten", unit+".canvas")
	}

	c := canvas.Render(run, nodes, edges, v)
	if err := c.WriteFile(out); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d nodes, %d edges)\n", out, len(c.Nodes), len(c.Edges))

	// The run note carries the frontmatter, the verdict, and the wikilinks that put this run
	// in the same graph as the code (graphify) and the decisions (engram) — the whole point.
	if w != nil {
		usg, _ := st.UsageByNode(run.RunID)
		if err := w.WriteRun(run, nodes, edges, v, usg, w.CanvasRel(unit)); err != nil {
			return fmt.Errorf("run note: %w", err)
		}
		if err := w.WriteBases(); err != nil {
			return fmt.Errorf("bases: %w", err)
		}
		fmt.Printf("wrote run note %s — open the vault in Obsidian\n", w.RunNotePath(unit))
	}
	return nil
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
	}
	if sp.Capabilities.Memory.Provider != "" && sp.Capabilities.Memory.Provider != "none" {
		fmt.Printf("· memory: %s (via MCP; batten does not store episodic memory)\n",
			sp.Capabilities.Memory.Provider)
	}
	if sp.Capabilities.CompressionEnabled() {
		report("compression", sp.Capabilities.Compression.Provider, have(sp.Capabilities.Compression.Provider))
		if sp.Capabilities.Compression.Memory {
			fmt.Println("  ⚠ compression.memory is on; it duplicates engram. Prefer memory: false")
		}
		if !sp.Capabilities.Compression.Measure {
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

// ---------- init ----------

func cmdInit(args []string) error {
	from := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--from" && i+1 < len(args) {
			from = args[i+1]
		}
	}
	cwd, _ := os.Getwd()
	dst := filepath.Join(cwd, spec.Filename)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", spec.Filename)
	}

	// The real work of `init` is a judgment call about a specific repo, and a model
	// makes it far better than a heuristic would. So the binary prepares the ground
	// and hands the interview to the agent, via the /batten-init command.
	fmt.Printf(`batten init prepares a spec by interviewing this repo.

Run this inside Claude Code:

    /batten-init%s

The command inspects your build files, per-directory AGENTS.md/CLAUDE.md, installed
skills, and test targets, then writes %s. Review it — it is your process, as data.
`, fromArg(from), spec.Filename)
	return nil
}

func fromArg(from string) string {
	if from == "" {
		return ""
	}
	return " --from " + from
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
