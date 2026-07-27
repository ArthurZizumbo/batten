// Package scan interviews a repository so `batten init` can propose a batten.yaml grounded in
// what the repo actually is, rather than a blank template. It reports facts; the judgment
// (which invariants matter, what a gate should require) is left to the agent or the user.
package scan

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/arthu/batten/internal/discovery"
)

// Facts is everything the scanner could learn without a human.
type Facts struct {
	Project     string           `json:"project"`
	UnitPattern string           `json:"unit_pattern"` // derived from branch names, or a sensible default
	UnitName    string           `json:"unit_name"`
	Domains     []DomainFact     `json:"domains"`
	Stack       []string         `json:"stack"`   // languages and tooling, from marker files actually present
	Purpose     []string         `json:"purpose"` // files where the repo describes what it is for
	Harness     []HarnessFact    `json:"harness"` // the process already governing this repo
	Graphify    bool             `json:"graphify"` // graphify on PATH or graphify-out/ present
	Engram      bool             `json:"engram"`   // engram plugin/binary detectable
	Skills      []discovery.Item `json:"skills"`
	Agents      []discovery.Item `json:"agents"`
	Notes       []string         `json:"notes"` // things the user should decide, surfaced honestly
}

// HarnessFact is a piece of process this repo ALREADY has. It is reported so that init can
// complement what exists instead of proposing a workflow over the top of one that already works —
// a repo with per-directory AGENTS.md and a Makefile full of checks is not a blank slate, and
// treating it as one is how a tool gets uninstalled on day two.
type HarnessFact struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // agent-rules | contributing | workflow-doc | editor-rules | build | batten-spec
	Note string `json:"note"` // why it matters for the spec
}

type DomainFact struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Check  []string `json:"check"`  // real commands from Makefile/package.json/etc, verbatim
	Rules  string   `json:"rules"`  // AGENTS.md / CLAUDE.md in that dir, if present
	Skills []string `json:"skills"` // discovery's suggestion for this domain
}

// codeDir is a top-level directory worth treating as a domain: it holds source, not vendored
// deps or build junk.
var skipDirs = map[string]bool{
	".git": true, ".github": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".venv": true, "venv": true, "__pycache__": true,
	".idea": true, ".vscode": true, "docs": true, ".claude": true, ".batten": true,
	"graphify-out": true,
}

// Scan walks the repo root and gathers facts. It never writes anything.
func Scan(root string) (*Facts, error) {
	f := &Facts{
		Project:  filepath.Base(root),
		UnitName: "TASK",
	}

	f.UnitName, f.UnitPattern = deriveUnit(root)
	f.Domains = deriveDomains(root)
	f.Stack = deriveStack(root)
	f.Purpose = derivePurpose(root)
	f.Harness = deriveHarness(root)
	f.Graphify = onPath("graphify") || exists(filepath.Join(root, "graphify-out", "graph.json"))
	f.Engram = onPath("engram")

	if sk, err := discovery.Skills(root); err == nil {
		f.Skills = sk
	}
	if ag, err := discovery.Agents(root); err == nil {
		f.Agents = ag
	}
	// Map skills onto domains as a starting point.
	domMap := map[string]struct {
		Path string
	}{}
	for _, d := range f.Domains {
		domMap[d.Name] = struct{ Path string }{d.Path}
	}
	// discovery.Suggest wants spec.Domain; emulate its intent with a light match here to avoid a
	// cyclic dependency on spec. Match skill name/description against domain name.
	for i, d := range f.Domains {
		for _, s := range f.Skills {
			hay := strings.ToLower(s.Name + " " + s.Description)
			if strings.Contains(hay, strings.ToLower(d.Name)) {
				f.Domains[i].Skills = append(f.Domains[i].Skills, s.Name)
			}
		}
	}

	if f.UnitPattern == "" {
		f.UnitPattern = `TASK-\d+`
		f.Notes = append(f.Notes, "no work-item pattern found in branch names; defaulted to TASK-N — edit unit.pattern to match your tracker (e.g. US-\\d{3}, PROJ-\\d+)")
	}
	if len(f.Domains) == 0 {
		f.Notes = append(f.Notes, "no obvious code directories found; add domains by hand")
	}
	f.Notes = append(f.Notes,
		"invariants are empty — fill each domain's invariants with the rules a reviewer would catch (mine them from AGENTS.md)")
	// Only claim the checks came from the build files when some actually did. A gate whose
	// checks list is empty is a gate that passes everything, and saying "taken from your build
	// files" over an empty list is the kind of confident-sounding lie that gets believed.
	if len(allChecks(f.Domains)) > 0 {
		f.Notes = append(f.Notes,
			"gates.qa.checks were taken from your build files; confirm they are the right pre-commit checks")
	} else {
		f.Notes = append(f.Notes,
			"NO check command was found — gates.qa.checks is empty, which means the gate currently verifies nothing. Fill it before flipping enforcement to 'enforce'")
	}
	f.Notes = append(f.Notes,
		"enforcement starts at 'report' (gates warn, don't block) — flip to 'enforce' when trusted")
	if len(f.Harness) > 0 {
		f.Notes = append(f.Notes,
			"this repo already has a process (see harness[]) — the spec must agree with it, not replace it")
	}
	if len(f.Stack) == 0 {
		f.Notes = append(f.Notes,
			"no language marker files found; the stack is unknown and every check will need to be written by hand")
	}
	return f, nil
}

