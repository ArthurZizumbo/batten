package main

// `batten demo` — the full flow, on a repo batten builds and throws away.
//
// Even in report mode, §6.2's problem remains: you have to install and configure something
// before you see anything. This is the "try it before configuring it" that did not exist.
//
// It builds a small repo in a temp directory, runs the real engine over it — the real scanner,
// the real spec loader, the real PreToolUse hook, the real check runner — and narrates what
// happens. Then it deletes the whole thing.
//
// THREE THINGS IT MUST NEVER DO, and the reason each is enforced rather than intended:
//
//   - Touch your repository. Everything happens under a temp directory it created.
//   - Touch your database. The store path is passed explicitly, never dbPath(), so BATTEN_DB
//     and ~/.batten are both out of reach even by accident.
//   - Leave anything behind. The cleanup is deferred before the first file is written.
//
// It doubles as the end-to-end integration test the project did not have: every step below is
// the production path, not a re-implementation of it. A demo that faked its own output would be
// the exact failure batten exists to eliminate — a claim with nothing behind it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArthurZizumbo/batten/internal/hooks"
	"github.com/ArthurZizumbo/batten/internal/scan"
	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

func cmdDemo(args []string) error {
	keep := false
	for _, a := range args {
		switch a {
		case "--keep":
			// For someone who wants to poke at the sandbox afterwards. It prints the path.
			keep = true
		default:
			return fmt.Errorf("demo: unknown flag %q", a)
		}
	}

	dir, err := os.MkdirTemp("", "batten-demo-")
	if err != nil {
		return err
	}

	d := &demo{dir: dir, out: os.Stdout}
	runErr := d.run()

	// The store must be CLOSED before the directory can go. Windows refuses to delete an open
	// file, so a deferred RemoveAll over a live SQLite handle failed silently and the demo
	// announced "the sandbox is gone" over a sandbox that was still there. Deleting is the
	// promise this command makes; it does not get to be best-effort and quiet.
	if d.st != nil {
		_ = d.st.Close()
	}
	if runErr != nil {
		return runErr
	}

	if keep {
		fmt.Printf("\nsandbox kept at %s — delete it when you are done.\n", dir)
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Printf("\nCould not delete the sandbox at %s: %v\n"+
			"Nothing of yours was touched, but that directory is still there — remove it.\n", dir, err)
		return nil
	}
	fmt.Printf("\nThe sandbox is gone. Your repo and your database were never touched.\n" +
		"To govern a real repo: batten init\n")
	return nil
}

type demo struct {
	dir  string
	out  *os.File
	sp   *spec.Spec
	st   *store.Store
	run_ *store.Run
}

func (d *demo) say(format string, a ...any) { fmt.Fprintf(d.out, format+"\n", a...) }

func (d *demo) step(n int, title string) {
	d.say("\n\033[1m%d. %s\033[0m", n, title)
}

