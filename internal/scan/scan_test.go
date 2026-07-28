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

// TestUnitComesFromTheBacklogNotJustBranches is the proyecto_ui case that motivated reading the
// planning doc at all. That repo had US-001..US-020 written down and a single `main` branch, so
// deriving from branch names alone proposed `TASK-\d+` — and every default downstream of the
// unit was then wrong. The backlog is where the ids actually live.
func TestUnitComesFromTheBacklogNotJustBranches(t *testing.T) {
	root := t.TempDir()
	write(t, root, "context/planeacion_proyecto.md", strings.Join([]string{
		"# Plan",
		"Intro prose mentioning US-001 several times: US-001, US-001, US-001, US-001.",
		"### US-001 — Docker Compose",
		"body",
		"### US-002 — Dependencies",
		"body",
		"### US-003 — Terraform",
		"body",
		"### US-014 — RAG",
		"body",
	}, "\n"))
	write(t, root, "main.go", "package main\n")

	f, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}

	if f.UnitName != "US" {
		t.Errorf("unit name = %q, want US — the backlog says so on every heading", f.UnitName)
	}
	// Three digits were observed, so the pattern must demand three. A permissive \d+ would match
	// US-1 and US-12345 too, and the unit id is what attribution hangs on.
	if f.UnitPattern != `US-\d{3}` {
		t.Errorf("unit pattern = %q, want %s", f.UnitPattern, `US-\d{3}`)
	}
	// Knowing WHERE the items are defined and HOW their headings look means plan and locator
	// stop being TODOs the user has to discover.
	if f.UnitPlan != "context/planeacion_proyecto.md" {
		t.Errorf("unit plan = %q, want the backlog document", f.UnitPlan)
	}
	if f.UnitLocator != "### {id}" {
		t.Errorf("unit locator = %q, want '### {id}'", f.UnitLocator)
	}

	y := f.ToYAML()
	for _, want := range []string{`pattern: 'US-\d{3}'`, "plan: context/planeacion_proyecto.md", "locator: '### {id}'"} {
		if !strings.Contains(y, want) {
			t.Errorf("the drafted spec is missing %q:\n%s", want, y)
		}
	}
}

// Prose citations must not outvote headings, and too few headings must not be called a
// convention at all — a single "see US-001" in a README is not a backlog.
func TestASingleMentionIsNotAConvention(t *testing.T) {
	root := t.TempDir()
	write(t, root, "README.md", "# Thing\n\nRelated to US-001. See also US-001 and US-001.\n")
	write(t, root, "main.go", "package main\n")

	f, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.UnitName == "US" {
		t.Errorf("three prose mentions and zero headings is not a backlog; unit = %q/%q", f.UnitName, f.UnitPattern)
	}
	if f.UnitPlan != "" {
		t.Errorf("no backlog was found, so unit.plan must stay empty; got %q", f.UnitPlan)
	}
}

// TestSkillSuggestionsMatchNamesNotProse pins a fix motivated by real output on a real repo.
//
// The old matcher substring-searched the skill name AND its description for the domain name,
// which produced confident nonsense: an infrastructure skill whose description mentioned
// "2 Cloud Run services (frontend/backend)" was suggested for BOTH of those domains, and a
// domain named `db` matched any word containing those two letters anywhere in any prose.
//
// A skills list is not a harmless hint — it rides into that domain's agent prompt, so a wrong
// entry spends context arguing for the wrong tool. Suggesting nothing beats suggesting noise.
func TestSkillSuggestionsMatchNamesNotProse(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"backend", "frontend", "db", "ml"} {
		write(t, root, d+"/AGENTS.md", "- a rule\n")
	}
	skill := func(name, desc string) {
		write(t, root, ".claude/skills/"+name+"/SKILL.md",
			"---\nname: "+name+"\ndescription: "+desc+"\n---\n\nbody\n")
	}
	skill("portal-backend-api", "FastAPI routers and services.")
	skill("portal-db-models", "SQLAlchemy models and migrations.")
	// The trap: infrastructure, named for neither domain, describing both.
	skill("portal-terraform-gcp", "Terraform for 2 Cloud Run services (frontend/backend) with scale-to-zero.")
	// The other trap: 'db' appears inside an unrelated word in the prose.
	skill("portal-auth-jwt", "JWT with pwdlib Argon2 and a role matrix.")

	f, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, d := range f.Domains {
		got[d.Name] = d.Skills
	}

	if len(got["backend"]) != 1 || got["backend"][0] != "portal-backend-api" {
		t.Errorf("backend skills = %v, want exactly [portal-backend-api]", got["backend"])
	}
	if len(got["db"]) != 1 || got["db"][0] != "portal-db-models" {
		t.Errorf("db skills = %v, want exactly [portal-db-models] — prose containing 'db' is not a match", got["db"])
	}
	// The infra skill belongs to neither, however its description reads.
	for _, d := range []string{"backend", "frontend"} {
		if has(got[d], "portal-terraform-gcp") {
			t.Errorf("%s was given portal-terraform-gcp because its DESCRIPTION names the domain; "+
				"only the skill's own name may decide: %v", d, got[d])
		}
	}
	// No skill is named for ml, and saying so is the honest answer.
	if len(got["ml"]) != 0 {
		t.Errorf("ml skills = %v, want none — nothing is named for it", got["ml"])
	}
}
