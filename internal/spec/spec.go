// Package spec models batten.yaml: a project's procedural memory, declared as data.
//
// The engine knows nothing about any project. It reads Domain.Check and runs it;
// it does not know what "pytest" is. Everything project-specific lives here as data.
package spec

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const Filename = "batten.yaml"

type Spec struct {
	Version int    `yaml:"version"`
	Project string `yaml:"project"`
	// Enforcement is the adoption ramp. "enforce" (default) denies; "report" turns every gate
	// into a visible warning instead of a block, so a project mid-sprint can adopt batten without
	// the gates getting in the way on day one. Flip to enforce when the team trusts it.
	Enforcement  string              `yaml:"enforcement"`
	Unit         Unit                `yaml:"unit"`
	Artifacts    map[string]string   `yaml:"artifacts"`
	Phases       []Phase             `yaml:"phases"`
	Domains      map[string]Domain   `yaml:"domains"`
	Resources    map[string]Resource `yaml:"resources"`
	Gates        map[string]Gate     `yaml:"gates"`
	Budget       Budget              `yaml:"budget"`
	Capabilities Capabilities        `yaml:"capabilities"`

	// Root is the directory batten.yaml was loaded from. Not serialized.
	Root string `yaml:"-"`

	// models.{tiers,phases} and provenance.format were REMOVED from the declared surface
	// (plan §8, ítem 23). The schema promised "batten routes subagents and verifies it from
	// the ledger" and batten deliberately does not orchestrate, so the promise could never be
	// kept; provenance.format had no writer and no reader. Per-domain routing survives as
	// Domain.Model, which `batten show` DOES verify against the ledger.
}

// ReportOnly reports whether gates should warn instead of deny.
func (s *Spec) ReportOnly() bool { return s.Enforcement == "report" }

// Unit is the work-item noun: US, ticket, issue, story.
type Unit struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"` // regexp identifying a unit id, e.g. US-\d{3}
	Plan    string `yaml:"plan"`    // canonical plan doc holding the unit blocks
	Locator string `yaml:"locator"` // how to find a unit block in the plan, e.g. "### {id}"

	re *regexp.Regexp
}

// Phase is one step of the state machine. Names are the user's; mechanics are ours.
type Phase struct {
	ID              string   `yaml:"id"`
	Optional        bool     `yaml:"optional"`
	Interactive     bool     `yaml:"interactive"`
	Fanout          bool     `yaml:"fanout"`           // spawns one subagent per domain / sub-task
	Reads           []string `yaml:"reads"`            // artifacts of prior phases
	GraphQuery      bool     `yaml:"graph_query"`      // consult the code graph instead of grepping
	Anchor          string   `yaml:"anchor"`           // "git_sha": record base SHA, anchors the unit diff
	DiffFrom        string   `yaml:"diff_from"`        // "anchor": operate only on the unit's diff
	Gate            string   `yaml:"gate"`             // name of the gate this phase must emit
	RequiresVerdict string   `yaml:"requires_verdict"` // "ok": HARD gate, enforced by hook
	When            string   `yaml:"when"`             // free-form condition, advisory
}

// Domain is a fan-out axis. This is the only genuinely project-specific part.
type Domain struct {
	Path      string   `yaml:"path"`
	Exclude   []string `yaml:"exclude"`
	Rules     string   `yaml:"rules"` // path to AGENTS.md/CLAUDE.md governing this domain
	Check     []string `yaml:"check"` // commands that must pass before the domain agent finishes
	Coverage  int      `yaml:"coverage"`
	Skills    []string `yaml:"skills"`
	Agent     string   `yaml:"agent"`     // a custom subagent (.claude/agents/<name>.md) to run this domain
	Model     string   `yaml:"model"`     // pin this domain's agents to a model (wins over the plan's tier)
	Resources []string `yaml:"resources"` // shared resources this domain contends for

	// Invariants are the rules a reviewer would catch and a distracted agent would break.
	// They ride verbatim into every fanned-out agent's prompt.
	Invariants []string `yaml:"invariants"`
}

