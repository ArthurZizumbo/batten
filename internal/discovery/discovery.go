// Package discovery finds the skills, subagents and commands a machine actually has,
// and checks the ones batten.yaml claims against them.
//
// batten.yaml names skills and agents by string: `domains.ml.skills: [pytorch-review]`.
// Those strings rot. A skill gets renamed, a plugin is uninstalled, a teammate clones the
// repo and never had the agent at all. Claude Code does not complain — the phase simply
// runs without the skill it was promised, and the run looks exactly like a good one.
// Discovery exists so that rot surfaces in `batten doctor` at 9am, not inside an
// unattended fan-out at 3am.
//
// It also runs the other direction: `batten init` proposes a domain→skill mapping from
// what is actually installed, instead of inventing names the user does not have.
//
// # Two things the docs say and the disk does not
//
// Both were confirmed against a real ~/.claude, and both are load-bearing:
//
//  1. A plugin's skills live where its .claude-plugin/plugin.json says they live. The
//     "skills" key is a string OR an array, and an array entry may name a single skill
//     directory rather than a container. ui-ux-pro-max ships seven SKILL.md files under
//     .claude/skills/ and declares exactly one of them; Claude Code exposes only that one.
//     A naive walk would report six skills the user cannot invoke — inventing capability
//     is the same sin as inventing a number.
//
//  2. The DIRECTORY, not the frontmatter `name`, is what a plugin skill is addressed by.
//     claude-mem's skills/version-bump/SKILL.md declares `name: claude-code-plugin-release`,
//     and Claude Code still loads it as `claude-mem:version-bump`. So Item.Name keeps the
//     frontmatter (it is the human-readable label) but Item.Ref — the only string that is
//     safe to write into batten.yaml — is built from the directory. Validate accepts either
//     spelling, because a doctor that cries wolf gets muted.
package discovery

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ArthurZizumbo/batten/internal/spec"
)

type Source string

const (
	SourceProject Source = "project"
	SourceUser    Source = "user"
	SourcePlugin  Source = "plugin"
)

// Item is one discovered skill, subagent or command.
type Item struct {
	Name        string // from frontmatter when present, else the file/dir name
	Slug        string // the file/dir name — what Claude Code actually addresses (see package doc)
	Description string
	Path        string
	Source      Source
	Plugin      string // set when Source == "plugin"
}

// Ref is how batten.yaml must spell this item: a plugin's skill is addressed
// `plugin:skill`, everything else by bare name. This is the only spelling `batten init`
// may write into a spec — Name can disagree with it, and when it does, Name is the one
// Claude Code ignores.
func (i Item) Ref() string {
	if i.Source == SourcePlugin && i.Plugin != "" {
		return i.Plugin + ":" + i.Slug
	}
	return i.Slug
}

// Problem is a spec reference that points at something that does not exist.
type Problem struct {
	Where string // e.g. `domains.ml.skills` or `gates.qa.skills`
	Ref   string // the name the spec asked for
	Hint  string // nearest existing name, when there is a plausible one
}

const (
	// A SKILL.md is a document, not a config file: the body runs to thousands of lines.
	// Discovery runs in `doctor` and `init` with the user watching a prompt, so we read the
	// leading YAML block and stop. These bounds are what "stop" means.
	maxFrontmatterLines = 200
	maxLineBytes        = 256 << 10

	// Skills sit one directory deep; commands may nest a level or two for namespacing. The
	// cap is here so a vendored tree or a symlink under .claude cannot turn discovery into
	// a full-disk crawl.
	maxDepth = 4

	// Suggest is a proposal the user edits, not an authority. A short list invites editing;
	// a long one invites accepting it wholesale, which is how a wrong mapping ships.
	maxSuggestions  = 3
	minSuggestScore = 3
)

// Skills returns every skill visible to projectDir, project first.
func Skills(projectDir string) ([]Item, error) { return listed(projectDir, kindSkills) }

// Agents returns every subagent visible to projectDir, project first.
func Agents(projectDir string) ([]Item, error) { return listed(projectDir, kindAgents) }

// Commands returns every slash command visible to projectDir, project first.
func Commands(projectDir string) ([]Item, error) { return listed(projectDir, kindCommands) }

