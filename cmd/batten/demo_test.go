package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDemoIsTheEndToEndTestThisProjectDidNotHave.
//
// Every step of `batten demo` runs the production path — the real scanner, the real spec loader,
// the real PreToolUse hook, the real check runner. So exercising it once covers the whole engine
// end to end, which nothing else did.
//
// The assertions are on the OUTCOMES, not the wording, so the narration can be rewritten without
// breaking the test — except where the wording IS the claim.
func TestDemoIsTheEndToEndTestThisProjectDidNotHave(t *testing.T) {
	// Pointed at a database the demo must never open. If it ever falls back to dbPath(), this
	// file appears and the test fails on it below.
	forbidden := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("BATTEN_DB", forbidden)

	out := captureStdout(t, func() {
		if err := cmdDemo(nil); err != nil {
			t.Fatalf("the demo must run to completion on a clean machine: %v", err)
		}
	})

	// The arc, in order. Each is a distinct mechanism, and a demo that lost one would be
	// describing a product batten does not have.
	steps := []struct{ what, want string }{
		{"the unit pattern comes from the backlog", `US-\d{3}`},
		{"both domains are found from their AGENTS.md", "api, store"},
		{"an ungated commit warns rather than passing in silence", "NOT gated"},
		{"a commit with no verdict is denied", "no verdict envelope"},
		{"a reviewer alone is not enough", "no batten-verified pass"},
		{"the write-set guard denies a cross-agent write", "write-set collision"},
		{"the declared check RUNS and fails for a real reason", "FAIL: store/notfound.go"},
		{"and passes once the bug is fixed", "PASS: a missing task maps to 404"},
		{"the report says what was stopped", "what batten stopped"},
	}
	// Only where git exists: the sandbox is a real repo, and the stale-target check has no tree
	// to fingerprint without one. Skipping the assertion is right; asserting it unconditionally
	// would make the suite fail on a machine batten degrades on correctly.
	if strings.Contains(out, "Something edits the tree") {
		steps = append(steps,
			struct{ what, want string }{"a tree edited after the checks passed is caught",
				"STALE TARGET"})
	}
	for _, s := range steps {
		if !strings.Contains(out, s.want) {
			t.Errorf("%s — expected %q in:\n%s", s.what, s.want, out)
		}
	}

	// The demo must reach a CLEAN gate at the step that promises one. Bounded to that step
	// rather than to "the rest of the output": the step AFTER it deliberately earns a denial
	// (a formatter edits the tree), and an unbounded window read that as this step failing.
	step9 := out[strings.LastIndex(out, "Now the commit is allowed"):]
	if end := strings.Index(step9, "\n\n"); end > 0 {
		step9 = step9[:end]
	}
	if strings.Contains(step9, "DENIED") {
		t.Errorf("the step that promises a clean gate did not get one:\n%s", step9)
	}
}

// The three promises the command makes about isolation. They are the reason anyone would run an
// unfamiliar binary that says it will build a repo, so they are asserted rather than intended.
func TestDemoTouchesNothingOfYours(t *testing.T) {
	forbidden := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("BATTEN_DB", forbidden)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	before := dirListing(t, cwd)

	out := captureStdout(t, func() {
		if err := cmdDemo(nil); err != nil {
			t.Fatal(err)
		}
	})

	// 1. It never opens the configured database. dbPath() is the trapdoor: one accidental
	//    load() inside the demo and it would write into the user's real ledger.
	if _, err := os.Stat(forbidden); err == nil {
		t.Errorf("the demo opened BATTEN_DB (%s). It must pass its own store path explicitly, "+
			"so neither BATTEN_DB nor ~/.batten is reachable even by accident", forbidden)
	}

	// 2. It leaves the working directory exactly as it found it.
	if after := dirListing(t, cwd); after != before {
		t.Errorf("the demo changed the current directory.\nbefore: %s\nafter:  %s", before, after)
	}

	// 3. It deletes its sandbox — and the claim in the output has to be true. Windows will not
	//    delete an open file, so a live SQLite handle used to make RemoveAll fail silently while
	//    the demo announced that the sandbox was gone.
	sandbox := sandboxPathFrom(out)
	if sandbox == "" {
		t.Fatalf("the demo did not print the sandbox path it used:\n%s", out)
	}
	if _, err := os.Stat(sandbox); err == nil {
		t.Errorf("the sandbox %s still exists after the demo said it was gone", sandbox)
	}
}