func (d *demo) run() error {
	d.say("batten demo — a whole workflow on a repo that does not exist yet.")
	d.say("Nothing below touches your repository or your database.\n")
	d.say("sandbox: %s", d.dir)

	for _, f := range []func() error{
		d.buildRepo,
		d.stepInit,
		d.stepUngovernedCommit,
		d.stepOpenRun,
		d.stepDeniedNoVerdict,
		d.stepReviewerAlone,
		d.stepWriteSetGuard,
		d.stepCheckFails,
		d.stepFixAndPass,
		d.stepCommitLands,
		d.stepReport,
	} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

// ---------- the repo ----------

// buildRepo writes a small project with the shape batten was designed for: two domains, each
// with its own rules file, a backlog the unit pattern can be derived from, and a check that
// actually fails before it passes.
func (d *demo) buildRepo() error {
	write := func(rel, content string) error {
		p := filepath.Join(d.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(content), 0o644)
	}

	files := map[string]string{
		"api/AGENTS.md":   "# api\n\n## Invariants\n- Every handler maps a domain error, never a raw 500.\n",
		"store/AGENTS.md": "# store\n\n## Invariants\n- ErrNotFound maps to 404, never to 500.\n",
		// The bug the demo fixes, in a file a check can read. This is the whole reason the
		// check can fail for a real reason and then pass for a real reason.
		"store/notfound.go": "package store\n\n// BUG: a missing task answers 500.\nconst NotFoundStatus = 500\n",
		"api/handler.go":    "package api\n\nfunc Handle() int { return 200 }\n",
		"docs/backlog.md": "# Backlog\n\n" +
			"### US-001 — list tasks\n\n### US-002 — create a task\n\n" +
			"### US-003 — a missing task must answer 404\n\n" +
			"**Acceptance**\n- GET /tasks/{unknown} returns 404, not 500\n\n" +
			"### US-004 — delete a task\n\n### US-005 — pagination\n",
	}
	for rel, content := range files {
		if err := write(rel, content); err != nil {
			return err
		}
	}

	// The check lives in a SCRIPT, in both dialects, rather than as a shell one-liner in the
	// spec. The first attempt picked the dialect from runtime.GOOS and broke immediately: on
	// Windows-with-git, `make` runs its recipes through `sh`, so cmd syntax arrived at a POSIX
	// shell — `store\notfound.go` lost its backslash and `exit /b 1` is not a number. The shell
	// that runs a check is decided by whoever runs it, not by the operating system.
	if err := write("check.sh",
		"#!/bin/sh\n"+
			"if grep -q 'NotFoundStatus = 404' store/notfound.go; then\n"+
			"  echo 'PASS: a missing task maps to 404'\n"+
			"else\n"+
			"  echo 'FAIL: store/notfound.go maps a missing task to 500, not 404'\n"+
			"  exit 1\n"+
			"fi\n"); err != nil {
		return err
	}
	if err := write("check.cmd",
		"@echo off\r\n"+
			"findstr /C:\"NotFoundStatus = 404\" store\\notfound.go >nul\r\n"+
			"if errorlevel 1 (\r\n"+
			"  echo FAIL: store/notfound.go maps a missing task to 500, not 404\r\n"+
			"  exit /b 1\r\n"+
			")\r\n"+
			"echo PASS: a missing task maps to 404\r\n"); err != nil {
		return err
	}

	// A Makefile only when make AND sh are both really here, because that is the only case in
	// which the scanner lifting `make test` verbatim actually runs. Claiming the check came
	// from a build file that cannot execute would be the demo lying about its own subject.
	if d.useMake() {
		return write("Makefile", "test:\n\t"+d.checkCommand()+"\n")
	}
	return nil
}

func (d *demo) useMake() bool {
	return onPath("make") && onPath("sh")
}

// checkCommand runs the script through whichever shell this machine actually has.
//
// The Windows form is `.\check.cmd`, spelled out: cmd.exe does not reliably search the current
// directory for a bare name (NoDefaultCurrentDirectoryInExePath is commonly set), so `check.cmd`
// came back as "not recognized as an internal or external command" while the file sat right
// there. It is also the form `batten doctor` can verify — a token with a separator is resolved
// against the repo root, a bare one against PATH — so the demo's own check is one the doctor
// would pass.
func (d *demo) checkCommand() string {
	if onPath("sh") {
		return "sh check.sh"
	}
	return `.\check.cmd`
}

func onPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// ---------- the steps ----------

func (d *demo) stepInit() error {
	d.step(1, "batten init — read the repo, propose a spec")

	facts, err := scan.Scan(d.dir)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(d.dir, spec.Filename), []byte(facts.ToYAML()), 0o644); err != nil {
		return err
	}
	// The demo flips to enforce so the denials below are real denials. init would leave it on
	// report, which is the right default for a real adoption and the wrong one for showing
	// what a gate does.
	if err := d.rewriteSpec(); err != nil {
		return err
	}
	if d.sp, err = spec.LoadFrom(d.dir); err != nil {
		return err
	}
	if d.st, err = store.Open(filepath.Join(d.dir, ".batten", "demo.db")); err != nil {
		return err
	}

	d.say("   unit %q, pattern %s — derived from the backlog's headings, not from a branch name",
		d.sp.Unit.Name, d.sp.Unit.Pattern)
	d.say("   %d domains detected from their AGENTS.md: %s", len(d.sp.Domains),
		strings.Join(sortedDomainNames(d.sp), ", "))
	for name, g := range d.sp.Gates {
		if d.useMake() {
			d.say("   gate %q runs %d check(s), lifted verbatim from the Makefile", name, len(g.Checks))
		} else {
			d.say("   gate %q runs %d check(s) — no build file on this machine that batten could "+
				"lift one from, so the demo declared it directly", name, len(g.Checks))
		}
	}
	d.say("   (enforcement flipped to `enforce` for this demo; init leaves it on `report`)")
	return nil
}

func (d *demo) stepUngovernedCommit() error {
	d.step(2, "Commit before opening a run — batten says it is not governing you")
	out := d.commit(`git commit -m "wip"`)
	d.showDecision(out)
	d.say("   Silence here would be indistinguishable from approval. So it does not stay silent.")
	return nil
}

func (d *demo) stepOpenRun() error {
	d.step(3, "batten phase — open the run")
	var err error
	if d.run_, err = d.st.EnsureRun(d.sp.Project, "US-003", "demo-session"); err != nil {
		return err
	}
	if err := d.st.SetPhase(d.run_.RunID, "build"); err != nil {
		return err
	}
	if err := d.st.AddNode(store.Node{
		NodeID: store.PhaseNodeID(d.run_.RunID, "build"), RunID: d.run_.RunID,
		Kind: "phase", Label: "build", Status: "running",
	}); err != nil {
		return err
	}
	d.say("   US-003 is open at phase `build`. From here the gate governs.")
	return nil
}