// Validate reports every skill/agent the spec references that cannot be found.
// This is what turns "my skill got renamed" from a 3am mystery into a doctor warning.
func Validate(sp *spec.Spec, projectDir string) ([]Problem, error) {
	if sp == nil {
		return nil, nil
	}
	rawSkills, err := scan(projectDir, kindSkills)
	if err != nil {
		return nil, err
	}
	rawAgents, err := scan(projectDir, kindAgents)
	if err != nil {
		return nil, err
	}
	skills, agents := newIndex(rawSkills), newIndex(rawAgents)

	var out []Problem
	check := func(ix *index, where, ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" || ix.has(ref) {
			return
		}
		out = append(out, Problem{Where: where, Ref: ref, Hint: ix.hint(ref)})
	}

	// Sorted, because doctor's output must be diffable between runs. A warning list that
	// reshuffles itself every invocation is a warning list nobody reads twice.
	for _, name := range sortedKeys(sp.Domains) {
		d := sp.Domains[name]
		for _, s := range d.Skills {
			check(skills, "domains."+name+".skills", s)
		}
		check(agents, "domains."+name+".agent", d.Agent)
	}
	for _, name := range sortedKeys(sp.Gates) {
		for _, s := range sp.Gates[name].Skills {
			check(skills, "gates."+name+".skills", s)
		}
	}
	return out, nil
}

// Suggest maps discovered skills onto the spec's domains by matching the skill's name and
// description against the domain name and path. Used by `batten init` to PROPOSE a mapping
// that the user then edits — it is a starting point, never an authority.
//
// The scoring is deliberately blunt (token overlap; a hit in the name outweighs a hit in
// the description) and the threshold deliberately high: a domain with no confident match
// gets no key at all. An empty mapping the user must fill in beats a plausible-looking
// wrong one they never re-read.
func Suggest(domains map[string]spec.Domain, skills []Item) map[string][]string {
	out := map[string][]string{}
	for name, d := range domains {
		want := tokens(name + " " + filepath.ToSlash(d.Path))
		if len(want) == 0 {
			continue
		}
		type cand struct {
			ref   string
			score int
		}
		var cs []cand
		for _, s := range skills {
			if sc := score(want, s); sc >= minSuggestScore {
				cs = append(cs, cand{s.Ref(), sc}) // Ref, never Name: see the package doc
			}
		}
		if len(cs) == 0 {
			continue
		}
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].score != cs[j].score {
				return cs[i].score > cs[j].score
			}
			return cs[i].ref < cs[j].ref
		})
		if len(cs) > maxSuggestions {
			cs = cs[:maxSuggestions]
		}
		refs := make([]string, len(cs))
		for i, c := range cs {
			refs[i] = c.ref
		}
		out[name] = refs
	}
	return out
}

// kind is one discoverable thing. file is the required file name, or "" for any *.md.
type kind struct {
	dir  string
	file string
}

var (
	kindSkills   = kind{dir: "skills", file: "SKILL.md"}
	kindAgents   = kind{dir: "agents"}
	kindCommands = kind{dir: "commands"}
)

func listed(projectDir string, k kind) ([]Item, error) {
	raw, err := scan(projectDir, k)
	if err != nil {
		return nil, err
	}
	return shadow(raw), nil
}