// Resource is something scarce that forces serialization across agents (a GPU, a staging DB).
type Resource struct {
	Kind     string   `yaml:"kind"`  // exclusive_pool | mutex
	Probe    string   `yaml:"probe"` // command reporting free capacity
	Unit     string   `yaml:"unit"`
	Priority []string `yaml:"priority"` // ordering when capacity is short
}

// Gate is a verdict checkpoint.
type Gate struct {
	Checks   []string `yaml:"checks"`
	Skills   []string `yaml:"skills"`
	Verdict  string   `yaml:"verdict"`  // "required"
	Evidence string   `yaml:"evidence"` // "required" -> empty evidence[] is blocked. Non-negotiable.
}

func (g Gate) EvidenceRequired() bool { return g.Evidence == "required" }

// Budget is the tripwire an unattended overnight run otherwise lacks.
//
// On a subscription the marginal dollar cost of a token is zero, so "dollars spent" is
// the wrong ceiling. Three honest ceilings replace it:
//
//   - TokensPerRun    — unambiguous, and the only thing we can count exactly.
//   - ImputedUSDPerRun — what those tokens WOULD have cost on the API. Not a bill:
//     a measure of the value being pulled out of the subscription.
//   - QuotaPctPerRun  — share of the rolling 5-hour window this run may burn. Anthropic
//     publishes no absolute quota numbers, so a percentage is the ONLY
//     trustworthy quota metric. Requires `batten statusline` to be installed;
//     without it this ceiling is simply not enforced (and says so).
type Budget struct {
	TokensPerRun     int64   `yaml:"tokens_per_run"`
	ImputedUSDPerRun float64 `yaml:"imputed_usd_per_run"`
	QuotaPctPerRun   float64 `yaml:"quota_pct_per_run"`
	MaxIterations    int     `yaml:"max_iterations"`
	OnExceed         string  `yaml:"on_exceed"` // block | warn | downgrade_effort
}

// Set reports whether any ceiling at all was declared.
func (b Budget) Set() bool {
	return b.TokensPerRun > 0 || b.ImputedUSDPerRun > 0 || b.QuotaPctPerRun > 0
}

type Capabilities struct {
	Graph       GraphCap       `yaml:"graph"`
	Memory      MemoryCap      `yaml:"memory"`
	Obsidian    ObsidianCap    `yaml:"obsidian"`
	Compression CompressionCap `yaml:"compression"`
}

// GraphCap: structural memory. Optional; degrades to grep when absent.
type GraphCap struct {
	Provider        string `yaml:"provider"` // graphify | none
	QueryBeforeRead bool   `yaml:"query_before_read"`
	Lessons         bool   `yaml:"lessons"` // graphify's outcome layer; off — engram owns episodic memory
}

// MemoryCap: episodic memory. We never build this; we interoperate.
type MemoryCap struct {
	Provider string `yaml:"provider"` // engram | claude-mem | none
}

type ObsidianCap struct {
	Vault  string   `yaml:"vault"`
	Export []string `yaml:"export"` // runs | verdicts | canvas
}

// CompressionCap: headroom. Admitted, but measured — not taken on faith.
type CompressionCap struct {
	Provider string `yaml:"provider"` // headroom | none
	Memory   bool   `yaml:"memory"`   // keep false: engram owns episodic memory
	Measure  bool   `yaml:"measure"`  // prove it saves tokens in OUR fan-out
}

func (c Capabilities) GraphEnabled() bool {
	return c.Graph.Provider != "" && c.Graph.Provider != "none"
}
func (c Capabilities) CompressionEnabled() bool {
	return c.Compression.Provider != "" && c.Compression.Provider != "none"
}

