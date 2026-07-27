package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// fixture builds a repo with a spec, a vault, and a run that fanned out to two subagents with
// disjoint write-sets — the shape the run note is meant to describe.
func fixture(t *testing.T, vault string) (*spec.Spec, *store.Store) {
	t.Helper()
	dir := t.TempDir()

	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n    fanout: true\n  - id: close\n    requires_verdict: ok\n"
	if vault != "" {
		y += "capabilities:\n  obsidian:\n    vault: '" + filepath.ToSlash(vault) + "'\n    export: [runs, verdicts, canvas]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	r, err := st.EnsureRun("p", "TASK-1", "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []store.Node{
		{NodeID: "p-build", RunID: r.RunID, Kind: "phase", Label: "build", Status: "ok"},
		{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "api", Domain: "api", Status: "ok"},
		{NodeID: "n-b", RunID: r.RunID, Kind: "subagent", Label: "web", Domain: "web", Status: "ok"},
	} {
		if err := st.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.AddEdge(r.RunID, "p-build", "n-a", "spawn")
	_ = st.AddEdge(r.RunID, "p-build", "n-b", "spawn")
	if err := st.ClaimWriteSet(r.RunID, "n-a", []string{"server/handler.go", "server/handler_test.go"}); err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimWriteSet(r.RunID, "n-b", []string{"client/app.tsx"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveVerdict(store.Verdict{
		RunID: r.RunID, Gate: "qa", CheckID: "qa", Result: "ok",
		Evidence: []string{"go test ./...: PASS"},
	}, true); err != nil {
		t.Fatal(err)
	}
	return sp, st
}

// TestRunNoteReportsTheWriteSetsThatWereActuallyClaimed is the regression test for a bug that
// lived in every release of the vault export: the note rendered "not recorded" in the write-set
// column and never printed the Files touched list, because export.Run — the only production
// path — never populated Writer.WriteSets. The rendering was right; nothing filled it.
func TestRunNoteReportsTheWriteSetsThatWereActuallyClaimed(t *testing.T) {
	vault := t.TempDir()
	sp, st := fixture(t, vault)

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.RunNotePath == "" {
		t.Fatal("a configured vault must produce a run note")
	}
	b, err := os.ReadFile(res.RunNotePath)
	if err != nil {
		t.Fatal(err)
	}
	note := string(b)

	if strings.Contains(note, "not recorded") {
		t.Errorf("write-sets WERE claimed, so the note must not say \"not recorded\":\n%s", note)
	}
	if !strings.Contains(note, "2 files") || !strings.Contains(note, "1 file") {
		t.Errorf("the fan-out table must show each agent's write-set size (2 files / 1 file):\n%s", note)
	}
	for _, f := range []string{"server/handler.go", "server/handler_test.go", "client/app.tsx"} {
		if !strings.Contains(note, f) {
			t.Errorf("Files touched is missing %q:\n%s", f, note)
		}
	}
}

// The other half of that fix: when nothing was claimed, the note must say "not recorded" rather
// than "0 files". "Nobody recorded a write-set" and "this agent owned nothing" are different
// facts, and reporting the second when the first is true is inventing one.
func TestRunNoteSaysNotRecordedWhenNothingWasClaimed(t *testing.T) {
	vault := t.TempDir()
	dir := t.TempDir()
	y := "version: 1\nproject: p\nunit:\n  name: TASK\n  pattern: 'TASK-\\d+'\n" +
		"phases:\n  - id: build\n  - id: close\n    requires_verdict: ok\n" +
		"capabilities:\n  obsidian:\n    vault: '" + filepath.ToSlash(vault) + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "batten.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := spec.LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "batten.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	r, _ := st.EnsureRun("p", "TASK-9", "sess-a")
	_ = st.AddNode(store.Node{NodeID: "n-a", RunID: r.RunID, Kind: "subagent", Label: "solo", Status: "ok"})

	res, err := Run(sp, st, "TASK-9")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(res.RunNotePath)
	if !strings.Contains(string(b), "not recorded") {
		t.Errorf("with no claims at all the note must read \"not recorded\", never \"0 files\":\n%s", b)
	}
}

// A closed run is exactly the one the vault should reflect — the Stop hook exports AFTER work
// concludes. Exporting only open runs meant a finished unit never reached the vault at all.
func TestExportWorksForAClosedRun(t *testing.T) {
	vault := t.TempDir()
	sp, st := fixture(t, vault)
	r, err := st.LatestRun("p", "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CloseRun(r.RunID, "ok"); err != nil {
		t.Fatal(err)
	}

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatalf("a closed run must still export: %v", err)
	}
	if res.RunNotePath == "" || res.CanvasPath == "" {
		t.Fatalf("closed run produced note=%q canvas=%q", res.RunNotePath, res.CanvasPath)
	}
	if _, err := os.Stat(res.CanvasPath); err != nil {
		t.Errorf("the canvas was not written: %v", err)
	}
	if res.Nodes == 0 {
		t.Error("the canvas has no nodes")
	}
}

// With no vault configured the canvas still gets written, into .batten/ — the feature degrades
// rather than disappearing, and no note is claimed that does not exist.
func TestWithoutAVaultTheCanvasStillLands(t *testing.T) {
	sp, st := fixture(t, "")

	res, err := Run(sp, st, "TASK-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.RunNotePath != "" {
		t.Errorf("no vault is configured, so no note path may be reported; got %q", res.RunNotePath)
	}
	if !strings.Contains(filepath.ToSlash(res.CanvasPath), "/.batten/") {
		t.Errorf("without a vault the canvas belongs in .batten/, got %q", res.CanvasPath)
	}
	if _, err := os.Stat(res.CanvasPath); err != nil {
		t.Errorf("the canvas was not written: %v", err)
	}
}

// A unit with no run at all is not an error worth breaking a hook over — the Stop hook calls
// this on every session end, including sessions that never started a unit.
func TestUnknownUnitFailsQuietlyEnoughForAHook(t *testing.T) {
	sp, st := fixture(t, t.TempDir())
	if _, err := Run(sp, st, "TASK-404"); err == nil {
		t.Error("an unknown unit should report an error to its caller, which ignores it on the hook path")
	}
}

func TestExpandHomeLeavesAbsolutePathsAlone(t *testing.T) {
	if got := expandHome("/var/vaults/acme"); got != "/var/vaults/acme" {
		t.Errorf("an absolute path must not be rewritten; got %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}
	got := expandHome("~/vaults/acme")
	if !strings.HasPrefix(got, home) {
		t.Errorf("~ must expand to the home directory; got %q", got)
	}
	if strings.Contains(got, "~") {
		t.Errorf("the tilde survived expansion: %q", got)
	}
}
