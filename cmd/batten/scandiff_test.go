package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/store"
)

// TestContrastDiffSeparatesTheThreeCases is the whole comparison, without a repo.
//
// The three outcomes are not symmetric and must not be collapsed: a file changed that nobody
// claimed is a possible fence-crossing, a path claimed and never touched is over-declaration, and
// a path both claimed and touched is the system working.
func TestContrastDiffSeparatesTheThreeCases(t *testing.T) {
	changed := []string{"api/handler.go", "ui/page.tsx", "generated/schema.pb.go"}
	ws := map[string][]string{
		"n-api": {"api/handler.go", "api/never_touched.go"},
		"n-ui":  {"ui/page.tsx"},
	}
	rep := contrastDiff(changed, ws)

	if got, want := rep.Undeclared, []string{"generated/schema.pb.go"}; !equalStrings(got, want) {
		t.Errorf("undeclared = %v, want %v — a generated file nobody claimed is exactly what no "+
			"shell parser can see", got, want)
	}
	if got := rep.Unused["n-api"]; !equalStrings(got, []string{"api/never_touched.go"}) {
		t.Errorf("unused for n-api = %v, want [api/never_touched.go]", got)
	}
	if len(rep.Unused["n-ui"]) != 0 {
		t.Errorf("n-ui claimed exactly what it touched, and was reported as over-declaring: %v",
			rep.Unused["n-ui"])
	}
	if got := rep.Owned["n-api"]; !equalStrings(got, []string{"api/handler.go"}) {
		t.Errorf("owned for n-api = %v", got)
	}
	// 1 unused of 3 claimed.
	if over := rep.OverDeclared(); over < 0.32 || over > 0.34 {
		t.Errorf("over-declaration = %.4f, want ~0.3333", over)
	}
}

// Zero claims is NOT zero violations. A run with no declared write-sets is a planning gap, and
// reporting it as clean would be the emptiest possible green tick — the same failure as reporting
// an unmeasured token count as 0.
func TestNoClaimsIsNotACleanResult(t *testing.T) {
	rep := contrastDiff([]string{"api/handler.go"}, map[string][]string{})
	if rep.Claims != 0 {
		t.Fatalf("Claims = %d", rep.Claims)
	}
	if over := rep.OverDeclared(); over >= 0 {
		t.Errorf("OverDeclared() = %.2f with zero claims; 0%% over-declaration on no claims is "+
			"not a measurement, and -1 is how this says so", over)
	}
}

// Path shapes must not create phantom findings. A claim written `./api/x.go` and a diff line
// reading `api/x.go` are the same file, and reporting them as a violation plus an over-declaration
// would be two false findings from one path.
func TestPathShapesDoNotCreatePhantomFindings(t *testing.T) {
	rep := contrastDiff(
		[]string{"api/x.go"},
		map[string][]string{"n-a": {"./api/x.go"}},
	)
	if len(rep.Undeclared) != 0 {
		t.Errorf("`./api/x.go` and `api/x.go` were treated as different files: %v", rep.Undeclared)
	}
	if len(rep.Unused["n-a"]) != 0 {
		t.Errorf("the same path was also counted as over-declared: %v", rep.Unused["n-a"])
	}
}

