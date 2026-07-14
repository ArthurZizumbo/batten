package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/arthu/batten/internal/spec"
)

// fixture builds a machine: a project with its own .claude, a user config dir (pointed at by
// CLAUDE_CONFIG_DIR so no test ever reads the developer's real ~/.claude), and three plugins
// modelled on ones actually installed on this author's box. It returns the project dir.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	proj := filepath.Join(root, "repo")
	user := filepath.Join(root, "config")

	// --- project: one unique skill, one that collides with a user skill, and one whose
	// frontmatter name disagrees with its directory.
	writeSkill(t, proj, ".claude/skills/pytorch-review/SKILL.md", "pytorch-review",
		"Review pytorch training code for silent correctness bugs.")
	writeSkill(t, proj, ".claude/skills/shared/SKILL.md", "shared", "project copy")
	writeSkill(t, proj, ".claude/skills/dir-name/SKILL.md", "real-name", "named by frontmatter")

	write(t, filepath.Join(proj, ".claude/agents/coder.md"),
		"---\nname: coder\ndescription: writes code\ntools: Read, Edit\n---\n\nbody\n")
	write(t, filepath.Join(proj, ".claude/commands/ship.md"), "no frontmatter here\n")

	// --- user: the collision, plus one only the user has.
	writeSkill(t, user, "skills/shared/SKILL.md", "shared", "user copy")
	writeSkill(t, user, "skills/api-contract/SKILL.md", "api-contract",
		"Check http api handlers against the openapi contract.")
	write(t, filepath.Join(user, "agents/reviewer.md"), "---\nname: reviewer\ndescription: reviews\n---\n")

	// --- plugin 1 (gsap-skills): declares its skills dir as a string.
	gsap := filepath.Join(root, "plugins", "gsap")
	writeSkill(t, gsap, "skills/gsap-core/SKILL.md", "gsap-core", "core gsap tweens")
	write(t, filepath.Join(gsap, ".claude-plugin/plugin.json"),
		`{"name":"gsap-skills","skills":"./skills/"}`)

	// --- plugin 2 (uikit): declares ONE skill directory out of two on disk. Claude Code
	// exposes only the declared one; discovering the other would invent a capability.
	uikit := filepath.Join(root, "plugins", "uikit")
	writeSkill(t, uikit, ".claude/skills/uikit-main/SKILL.md", "uikit-main", "the declared one")
	writeSkill(t, uikit, ".claude/skills/uikit-hidden/SKILL.md", "uikit-hidden", "on disk, not declared")
	write(t, filepath.Join(uikit, ".claude-plugin/plugin.json"),
		`{"name":"uikit","skills":["./.claude/skills/uikit-main"]}`)

	// --- plugin 3 (cmem): declares no paths (default ./skills), and its SKILL.md carries a
	// frontmatter name that disagrees with its directory — exactly like claude-mem's
	// version-bump/, which Claude Code still addresses as claude-mem:version-bump.
	cmem := filepath.Join(root, "plugins", "cmem")
	writeSkill(t, cmem, "skills/version-bump/SKILL.md", "claude-code-plugin-release",
		"Semantic versioning and release workflow.")
	write(t, filepath.Join(cmem, ".claude-plugin/plugin.json"), `{"name":"cmem"}`)

	// --- plugin 4 (elsewhere): installed with project scope, for a DIFFERENT repo.
	other := filepath.Join(root, "plugins", "elsewhere")
	writeSkill(t, other, "skills/not-here/SKILL.md", "not-here", "scoped to another repo")
	write(t, filepath.Join(other, ".claude-plugin/plugin.json"), `{"name":"elsewhere"}`)

	write(t, filepath.Join(user, "plugins/installed_plugins.json"), `{
	  "version": 2,
	  "plugins": {
	    "gsap-skills@mkt": [{"scope":"user","installPath":`+jsonStr(gsap)+`}],
	    "uikit@mkt":       [{"scope":"user","installPath":`+jsonStr(uikit)+`}],
	    "cmem@mkt":        [{"scope":"user","installPath":`+jsonStr(cmem)+`}],
	    "elsewhere@mkt":   [{"scope":"project","projectPath":`+jsonStr(filepath.Join(root, "other-repo"))+
		`,"installPath":`+jsonStr(other)+`}]
	  }
	}`)

	t.Setenv("CLAUDE_CONFIG_DIR", user)
	return proj
}

func writeSkill(t *testing.T, root, rel, name, desc string) {
	t.Helper()
	write(t, filepath.Join(root, filepath.FromSlash(rel)),
		"---\nname: "+name+"\ndescription: "+desc+"\n---\n\n# body\n\nlots of prose.\n")
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsonStr renders a path as a JSON string literal. On Windows the separators are
// backslashes, which the real installed_plugins.json escapes and so must we.
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		t := "unmarshalable path"
		panic(t)
	}
	return string(b)
}