// deriveUnit inspects recent branch names for a work-item pattern the repo already uses.
func deriveUnit(root string) (name, pattern string) {
	out, err := gitOut(root, "for-each-ref", "--sort=-committerdate", "--count=50",
		"--format=%(refname:short)", "refs/heads")
	if err != nil {
		return "TASK", ""
	}
	// Look for the most common <PREFIX>-<digits> shape.
	re := regexp.MustCompile(`([A-Z][A-Z0-9]{1,9})-(\d{1,6})`)
	counts := map[string]int{}
	width := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if m := re.FindStringSubmatch(strings.ToUpper(line)); m != nil {
			counts[m[1]]++
			if len(m[2]) > width[m[1]] {
				width[m[1]] = len(m[2])
			}
		}
	}
	best, bestN := "", 0
	for p, n := range counts {
		if n > bestN {
			best, bestN = p, n
		}
	}
	if best == "" {
		return "TASK", ""
	}
	// e.g. US seen with 3-digit numbers -> US-\d{3}; otherwise \d+ to stay permissive.
	num := `\d+`
	if width[best] > 0 {
		num = `\d{` + itoa(width[best]) + `}`
	}
	return best, best + "-" + num
}

func deriveDomains(root string) []DomainFact {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var doms []DomainFact
	for _, e := range entries {
		if !e.IsDir() || skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		rules := rulesFile(dir)
		// Code makes a directory a domain. So does an AGENTS.md, even with no code under it yet:
		// somebody drew that boundary on purpose, and a repo that has been planned but not built
		// is exactly the repo where the fan-out axes are already decided and nothing else can
		// reveal them. Requiring code here returned an empty domain list for such repos.
		if !hasCode(dir) && rules == "" {
			continue
		}
		d := DomainFact{Name: e.Name(), Path: e.Name() + "/"}
		if rules != "" {
			d.Rules = filepath.ToSlash(filepath.Join(e.Name(), rules))
		}
		d.Check = checksFor(root, dir)
		doms = append(doms, d)
	}
	sort.Slice(doms, func(i, j int) bool { return doms[i].Name < doms[j].Name })
	return doms
}

// checksFor pulls real build/test commands from the repo's own tooling, verbatim — never invented.
func checksFor(root, dir string) []string {
	// Language-specific defaults, keyed off marker files actually present.
	switch {
	case exists(filepath.Join(dir, "package.json")) || exists(filepath.Join(root, "package.json")):
		return pickScripts(filepath.Join(root, "package.json"))
	case exists(filepath.Join(dir, "go.mod")) || exists(filepath.Join(root, "go.mod")):
		return []string{"go build ./...", "go vet ./...", "go test ./..."}
	case exists(filepath.Join(dir, "pyproject.toml")) || exists(filepath.Join(root, "pyproject.toml")):
		return []string{"ruff check .", "pytest"}
	case globAny(dir, "*.tf"):
		return []string{"terraform fmt -check", "terraform validate"}
	}
	if targets := makeTargets(filepath.Join(root, "Makefile")); len(targets) > 0 {
		return targets
	}
	return nil
}