// scan collects one kind from all three sources, in precedence order.
func scan(projectDir string, k kind) ([]Item, error) {
	var out []Item
	add := func(root string, src Source, plugin string) error {
		items, err := collect(root, k, src, plugin)
		if err != nil {
			return err
		}
		out = append(out, items...)
		return nil
	}

	if projectDir != "" {
		if err := add(filepath.Join(projectDir, ".claude", k.dir), SourceProject, ""); err != nil {
			return nil, err
		}
	}
	cfg := configDir()
	if cfg != "" {
		if err := add(filepath.Join(cfg, k.dir), SourceUser, ""); err != nil {
			return nil, err
		}
	}
	plugs, err := plugins(projectDir, cfg)
	if err != nil {
		return nil, err
	}
	for _, p := range plugs {
		for _, root := range p.roots(k) {
			if err := add(root, SourcePlugin, p.name); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// collect walks one location. A location that does not exist is not an error — most
// machines have no ~/.claude/skills, and that is a fact about the machine, not a failure.
//
// root may itself BE the item (a plugin that declares `skills: ["./x/my-skill"]`, or an
// agent declared as a bare .md path), so the file case is handled before the walk.
func collect(root string, k kind, src Source, plugin string) ([]Item, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, nil
	}
	if !st.IsDir() {
		if k.file != "" || !strings.EqualFold(filepath.Ext(root), ".md") {
			return nil, nil
		}
		// Strip the .md extension so a bare-file declaration (`"agents": "./x.md"`)
		// yields the same addressable slug the directory walk would ("x", not "x.md").
		b := base(root)
		return []Item{newItem(root, strings.TrimSuffix(b, filepath.Ext(b)), src, plugin)}, nil
	}
	// A declared skill directory holds its SKILL.md directly rather than one per child.
	if k.file != "" {
		if p := filepath.Join(root, k.file); fileExists(p) {
			return []Item{newItem(p, base(root), src, plugin)}, nil
		}
	}

	var items []Item
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable subtree costs us that subtree, not the whole scan
		}
		rel := filepath.ToSlash(relTo(root, p))
		if d.IsDir() {
			if p == root {
				return nil
			}
			if n := d.Name(); n == "node_modules" || strings.HasPrefix(n, ".") {
				return fs.SkipDir
			}
			if strings.Count(rel, "/")+1 >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		if k.file != "" && !strings.EqualFold(d.Name(), k.file) {
			return nil
		}
		slug := slugFor(rel, k)
		if slug == "" {
			return nil
		}
		items = append(items, newItem(p, slug, src, plugin))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Slug < items[j].Slug })
	return items, nil
}

func newItem(p, slug string, src Source, plugin string) Item {
	name, desc := frontmatter(p)
	if name == "" {
		name = slug
	}
	return Item{
		Name:        name,
		Slug:        slug,
		Description: desc,
		Path:        filepath.ToSlash(p), // slash-normalized: these paths end up in YAML and JSON
		Source:      src,
		Plugin:      plugin,
	}
}

// slugFor derives the addressable name from a path relative to the scan root: the parent
// directory for a skill, the file name for an agent or command (nesting namespaces a
// command, foo/bar.md -> foo:bar).
func slugFor(rel string, k kind) string {
	if k.file != "" {
		dir := path.Dir(rel)
		if dir == "." {
			return "" // handled by the caller, which knows the root's own name
		}
		return path.Base(dir)
	}
	return strings.ReplaceAll(strings.TrimSuffix(rel, path.Ext(rel)), "/", ":")
}

// frontmatter reads only the leading YAML block, never the body.
//
// Errors are swallowed on purpose: a SKILL.md with broken YAML is still a skill Claude Code
// will load by directory name, and failing all of `batten doctor` over one malformed file
// is a worse outcome than falling back to that name.
func frontmatter(p string) (name, desc string) {
	f, err := os.Open(p)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxLineBytes)

	var buf strings.Builder
	opened := false
	for lines := 0; lines < maxFrontmatterLines && sc.Scan(); lines++ {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if !opened {
			// Editors on Windows leave a BOM (U+FEFF) on line 1; it would hide the opening ---.
			line = strings.TrimLeftFunc(line, func(r rune) bool { return r == 0xFEFF })
			if strings.TrimSpace(line) == "" {
				continue
			}
			if strings.TrimSpace(line) != "---" {
				return "", "" // no frontmatter at all
			}
			opened = true
			continue
		}
		if t := strings.TrimSpace(line); t == "---" || t == "..." {
			var fm struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if yaml.Unmarshal([]byte(buf.String()), &fm) != nil {
				return "", ""
			}
			return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description)
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	return "", "" // unterminated block: treat as absent rather than guess
}

