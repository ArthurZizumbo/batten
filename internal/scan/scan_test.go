package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write creates a file and every directory above it.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestPlannedButUnbuiltRepoStillHasDomains is the proyecto_ui case, and the reason deriveDomains
// no longer requires code. A repo can be fully planned — one AGENTS.md per domain, a planning
// doc, no source files yet — and those directories ARE the fan-out axes. Somebody drew those
// boundaries deliberately; requiring code to see them returned an empty domain list for exactly
// the repos where the axes were already decided.
func TestPlannedButUnbuiltRepoStillHasDomains(t *testing.T) {
	root := t.TempDir()
	write(t, root, "AGENTS.md", "# repo rules\n")
	write(t, root, "CLAUDE.md", "# claude rules\n")
	write(t, root, "backend/AGENTS.md", "- session_id in every query\n")
	write(t, root, "frontend/AGENTS.md", "- no color literals\n")
	write(t, root, "ml/AGENTS.md", "- seed every experiment\n")
	write(t, root, "context/planeacion_proyecto.md", "## US-001\n")

	f, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(f.Domains) != 3 {
		t.Fatalf("got %d domains, want 3 (backend, frontend, ml) — a planned repo has axes before it has code", len(f.Domains))
	}
	for _, d := range f.Domains {
		if d.Rules == "" {
			t.Errorf("domain %q lost its rules file", d.Name)
		}
		if len(d.Check) != 0 {
			t.Errorf("domain %q invented %v — there are no build files to take a check from", d.Name, d.Check)
		}
	}

	// The harness must be reported, root and per-directory alike: it is what the spec has to
	// agree with, and it is the only source for the invariants.
	byPath := map[string]HarnessFact{}
	for _, h := range f.Harness {
		byPath[h.Path] = h
	}
	for _, want := range []string{"AGENTS.md", "CLAUDE.md", "backend/AGENTS.md", "ml/AGENTS.md"} {
		if _, ok := byPath[want]; !ok {
			t.Errorf("harness is missing %q; got %v", want, keys(byPath))
		}
	}
	if !has(f.Purpose, "context/planeacion_proyecto.md") {
		t.Errorf("the planning doc is where the work items live; purpose = %v", f.Purpose)
	}

	// With no build files, the gate verifies nothing. That has to be said out loud rather than
	// dressed up as "checks were taken from your build files".
	joined := strings.Join(f.Notes, "\n")
	if !strings.Contains(joined, "NO check command was found") {
		t.Errorf("an empty gate must be reported as empty; notes =\n%s", joined)
	}
	if strings.Contains(joined, "were taken from your build files") {
		t.Errorf("claimed checks came from build files that do not exist; notes =\n%s", joined)
	}
}

// TestStackComesFromMarkerFiles pins that the stack is read off files that exist, never guessed
// from a directory name — a wrong guess produces check commands that cannot run.
func TestStackComesFromMarkerFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"acme","scripts":{"lint":"eslint .","test":"vitest"},"dependencies":{"next":"15.0.0"},"devDependencies":{"vitest":"2.0.0"}}`)
	write(t, root, "tsconfig.json", "{}")
	write(t, root, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	write(t, root, "Makefile", "test:\n\tpnpm test\n")
	write(t, root, "client/app.tsx", "export default function App(){}\n")

	f, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"node", "typescript", "next.js", "vitest", "pnpm", "make"} {
		if !has(f.Stack, want) {
			t.Errorf("stack is missing %q; got %v", want, f.Stack)
		}
	}
	// The package manager comes from the lockfile. A repo with pnpm-lock.yaml must not be told
	// to run npm, and vice versa.
	for _, never := range []string{"npm", "yarn", "bun", "go", "python"} {
		if has(f.Stack, never) {
			t.Errorf("stack invented %q; got %v", never, f.Stack)
		}
	}
	if !has(f.Purpose, "README.md") && len(f.Purpose) != 0 {
		t.Logf("purpose = %v", f.Purpose) // no README here; only assert it did not fabricate one
	}
	if len(f.Purpose) != 0 {
		t.Errorf("no README or planning doc exists, but purpose = %v", f.Purpose)
	}
}

// TestExistingSpecIsReportedNotOverwritten: init refuses to clobber a batten.yaml, and the scan
// has to surface that it exists so the caller reconciles instead of drafting over it.
func TestExistingSpecIsReportedNotOverwritten(t *testing.T) {
	root := t.TempDir()
	write(t, root, "batten.yaml", "version: 1\nproject: already-here\n")
	write(t, root, "docs/workflow.md", "# our process\n")
	write(t, root, "main.go", "package main\n")

	f, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	var spec, flow bool
	for _, h := range f.Harness {
		switch h.Kind {
		case "batten-spec":
			spec = true
		case "workflow-doc":
			flow = true
			if !strings.Contains(h.Note, "--from") {
				t.Errorf("a prose workflow should point at --from; note = %q", h.Note)
			}
		}
	}
	if !spec {
		t.Error("an existing batten.yaml must be reported in harness[]")
	}
	if !flow {
		t.Error("docs/workflow.md is the best possible --from input and must be reported")
	}
}

func keys(m map[string]HarnessFact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