func pickScripts(pkgPath string) []string {
	b, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return nil
	}
	var out []string
	for _, name := range []string{"lint", "typecheck", "test"} {
		if _, ok := pkg.Scripts[name]; ok {
			// Prefer pnpm since batten's own example uses it; fall back gracefully.
			out = append(out, "pnpm "+name)
		}
	}
	return out
}

func makeTargets(makefile string) []string {
	b, err := os.ReadFile(makefile)
	if err != nil {
		return nil
	}
	var out []string
	re := regexp.MustCompile(`(?m)^(lint|test|check|build):`)
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		out = append(out, "make "+m[1])
	}
	return out
}

func rulesFile(dir string) string {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if exists(filepath.Join(dir, name)) {
			return name
		}
	}
	return ""
}

// deriveStack reports the languages and tooling in play, from marker files that are actually
// present. It never guesses from a directory name: `ml/` might be Python or it might be Rust, and
// a spec built on the wrong guess produces `check` commands that do not run.
func deriveStack(root string) []string {
	var out []string
	add := func(s string) {
		for _, e := range out {
			if e == s {
				return
			}
		}
		out = append(out, s)
	}

	walkTop(root, func(dir string) {
		switch {
		case exists(filepath.Join(dir, "go.mod")):
			add("go")
		case exists(filepath.Join(dir, "Cargo.toml")):
			add("rust")
		case exists(filepath.Join(dir, "pyproject.toml")), exists(filepath.Join(dir, "requirements.txt")),
			exists(filepath.Join(dir, "setup.py")):
			add("python")
		case exists(filepath.Join(dir, "pom.xml")), exists(filepath.Join(dir, "build.gradle")),
			exists(filepath.Join(dir, "build.gradle.kts")):
			add("jvm")
		case exists(filepath.Join(dir, "Gemfile")):
			add("ruby")
		}
		if exists(filepath.Join(dir, "package.json")) {
			add("node")
			if exists(filepath.Join(dir, "tsconfig.json")) {
				add("typescript")
			}
			for dep, label := range jsFrameworks {
				if hasDep(filepath.Join(dir, "package.json"), dep) {
					add(label)
				}
			}
		}
		if globAny(dir, "*.tf") {
			add("terraform")
		}
	})

	// Package manager: the lockfile is the only honest source. `pnpm test` in a repo with a
	// package-lock.json is a check that fails on the first run.
	for lock, pm := range map[string]string{
		"pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn", "package-lock.json": "npm",
		"bun.lockb": "bun", "poetry.lock": "poetry", "uv.lock": "uv",
	} {
		if exists(filepath.Join(root, lock)) {
			add(pm)
		}
	}
	if exists(filepath.Join(root, "Makefile")) {
		add("make")
	}
	if exists(filepath.Join(root, "Dockerfile")) || exists(filepath.Join(root, "compose.yaml")) ||
		exists(filepath.Join(root, "docker-compose.yml")) {
		add("docker")
	}
	sort.Strings(out)
	return out
}

// jsFrameworks maps a dependency name to the stack label it implies. Only frameworks that change
// what a check command looks like are worth reporting.
var jsFrameworks = map[string]string{
	"next": "next.js", "react": "react", "vue": "vue", "svelte": "svelte",
	"@angular/core": "angular", "express": "express", "@nestjs/core": "nestjs",
	"vitest": "vitest", "jest": "jest",
}

func hasDep(pkgPath, dep string) bool {
	b, err := os.ReadFile(pkgPath)
	if err != nil {
		return false
	}
	var pkg struct {
		Deps    map[string]string `json:"dependencies"`
		DevDeps map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return false
	}
	_, a := pkg.Deps[dep]
	_, d := pkg.DevDeps[dep]
	return a || d
}