// TestScanDiffSeesWhatNoShellParserCan is the end-to-end case, and it is the argument for this
// command existing alongside the Bash write guard.
//
// The write is done by a script — which is what a code generator, a Makefile target or any
// third-party tool looks like from the outside. No parser of shell commands reaches inside it. This
// check never looks at commands, so it does not care.
func TestScanDiffSeesWhatNoShellParserCan(t *testing.T) {
	dir := gitRepoWithSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: build\n    fanout: true\n    anchor: git_sha\n  - id: close\n    requires_verdict: ok\n")
	db := filepath.Join(t.TempDir(), "batten.db")
	t.Setenv("BATTEN_DB", db)

	_ = captureStdout(t, func() { inDir(t, dir, func() { _ = cmdPhase([]string{"TASK-1", "build"}) }) })

	// One agent, one claim.
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.LatestRun("p", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	node := store.AgentNodeID(run.RunID, "agent-a")
	if err := st.AddNode(store.Node{
		NodeID: node, RunID: run.RunID, Kind: "subagent", Label: "api", AgentID: "agent-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimWriteSet(run.RunID, node, []string{"seed.txt", "claimed-but-never-written.go"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// The claimed write, and an UNCLAIMED one produced the way a generator produces it: from
	// inside a script, where no command parser can follow.
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("agent-a did this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "gen.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho generated > generated.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// cmd.Dir, and it is not incidental. The first version of this test left it unset, so the
	// script's `> generated.txt` resolved against the TEST PROCESS's cwd — `cmd/batten` — and wrote
	// a file into the source tree that broke the next compile. A test that writes outside its
	// fixture is the same class of bug the thing under test exists to find.
	gen := exec.Command("sh", script)
	gen.Dir = dir
	if out, err := gen.CombinedOutput(); err != nil {
		// No usable shell here does not invalidate the point — what matters is that the file was
		// produced by something batten cannot read, so writing it directly is the same situation.
		t.Logf("sh unavailable (%v: %s); writing the generated file directly", err, out)
		if err := os.WriteFile(filepath.Join(dir, "generated.txt"), []byte("generated\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "generated.txt")); err != nil {
		t.Fatalf("the fixture did not produce the unclaimed file: %v", err)
	}

	out := captureStdout(t, func() {
		inDir(t, dir, func() {
			if err := cmdScanDiff([]string{"TASK-1"}); err != nil {
				t.Errorf("scan-diff returned an error without --strict: %v", err)
			}
		})
	})

	if !strings.Contains(out, "generated.txt") {
		t.Errorf("the file a script wrote outside every write-set is not reported:\n%s", out)
	}
	if !strings.Contains(out, "NO write-set claimed") {
		t.Errorf("the undeclared section is missing:\n%s", out)
	}
	if !strings.Contains(out, "claimed-but-never-written.go") {
		t.Errorf("over-declaration is not reported:\n%s", out)
	}
	if !strings.Contains(out, "over-declaration") {
		t.Errorf("the over-declaration number — which nobody else measures — is missing:\n%s", out)
	}
	// batten must not conclude WHO did it from a diff.
	if strings.Contains(out, "went around the fence.") && !strings.Contains(out, "will not guess") {
		t.Errorf("the report concluded intent from a diff:\n%s", out)
	}

	// --strict is what makes it usable as a gate check.
	var serr error
	_ = captureStdout(t, func() {
		inDir(t, dir, func() { serr = cmdScanDiff([]string{"TASK-1", "--strict"}) })
	})
	if serr == nil {
		t.Error("--strict passed with an undeclared change, so it cannot be wired into gates.checks")
	}
}

// A run with no anchor has an UNKNOWN diff, not an empty one. Reporting "0 files changed" for a
// unit that never entered its anchoring phase is the same inversion as reporting an unmeasured
// token count as zero.
func TestScanDiffRefusesWithoutAnAnchor(t *testing.T) {
	dir := gitRepoWithSpec(t, "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n"+
		"phases:\n  - id: build\n    anchor: git_sha\n  - id: verify\n  - id: close\n    requires_verdict: ok\n")
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "batten.db"))
	// Skip build, so no anchor is ever stamped.
	_ = captureStdout(t, func() { inDir(t, dir, func() { _ = cmdPhase([]string{"TASK-1", "verify"}) }) })

	var err error
	_ = captureStdout(t, func() {
		inDir(t, dir, func() { err = cmdScanDiff([]string{"TASK-1"}) })
	})
	if err == nil {
		t.Fatal("scan-diff reported on a run with no anchor instead of saying it cannot measure")
	}
	if !strings.Contains(err.Error(), "measurable") {
		t.Errorf("the refusal must distinguish not-measurable from clean: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