// configDir resolves ~/.claude, honoring CLAUDE_CONFIG_DIR (which Claude Code itself
// honors, and which is the only way to test discovery without reading the real ~/.claude).
func configDir() string {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

type installedPlugin struct {
	name string
	dir  string
	// declared holds the plugin.json paths for each kind. Empty means "use the default".
	declared map[string][]string
}

// roots returns the directories (or files) to scan for one kind. A plugin that declares
// nothing gets the conventional ./<kind>; a plugin that declares something gets exactly
// what it declared and nothing else — see the package doc for why that matters.
func (p installedPlugin) roots(k kind) []string {
	rels := p.declared[k.dir]
	if len(rels) == 0 {
		return []string{filepath.Join(p.dir, k.dir)}
	}
	out := make([]string, 0, len(rels))
	for _, r := range rels {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if filepath.IsAbs(r) {
			out = append(out, filepath.Clean(r))
			continue
		}
		out = append(out, filepath.Join(p.dir, filepath.FromSlash(r)))
	}
	return out
}

// plugins reads the installed-plugin manifest. A plugin installed with project scope
// belongs to THAT project: counting it here would tell the user a skill exists when, in
// this repo, it does not.
func plugins(projectDir, cfg string) ([]installedPlugin, error) {
	if cfg == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(cfg, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil, nil // no plugins installed is not a failure
	}
	var doc struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			ProjectPath string `json:"projectPath"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil, nil // a manifest we cannot parse is a manifest we report nothing from
	}

	var out []installedPlugin
	for key, installs := range doc.Plugins {
		fallback := key
		if i := strings.Index(fallback, "@"); i > 0 { // the key is "<plugin>@<marketplace>"
			fallback = fallback[:i]
		}
		for _, in := range installs {
			if in.InstallPath == "" {
				continue
			}
			if in.Scope == "project" && !samePath(in.ProjectPath, projectDir) {
				continue
			}
			if st, err := os.Stat(in.InstallPath); err != nil || !st.IsDir() {
				continue // a manifest entry outliving its files
			}
			name, declared := manifest(in.InstallPath, fallback)
			out = append(out, installedPlugin{name: name, dir: in.InstallPath, declared: declared})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].dir < out[j].dir
	})
	return out, nil
}

// manifest reads .claude-plugin/plugin.json. The name comes from the plugin itself when it
// declares one: the marketplace key is a distribution detail, but `plugin:skill` is spelled
// with the name the plugin gives itself.
func manifest(dir, fallback string) (name string, declared map[string][]string) {
	declared = map[string][]string{}
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return fallback, declared
	}
	var m struct {
		Name     string          `json:"name"`
		Skills   json.RawMessage `json:"skills"`
		Agents   json.RawMessage `json:"agents"`
		Commands json.RawMessage `json:"commands"`
	}
	if json.Unmarshal(b, &m) != nil {
		return fallback, declared
	}
	name = strings.TrimSpace(m.Name)
	if name == "" {
		name = fallback
	}
	for k, raw := range map[string]json.RawMessage{
		"skills": m.Skills, "agents": m.Agents, "commands": m.Commands,
	} {
		if v := paths(raw); len(v) > 0 {
			declared[k] = v
		}
	}
	return name, declared
}

// paths accepts both shapes plugin.json uses in the wild: "./skills/" and ["./a", "./b"].
func paths(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if strings.TrimSpace(one) == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	// EqualFold because Windows paths are case-insensitive and the manifest records whatever
	// casing the shell happened to use that day.
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func base(p string) string {
	return filepath.Base(filepath.Clean(p))
}

func relTo(root, target string) string {
	r, err := filepath.Rel(root, target)
	if err != nil {
		return filepath.Base(target)
	}
	return r
}

// shadow applies precedence: project shadows user shadows plugin for the same name.
// A plugin item keeps its qualified ref even when its bare name is shadowed, so two plugins
// that both ship a "core" skill do not erase each other.
func shadow(raw []Item) []Item {
	seen := map[string]bool{}
	out := make([]Item, 0, len(raw))
	for _, it := range raw { // raw arrives project, then user, then plugin
		if seen[it.Ref()] || (it.Source == SourcePlugin && seen[it.Slug]) {
			continue
		}
		seen[it.Ref()] = true
		out = append(out, it)
	}
	return out
}

// index resolves a spec reference to a discovered item, and hints when it cannot.
type index struct {
	refs  map[string]Item
	names []string // sorted; the candidate set for hints
}

// newIndex accepts every spelling a spec author could reasonably have written: the
// addressable ref, the plugin-qualified frontmatter name, and both bare forms. Being
// lenient here is not sloppiness — a false "missing skill" in doctor costs more trust than
// it buys, and the real missing-skill case (nothing matches under any spelling) still fires.
func newIndex(raw []Item) *index {
	ix := &index{refs: map[string]Item{}}
	put := func(k string, it Item) {
		if k == "" {
			return
		}
		if _, ok := ix.refs[k]; !ok {
			ix.refs[k] = it
		}
	}
	for _, it := range raw { // qualified refs first: plugin:skill must always resolve
		put(it.Ref(), it)
	}
	for _, it := range raw {
		if it.Source == SourcePlugin && it.Plugin != "" && it.Name != it.Slug {
			put(it.Plugin+":"+it.Name, it)
		}
	}
	for _, it := range raw { // then bare forms, in precedence order
		put(it.Slug, it)
	}
	for _, it := range raw {
		put(it.Name, it)
	}
	for k := range ix.refs {
		ix.names = append(ix.names, k)
	}
	sort.Strings(ix.names)
	return ix
}

func (ix *index) has(ref string) bool {
	_, ok := ix.refs[ref]
	return ok
}

// hint returns the nearest existing name, or "" when nothing is close.
//
// A wrong hint is worse than none: it sends the reader off to rename a thing that was never
// the problem. Hence the edit-distance ceiling scaled to the name's length, and a
// containment fallback only for names long enough that a shared substring means something.
func (ix *index) hint(ref string) string {
	want := bare(strings.ToLower(strings.TrimSpace(ref)))
	if want == "" || len(ix.names) == 0 {
		return ""
	}

	limit := 1
	switch {
	case len(want) > 8:
		limit = 3
	case len(want) > 4:
		limit = 2
	}

	best, bestD := "", limit+1
	for _, c := range ix.names { // sorted, so ties resolve deterministically
		d := distance(want, bare(strings.ToLower(c)))
		if d == 0 { // the right skill, wrongly qualified or wrongly cased
			return c
		}
		if d < bestD {
			best, bestD = c, d
		}
	}
	if best != "" {
		return best
	}

	// A rename that added or dropped a word ("review" -> "code-review") is far in edit
	// distance and obvious to a human. Only trust it for names with some substance.
	if len(want) >= 4 {
		for _, c := range ix.names {
			cb := bare(strings.ToLower(c))
			if strings.Contains(cb, want) || (len(cb) >= 4 && strings.Contains(want, cb)) {
				return c
			}
		}
	}
	return ""
}

// bare drops a plugin qualifier: "gsap-skills:gsap-core" -> "gsap-core".
func bare(s string) string {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// distance is Levenshtein over two short identifiers. Two rows, because that is all a name
// comparison needs and this runs once per unresolved reference.
func distance(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// stop holds the structural noise in a domain path. "internal/ml/train" is about ml and
// training; "internal" describes Go's directory conventions, not the domain.
var stop = map[string]bool{
	"src": true, "lib": true, "libs": true, "pkg": true, "pkgs": true, "packages": true,
	"internal": true, "cmd": true, "app": true, "apps": true, "module": true, "modules": true,
	"the": true, "and": true, "for": true, "with": true, "dir": true,
}

// tokens lowercases and splits on anything that is not a letter or digit, which covers
// paths, kebab-case skill names and prose descriptions under one rule.
func tokens(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	var out []string
	for _, p := range parts {
		if len(p) < 3 || stop[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// score weighs a hit in the name far above a hit in the description: a skill called
// "pytorch-review" is about pytorch, while one that merely mentions pytorch in passing
// is not.
func score(want []string, s Item) int {
	name := set(append(tokens(s.Name), tokens(s.Slug)...))
	desc := set(tokens(s.Description))
	total := 0
	for _, w := range want {
		switch {
		case name[w]:
			total += 3
		case desc[w]:
			total++
		}
	}
	return total
}

func set(ts []string) map[string]bool {
	m := make(map[string]bool, len(ts))
	for _, t := range ts {
		m[t] = true
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