func (d *demo) stepDeniedNoVerdict() error {
	d.step(4, "Commit with no verdict — denied")
	d.showDecision(d.commit(`git commit -m "US-003: map 404"`))
	return nil
}

func (d *demo) stepReviewerAlone() error {
	d.step(5, "A reviewer approves — and it is STILL denied")
	if err := d.st.SaveVerdict(store.Verdict{
		RunID: d.run_.RunID, Gate: "qa", CheckID: "review", Result: "ok", Source: "agent",
		Evidence: []string{"read the diff; it addresses the acceptance criterion"},
		Why:      "the reviewer is satisfied",
	}, true); err != nil {
		return err
	}
	d.showDecision(d.commit(`git commit -m "US-003: map 404"`))
	d.say("   Two verdicts are required, from two different producers: somebody's judgement of")
	d.say("   the work, and batten's own proof that the declared checks were RUN.")
	return nil
}

// stepWriteSetGuard shows the OTHER wedge. The verdict gate is the one people quote; the
// write-set guard is the one that makes parallel fan-out safe, and a demo that only showed the
// gate would be describing half the product.
func (d *demo) stepWriteSetGuard() error {
	d.step(6, "Two agents fan out — and the second is stopped from writing the first's file")

	for _, a := range []struct{ id, domain, file string }{
		{"store-agent", "store", "store/notfound.go"},
		{"api-agent", "api", "api/handler.go"},
	} {
		node := store.AgentNodeID(d.run_.RunID, a.id)
		if err := d.st.AddNode(store.Node{
			NodeID: node, RunID: d.run_.RunID, Kind: "subagent", Label: a.domain,
			Domain: a.domain, AgentID: a.id, AgentType: "domain-agent", Status: "running",
		}); err != nil {
			return err
		}
		_ = d.st.AddEdge(d.run_.RunID, store.PhaseNodeID(d.run_.RunID, "build"), node, "spawn")
		if err := d.st.ClaimWriteSet(d.run_.RunID, node, []string{a.file}); err != nil {
			return err
		}
		d.say("   %s claims %s", a.id, a.file)
	}

	// api-agent reaches into the file store-agent owns. One owner per file is a PRIMARY KEY in
	// SQLite, not a line in a document asking agents to be careful.
	h := &hooks.Handler{Spec: d.sp, Store: d.st}
	// An ABSOLUTE path, because that is what Claude Code's Write tool passes and what the guard
	// resolves against the repo root. Handing it a relative path made filepath.Rel walk out of
	// the repo, and the guard correctly declined to police a file outside it — so the demo
	// printed "allowed" under a heading promising a denial.
	raw := fmt.Sprintf(`{"session_id":"demo-session","agent_id":"api-agent","cwd":%s,`+
		`"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":%s}}`,
		jsonQuote(d.dir), jsonQuote(filepath.Join(d.dir, "store", "notfound.go")))
	out, _ := h.Dispatch("PreToolUse", []byte(raw))
	d.showDecision(out)
	d.say("   One owner per file, enforced by a PRIMARY KEY — not by asking agents to be careful.")
	return nil
}

func (d *demo) stepCheckFails() error {
	d.step(7, "batten check — the checks actually run, and one fails")
	res := d.runChecks()
	for _, line := range res.lines {
		d.say("   %s", line)
	}
	d.say("   That output is not a message batten composed. It is what the command printed.")
	return nil
}

func (d *demo) stepFixAndPass() error {
	d.step(8, "Fix the bug, run the checks again")
	p := filepath.Join(d.dir, "store", "notfound.go")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fixed := strings.Replace(string(b),
		"// BUG: a missing task answers 500.\nconst NotFoundStatus = 500",
		"const NotFoundStatus = 404", 1)
	if err := os.WriteFile(p, []byte(fixed), 0o644); err != nil {
		return err
	}
	d.say("   store/notfound.go: 500 -> 404")
	res := d.runChecks()
	for _, line := range res.lines {
		d.say("   %s", line)
	}
	return nil
}

func (d *demo) stepCommitLands() error {
	d.step(9, "Now the commit is allowed")
	out := d.commit(`git commit -m "US-003: map a missing task to 404"`)
	d.showDecision(out)
	// The narration follows the outcome instead of asserting it. A demo that printed "both
	// halves are satisfied" over a DENIED would be doing the thing batten exists to stop.
	if out == nil || out.HookSpecific == nil || out.HookSpecific.PermissionDecision != "deny" {
		d.say("   Both halves of the gate are satisfied: a reviewer judged the work, and batten")
		d.say("   watched the declared checks pass. The record says which is which.")
	} else {
		d.say("   Still denied — read the reason above. The demo did not get to a clean gate on")
		d.say("   this machine, and it says so rather than claiming otherwise.")
	}
	return nil
}