// derivePurpose points at the files where the repo says what it is for. init reads them; the
// scanner only locates them, because "what is this project about" is a judgment, not a fact a
// heuristic can extract.
func derivePurpose(root string) []string {
	var out []string
	for _, p := range []string{
		"README.md", "README.rst", "README.txt",
		"docs/README.md", "docs/overview.md", "docs/architecture.md", "DESIGN.md",
	} {
		if exists(filepath.Join(root, filepath.FromSlash(p))) {
			out = append(out, p)
		}
	}
	// A planning/backlog doc is where the work items live, which is where the unit pattern is
	// really decided — branch names are only a proxy for it.
	for _, dir := range []string{"docs", "context", "planning"} {
		matches, _ := filepath.Glob(filepath.Join(root, dir, "*plan*"))
		more, _ := filepath.Glob(filepath.Join(root, dir, "*backlog*"))
		for _, m := range append(matches, more...) {
			if rel, err := filepath.Rel(root, m); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
	}
	sort.Strings(out)
	return out
}

// deriveHarness finds the process this repo already runs on. Every entry here is something a
// human already decided, and the spec should agree with it rather than replace it.
func deriveHarness(root string) []HarnessFact {
	var out []HarnessFact
	seen := map[string]bool{}
	add := func(path, kind, note string) {
		if seen[path] || !exists(filepath.Join(root, filepath.FromSlash(path))) {
			return
		}
		seen[path] = true
		out = append(out, HarnessFact{Path: path, Kind: kind, Note: note})
	}

	add("batten.yaml", "batten-spec", "a spec already exists — init will NOT overwrite it; reconcile by hand")
	add("AGENTS.md", "agent-rules", "repo-wide agent rules — the source for invariants no heuristic can derive")
	add("CLAUDE.md", "agent-rules", "repo-wide agent rules — same")
	add("CONTRIBUTING.md", "contributing", "the human process; the executable parts belong in the spec, the rest stays prose")
	add(".cursorrules", "editor-rules", "another agent harness is already in use — do not contradict it")
	add(".windsurfrules", "editor-rules", "another agent harness is already in use — do not contradict it")
	add(".github/copilot-instructions.md", "editor-rules", "another agent harness is already in use — do not contradict it")
	add("Makefile", "build", "the check commands come from here VERBATIM — never invent one")
	add("justfile", "build", "the check commands come from here VERBATIM — never invent one")
	add("Taskfile.yml", "build", "the check commands come from here VERBATIM — never invent one")

	// Per-directory agent rules are the strongest signal a repo has domains already: someone
	// drew those boundaries deliberately, and they are almost certainly the fan-out axes.
	walkTop(root, func(dir string) {
		if r := rulesFile(dir); r != "" {
			rel, err := filepath.Rel(root, filepath.Join(dir, r))
			if err != nil || rel == r {
				return // the root file, already added above
			}
			add(filepath.ToSlash(rel), "agent-rules",
				"per-directory rules — this boundary is probably a fan-out axis, and these are its invariants")
		}
	})

	// A workflow already written in prose is the best possible input for `--from`.
	for _, dir := range []string{".", "docs"} {
		for _, pat := range []string{"*workflow*", "*prompts*", "*process*"} {
			matches, _ := filepath.Glob(filepath.Join(root, dir, pat))
			for _, m := range matches {
				rel, err := filepath.Rel(root, m)
				if err != nil || !strings.HasSuffix(strings.ToLower(rel), ".md") {
					continue
				}
				add(filepath.ToSlash(rel), "workflow-doc",
					"a workflow already written in prose — pass it with `batten init --from <path>`")
			}
		}
	}
	return out
}

// walkTop calls fn for the repo root and each top-level directory worth looking at.
func walkTop(root string, fn func(dir string)) {
	fn(root)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && !skipDirs[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
			fn(filepath.Join(root, e.Name()))
		}
	}
}

// ---- YAML emission ----