func byRef(items []Item) map[string]Item {
	m := map[string]Item{}
	for _, it := range items {
		m[it.Ref()] = it
	}
	return m
}

func TestSkillsSourcesAndPrecedence(t *testing.T) {
	proj := fixture(t)

	items, err := Skills(proj)
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(items)

	if it, ok := got["pytorch-review"]; !ok || it.Source != SourceProject {
		t.Fatalf("pytorch-review: want a project skill, got %+v (ok=%v)", it, ok)
	}
	if it, ok := got["api-contract"]; !ok || it.Source != SourceUser {
		t.Fatalf("api-contract: want a user skill, got %+v (ok=%v)", it, ok)
	}

	// The collision: project shadows user, and the user copy is gone entirely.
	it, ok := got["shared"]
	if !ok {
		t.Fatal("shared skill missing")
	}
	if it.Source != SourceProject || it.Description != "project copy" {
		t.Fatalf("precedence: project must shadow user, got %+v", it)
	}
	n := 0
	for _, x := range items {
		if x.Slug == "shared" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("shadowed skill still listed: %d copies of \"shared\"", n)
	}

	// A plugin skill is addressed plugin:skill, with the plugin's self-declared name.
	if it, ok := got["gsap-skills:gsap-core"]; !ok || it.Source != SourcePlugin || it.Plugin != "gsap-skills" {
		t.Fatalf("gsap-skills:gsap-core: want a plugin skill, got %+v (ok=%v)", it, ok)
	}

	// A plugin scoped to a different project must not appear here.
	if _, ok := got["elsewhere:not-here"]; ok {
		t.Fatal("a plugin scoped to another project leaked into this project's skills")
	}

	// Paths are slash-normalized even on Windows: they end up in YAML and JSON.
	for _, x := range items {
		if filepath.ToSlash(x.Path) != x.Path {
			t.Fatalf("path not slash-normalized: %q", x.Path)
		}
	}
}

// A plugin.json that declares one skill directory exposes ONE skill, however many SKILL.md
// files happen to sit next to it. Reporting the others would be inventing capability.
func TestPluginDeclaredSkillPathsOnly(t *testing.T) {
	proj := fixture(t)

	items, err := Skills(proj)
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(items)

	if it, ok := got["uikit:uikit-main"]; !ok || it.Description != "the declared one" {
		t.Fatalf("uikit:uikit-main: want the declared skill, got %+v (ok=%v)", it, ok)
	}
	if _, ok := got["uikit:uikit-hidden"]; ok {
		t.Fatal("undeclared SKILL.md reported as a skill: batten would propose a skill Claude Code cannot load")
	}
}

// The directory names a plugin skill, not its frontmatter: claude-mem ships
// skills/version-bump/SKILL.md declaring `name: claude-code-plugin-release`, and Claude Code
// loads it as claude-mem:version-bump. Ref must follow the loader, not the YAML.
func TestSlugIsAddressableNameFrontmatterIsLabel(t *testing.T) {
	proj := fixture(t)

	items, err := Skills(proj)
	if err != nil {
		t.Fatal(err)
	}
	got := byRef(items)

	it, ok := got["cmem:version-bump"]
	if !ok {
		t.Fatalf("cmem:version-bump not addressable; refs were %v", refs(items))
	}
	if it.Slug != "version-bump" {
		t.Fatalf("slug: got %q want %q", it.Slug, "version-bump")
	}
	if it.Name != "claude-code-plugin-release" {
		t.Fatalf("frontmatter name lost: got %q", it.Name)
	}
	if _, bad := got["cmem:claude-code-plugin-release"]; bad {
		t.Fatal("Ref built from frontmatter: init would write a name Claude Code cannot resolve")
	}

	// Same rule for a project skill whose frontmatter disagrees with its directory.
	p, ok := got["dir-name"]
	if !ok {
		t.Fatalf("dir-name not addressable; refs were %v", refs(items))
	}
	if p.Name != "real-name" {
		t.Fatalf("frontmatter name lost: got %q", p.Name)
	}
}

func refs(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Ref()
	}
	return out
}

func TestAgentsAndCommands(t *testing.T) {
	proj := fixture(t)

	agents, err := Agents(proj)
	if err != nil {
		t.Fatal(err)
	}
	a := byRef(agents)
	if it, ok := a["coder"]; !ok || it.Source != SourceProject || it.Description != "writes code" {
		t.Fatalf("coder agent: got %+v (ok=%v)", it, ok)
	}
	if it, ok := a["reviewer"]; !ok || it.Source != SourceUser {
		t.Fatalf("reviewer agent: got %+v (ok=%v)", it, ok)
	}

	cmds, err := Commands(proj)
	if err != nil {
		t.Fatal(err)
	}
	// No frontmatter: the file name is the fallback, and that is not an error.
	c, ok := byRef(cmds)["ship"]
	if !ok || c.Source != SourceProject || c.Name != "ship" || c.Description != "" {
		t.Fatalf("ship command: got %+v (ok=%v)", c, ok)
	}
}