func (d *demo) stepReport() error {
	d.step(10, "batten report — what happened, and what batten stopped")
	var b strings.Builder
	reportRun(&b, d.sp, d.st, *mustRun(d.st, d.run_.RunID))
	reportImpact(&b, d.st, d.sp.Project, 0, time.Hour)
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		d.say("%s", line)
	}
	return nil
}

// ---------- the machinery, which is the real machinery ----------

// commit drives the actual PreToolUse hook with an actual payload. Nothing here is a stand-in:
// this is the same Dispatch a Claude Code session calls.
func (d *demo) commit(cmd string) *hooks.Output {
	h := &hooks.Handler{Spec: d.sp, Store: d.st}
	raw := fmt.Sprintf(`{"session_id":"demo-session","cwd":%s,"hook_event_name":"PreToolUse",`+
		`"tool_name":"Bash","tool_input":{"command":%s}}`, jsonQuote(d.dir), jsonQuote(cmd))
	out, _ := h.Dispatch("PreToolUse", []byte(raw))
	return out
}

func (d *demo) showDecision(out *hooks.Output) {
	switch {
	case out == nil:
		d.say("   → allowed (batten had no objection)")
	case out.HookSpecific != nil && out.HookSpecific.PermissionDecision == "deny":
		d.say("   → \033[31mDENIED\033[0m")
		for _, line := range strings.Split(out.HookSpecific.PermissionDecisionReason, "\n") {
			d.say("     %s", line)
		}
	default:
		d.say("   → allowed, with a warning:")
		d.say("     %s", out.SystemMessage)
	}
}

type checkResult struct {
	lines []string
	pass  bool
}

// meaningfulLine picks the line a human wants out of a check's output: the one the check itself
// printed about the outcome, in preference to whatever the tool that wrapped it said afterwards.
func meaningfulLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if strings.Contains(s, "PASS") || strings.Contains(s, "FAIL") {
			return s
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return "(no output)"
}

// runChecks runs the gate's declared commands for real and records the verdict batten produces
// from what they printed — which is the difference between "the tests pass" and "batten watched
// the tests pass".
func (d *demo) runChecks() checkResult {
	var res checkResult
	gate := ""
	for name := range d.sp.Gates {
		gate = name
	}
	g := d.sp.Gates[gate]

	var evidence []string
	allPass := true
	for _, c := range g.Checks {
		out, code, took := runCheck(d.dir, c, 30*time.Second)
		// The line the CHECK printed, not the last line of the transcript. Reading only the
		// last line showed `make: *** [Makefile:2: test] Error 2` — the wrapper's complaint —
		// and hid the sentence that actually says what is wrong.
		msg := meaningfulLine(out)
		if code == 0 {
			res.lines = append(res.lines, fmt.Sprintf("✓ %s  %s  (%s)", c, msg, took))
			evidence = append(evidence, fmt.Sprintf("%s: PASS (exit 0, %s)\n%s", c, took, msg))
			continue
		}
		allPass = false
		res.lines = append(res.lines, fmt.Sprintf("✗ %s  (exit %d)", c, code))
		res.lines = append(res.lines, "  "+msg)
		evidence = append(evidence, fmt.Sprintf("%s: FAIL (exit %d)\n%s", c, code, lastLines(out, 5)))
	}
	res.pass = allPass

	result := "blocked"
	if allPass {
		result = "ok"
	}
	_ = d.st.SaveVerdict(store.Verdict{
		RunID: d.run_.RunID, Gate: gate, CheckID: "checks", Result: result, Source: "batten",
		Evidence: evidence, Why: "batten ran the gate's declared checks",
	}, true)
	return res
}

// rewriteSpec flips the generated spec out of report mode, and — where the scanner had no build
// file to lift a check from — declares the check the demo needs.
//
// Both edits are made to the FILE the scanner produced and then reloaded through spec.LoadFrom,
// rather than by patching the struct in memory: the demo has to run the same spec path everyone
// else runs, or it stops being evidence of anything.
func (d *demo) rewriteSpec() error {
	p := filepath.Join(d.dir, spec.Filename)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	s := strings.Replace(string(b), "enforcement: report", "enforcement: enforce", 1)
	if !d.useMake() {
		s = strings.Replace(s, "    checks: []",
			"    checks: ['"+d.checkCommand()+"']", 1)
	}
	return os.WriteFile(p, []byte(s), 0o644)
}

func mustRun(st *store.Store, runID string) *store.Run {
	r, err := st.Run(runID)
	if err != nil {
		return &store.Run{RunID: runID}
	}
	return r
}

// jsonQuote is enough for the two strings the demo embeds: a Windows path and a shell command.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