// ToYAML renders the facts as a batten.yaml draft: valid, with TODO comments where judgment
// is required. The draft loads and passes doctor; it just needs a human to fill invariants.
func (f *Facts) ToYAML() string {
	var b strings.Builder
	w := func(s string) { b.WriteString(s); b.WriteByte('\n') }

	w("# batten.yaml — drafted by `batten init`. Review the TODOs before trusting it.")
	if len(f.Stack) > 0 {
		w("# Stack detected from marker files: " + strings.Join(f.Stack, ", "))
	}
	if len(f.Harness) > 0 {
		// Naming what already governs the repo, in the file itself, so nobody has to be told
		// twice that this spec is meant to agree with it rather than replace it.
		w("#")
		w("# This repo ALREADY has a process. The spec below should agree with it, not replace it:")
		for _, h := range f.Harness {
			w("#   " + h.Path + " (" + h.Kind + ") — " + h.Note)
		}
	}
	w("version: 1")
	w("project: " + f.Project)
	w("")
	w("# Gates WARN, they do not block, until you flip this to `enforce`.")
	w("enforcement: report")
	w("")
	w("unit:")
	w("  name: " + f.UnitName)
	w("  pattern: '" + f.UnitPattern + "'")
	w("")
	w("artifacts:")
	w("  handoff: docs/{id}.md   # TODO: where should per-unit handoffs live?")
	w("")
	w("phases:")
	w("  - id: build")
	w("    fanout: true")
	w("    anchor: git_sha")
	w("  - id: verify")
	w("    gate: qa")
	w("    diff_from: anchor")
	w("  - id: close")
	w("    requires_verdict: ok")
	w("")
	w("domains:")
	if len(f.Domains) == 0 {
		w("  # TODO: no code dirs auto-detected. Add one domain per fan-out axis.")
	}
	for _, d := range f.Domains {
		w("  " + d.Name + ":")
		w("    path: " + d.Path)
		if d.Rules != "" {
			w("    rules: " + d.Rules)
		}
		if len(d.Check) > 0 {
			w("    check: [" + quoteList(d.Check) + "]")
		} else {
			w("    check: []   # TODO: no build/test command auto-detected")
		}
		if len(d.Skills) > 0 {
			w("    skills: [" + strings.Join(dedup(d.Skills), ", ") + "]")
		}
		w("    invariants: []   # TODO: the rules a reviewer would catch (mine from " + orDash(d.Rules) + ")")
	}
	w("")
	w("gates:")
	w("  qa:")
	// Union of all domain checks as the gate's checks — a reasonable, editable default.
	w("    checks: [" + quoteList(allChecks(f.Domains)) + "]")
	w("    verdict: required")
	w("    evidence: required")
	w("")
	w("budget:")
	w("  tokens_per_run: 3000000")
	w("  on_exceed: warn   # report-mode default; tighten later")
	w("")
	w("capabilities:")
	if f.Graphify {
		w("  graph:")
		w("    provider: graphify")
		w("    query_before_read: true")
		w("    lessons: false   # engram owns episodic memory")
	}
	if f.Engram {
		w("  memory:")
		w("    provider: engram")
	}
	w("  obsidian:")
	w("    vault: ''   # TODO: set a vault path to have run notes appear in Obsidian")
	w("")
	if len(f.Notes) > 0 {
		w("# --- things to decide (surfaced honestly by init) ---")
		for _, n := range f.Notes {
			w("# - " + n)
		}
	}
	return b.String()
}

// ---- helpers ----

func allChecks(doms []DomainFact) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range doms {
		for _, c := range d.Check {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

func quoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = "'" + s + "'"
	}
	return strings.Join(q, ", ")
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "AGENTS.md if present"
	}
	return s
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func onPath(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func hasCode(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			switch filepath.Ext(p) {
			case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".rb",
				".vue", ".svelte", ".tf", ".sql", ".c", ".cpp", ".cs", ".kt":
				found = true
			}
		}
		return nil
	})
	return found
}

func globAny(dir, pattern string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(m) > 0
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func itoa(n int) string { b, _ := yaml.Marshal(n); return strings.TrimSpace(string(b)) }