// Find walks up from dir looking for batten.yaml.
func Find(dir string) (string, error) {
	for {
		p := filepath.Join(dir, Filename)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s found in %s or any parent", Filename, dir)
		}
		dir = parent
	}
}

func Load(path string) (*Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Root = filepath.Dir(path)
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// UnknownKeys lists the keys in a spec file that this batten does not read.
//
// It is deliberately NOT an error. A spec written for a newer batten must still load on an older
// one, and refusing the whole file over one key would turn a cosmetic mismatch into a machine
// where nothing is gated. But it cannot be silence either: a key that is declared and never read
// is the exact failure this tool exists to eliminate, and it happened here. `provenance.format`
// and `models.*` were removed from the struct and the published schema in one commit and left in
// this repo's own batten.yaml and two of its examples — so an editor validating against
// batten.schema.json called the file invalid while `batten doctor` called it perfect, and CI's
// schema job went red without anyone reading it.
//
// Returned sorted, because doctor's output has to be diffable between runs.
func UnknownKeys(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Spec
	if err := dec.Decode(&s); err == nil {
		return nil
	} else {
		// yaml.v3 reports every unknown field as one line of a TypeError:
		//   line 47: field provenance not found in type spec.Spec
		var out []string
		seen := map[string]bool{}
		for _, line := range strings.Split(err.Error(), "\n") {
			const marker = "field "
			i := strings.Index(line, marker)
			if i < 0 {
				continue
			}
			rest := line[i+len(marker):]
			name, _, ok := strings.Cut(rest, " not found in type")
			if !ok || name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
}

// LoadFrom finds and loads the spec governing dir.
func LoadFrom(dir string) (*Spec, error) {
	p, err := Find(dir)
	if err != nil {
		return nil, err
	}
	return Load(p)
}

func (s *Spec) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if s.Version != 1 {
		add("version must be 1, got %d", s.Version)
	}
	if s.Project == "" {
		add("project is required")
	}
	switch s.Enforcement {
	case "", "enforce", "report":
	default:
		add("enforcement must be \"enforce\" or \"report\", got %q", s.Enforcement)
	}
	if s.Unit.Name == "" {
		add("unit.name is required (the work-item noun: US, ticket, issue...)")
	}
	if s.Unit.Pattern == "" {
		add("unit.pattern is required (regexp matching a unit id)")
	} else {
		re, err := regexp.Compile(s.Unit.Pattern)
		if err != nil {
			add("unit.pattern is not a valid regexp: %v", err)
		} else {
			s.Unit.re = re
		}
	}
	if len(s.Phases) == 0 {
		add("at least one phase is required")
	}

	seen := map[string]bool{}
	for i, p := range s.Phases {
		if p.ID == "" {
			add("phases[%d].id is required", i)
			continue
		}
		if seen[p.ID] {
			add("duplicate phase id %q", p.ID)
		}
		seen[p.ID] = true

		if p.Gate != "" {
			if _, ok := s.Gates[p.Gate]; !ok {
				add("phase %q references unknown gate %q", p.ID, p.Gate)
			}
		}
		for _, r := range p.Reads {
			if _, ok := s.Artifacts[r]; !ok && !seen[r] {
				add("phase %q reads %q, which is neither an artifact nor a prior phase", p.ID, r)
			}
		}
		if p.RequiresVerdict != "" && p.RequiresVerdict != "ok" && p.RequiresVerdict != "warn" {
			add("phase %q: requires_verdict must be \"ok\" or \"warn\", got %q", p.ID, p.RequiresVerdict)
		}
	}

	for name, d := range s.Domains {
		if d.Path == "" {
			add("domain %q: path is required", name)
		}
		for _, r := range d.Resources {
			if _, ok := s.Resources[r]; !ok {
				add("domain %q contends for unknown resource %q", name, r)
			}
		}
	}

	// The rule that gives the gate its teeth. Refuse to load a spec that quietly
	// permits an approval with no evidence — that is the failure this tool exists to prevent.
	for name, g := range s.Gates {
		if g.Verdict == "required" && g.Evidence != "required" {
			add("gate %q requires a verdict but not evidence; that permits result=ok with an empty "+
				"evidence[], which is the exact failure batten exists to prevent. Set evidence: required", name)
		}
	}

	for _, a := range s.Artifacts {
		if !strings.Contains(a, "{id}") {
			add("artifact path %q has no {id} placeholder; every unit would overwrite the same file", a)
		}
	}

	if s.Budget.OnExceed != "" {
		switch s.Budget.OnExceed {
		case "block", "warn", "downgrade_effort":
		default:
			add("budget.on_exceed must be block|warn|downgrade_effort, got %q", s.Budget.OnExceed)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid spec:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// MatchUnit extracts a unit id (e.g. "US-034") from arbitrary text, or "".
func (s *Spec) MatchUnit(text string) string {
	if s.Unit.re == nil {
		re, err := regexp.Compile(s.Unit.Pattern)
		if err != nil {
			return ""
		}
		s.Unit.re = re
	}
	return s.Unit.re.FindString(text)
}

// ValidUnitID reports whether id IS a unit id — the whole string, not a substring. MatchUnit
// is for arbitrary text (a commit message, a branch name) and is the wrong tool for argv:
// with pattern `US-\d{3}`, FindString accepts US-0001 by matching its US-000 prefix, so a
// typo quietly becomes a different unit. Anchored with ^$, or it is not an identifier (#21).
func (s *Spec) ValidUnitID(id string) bool {
	re, err := regexp.Compile("^(?:" + s.Unit.Pattern + ")$")
	if err != nil {
		return false
	}
	return re.MatchString(id)
}

// Artifact resolves an artifact path for a unit, relative to the spec root.
func (s *Spec) Artifact(kind, unitID string) (string, bool) {
	tmpl, ok := s.Artifacts[kind]
	if !ok {
		return "", false
	}
	return filepath.Join(s.Root, strings.ReplaceAll(tmpl, "{id}", unitID)), true
}

// Phase looks up a phase by id.
func (s *Spec) Phase(id string) (Phase, bool) {
	for _, p := range s.Phases {
		if p.ID == id {
			return p, true
		}
	}
	return Phase{}, false
}

// ClosingPhase returns the phase that requires a verdict to proceed, if any.
// This is what the verdict gate enforces.
func (s *Spec) ClosingPhase() (Phase, bool) {
	for _, p := range s.Phases {
		if p.RequiresVerdict != "" {
			return p, true
		}
	}
	return Phase{}, false
}

// ClosingGateName is the gate the commit gate enforces: the closing phase's own gate, falling
// back to the last gate any phase declares. Empty when nothing declares one. The hook, the
// CLI and the statusline all need this same resolution, and each having its own copy is how
// two of them come to disagree about which gate an override opened.
func (s *Spec) ClosingGateName() string {
	if c, ok := s.ClosingPhase(); ok && c.Gate != "" {
		return c.Gate
	}
	g := ""
	for _, p := range s.Phases {
		if p.Gate != "" {
			g = p.Gate
		}
	}
	return g
}

// DomainFor returns the domain owning a repo-relative file path.
func (s *Spec) DomainFor(rel string) (string, bool) {
	rel = filepath.ToSlash(rel)
	best, bestLen := "", -1
	for name, d := range s.Domains {
		p := strings.TrimSuffix(filepath.ToSlash(d.Path), "/")
		if p == "" || !strings.HasPrefix(rel, p+"/") {
			continue
		}
		excluded := false
		for _, e := range d.Exclude {
			if strings.HasPrefix(rel, strings.TrimSuffix(filepath.ToSlash(e), "/")+"/") {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if len(p) > bestLen { // most specific path wins
			best, bestLen = name, len(p)
		}
	}
	return best, best != ""
}
