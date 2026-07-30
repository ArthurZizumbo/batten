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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/ArthurZizumbo/batten/internal/canvas"
	"github.com/ArthurZizumbo/batten/internal/discovery"
	"github.com/ArthurZizumbo/batten/internal/export"
	"github.com/ArthurZizumbo/batten/internal/gitx"
	"github.com/ArthurZizumbo/batten/internal/hooks"
	"github.com/ArthurZizumbo/batten/internal/install"
	"github.com/ArthurZizumbo/batten/internal/mcp"
	"github.com/ArthurZizumbo/batten/internal/plan"
	"github.com/ArthurZizumbo/batten/internal/render"
	"github.com/ArthurZizumbo/batten/internal/scan"
	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/statusline"
	"github.com/ArthurZizumbo/batten/internal/store"
	"github.com/ArthurZizumbo/batten/internal/tui"
	"github.com/ArthurZizumbo/batten/internal/usage"
)

// version is what `batten version` prints and what `doctor` compares the installed binary
// against. GoReleaser overrides it with `-X main.version={{.Version}}`; buildVersion recovers it
// for every other way batten can legitimately be built.
var version = buildVersion("0.1.0")

// buildVersion answers "which batten is this" for a binary GoReleaser did not stamp.
//
// `go install github.com/ArthurZizumbo/batten/cmd/batten@v0.1.0-beta.1` is a real installation
// path — it is the only one engram offers, it is the fallback when a download is blocked, and it
// builds from source the user can audit. Without this it reported the hardcoded default, so a
// perfectly good install disagreed with `plugin.json` and `batten doctor` told the user their
// plugin and binary "disagree about what batten is" and to reinstall. A diagnostic that fires on
// a healthy machine is worse than no diagnostic.
//
// The module version is what the go toolchain embeds for a module-versioned build. "(devel)" is
// what it embeds for a plain `go build` in a checkout, which is not a version and must not be
// presented as one — that case keeps the fallback.
func buildVersion(fallback string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return fallback
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

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
	case "status":
		err = cmdStatus()
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
	case "worktree":
		err = cmdWorktree(os.Args[2:])
	case "unattended":
		err = cmdUnattended(os.Args[2:])
	case "scan-diff":
		err = cmdScanDiff(os.Args[2:])
	case "iterate":
		err = cmdIterate(os.Args[2:])
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
	case "report":
		err = cmdReport(os.Args[2:])
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "pr":
		err = cmdPR(os.Args[2:])
	case "recover":
		err = cmdRecover(os.Args[2:])
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
	if errors.Is(err, errSilent) {
		os.Exit(1) // the command already explained itself; do not say it twice
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
  batten verdict [--file v.json]     record a verdict envelope (the reviewer's judgment)
  batten check <unit> [--gate g]     RUN the gate's checks and record what they printed
  batten close <unit> [--status s]   close a unit through the gate, releasing its write-sets
  batten worktree <unit> [--merge]   one tree per unit; the merge back is gated like a commit
  batten unattended <unit> [--off]   nobody is watching: 4 rules become denials (/batten-night)
  batten iterate <unit>              count one fix->re-verify round; refuses past max_iterations
  batten scan-diff <unit> [--strict] the real git diff vs the declared write-sets
  batten demo [--keep]               the whole flow on a throwaway repo; touches nothing of yours
  batten pr <unit> [--out p]         a PR body from the run record: Mermaid DAG, evidence, cost
  batten recover <unit>              re-anchor a run whose base moved (rebase, amend, pull)
  batten report [--since d|--week]   what batten saw, and what it stopped (--share for markdown)
  batten runs                        list runs
  batten status                      the backlog against the record: runs and criteria coverage
  batten show <unit> [--run <id>]    run detail
  batten canvas <unit> [--out p]     emit the run DAG as JSON Canvas (--html for a standalone page)
  batten budget [<unit>]             governor status (tokens / imputed $ / quota)
  batten override <unit> --reason    audited escape from the gate
  batten tui                         review runs in a terminal UI
  batten mcp                         MCP server (exposes the run graph to the agent)
  batten statusline [--install]      status line + the only subscription-quota sensor
  batten ingest <unit> --transcript  price a transcript's tokens into the run ledger
  batten measure                     spend by model, and what the capabilities bought
`)
}

// ---------- wiring ----------

// dbPath keeps state where it survives a plugin update AND where every process agrees it is.
// Always ~/.batten, never ${CLAUDE_PLUGIN_DATA}: hook processes have that env var set but the
// user's terminal does not, so an env-dependent path splits the state into two databases — the
// TUI shows "no runs" while the hooks are happily writing runs somewhere else. Found live in
// E0 (twice: first the tap, then this). ${CLAUDE_PLUGIN_ROOT} stays forbidden regardless: it
// is wiped on every plugin update.
func dbPath() string {
	if d := os.Getenv("BATTEN_DB"); d != "" {
		return d
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

// errNotGoverned means no batten.yaml was found above the hook's cwd. It is the ONE failure
// that deserves silence: batten governs where it was invited and nowhere else, and a plugin
// installed globally fires its hooks in every repo the user opens.
//
// Every other failure is the opposite case — there IS a batten.yaml, so this repo asked to be
// governed, and batten just didn't govern it. That has to be said out loud.
var errNotGoverned = errors.New("batten: no batten.yaml governs this directory")

func cmdHook(args []string) (retErr error) {
	if len(args) < 1 {
		return errors.New("hook: need an event name")
	}
	event := args[0]
	wrote := false

	// A hook runs on every tool call. A panic here — a nil deref, a bad type assertion on a
	// payload shape we didn't expect — would paint a Go stack trace across the user's session.
	// Principle #2: degrade, never break. Swallow it and exit clean; the tool call proceeds.
	//
	// But principle #3 is that we only fail open OUT LOUD. A recovered panic used to leave
	// nothing at all on stdout, and this hook's silence is indistinguishable from an allow —
	// so the guard that just crashed reads exactly like a guard that just approved.
	defer func() {
		if r := recover(); r != nil {
			if !wrote {
				degraded(event, fmt.Sprintf("batten crashed while handling this event (%v)", r))
			}
			retErr = nil
		}
	}()

	raw, err := hooks.ReadInput(os.Stdin)
	if err != nil {
		degraded(event, fmt.Sprintf("could not read the hook payload (%v)", err))
		return nil
	}

	sp, st, err := loadForHook(raw)
	switch {
	case errors.Is(err, errNotGoverned):
		return nil // correct silence: nobody asked us to govern here
	case err != nil:
		degraded(event, err.Error())
		return nil
	}
	defer st.Close()

	h := &hooks.Handler{Spec: sp, Store: st, TapPath: tapPath()}
	out, err := h.Dispatch(event, raw)
	if err != nil {
		degraded(event, fmt.Sprintf("the %s handler failed (%v)", event, err))
		return nil
	}
	if out == nil {
		return nil
	}
	wrote = true
	return json.NewEncoder(os.Stdout).Encode(out)
}

// degraded reports that batten did not do its job for this event, without blocking anything.
//
// It is deliberately NOT fail-closed. The most common cause is a busy SQLite file, and denying
// every tool call while another process holds the write lock would brick the session — batten
// would be uninstalled the first afternoon two agents ran at once. So the tool call proceeds and
// the user is told that it proceeded ungoverned, which is the one thing they cannot infer
// themselves: `batten hook` exits 0 with no output for at least six different reasons, and
// "allowed" is only one of them.
//
// Only the ENFORCING events say anything. PreToolUse is where the commit gate and the write-set
// guard live, so silence there is a false approval. SessionStart, Stop and the subagent events
// only record; losing one costs a node in the graph, not a rule.
func degraded(event, cause string) {
	if event != "PreToolUse" {
		return
	}
	// Not journalled: the reason batten is degraded is usually that it cannot write to the
	// store, so a row recording that it could not write a row is not something to count on.
	_ = json.NewEncoder(os.Stdout).Encode(hooks.AdviseDegraded(fmt.Sprintf(
		"batten did NOT run for this tool call — neither the commit gate nor the write-set "+
			"guard was applied, and nothing here was verified.\ncause: %s\n"+
			"Retry, or diagnose it with `batten doctor`.", cause)))
}

// loadForHook resolves the spec from the hook's own cwd, not the process cwd —
// a hook process may be started anywhere.
//
// The two failures are kept apart because they mean opposite things to the user. "There is no
// batten.yaml" is not a problem; "there is a batten.yaml and it does not load" is the gate being
// down in a repo that asked for one. Collapsing both into a bare error made them indistinguishable
// at the call site, and the call site chose silence for both.
func loadForHook(raw []byte) (*spec.Spec, *store.Store, error) {
	var probe struct {
		CWD string `json:"cwd"`
	}
	_ = json.Unmarshal(raw, &probe)
	dir := probe.CWD
	if dir == "" {
		dir, _ = os.Getwd()
	}

	path, err := spec.Find(dir)
	if err != nil {
		return nil, nil, errNotGoverned
	}
	sp, err := spec.Load(path)
	if err != nil {
		return nil, nil, fmt.Errorf("%s did not load (%v)", spec.Filename, err)
	}
	st, err := store.Open(dbPath())
	if err != nil {
		return nil, nil, fmt.Errorf("could not open the store at %s (%v)", dbPath(), err)
	}
	return sp, st, nil
}

// ---------- verdict ----------

// verdictShapeError translates the JSON decoder's complaint into something the agent that wrote
// the file can act on (#27).
//
// This fires at the worst possible moment — the FIRST verdict a new adopter records, which is the
// step the whole workflow exists to reach — and what it used to hand back was
// `json: cannot unmarshal object into Go struct field Verdict.evidence of type string`. True, and
// it names a Go type and a Go struct field that appear nowhere in the documentation they were
// following, so the only way out is to go read batten's source.
//
// The specific mistake behind the finding is worth teaching rather than merely rejecting: a model
// asked for evidence naturally emits a list of OBJECTS (`{"criterion": ..., "output": ...}`), and
// evidence[] is a list of strings on purpose — a string is what survives the envelope round trip
// and what every surface renders. The convention that replaces the object is `AC-<n>:` as a
// prefix, so the error says so; a rejection that does not name the shape it wants sends the agent
// guessing, and it will guess objects again.
func verdictShapeError(err error) error {
	const envelope = "verdict must be a JSON envelope {check_id, result, evidence[], why, " +
		"safe_next_step, requires_confirmation}"

	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return fmt.Errorf("%s: %w", envelope, err)
	}
	if strings.HasPrefix(te.Field, "evidence") {
		return fmt.Errorf("verdict: evidence[] must be a list of STRINGS, and this envelope has "+
			"%s in it.\n"+
			"Each entry is one citation — a command and its output, a test count, a criterion "+
			"verified — written as plain text:\n"+
			"    \"evidence\": [\"go test ./... — ok, 137 tests\", \"AC-2: rejects an empty body (handler_test.go:88)\"]\n"+
			"To tie a citation to an acceptance criterion, prefix it `AC-<n>:` rather than nesting "+
			"an object. The prefix is the whole convention; there is no object form.", aOrAn(te.Value))
	}
	if te.Field != "" {
		return fmt.Errorf("verdict: field %q must be %s, and this envelope has %s in it.\n%s",
			te.Field, te.Type, aOrAn(te.Value), envelope)
	}
	return fmt.Errorf("%s — this file is %s, not an object: %w", envelope, aOrAn(te.Value), err)
}

// aOrAn renders the decoder's Value ("object", "array", "number", "string", "bool") with an
// article, because the message reads as a sentence to whoever has to fix the file.
func aOrAn(value string) string {
	if value == "" {
		return "the wrong type"
	}
	if strings.ContainsAny(value[:1], "aeiou") {
		return "an " + value
	}
	return "a " + value
}

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
		return verdictShapeError(err)
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

	// ActiveRun, never EnsureRun: a verdict judges an OPEN run. On a closed unit, EnsureRun
	// silently opened a second run with no anchor and no phase, filed the verdict there, and
	// exit 0 made it look right (#12). Opening a run is `batten phase`'s job alone.
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no open run for %s — a verdict judges an open run, and recording one "+
				"cannot start one.\nReopen with: batten phase %s %s", unit, unit, sp.Phases[0].ID)
		}
		return err
	}
	if gate == "" {
		gate = sp.ClosingGateName()
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
	if export.VaultPath(sp) != "" {
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
		// #7's sibling case, and the same failure as the directory claim below: the write-set
		// guard compares REPO-RELATIVE paths, so a path outside the root can never match one. An
		// absolute path elsewhere used to be stored verbatim and answered with the success line —
		// an imaginary fence, reported as protection, around a file batten will never guard. A
		// plan that trusts it is worse off than one with no claim at all.
		//
		// A relative argument is resolved against the ROOT, not the process cwd, because that is
		// what the write-set is keyed by; `../..` escapes are caught by the same comparison.
		orig := f
		abs := f
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(sp.Root, abs)
		}
		rel, relErr := filepath.Rel(sp.Root, abs)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("claim: %q is outside this repository (%s), and the write-set fence "+
				"only matches paths INSIDE it — this claim would be recorded as protection and "+
				"protect nothing.\n"+
				"batten governs the repo it was invited into. Claim a path under %s.",
				orig, sp.Root, sp.Root)
		}
		f = filepath.ToSlash(rel)
		// A directory or glob claim is accepted by the fence and fences NOTHING: the guard
		// compares exact repo-relative paths, so `src/**` was recorded, reported as protection,
		// and protected no file at all (#7). Refusing is the honest option — a false fence is
		// worse than none, because the plan trusts it. A path that does not exist yet is fine:
		// agents legitimately claim files they are about to create.
		if why := dirClaimShape(sp.Root, f); why != "" {
			return fmt.Errorf("claim: %q is %s, and the write-set fence matches EXACT paths — a "+
				"pattern would be recorded as protection and protect nothing.\n"+
				"List the files instead (your shell can expand a glob):\n"+
				"  batten claim %s <file> <file> ...", f, why, agent)
		}
		// #4 — the half that existed and was never called.
		//
		// `claim` only ever looked inside its OWN run: the writesets primary key defends one run,
		// so the second run of a project claimed the same path, got the success line *"any other
		// agent writing them is now denied"*, and then the guard denied BOTH owners at write time.
		// batten handed out a fence it could not honour and only said so after the work started.
		//
		// store.WriteSetOwnerAcrossOpenRuns is the exact query the guard already uses
		// (hooks.go, bashwrite.go). Refusing here moves the discovery from "mid-fan-out, to both
		// agents" to "at plan time, to the one who can still change the plan".
		if cross, cerr := st.WriteSetOwnerAcrossOpenRuns(sp.Project, f, node.RunID); cerr == nil && cross != nil {
			// The worktree carve-out the guard makes, made here too: two trees, two branches, two
			// checkouts of the same repo-relative path is not a race — it is the arrangement
			// batten's own messages recommend, and denying it would punish the fix.
			if cross.Worktree == "" || gitx.SameTree(sp.Root, cross.Worktree) {
				return fmt.Errorf("claim: %s is already claimed by %s in run %s (unit %s), which is "+
					"still open.\n"+
					"Claiming it here would hand the same file to two owners, and the guard then "+
					"denies BOTH of them at write time.\n"+
					"Close that run, drop this path from the plan, or give each unit its own tree: "+
					"batten worktree %s", f, store.DisplayNodeID(cross.NodeID), cross.RunID,
					cross.UnitID, cross.UnitID)
			}
		}
		files = append(files, f)
	}
	if err := st.ClaimWriteSet(node.RunID, node.NodeID, files); err != nil {
		return err
	}
	fmt.Printf("%s owns %d file(s); any other agent writing them is now denied\n", agent, len(files))
	return nil
}

// dirClaimShape reports why a claimed path is a directory-shaped claim, or "" when it is a
// plain file path.
func dirClaimShape(root, f string) string {
	if strings.ContainsAny(f, "*?[") {
		return "a glob"
	}
	if strings.HasSuffix(f, "/") {
		return "a directory"
	}
	if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil && fi.IsDir() {
		return "a directory"
	}
	return ""
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
	// The same command hard-rejects a phase that does not exist; a unit id deserved the same
	// rigor and got none — `batten phase FOO-9 build` opened a phantom run with exit 0, listed
	// forever by `batten runs` with no way to delete it (#21).
	if !sp.ValidUnitID(unit) {
		return fmt.Errorf("%q does not match unit.pattern %q — no run was opened", unit, sp.Unit.Pattern)
	}
	run, err := st.EnsureRun(sp.Project, unit, "")
	if err != nil {
		return err
	}
	// Seed the run's acceptance criteria from the unit's block in unit.plan (ítem 21). Once
	// per run — SeedCriteria is a no-op when rows exist, so statuses survive phase changes.
	// Best-effort: an ad-hoc unit that is not in the backlog, or no backlog at all, is a
	// normal state and must never block a phase.
	if units, perr := plan.Load(sp); perr == nil {
		if u, ok := plan.Find(units, unit); ok {
			if crit := u.Criteria(); len(crit) > 0 {
				_ = st.SeedCriteria(run.RunID, unit, crit)
			}
		}
	}
	if err := st.SetPhase(run.RunID, phaseID); err != nil {
		return err
	}
	// Rule 2 of the unsupervised run, checked before the phase moves. `batten iterate` is what a
	// well-behaved loop calls, and this is what catches the loop that does not: an unattended run
	// cannot advance a phase once it has spent its declared rounds.
	if run.Unattended() {
		if reached, used, ceiling := hooks.IterationCeiling(run, sp.Budget.MaxIterations); reached {
			return errors.New(hooks.Refusal(hooks.CodeIterationCeiling, fmt.Sprintf(
				"%s has used %d of %d iterations and is running unattended: refusing to advance "+
					"to %s.\nStop and report. A human turns the mode off: "+
					"batten unattended %s --off", unit, used, ceiling, phaseID, unit)))
		}
	}
	// Which tree this run is standing in. Recorded here, from the spec's own directory, because
	// that is the identity the write-set guard compares against on the hook path and it must come
	// from the same place on both sides — a guard that computed it differently would deny across
	// worktrees exactly when it was supposed to stop.
	if run.Worktree == "" {
		_ = st.SetWorktree(run.RunID, sp.Root)
		run.Worktree = sp.Root
	}
	// Tag the run with whether compression is live, so `batten measure` can compare later.
	if sp.Capabilities.CompressionEnabled() && sp.Capabilities.Compression.Measure {
		_ = st.SetHeadroom(run.RunID, headroomAlive())
	}
	// Same treatment for the code graph: does having one actually cut orientation cost?
	if sp.Capabilities.GraphEnabled() {
		_, fresh := codeGraphFresh(sp)
		_ = st.SetCodeGraph(run.RunID, fresh)
	}
	// Finish the phase this run is leaving, so a phase reads `running` only while it is.
	// Scoped by run: with a global id this would have closed another unit's phase.
	prev := ""
	if run.Phase != "" && run.Phase != phaseID {
		prev = run.Phase
		_ = st.FinishNode(store.PhaseNodeID(run.RunID, prev), "ok", 0)
	}
	_ = st.AddNode(store.Node{
		NodeID: store.PhaseNodeID(run.RunID, phaseID), RunID: run.RunID,
		Kind: "phase", Label: phaseID, Status: "running",
	})
	// Chain the phases. Without this the run graph is a row of disconnected boxes with subagents
	// hanging off them: every surface called it a DAG and it had no edge between any two phases.
	// `depends_on` was the second relation with readers and no producer — the canvas gave it a
	// colour, and nothing could ever be that colour.
	if prev != "" {
		_ = st.AddEdge(run.RunID, store.PhaseNodeID(run.RunID, phaseID),
			store.PhaseNodeID(run.RunID, prev), "depends_on")
	}

	// The anchor. Every later phase diffs from here, not from HEAD~N.
	if ph.Anchor == "git_sha" && run.BaseSHA == "" {
		sha, err := gitSHA()
		if err == nil {
			_ = st.SetBaseSHA(run.RunID, sha)
			run.BaseSHA = sha
			fmt.Printf("anchor: %s base SHA = %s\n", unit, sha)
		}
	}
	fmt.Printf("%s -> phase %s\n", unit, phaseID)
	// And what `diff_from: anchor` means here, if the phase declares it. Entering a phase scoped
	// to an anchor that was never recorded used to print nothing at all: the run carried an empty
	// base, the promise was void, and the only way to find out was to read the database.
	if s := hooks.DiffScope(sp, st, &ph, run); s != "" {
		fmt.Print(strings.TrimPrefix(s, "- "))
	}
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
	verified, floor, overridden := false, false, false
	gate := sp.ClosingGateName()
	for _, r := range runs {
		verdict := "—"
		if v, err := st.LatestVerdict(r.RunID, ""); err == nil {
			verdict = v.Result
			// A batten-sourced verdict means the gate's checks actually ran; mark it
			// so a verified pass is distinguishable from an agent claim at a glance.
			if v.Source == "batten" {
				verdict += "*"
				verified = true
			}
		}
		// An override opens the gate no matter what the verdict column says; a row that
		// hides it presents an ungoverned run as a governed one (#10).
		if ov, err := st.OverrideFor(r.RunID, gate); err == nil && ov != nil {
			if verdict == "—" {
				verdict = "overridden"
			} else {
				verdict += " (overridden)"
			}
			overridden = true
		}
		tok, usd := "—", "—"
		if r.TokensSpent > 0 {
			tok = render.Tokens(r.TokensSpent)
			usd = render.ImputedShort(r.ImputedUSD, r.UnpricedTokens, r.TokensSpent)
			if r.UnpricedTokens > 0 {
				floor = true
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.UnitID, r.Status, r.Phase, verdict, tok, usd)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if verified {
		fmt.Println("* batten-verified: the gate's checks were actually run")
	}
	if overridden {
		fmt.Println("overridden: the gate is OPEN by `batten override` — the commit is audited, not verified. Details: batten show <unit>")
	}
	if floor {
		fmt.Println("≥ / not priced: part of those tokens are on models with no published rate — the dollar figure is a floor, never a total")
	}
	return nil
}

// cmdStatus is the compliance view (ítem 21, fase C): the backlog against the record. Every
// unit the plan document defines, whether batten has seen it or not — which is the half
// `batten runs` cannot show, because a unit nobody started has no run to list.
func cmdStatus() error {
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	units, err := plan.Load(sp)
	if err != nil {
		if errors.Is(err, plan.ErrNoPlan) {
			return errors.New("status: no unit.plan declared in batten.yaml — there is no backlog to " +
				"report against. `batten runs` lists what batten has seen")
		}
		return err
	}
	if len(units) == 0 {
		return fmt.Errorf("status: unit.plan %s has no unit matching locator %q — batten doctor explains",
			sp.Unit.Plan, sp.Unit.Locator)
	}

	fmt.Printf("backlog %s — %d unit(s)\n\n", sp.Unit.Plan, len(units))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	seen := map[string]bool{}
	for _, u := range units {
		seen[u.ID] = true
		run, err := st.LatestRun(sp.Project, u.ID)
		state, criteria := "· not started", ""
		if err == nil {
			switch run.Status {
			case "running":
				state = "◐ " + run.Status + " (" + run.Phase + ")"
			case "ok":
				state = "✓ closed ok"
			default:
				state = "✗ " + run.Status
			}
			// The coverage column never invents: no rows seeded means the unit declares no
			// criteria (or predates the table) — which is not the same fact as 0 covered.
			if cs, err := st.Criteria(run.RunID); err == nil && len(cs) > 0 {
				covered := 0
				for _, c := range cs {
					if c.Status == store.StatusCovered {
						covered++
					}
				}
				criteria = fmt.Sprintf("AC %d/%d covered", covered, len(cs))
			} else {
				criteria = "no criteria seeded"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", u.ID, firstLineOf(u.Title), state, criteria)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Work the record knows that the backlog does not: ad-hoc units are normal, and hiding
	// them here would make this view claim the backlog is the whole world.
	runs, err := st.ListRuns(sp.Project, 200)
	if err != nil {
		return err
	}
	var adhoc []string
	adhocSeen := map[string]bool{}
	for _, r := range runs {
		if !seen[r.UnitID] && !adhocSeen[r.UnitID] {
			adhocSeen[r.UnitID] = true
			adhoc = append(adhoc, fmt.Sprintf("%s (%s)", r.UnitID, r.Status))
		}
	}
	if len(adhoc) > 0 {
		fmt.Printf("\nnot in the backlog: %s\n", strings.Join(adhoc, ", "))
	}
	return nil
}

func cmdShow(args []string) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "--") {
		return errors.New("show: batten show <unit> [--run <id>]")
	}
	unit, runID := args[0], ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--run":
			if i+1 >= len(args) {
				return errors.New("show: --run needs a run id (batten mcp's batten_runs lists them)")
			}
			runID, i = args[i+1], i+1
		default:
			// A flag this command does not know must fail, not vanish: `--run <id>` was being
			// swallowed here, showing the LATEST run under the id the user asked for (#23).
			return fmt.Errorf("show: unknown flag %q", args[i])
		}
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()
	var run *store.Run
	if runID != "" {
		run, err = st.Run(runID)
		if err != nil {
			return fmt.Errorf("no run %q recorded — exact ids come from `batten show <unit>` or MCP's batten_runs", runID)
		}
		if run.UnitID != unit {
			return fmt.Errorf("run %q belongs to %s, not %s", runID, run.UnitID, unit)
		}
	} else {
		// Latest run regardless of status: the run you just closed is the one you most
		// want to inspect, and "no active run" right after a clean close reads like a bug.
		run, err = st.LatestRun(sp.Project, unit)
		if err != nil {
			return fmt.Errorf("no run recorded for %s", unit)
		}
	}
	fmt.Printf("%s  run=%s  status=%s  phase=%s  base=%s\n",
		run.UnitID, run.RunID, run.Status, run.Phase, run.BaseSHA)
	if run.TokensSpent > 0 {
		fmt.Printf("usage: %s tokens, %s\n",
			render.Tokens(run.TokensSpent), render.ImputedLong(run.ImputedUSD, run.UnpricedTokens, run.TokensSpent))
	}
	nodes, _ := st.Nodes(run.RunID)
	models, _ := st.ModelsByNode(run.RunID) // what each node actually ran on, from the ledger
	for _, n := range nodes {
		ws, _ := st.WriteSet(run.RunID, n.NodeID)
		label := n.Label
		if n.Attempt > 1 {
			// Two rows reading `ui failed` and `ui ok` are the retry, and nothing said so.
			label = fmt.Sprintf("%s #%d", label, n.Attempt)
		}
		fmt.Printf("  [%s] %-24s %-8s", n.Kind, label, n.Status)
		if len(ws) > 0 {
			fmt.Printf(" owns %d file(s)", len(ws))
		}
		// Show the real model, and flag it when the domain declared a different one — this is
		// the routing VERIFICATION: "declared haiku, ran opus" is a silent overspend otherwise.
		if ran := models[n.NodeID]; len(ran) > 0 {
			fmt.Printf(" ran %s", strings.Join(ran, ","))
			if n.Domain != "" {
				if d, ok := sp.Domains[n.Domain]; ok && d.Model != "" && !modelMatches(d.Model, ran) {
					fmt.Printf("  ⚠ declared %s", d.Model)
				}
			}
		}
		fmt.Println()
	}
	// Both verdicts, labelled by producer. The gate needs one of each — batten's, proving the
	// declared checks ran, and a reviewer's, judging the work against its acceptance criteria.
	// Rendering only the newest row meant `batten check` hid the reviewer's evidence behind its
	// own check output, so the screen showed one half of a two-half rule.
	bv, bErr := st.LatestVerdictBySource(run.RunID, "", "batten")
	av, aErr := st.LatestVerdictNotBySource(run.RunID, "", "batten")
	// The override changes what every line below means: the gate is OPEN no matter what the
	// verdicts say, so claiming "the close gate will deny a commit" here states the literal
	// opposite of what the hook will do (#10). The statusline and MCP already read this from
	// the same store; the CLI was simply not asking.
	ov, _ := st.OverrideFor(run.RunID, sp.ClosingGateName())
	if bErr != nil && aErr != nil {
		if ov != nil {
			fmt.Printf("\nno verdict — and the gate is OPEN anyway: overridden %s\n  reason: %s\n"+
				"a commit will be ALLOWED without verification (audited, not verified)\n",
				time.Unix(ov.TS, 0).Format("2006-01-02 15:04"), ov.Reason)
			return nil
		}
		fmt.Println("\nno verdict: the close gate will deny a commit")
		return nil
	}
	fmt.Println()
	for _, v := range []*store.Verdict{av, bv} {
		if v == nil {
			continue
		}
		fmt.Printf("verdict %s=%s (%s): %s\n", v.Gate, v.Result, v.Source, v.Why)
		for _, e := range v.Evidence {
			fmt.Println("  -", firstLineOf(e))
		}
		if len(v.Evidence) == 0 {
			fmt.Println("  (no evidence — this cannot be an approval)")
		}
	}
	if aErr != nil {
		fmt.Println("no reviewer verdict yet — `batten check` proves the checks ran, not that " +
			"the work meets its acceptance criteria")
	}
	if bErr != nil {
		fmt.Printf("no batten-verified pass yet — run: batten check %s\n", run.UnitID)
	}
	if ov != nil {
		fmt.Printf("⚠ overridden %s: the gate will allow a commit REGARDLESS of the verdicts above\n"+
			"  reason: %s\n", time.Unix(ov.TS, 0).Format("2006-01-02 15:04"), ov.Reason)
	}
	return nil
}

// canvasHTML writes the run as a single self-contained page.
//
// It works on the LATEST run rather than only a closed one, on purpose: an artifact that exists
// only once the work is over does not get shared at the moment somebody is excited about it, and
// mid-run is exactly when a fan-out is worth looking at.
func canvasHTML(sp *spec.Spec, st *store.Store, unit, out string) error {
	run, err := st.LatestRun(sp.Project, unit)
	if err != nil {
		return fmt.Errorf("no run recorded for %s", unit)
	}
	nodes, err := st.Nodes(run.RunID)
	if err != nil {
		return err
	}
	edges, _ := st.Edges(run.RunID)
	ws, _ := st.WriteSetsByRun(run.RunID)
	usg, _ := st.UsageByNode(run.RunID)
	rv, _ := st.LatestVerdictNotBySource(run.RunID, "", "batten")
	bv, _ := st.LatestVerdictBySource(run.RunID, "", "batten")
	ov, _ := st.OverrideFor(run.RunID, sp.ClosingGateName())

	details := map[string]canvas.Detail{}
	for _, n := range nodes {
		d := canvas.Detail{
			Kind: n.Kind, Domain: n.Domain, AgentID: n.AgentID,
			AgentType: n.AgentType, Status: n.Status, WriteSet: ws[n.NodeID],
		}
		if u, ok := usg[n.NodeID]; ok {
			d.Tokens = totalTokens(u)
			d.ImputedUSD, d.Priced = u.ImputedUSD, u.ImputedUSD > 0
		}
		details[n.NodeID] = d
	}
	retries := 0
	for _, e := range edges {
		if e.Rel == "retry_of" {
			retries++
		}
	}

	c := canvas.Render(run, nodes, edges, rv, bv, ov)
	if out == "" {
		out = filepath.Join(sp.Root, ".batten", unit+".html")
	}
	if err := c.WriteHTML(out, canvas.HTMLInput{
		Run: run, Details: details, Reviewer: rv, Batten: bv, Retries: retries, Override: ov,
	}); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d nodes, %d edges)\n", out, len(c.Nodes), len(c.Edges))
	fmt.Println("one file, no network — open it in any browser, or attach it anywhere.")
	return nil
}

// modelMatches reports whether the declared model (an alias like "haiku") appears in what
// actually ran (full ids like "claude-haiku-4-5-20251001"). Substring match, since the spec
// uses short aliases and the ledger stores the concrete id.
func modelMatches(declared string, ran []string) bool {
	for _, r := range ran {
		if strings.Contains(strings.ToLower(r), strings.ToLower(declared)) {
			return true
		}
	}
	return false
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func cmdCanvas(args []string) error {
	if len(args) < 1 {
		return errors.New("canvas: batten canvas <unit> [--out <path>] [--html]")
	}
	sp, st, err := load()
	if err != nil {
		return err
	}
	defer st.Close()

	unit := args[0]
	out, asHTML := "", false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--out":
			if i+1 < len(args) {
				out, i = args[i+1], i+1
			}
		case "--html":
			// One file, openable in any browser, no Obsidian. The JSON Canvas needs a reader;
			// this needs nothing.
			asHTML = true
		}
	}
	if asHTML {
		return canvasHTML(sp, st, unit, out)
	}

	// --out is the escape hatch for "just give me the canvas here"; without it, export.Run
	// does the full thing (canvas + vault note + dashboards) — the same path the Stop hook fires.
	if out != "" {
		// Latest, not active: exporting the DAG of a run you just closed is the normal case.
		run, err := st.LatestRun(sp.Project, unit)
		if err != nil {
			return fmt.Errorf("no run recorded for %s", unit)
		}
		nodes, _ := st.Nodes(run.RunID)
		edges, _ := st.Edges(run.RunID)
		// Both, by producer — same reason as export.Run: the newest row alone lets `batten check`
		// stand in for the reviewer, and the canvas then draws one green pass for a gate that
		// still needs a second one.
		rv, _ := st.LatestVerdictNotBySource(run.RunID, "", "batten")
		bv, _ := st.LatestVerdictBySource(run.RunID, "", "batten")
		ov, _ := st.OverrideFor(run.RunID, sp.ClosingGateName())
		c := canvas.Render(run, nodes, edges, rv, bv, ov)
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
	if res.CanvasPath != "" {
		fmt.Printf("wrote %s (%d nodes, %d edges)\n", res.CanvasPath, res.Nodes, res.Edges)
	} else {
		fmt.Println("canvas not written: capabilities.obsidian.export excludes it — add \"canvas\" to the list, or use --out for a one-off file")
	}
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

	// Model routing: where the tokens actually went, by model. This makes the whole point of
	// routing visible — is the cheap tier actually carrying the mechanical work? — with real
	// numbers, not a promise.
	if byModel, err := st.MeasureByModel(sp.Project); err == nil && len(byModel) > 0 {
		fmt.Println("spend by model (imputed, not billed):")
		for _, m := range byModel {
			name := m.Model
			if name == "" {
				name = "(unknown)"
			}
			fmt.Printf("  %-28s %d req, %s tokens, %s\n", name, m.Requests, render.Tokens(m.Tokens), measurePrice(m))
		}
		fmt.Println()
	}

	if groups, err := st.MeasureByHeadroom(sp.Project); err == nil {
		printFlagComparison(groups, "headroom")
	}
	if groups, err := st.MeasureByCodeGraph(sp.Project); err == nil {
		printFlagComparison(groups, "code graph")
	}
	printWriteSets(st, sp.Project)
	return nil
}

// printWriteSets answers one question: does declaring a write-set by hand
// over-declare the way S-Bus (arXiv:2605.17076) measured automatically reconstructed read-sets
// over-declaring, between 32% and 49%?
//
// Three rules hold this honest, and each of them was a way to get a flattering number:
//
//   - a run that claimed something and was never scanned is NOT MEASURED, never 0%. Silence
//     about the unscanned would make the median describe whichever runs somebody bothered to
//     check, and those are the ones somebody was already worried about.
//   - the deny and advise counts are printed SEPARATELY, because under `enforcement: report`
//     every deny becomes an advise and a cross-run collision with no agent_id is advisory
//     always. Summing them subcounts nothing, but reading the deny line alone in a report-mode
//     history reads as "the guard never fired" when it fired every time.
//   - `bash_write` is its own line. It is the advisory half of the guard (the `sed -i` route),
//     and folding it into write_set would claim a fence held where it only spoke.
//
// And the fence around the whole block: FirstDecisionAt. Without it a reader takes these for
// project-lifetime totals, and they are only ever "since batten started watching".
func printWriteSets(st *store.Store, project string) {
	ws, err := st.WriteSetUtilization(project)
	if err != nil {
		return
	}
	if ws.Runs == 0 && ws.Unscanned == 0 {
		return // no run of this project ever claimed a path; there is nothing to be honest about
	}

	fmt.Println("write-sets — declared against touched:")
	if ws.Runs == 0 {
		fmt.Printf("  NOT MEASURED: %d run(s) declared write-sets and none was ever scanned.\n", ws.Unscanned)
		fmt.Println("  Run `batten scan-diff <unit>` after a fan-out, or put it in gates.checks so")
		fmt.Println("  every `batten check` records one. Nothing here is 0% — it is unmeasured.")
		fmt.Println()
		return
	}

	pct := ws.MedianUnused * 100
	fmt.Printf("  median over-declaration: %.0f%% of claimed paths were never touched (N=%d run(s))\n", pct, ws.Runs)
	if ws.Unscanned > 0 {
		fmt.Printf("  NOT MEASURED: %d further run(s) claimed paths and were never scanned — not 0%%.\n", ws.Unscanned)
	}
	if ws.Undeclared > 0 {
		fmt.Printf("  %d file(s) changed across those runs that no write-set claimed.\n", ws.Undeclared)
	}
	// The pre-registered reading, printed with the number so the threshold cannot be chosen
	// after seeing it. The bands: ≥32% (the S-Bus floor) means over-declaration is real and advise()
	// should extend to low-severity collisions, with the severity decided BY A RULE; ≤15% with ≥5 runs
	// in enforce and zero write_set denials means the problem was invented and the line closes.
	switch {
	case ws.Runs < 10:
		fmt.Printf("  (below the pre-registered N: the threshold needs ≥10 scanned runs.\n")
		fmt.Printf("   %d more to go. Read this as a direction, not a result.)\n", 10-ws.Runs)
	case pct >= 32:
		fmt.Println("  → at or above the 32% floor of the S-Bus band: over-declaration is real here.")
	case pct <= 15:
		fmt.Println("  → at or below 15%: hand-declared write-sets are not over-declaring.")
	default:
		fmt.Println("  → between the pre-registered bands: keep measuring.")
	}
	// Stated every time, because `unused` is a ceiling and the number is easy to quote as
	// something sharper than it is.
	fmt.Println("  (`unused` is an UPPER BOUND on over-declaration: it mixes \"claimed too much\"")
	fmt.Println("   with \"the file legitimately needed no change\". It picks between actions;")
	fmt.Println("   it is not a percentage of lying.)")

	printGuardDecisions(st, project)
	fmt.Println()
}

// printGuardDecisions is the other half: what the guard actually did over the same history.
// Split deny/advise per rule for the reason in printWriteSets' comment.
func printGuardDecisions(st *store.Store, project string) {
	first, err := st.FirstDecisionAt(project)
	if err != nil || first == 0 {
		return
	}
	counts, err := st.CountDecisions(project, 0)
	if err != nil {
		return
	}
	denied, advised := map[string]int{}, map[string]int{}
	for _, c := range counts {
		switch c.Decision {
		case store.DecisionDeny:
			denied[c.Rule] += c.N
		case store.DecisionAdvise:
			advised[c.Rule] += c.N
		}
	}
	fmt.Printf("  write-set guard: %d denied, %d advised   ·   bash writes: %d advised\n",
		denied[store.RuleWriteSet], advised[store.RuleWriteSet], advised[store.RuleBashWrite]+denied[store.RuleBashWrite])
	fmt.Printf("  (counted since batten's first recorded decision, %s — not the project's lifetime.\n",
		time.Unix(first, 0).Format("2006-01-02"))
	fmt.Println("   Under `enforcement: report` a denial is recorded as an advisory, so a zero on the")
	fmt.Println("   left with a number on the right means the guard fired and did not block.)")
}

// measurePrice renders one model row's dollar column. A model with no published rate has no
// dollar figure — never $0.00 under a header reading "spend by model", which would price the
// unpriceable as free (the vault note already enforces this rule). A partially unpriced row
// is a floor, not a total, and says so.
func measurePrice(m store.ModelSpend) string {
	switch {
	case m.UnpricedRequests > 0 && m.UnpricedRequests == m.Requests:
		return "UNPRICED (no published rate; tokens exact)"
	case m.UnpricedRequests > 0:
		return fmt.Sprintf("≥$%.2f (%d of %d req unpriced)", m.ImputedUSD, m.UnpricedRequests, m.Requests)
	}
	return fmt.Sprintf("$%.2f", m.ImputedUSD)
}

// printFlagComparison renders one with/without comparison (headroom, code graph). It refuses
// to present a small sample as a conclusion: units differ in size, so the noise swamps the
// signal below ~3 runs per side.
func printFlagComparison(groups []store.MeasureGroup, name string) {
	if len(groups) == 0 {
		return
	}
	fmt.Printf("%s effect on THIS project's runs (imputed, not billed):\n", name)
	var with, without *store.MeasureGroup
	for i := range groups {
		g := groups[i]
		note := ""
		if g.Runs < 3 {
			note = "  (insufficient — need ≥3 runs to compare meaningfully)"
		}
		fmt.Printf("  %-20s %d run(s): %s tokens, $%.2f imputed (mean)%s\n",
			g.Label, g.Runs, render.Tokens(int64(g.MeanTokens)), g.MeanUSD, note)
		if g.Label == "with "+name {
			with = &groups[i]
		}
		if g.Label == "without "+name {
			without = &groups[i]
		}
	}
	if with != nil && without != nil && with.Runs >= 3 && without.Runs >= 3 && without.MeanTokens > 0 {
		delta := (1 - with.MeanTokens/without.MeanTokens) * 100
		fmt.Printf("\n  → with %s used %.1f%% %s tokens on average\n",
			name, abs(delta), map[bool]string{true: "fewer", false: "more"}[delta >= 0])
		fmt.Println("  (still noisy — runs are not identical work; treat as directional, not exact)")
	}
	fmt.Println()
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
	// A unit whose work lives on a branch in another tree is not integrated just because its run
	// is closed. Saying nothing here is how a verified unit ends up sitting on a branch nobody
	// merges — the run record says done and the main branch has none of it.
	if wt := worktreeOfRun(sp, run); wt != nil {
		fmt.Printf("its work is still on branch %s in %s.\n"+
			"  integrate it: batten worktree %s --merge   (from the tree you are merging into)\n",
			wt.Branch, wt.Path, unit)
	}
	if export.VaultPath(sp) != "" {
		_, _ = export.Run(sp, st, unit) // note now reflects the final state
	}
	return nil
}

// verdictTreeOf is the working tree a run's verdict is ABOUT.
//
// This is not always the tree you are standing in, and getting it wrong was caught by running
// the merge rather than by reading it. `batten check` fingerprints the tree it ran in; with a
// worktree per unit that is the UNIT's tree. Asking the gate from the main tree then compared the
// verdict's digest against a completely different checkout and reported a MOVED BASE — the run
// was in perfect shape and the gate said the history had moved under it.
//
// The rule the whole target digest rests on is that "verified" means verified about THIS. So the
// question has to be asked about the tree the verdict was made in, and a tree that no longer
// exists on disk falls back to here rather than answering out of a stale path.
func verdictTreeOf(sp *spec.Spec, run *store.Run) string {
	if run.Worktree == "" {
		return sp.Root
	}
	if fi, err := os.Stat(run.Worktree); err != nil || !fi.IsDir() {
		return sp.Root
	}
	return run.Worktree
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
	// When the gate declares checks, close must demand exactly what the commit gate demands.
	// They diverged, and `batten close --status ok` accepted an agent-asserted verdict that the
	// commit hook had denied moments earlier — a gate you can walk around is not a gate.
	if g, ok := sp.Gates[closing.Gate]; ok && len(g.Checks) > 0 {
		if reason := hooks.GateShortfallAt(st, verdictTreeOf(sp, run), run.RunID, closing.Gate, closing.RequiresVerdict); reason != "" {
			return fmt.Errorf("cannot close %s as ok: %s\n"+
				"Use --status failed to close a run that went wrong", run.UnitID, reason)
		}
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
	// ActiveRun, never EnsureRun: `batten check` verifies an open run. On a closed unit it
	// silently forked a second run — no anchor, no phase, exit 0 — and `batten show` then
	// displayed only that empty fork, hiding the closed run it grew out of (#12).
	run, err := st.ActiveRun(sp.Project, unit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no open run for %s — `batten check` verifies an open run, it does not "+
				"start one.\nReopen with: batten phase %s %s", unit, unit, sp.Phases[0].ID)
		}
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
		// Fingerprint WHAT the checks just passed against. Without it "batten-verified" survives
		// a formatter running a second later and starts describing a tree that no longer exists.
		TargetDigest: store.TargetDigest(sp.Root),
	}
	if err := st.SaveVerdict(v, gate.EvidenceRequired()); err != nil {
		return err
	}
	fmt.Printf("\n%s: %s (batten-verified). %s\n", unit, strings.ToUpper(result), why)
	if result != "ok" {
		fmt.Println("the commit gate will deny until this passes.")
		// An exit code is a contract with everything that is not a human reading a terminal.
		// Returning 0 on BLOCKED — the same code a full pass returns — means `batten check &&
		// ...` proceeds and `set -e` does not stop, so a CI job wires this up, watches it go
		// green, and gates nothing.
		return errSilent
	}
	return nil
}

// errSilent means "exit non-zero, the message is already printed". Returning a normal error
// here would print the failing check output a second time, wrapped.
var errSilent = errors.New("")

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
			return string(b), narrowExit(ee.ExitCode()), took
		}
		return err.Error(), 1, took
	}
	return string(b), 0, took
}

// narrowExit folds a Windows exit code back into its signed 32-bit meaning. A process that
// dies abnormally there reports a negative NTSTATUS, and the raw value renders as its
// unsigned wraparound: `npm test` with no package.json printed `exit 4294963238` instead of
// -4058 — and that garbage was PERSISTED into the verdict evidence, which `batten show`
// replays forever (#13).
func narrowExit(code int) int {
	return int(int32(code))
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
//
// Always ~/.batten, NEVER ${CLAUDE_PLUGIN_DATA}: the tap is toggled from the user's terminal
// (where that env var is unset) but written by hook processes (where it is set). If the path
// depended on the env, the two sides would disagree and the tap would silently capture nothing
// — which is exactly the bug this comment is the tombstone of.
func tapPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "hook-taps.jsonl"
	}
	return filepath.Join(home, ".batten", "hook-taps.jsonl")
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
	return mcp.Serve(sp, st, version)
}

func cmdTUI() error {
	// Bubble Tea with stdout on a pipe renders nothing and never exits: it emits 96 bytes of
	// terminal setup, then waits forever for key events that cannot arrive (#46). Refusing up
	// front turns a hang into a sentence.
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return errors.New("batten tui needs an interactive terminal — try `batten runs` or `batten show <unit>`")
	}
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
	added, fenced, err := st.RecordUsageFenced(rows)
	if err != nil {
		return err
	}
	r, _ := st.Run(run.RunID)
	if measured, err := st.HasUsage(run.RunID); err == nil && !measured {
		fmt.Printf("%s: +0 requests — usage remains NOT MEASURED for this run\n", unit)
	} else {
		fmt.Printf("%s: +%d requests, %s tokens total, $%.2f imputed\n",
			unit, added, render.Tokens(r.TokensSpent), r.ImputedUSD)
	}

	// The fence is right — a run must not inherit the whole session's history — but it used
	// to be silent, and the default flow opens the run AFTER the work happened. So the usual
	// outcome was "+0 requests, 0 tokens total", which reads as "this run was free" when what
	// actually happened is that every row was parsed, priced, and then discarded.
	if fenced.Requests > 0 {
		fmt.Printf("  %d request(s) in this transcript predate the run and were NOT counted "+
			"(%s tokens, $%.2f).\n", fenced.Requests, render.Tokens(fenced.Tokens), fenced.ImputedUSD)
		fmt.Printf("  They belong to the session, not to %s, which opened at %s.\n",
			unit, time.Unix(r.StartedAt, 0).Format("15:04:05"))
		if added == 0 {
			fmt.Printf("  Nothing was counted for this run. Its budget is UNMEASURED, not zero — " +
				"open the run before the work if you want it priced.\n")
		}
	}
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
		// "0 tokens, $0.00" for a run nobody measured is the fabricated number this tool
		// exists to refuse. A run with no usage row has not spent nothing — it is unmeasured,
		// and those need opposite responses from the reader.
		if measured, err := st.HasUsage(r.RunID); err == nil && !measured {
			fmt.Printf("%s  usage NOT MEASURED (not zero — nothing has been ingested for this run)\n",
				r.UnitID)
		} else {
			fmt.Printf("%s  %s tokens, %s\n",
				r.UnitID, render.Tokens(r.TokensSpent), render.ImputedLong(r.ImputedUSD, r.UnpricedTokens, r.TokensSpent))
		}

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
				// and the user deserves to know which — and why, since the causes have
				// different fixes and only one of them is "install the statusline".
				fmt.Printf("  %s %-12s NOT MEASURABLE — %s\n", mark, c.Kind, c.Reason)
			case c.Kind == "tokens":
				fmt.Printf("  %s %-12s %s / %s  %s\n", mark, c.Kind,
					render.Tokens(int64(c.Spent)), render.Tokens(int64(c.Cap)), bar(c.Spent/c.Cap))
			case c.Kind == "imputed_usd":
				fmt.Printf("  %s %-12s %s / $%.2f  %s\n", mark, c.Kind,
					render.ImputedShort(c.Spent, c.UnpricedTokens, c.TotalTokens), c.Cap, bar(c.Spent/c.Cap))
				if c.UnpricedTokens > 0 && c.UnpricedTokens < c.TotalTokens {
					fmt.Printf("      a floor, not a total: %d%% of the tokens have no published rate\n",
						render.UnpricedShare(c.UnpricedTokens, c.TotalTokens))
				}
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
	// Rule 3 of the unsupervised run. `--reason` exists because an override has to cost a
	// sentence a human wrote; at 3am there is no human, and a sentence the LOOP wrote satisfies
	// the letter of the rule and none of its purpose. A blocked verdict at the end of an
	// unattended run is a successful outcome, so this refusal is not a problem to route around.
	if run.Unattended() {
		return errors.New(hooks.Refusal(hooks.CodeUnattendedOverride, fmt.Sprintf(
			"refusing to override %s: it is running unattended, and an override needs a human "+
				"reason from a human.\n"+
				"A blocked verdict at the end of an unsupervised run is a SUCCESSFUL outcome — "+
				"report it and stop.\n"+
				"If you are that human and you are awake: batten unattended %s --off", unit, unit)))
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

// dx accumulates a diagnosis so doctor can emit EVERYTHING it knows in one pass.
//
// It used to `return errSilent` at the first fatal, so a repo with a broken spec AND an
// unopenable store reported one of them, you fixed it, reran, and met the next — one at a time.
// People give up on the third round trip. Nothing here returns early any more: a fatal marks the
// exit code and the run continues through every check that does not depend on what just failed.
type dx struct{ fatal bool }

func (d *dx) fail(format string, a ...any) {
	d.fatal = true
	fmt.Printf("✗ "+format+"\n", a...)
}
func (d *dx) warn(format string, a ...any) { fmt.Printf("⚠ "+format+"\n", a...) }
func (d *dx) ok(format string, a ...any)   { fmt.Printf("✓ "+format+"\n", a...) }

// err returns what cmdDoctor should hand back: a non-zero exit if anything fatal was found.
func (d *dx) err() error {
	if d.fatal {
		return errSilent
	}
	return nil
}

func cmdDoctor() error {
	d := &dx{}
	cwd, _ := os.Getwd()

	// The installed copy and the database are checked whatever happens to the spec: they are
	// how batten runs at all, and finding out about them only after fixing batten.yaml is the
	// one-at-a-time diagnosis this rewrite exists to end.
	defer func() {
		checkInstall(d, cwd)
	}()

	path, err := spec.Find(cwd)
	if err != nil {
		d.fail("no %s found. Run: batten init", spec.Filename)
		checkStore(d, nil)
		return d.err()
	}
	sp, err := spec.Load(path)
	if err != nil {
		// doctor's whole job is to say whether this repo is correctly governed. Printing
		// "✗ invalid spec" and then exiting 0 tells a script the opposite of what it just told
		// the human, so a CI step running `batten doctor` goes green on a broken spec.
		d.fail("%v", err)
		checkStore(d, nil) // spec-independent, and just as likely to be the reason nothing works
		return d.err()
	}
	d.ok("%s — project %q, unit %q, %d phases, %d domains",
		path, sp.Project, sp.Unit.Name, len(sp.Phases), len(sp.Domains))

	// Keys this batten does not read. Loading ignores them so a spec written for a newer batten
	// still works, but silence is the wrong answer: batten.schema.json REJECTS them, so an editor
	// and this command disagree about whether the file is valid — and the author, reading only the
	// green tick, ships a repo whose CI is red on its own spec. That is exactly what happened.
	if unknown := spec.UnknownKeys(path); len(unknown) > 0 {
		d.warn("keys batten does not read: %s\n"+
			"    They load without error, and they do nothing. batten.schema.json rejects them, so "+
			"your editor calls this file invalid while the line above calls it fine.\n"+
			"    Delete them, or check the CHANGELOG — some were removed on purpose.",
			strings.Join(unknown, ", "))
	}

	if c, ok := sp.ClosingPhase(); ok {
		// An empty Gate is not a nameless gate — the store reads it as "any gate"
		// (`?='' OR gate=?`). Printing `on gate ""` made a real, working config look like a
		// misconfiguration.
		gate := "any gate"
		if c.Gate != "" {
			gate = fmt.Sprintf("gate %q", c.Gate)
		}
		fmt.Printf("✓ close gate: phase %q requires verdict %q on %s\n",
			c.ID, c.RequiresVerdict, gate)
	} else {
		fmt.Println("⚠ no phase sets requires_verdict — nothing gates a commit")
	}

	// A gate with no checks cannot verify anything: batten has nothing to run, so the commit
	// rides on the agent's own claim. That is legitimate in a repo that has not written its
	// checks yet, but it must be visible HERE — the alternative is finding out at the commit,
	// having believed all along that the gate meant something.
	var unchecked []string
	for name, g := range sp.Gates {
		if len(g.Checks) == 0 {
			unchecked = append(unchecked, name)
		}
	}
	sort.Strings(unchecked)
	for _, name := range unchecked {
		d.warn("gate %q declares no checks — it verifies NOTHING and approves on the "+
			"agent's word. Add gates.%s.checks (take them verbatim from your build files).", name, name)
	}

	checkRunnable(d, sp)
	checkAnchors(d, sp)

	if sp.ReportOnly() {
		fmt.Println("● enforcement: REPORT — gates WARN, they do not block yet. " +
			"Set enforcement: enforce (or remove it) when the team trusts the gates.")
	} else {
		d.ok("enforcement: enforce — gates block")
	}

	st, err := store.Open(dbPath())
	if err != nil {
		checkStore(d, nil)
		// The store is how batten records anything, but the rest of this report — the vault, the
		// domain rules, the skills the spec names — is spec-level and still worth having in the
		// same pass. Only the checks that need st are skipped.
		checkSpecOnly(d, sp)
		return d.err()
	}
	defer st.Close()
	checkStore(d, st)

	checkSpecOnly(d, sp)

	// A run nobody closed keeps its write-set claims alive and muddies session attribution.
	// Surface the stale ones so they don't rot: 48h with no event means abandoned or forgotten.
	if stale, err := st.StaleRuns(sp.Project, 48*time.Hour); err == nil && len(stale) > 0 {
		d.warn("%d run(s) open >48h with no activity — close or resume them:", len(stale))
		for _, r := range stale {
			fmt.Printf("    %s (phase %s): batten close %s [--status ok|failed]\n", r.UnitID, r.Phase, r.UnitID)
		}
	}
	return d.err()
}

// checkSpecOnly is everything doctor can say from the spec alone. It is split out so a repo
// whose store will not open still gets its vault, domain and capability report in the same pass
// instead of one finding per run.
func checkSpecOnly(d *dx, sp *spec.Spec) {
	// Optional capabilities degrade; report what is actually live rather than what is declared.
	if sp.Capabilities.GraphEnabled() {
		report("graph", sp.Capabilities.Graph.Provider, have(sp.Capabilities.Graph.Provider))
		if sp.Capabilities.Graph.Lessons {
			fmt.Println("  ⚠ graph.lessons is on; it overlaps engram's job. Prefer lessons: false")
		}
		// Declaration crossed with reality. `query_before_read: true` is an
		// instruction batten now injects into the agent's orientation — and an instruction to
		// consult a tool that is not installed is worse than no instruction: the agent either
		// wastes a turn discovering it, or says it consulted something it could not.
		if sp.Capabilities.Graph.QueryBeforeRead {
			if have(sp.Capabilities.Graph.Provider) {
				fmt.Println("  ✓ query_before_read: agents are told to ask the graph before reading files")
			} else {
				d.warn("query_before_read is true and %s is NOT on PATH. Every fanned-out agent will "+
					"be told to consult a graph it cannot reach.\n"+
					"    Either install it, or set query_before_read: false so the instruction stops "+
					"promising something that is not there.", sp.Capabilities.Graph.Provider)
			}
		}
		graphStaleness(sp) // a stale code graph gives wrong answers silently; warn loudly
		// (graphify dropped its --obsidian export in 0.9.x; the visual graph is now
		// graphify-out/graph.html. Do not suggest flags that no longer exist.)
		fmt.Println("  → visual graph: open graphify-out/graph.html in a browser")
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
	if p := export.VaultPath(sp); p != "" {
		if _, err := os.Stat(p); err == nil {
			d.ok("obsidian vault: %s", p)
		} else {
			d.warn("obsidian vault not found: %s (canvas falls back to .batten/)", p)
		}
	}
	// unit.plan crossed with reality, same spirit as query_before_read: a declared backlog
	// that the locator cannot find a single unit in is a spec pointing at nothing, and every
	// surface built on it (criteria, `batten status`) would quietly show an empty world.
	if sp.Unit.Plan != "" {
		switch units, err := plan.Load(sp); {
		case err != nil:
			d.warn("unit.plan: %v", err)
		case len(units) == 0:
			d.warn("unit.plan %s has NO unit matching locator %q + pattern %q — the backlog "+
				"is declared and unreadable, which is worse than undeclared",
				sp.Unit.Plan, sp.Unit.Locator, sp.Unit.Pattern)
		default:
			d.ok("unit.plan: %d unit(s) in %s", len(units), sp.Unit.Plan)
		}
	}
	for name, dom := range sp.Domains {
		if dom.Rules != "" {
			if _, err := os.Stat(filepath.Join(sp.Root, dom.Rules)); err != nil {
				d.warn("domain %q: rules file missing: %s", name, dom.Rules)
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
			d.warn("%s references %q, which is not installed%s", p.Where, p.Ref, hint)
		}
	}

	// The quota ceiling is unenforceable without the statusline, so say plainly whether it is live.
	if sp.Budget.QuotaPctPerRun > 0 {
		if present, existing, _ := statusline.Installed(sp.Root); present && statusline.IsBatten(existing) {
			d.ok("statusline installed — the quota ceiling is enforced")
		} else {
			d.warn("budget.quota_pct_per_run is set but the statusline is not installed — that " +
				"ceiling is NOT enforced. Run: batten statusline --install")
		}
	}
}

// checkRunnable answers the question a gate's `checks:` list quietly assumes: can these commands
// actually be run on this machine?
//
// A check that cannot start does not fail loudly at `batten check` time in any useful way — it
// fails as "exit 1, command not found" buried in the evidence of a BLOCKED verdict, and the
// reader concludes the code is broken rather than the toolchain. Worse, `checks:` is copied
// verbatim from the build files of whoever ran `batten init`, so `make test` in a repo without
// make is the normal way this goes wrong, on a teammate's machine and not the author's.
//
// The resolution mirrors runCheck exactly — same shell, same lookup — because a doctor that
// resolves commands differently from the runner is answering a different question.
func checkRunnable(d *dx, sp *spec.Spec) {
	seen := map[string]bool{}
	probe := func(where, command string) {
		for _, exe := range commandHeads(command) {
			if exe == "" || seen[exe] {
				continue
			}
			seen[exe] = true
			if resolvable(sp.Root, exe) {
				continue
			}
			d.warn("%s: %q cannot be run here — %q is not on PATH and is not a file in this repo.\n"+
				"    The check will not fail your code, it will fail to start, and its output "+
				"lands in the evidence of a BLOCKED verdict as if the code were at fault.\n"+
				"    Install it, or replace the command with one this machine has.", where, command, exe)
		}
	}
	for _, name := range sortedGateNames(sp) {
		for _, c := range sp.Gates[name].Checks {
			probe(fmt.Sprintf("gate %q", name), c)
		}
	}
	for _, name := range sortedDomainNames(sp) {
		for _, c := range sp.Domains[name].Check {
			probe(fmt.Sprintf("domain %q", name), c)
		}
	}
}

func sortedGateNames(sp *spec.Spec) []string {
	out := make([]string, 0, len(sp.Gates))
	for n := range sp.Gates {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedDomainNames(sp *spec.Spec) []string {
	out := make([]string, 0, len(sp.Domains))
	for n := range sp.Domains {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// commandHeads pulls the executable out of each segment of a (possibly compound) shell command,
// so `go build ./... && make test` is reported as two tools and not one unrecognisable string.
// Leading VAR=value assignments are skipped: they are environment, not the program.
func commandHeads(command string) []string {
	var out []string
	for _, seg := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '&' || r == '|' || r == ';'
	}) {
		fields := strings.Fields(seg)
		for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.ContainsAny(fields[0], `/\`) {
			fields = fields[1:]
		}
		if len(fields) > 0 {
			out = append(out, strings.Trim(fields[0], `"'`))
		}
	}
	return out
}

// resolvable reports whether the shell that runCheck uses could find exe. A path-ish token is
// resolved against the repo root, because that is the directory runCheck runs in.
func resolvable(root, exe string) bool {
	if strings.ContainsAny(exe, `/\`) {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(exe)))
		return err == nil
	}
	if _, err := exec.LookPath(exe); err == nil {
		return true
	}
	// Fall back to the shell's own opinion, which knows about builtins and (on Windows) about
	// things LookPath's PATHEXT handling misses. Being generous here is deliberate: a doctor
	// that cries wolf about a working check is worse than one that misses a broken one.
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", "where "+exe).Run() == nil
	}
	return exec.Command("sh", "-c", "command -v "+exe).Run() == nil
}

// checkAnchors catches a spec that declares an anchor the repo cannot produce.
//
// Found by rebuilding the replica-ui fixture: `batten init` writes `anchor: git_sha` into the
// spec of a directory that is not a git repo at all, `batten phase` then records an empty
// base_sha, and nothing anywhere says so. `diff_from: anchor` is meant to read that anchor, so
// the whole "verify only what this unit changed" story rests on a value that was never written.
func checkAnchors(d *dx, sp *spec.Spec) {
	var anchored []string
	for _, p := range sp.Phases {
		if p.Anchor != "" {
			anchored = append(anchored, p.ID)
		}
	}
	if len(anchored) == 0 {
		return
	}
	if isGitRepo(sp.Root) {
		d.ok("anchor: %s can record a base SHA (git repo)", strings.Join(anchored, ", "))
		return
	}
	d.warn("phase(s) %s declare an anchor, but %s is not a git repository — the anchor is "+
		"recorded EMPTY and nothing that reads it (diff_from: anchor) has a base to diff from.\n"+
		"    Either `git init` here, or drop `anchor:` from those phases so the spec stops "+
		"promising something it cannot deliver.", strings.Join(anchored, ", "), sp.Root)
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	return cmd.Run() == nil
}

// checkStore reports the database, and then the thing that opening it does NOT prove: that
// batten can take the write lock.
//
// This is the diagnosis behind the fail-open-but-loudly rule. When another process holds the write
// lock past busy_timeout, the database still OPENS — so "✓ store" was printed while every hook
// that needed to record something was quietly failing. The user sees a healthy doctor and a gate
// with no opinions.
func checkStore(d *dx, st *store.Store) {
	if st == nil {
		st2, err := store.Open(dbPath())
		if err != nil {
			d.fail("store: %s cannot be opened — %v\n"+
				"    Every hook that records anything is failing right now. Check the path is "+
				"writable, or point BATTEN_DB somewhere that is.", dbPath(), err)
			return
		}
		defer st2.Close()
		st = st2
	}
	d.ok("store: %s", dbPath())
	if err := st.ProbeWriteLock(); err != nil {
		d.fail("store: the write lock is held by something else — %v\n"+
			"    Opening the database succeeded, which is why this looked healthy. Writing to it "+
			"does not, so hooks are degrading right now.\n"+
			"    Close other batten processes (a stuck `batten tui`, an orphaned hook), then rerun.", err)
		return
	}
	fmt.Println("  ✓ write lock available — hooks can record")
}

// checkInstall inspects the copy of batten that the HOOKS run, which is not the one you just
// typed. The installed tree can be stale or mangled while the source tree is perfect.
//
// The line-ending check is three lines and closes the only route by which a fixed bug returns.
// .gitattributes settles CRLF inside this repo and CI verifies it, but an install can travel by
// a path that mangles it, and the failure mode is total and silent: `#!/usr/bin/env bash\r` is a
// bad interpreter, bootstrap never downloads the binary, and every hook no-ops without a word.
func checkInstall(d *dx, projectDir string) {
	dir, ok := discovery.PluginDir(projectDir, "batten")
	if !ok {
		fmt.Println("· installed plugin: not found (running from a source checkout, or installed " +
			"under a different name) — the checks below are about the copy the hooks invoke")
		return
	}

	bin := install.BinPath(dir)
	switch out, err := exec.Command(bin, "version").Output(); {
	case err != nil:
		d.fail("installed binary %s does not run — %v\n"+
			"    Every hook invokes THIS file, not the batten on your PATH, so the gate is not "+
			"running at all.\n"+
			"    Start a new session (SessionStart runs the bootstrap, which installs it), or run "+
			"%s/scripts/bootstrap.sh by hand.", bin, err, dir)
	default:
		got := strings.TrimSpace(string(out))
		if got != "batten "+version {
			d.warn("installed binary is %q but you are running %q — the hooks and your terminal "+
				"disagree about what batten is.\n    Reinstall the plugin to match.", got, "batten "+version)
		} else {
			d.ok("installed binary: %s (%s)", bin, got)
		}
	}

	boot := filepath.Join(dir, "scripts", "bootstrap.sh")
	b, err := os.ReadFile(boot)
	switch {
	case err != nil:
		d.warn("installed bootstrap missing: %s — the binary will not self-download if it goes "+
			"missing. Reinstall the plugin.", boot)
	case bytes.Contains(b, []byte("\r\n")):
		d.fail("installed bootstrap %s has CRLF line endings.\n"+
			"    `#!/usr/bin/env bash\\r` is a bad interpreter: the script never runs, the binary "+
			"is never downloaded, and every hook no-ops IN SILENCE.\n"+
			"    Fix: dos2unix the file, or reinstall through a path that preserves LF.", boot)
	default:
		fmt.Println("  ✓ installed bootstrap has LF line endings")
	}
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
// codeGraphFresh reports whether graphify-out/graph.json exists and is no more than an hour
// behind HEAD. Used by doctor (to warn) and by run-open tagging (to measure the graph's
// effect on run cost, headroom-style).
func codeGraphFresh(sp *spec.Spec) (exists, fresh bool) {
	gj := filepath.Join(sp.Root, "graphify-out", "graph.json")
	fi, err := os.Stat(gj)
	if err != nil {
		return false, false
	}
	out, err := exec.Command("git", "-C", sp.Root, "log", "-1", "--format=%ct").Output()
	if err != nil {
		return true, true // no git history to compare against; existing graph counts as fresh
	}
	var headUnix int64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &headUnix)
	return true, headUnix <= fi.ModTime().Unix()+3600
}

func graphStaleness(sp *spec.Spec) {
	exists, fresh := codeGraphFresh(sp)
	switch {
	case !exists:
		fmt.Println("  ⚠ no graphify-out/graph.json yet — run: graphify . --code-only")
	case !fresh:
		fmt.Println("  ⚠ the code graph is older than HEAD — it may answer with stale code.")
		// `graphify update .`, NOT `graphify . --update`: the latter is not a flag, so graphify
		// silently ignores it and runs a FULL extraction, which then fails on a missing LLM API
		// key. Suggesting a command that walks the user into an error is worse than suggesting
		// nothing. (This is the second time a hint here outlived graphify's CLI; the first was
		// --obsidian, dropped in 0.9.x.)
		fmt.Println("    refresh: graphify update .   (re-extracts code only, no API key needed)")
	default:
		fmt.Println("  ✓ code graph is current")
	}
	graphHooks(sp)
}

// graphHooks reports graphify's git integration, which matters more here than it looks.
//
// graph.json is committed on purpose — it is a shared artifact, the whole point of a team graph.
// It is also a megabyte of generated JSON, so two branches that both touch code produce a
// guaranteed, unmergeable conflict. `graphify hook install` registers a union merge driver for
// exactly that, and adds post-commit/post-checkout hooks so the graph does not silently rot
// between rebuilds. A repo that commits the graph without the merge driver has a conflict
// waiting for its second contributor.
func graphHooks(sp *spec.Spec) {
	// -C the project root, not the process cwd: doctor answers about THIS repo, and running it
	// from a subdirectory (or anywhere else) used to query whatever repo the shell happened to
	// be in.
	out, err := exec.Command("graphify", "-C", sp.Root, "hook", "status").Output()
	if err != nil {
		// Older graphify has no -C. Fall back rather than lose the check entirely.
		out, err = exec.Command("graphify", "hook", "status").Output()
		if err != nil {
			return // graphify absent or too old for `hook status`: say nothing rather than guess
		}
	}
	s := string(out)

	// Assert the positive. This concluded "installed" from the ABSENCE of two known failure
	// strings, so any output it did not recognise read as success — and outside a git repo
	// graphify prints "Not in a git repository." and exits 0, which produced a green tick for
	// hooks that cannot exist. It is the same mistake as reading silence from `batten hook` as
	// an allow, made by the tool whose job is to catch that mistake.
	if !isGitRepo(sp.Root) {
		fmt.Println("  · graphify git hooks: not applicable — this is not a git repository")
		return
	}
	installedDriver := strings.Contains(s, "merge driver: registered")
	installedHooks := strings.Contains(s, "post-commit: installed")
	missingDriver := strings.Contains(s, "merge driver: not registered")
	missingHooks := strings.Contains(s, "post-commit: not installed")
	if !installedDriver && !installedHooks && !missingDriver && !missingHooks {
		fmt.Println("  · graphify git hooks: could not tell — `graphify hook status` printed " +
			"something this version does not recognise")
		return
	}
	if !missingDriver && !missingHooks {
		fmt.Println("  ✓ graphify git hooks installed (auto-rebuild + graph.json merge driver)")
		return
	}
	if missingDriver && trackedInGit(sp.Root, filepath.Join("graphify-out", "graph.json")) {
		fmt.Println("  ⚠ graph.json is committed but graphify's merge driver is NOT registered —")
		fmt.Println("    two branches touching code will conflict on it. Fix once: graphify hook install")
		// With worktrees in play this stops being a courtesy. It has to be written down before
		// starting rather than after somebody hits it: "two branches touching code" is the RARE
		// case in a single tree and the NORMAL case once every unit has its own, so the merge
		// driver goes from nice-to-have to a requirement of the installation.
		if trees, err := gitx.Worktrees(sp.Root); err == nil && len(trees) > 1 {
			fmt.Printf("    %d working trees are already open here, so that conflict is not a "+
				"maybe — it is the next merge.\n", len(trees))
		}
	} else if missingHooks {
		fmt.Println("  → auto-rebuild the graph on commit: graphify hook install")
	}
}

// trackedInGit reports whether git has rel under version control in root.
func trackedInGit(root, rel string) bool {
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", filepath.ToSlash(rel))
	return cmd.Run() == nil
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
			if i+1 >= len(args) {
				return errors.New("init: --from needs a path")
			}
			from, i = args[i+1], i+1
		case "--help", "-h":
			// This is the only command that both writes a file and had no default arm:
			// `batten init --help` printed no usage, WROTE batten.yaml and exited 0 (#54).
			fmt.Println("init: batten init [--from <doc>] [--scan-json]")
			fmt.Println("  scans the repo and writes batten.yaml in report mode; refuses if one exists")
			fmt.Println("  --from <doc>   record the prose workflow document as unit.plan")
			fmt.Println("  --scan-json    print the scanned facts as JSON (what /batten-init consumes)")
			return nil
		default:
			return fmt.Errorf("init: unknown flag %q", args[i])
		}
	}
	cwd, _ := os.Getwd()

	facts, err := scan.Scan(cwd)
	if err != nil {
		return err
	}
	// --from names the prose workflow document, so it must exist and it must be RECORDED.
	// It used to be a pure stdout echo: the generated yaml was byte-identical with or
	// without it, and a nonexistent path was accepted with exit 0 (#0). Setting UnitPlan
	// both emits `unit.plan` in the yaml and rides along in --scan-json to /batten-init.
	if from != "" {
		if _, err := os.Stat(from); err != nil {
			return fmt.Errorf("init: --from %s: %w", from, err)
		}
		facts.UnitPlan = filepath.ToSlash(from)
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
	if line, err := ensureGitignored(cwd); err != nil {
		// Never fatal. `init` succeeded; a .gitignore batten could not write is a nuisance, and
		// failing the on-ramp over it would be worse than the problem.
		fmt.Printf("could not update .gitignore (%v) — add %q by hand before your first `git add -A`.\n",
			err, gitignoreEntry)
	} else if line != "" {
		fmt.Printf("added %q to .gitignore — %s\n", line,
			"without it the first `git add -A` commits batten's database")
	}
	fmt.Println("Next:")
	fmt.Println("  1. fill the invariants (the TODOs) — the highest-value part of the file")
	if from != "" {
		fmt.Printf("     (%s is recorded as unit.plan; the binary does not mine prose — /batten-init does)\n", from)
	}
	fmt.Println("  2. run: batten doctor")
	fmt.Println("  3. flip enforcement: enforce when you trust the gates")
	fmt.Println("For a richer draft (invariants mined from AGENTS.md, migration from a prose doc),")
	fmt.Println("run /batten-init inside Claude Code instead — it uses `batten init --scan-json`.")
	return nil
}

// gitignoreEntry is what `.batten/` needs to be, and the leading slash is the point: it anchors
// the rule at the repo root, so a source directory somebody legitimately names `.batten` deeper in
// the tree is not silently ignored too. This repo's own .gitignore has carried this exact line
// since before init could write it.
const gitignoreEntry = "/.batten/"

// ensureGitignored is field-test finding #59: `batten init` wrote batten.yaml and said nothing
// about .gitignore, so the adopter's very next `git add -A` committed batten's database —
// a binary SQLite file that grows with every run, conflicts on every merge, and carries the
// project's whole decision history into the repository. The first thing batten asks anyone to do
// created the mess.
//
// It returns the line it added, or "" when the file already covered the directory. Additive only:
// appending to a .gitignore is safe, rewriting somebody's is not, and `init` is a first-contact
// command that has to leave everything it did not come for exactly as it found it.
func ensureGitignored(root string) (string, error) {
	path := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	existing := string(b)
	// Any spelling that already covers the directory counts. Re-adding a rule the user wrote
	// differently would be batten arguing with a file it does not own.
	for _, line := range strings.Split(existing, "\n") {
		switch strings.TrimSpace(line) {
		case gitignoreEntry, ".batten/", ".batten", "/.batten":
			return "", nil
		}
	}

	var sb strings.Builder
	sb.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		sb.WriteString("\n")
	}
	if existing != "" {
		sb.WriteString("\n")
	}
	sb.WriteString("# batten's local state: a SQLite database that grows with every run.\n")
	sb.WriteString("# It is per-machine and per-checkout; committing it conflicts on every merge.\n")
	sb.WriteString(gitignoreEntry + "\n")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return gitignoreEntry, nil
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