// --keep is the escape hatch, and it must keep what it says it keeps.
func TestDemoKeepLeavesTheSandboxAndSaysWhere(t *testing.T) {
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "unused.db"))

	out := captureStdout(t, func() {
		if err := cmdDemo([]string{"--keep"}); err != nil {
			t.Fatal(err)
		}
	})
	sandbox := sandboxPathFrom(out)
	if sandbox == "" {
		t.Fatal("--keep did not report where it kept the sandbox")
	}
	t.Cleanup(func() { os.RemoveAll(sandbox) })

	if _, err := os.Stat(filepath.Join(sandbox, "batten.yaml")); err != nil {
		t.Errorf("--keep did not leave a usable sandbox behind: %v", err)
	}
	if !strings.Contains(out, "sandbox kept at") {
		t.Errorf("--keep must say plainly that it left something behind:\n%s", out)
	}
}

func TestDemoRejectsAnUnknownFlag(t *testing.T) {
	if err := cmdDemo([]string{"--fast"}); err == nil {
		t.Error("an unknown flag must be refused, not ignored")
	}
}

// meaningfulLine is what keeps a wrapper's complaint from standing in for the check's own
// verdict: reading the last line alone reported `make: *** [Makefile:2: test] Error 2` and hid
// the sentence saying what was actually wrong.
func TestMeaningfulLinePrefersTheChecksOwnVerdict(t *testing.T) {
	cases := []struct{ in, want string }{
		{"go: downloading\nFAIL: TestFoo\nmake: *** [Makefile:2: test] Error 2\n", "FAIL: TestFoo"},
		{"ok  \tpkg\nPASS: all green\n", "PASS: all green"},
		{"something unexpected\n\n", "something unexpected"},
		{"", "(no output)"},
	}
	for _, c := range cases {
		if got := meaningfulLine(c.in); got != c.want {
			t.Errorf("meaningfulLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func dirListing(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

func sandboxPathFrom(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "sandbox: "); i >= 0 {
			return strings.TrimSpace(line[i+len("sandbox: "):])
		}
		if i := strings.Index(line, "sandbox kept at "); i >= 0 {
			return strings.TrimSpace(strings.TrimSuffix(
				strings.TrimSpace(line[i+len("sandbox kept at "):]), " — delete it when you are done."))
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --dir gives the .tape scripts a KNOWN sandbox path, which is the only reason they can cd into
// it afterwards. That convenience must not become a way to delete somebody's work: a path the
// demo did not create is refused outright, not emptied.
func TestDemoDirRefusesADirectoryItDidNotCreate(t *testing.T) {
	dir := t.TempDir()
	precious := filepath.Join(dir, "thesis.txt")
	if err := os.WriteFile(precious, []byte("years of work"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := cmdDemo([]string{"--dir", dir})
	if err == nil {
		t.Fatal("the demo agreed to build itself over a directory full of somebody else's files")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("the refusal must say why: %v", err)
	}
	if b, readErr := os.ReadFile(precious); readErr != nil || string(b) != "years of work" {
		t.Errorf("the file was touched: %v %q", readErr, b)
	}
}

// And it reuses its own sandbox, so re-running a tape twice does not fail on the second pass.
func TestDemoDirReusesItsOwnSandbox(t *testing.T) {
	t.Setenv("BATTEN_DB", filepath.Join(t.TempDir(), "unused.db"))
	dir := filepath.Join(t.TempDir(), "demo-here")

	for i := 0; i < 2; i++ {
		out := captureStdout(t, func() {
			if err := cmdDemo([]string{"--keep", "--dir", dir}); err != nil {
				t.Fatalf("run %d: %v", i+1, err)
			}
		})
		if !strings.Contains(out, "what batten stopped") {
			t.Fatalf("run %d did not complete:\n%s", i+1, out)
		}
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
}