func TestMissingLocationsAreNotErrors(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(empty, "nope"))

	for _, fn := range []func(string) ([]Item, error){Skills, Agents, Commands} {
		items, err := fn(filepath.Join(empty, "no-such-project"))
		if err != nil {
			t.Fatalf("a machine with nothing installed is not an error: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("want no items, got %d", len(items))
		}
	}
}

func TestValidate(t *testing.T) {
	proj := fixture(t)

	sp := &spec.Spec{
		Domains: map[string]spec.Domain{
			"ml": {
				Path:   "internal/ml",
				Skills: []string{"pytorch-review", "pytorch-reviw"}, // one real, one typo
				Agent:  "coder",
			},
			"api": {
				Path:  "internal/api",
				Agent: "revewer", // typo'd agent
			},
			"web": {
				Path: "web",
				// Qualified and bare must both resolve, as must the divergent frontmatter
				// spelling: a spec that is merely unidiomatic is not a spec that is broken.
				Skills: []string{"gsap-skills:gsap-core", "gsap-core",
					"cmem:version-bump", "cmem:claude-code-plugin-release"},
			},
			"ui": {
				Path:   "ui",
				Skills: []string{"uikit:uikit-hidden"}, // on disk, but not loadable
			},
		},
		Gates: map[string]spec.Gate{
			"qa": {Skills: []string{"api-contract", "zzzzqqqqxxxx"}}, // one real, one like nothing
		},
	}

	probs, err := Validate(sp, proj)
	if err != nil {
		t.Fatal(err)
	}

	want := []Problem{
		{Where: "domains.api.agent", Ref: "revewer", Hint: "reviewer"},
		{Where: "domains.ml.skills", Ref: "pytorch-reviw", Hint: "pytorch-review"},
		// uikit-main is a genuinely different skill, not a rename of uikit-hidden: pointing
		// the user there would be a misleading fix, so no hint is the honest answer.
		{Where: "domains.ui.skills", Ref: "uikit:uikit-hidden", Hint: ""},
		{Where: "gates.qa.skills", Ref: "zzzzqqqqxxxx", Hint: ""},
	}
	if len(probs) != len(want) {
		t.Fatalf("want %d problems, got %d: %+v", len(want), len(probs), probs)
	}
	// Sorted output, because doctor's warnings must be diffable between runs.
	for i := range want {
		if probs[i] != want[i] {
			t.Fatalf("problem %d:\n got %+v\nwant %+v", i, probs[i], want[i])
		}
	}
}

func TestValidateClaimsNothingReportsNothing(t *testing.T) {
	proj := fixture(t)

	sp := &spec.Spec{Domains: map[string]spec.Domain{"core": {Path: "internal/core"}}}
	probs, err := Validate(sp, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(probs) != 0 {
		t.Fatalf("a domain that claims no skill and no agent cannot be wrong, got %+v", probs)
	}
}

func TestHintDoesNotGuessWildly(t *testing.T) {
	ix := newIndex([]Item{
		{Name: "pytorch-review", Slug: "pytorch-review", Source: SourceProject},
		{Name: "api-contract", Slug: "api-contract", Source: SourceUser},
	})

	if got := ix.hint("pytorch-reviw"); got != "pytorch-review" {
		t.Fatalf("typo: got %q", got)
	}
	if got := ix.hint("contract"); got != "api-contract" { // a dropped word
		t.Fatalf("containment: got %q", got)
	}
	if got := ix.hint("terraform"); got != "" {
		t.Fatalf("nothing is close to \"terraform\"; a bad hint is worse than none, got %q", got)
	}
}

func TestSuggest(t *testing.T) {
	proj := fixture(t)
	skills, err := Skills(proj)
	if err != nil {
		t.Fatal(err)
	}

	domains := map[string]spec.Domain{
		"ml":    {Path: "internal/ml/pytorch"},
		"api":   {Path: "internal/api"},
		"web":   {Path: "web/gsap"},
		"infra": {Path: "deploy/terraform"},
	}
	got := Suggest(domains, skills)

	if want := []string{"pytorch-review"}; !eq(got["ml"], want) {
		t.Fatalf("ml: got %v want %v", got["ml"], want)
	}
	if want := []string{"api-contract"}; !eq(got["api"], want) {
		t.Fatalf("api: got %v want %v", got["api"], want)
	}
	// A plugin skill must be proposed by its qualified, loadable ref.
	if want := []string{"gsap-skills:gsap-core"}; !eq(got["web"], want) {
		t.Fatalf("web: got %v want %v", got["web"], want)
	}
	// Nothing installed is about terraform: propose nothing rather than something.
	if v, ok := got["infra"]; ok {
		t.Fatalf("infra: want no proposal, got %v", v)
	}
}

func eq(a, b []string) bool {
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
