// Package vault writes batten's human-facing surface as plain files in an Obsidian vault.
//
// Why files and not the first-party Obsidian CLI: that CLI requires the desktop app to be
// RUNNING. A hook fires in a headless subprocess and CI has no desktop at all — the two
// places batten actually lives are exactly the two the CLI cannot serve. A .md, a .base and
// a .canvas are just files. Obsidian renders whatever it finds. That is the whole trick, and
// it means there is no integration to keep alive.
//
// Why it is worth doing at all: a coding agent has three memories — structural (what the code
// IS), episodic (what we DECIDED) and procedural (HOW WE WORK). Pairing a code graph with
// Obsidian gets you the first one and calls it memory. batten owns the third, and by writing
// into the SAME vault the three become one navigable graph: a run note links to its neighbour
// units, embeds its own run DAG, and sits next to graphify's code notes and engram's decision
// notes so Obsidian's graph view connects them.
//
// Nothing in batten ever reads these files back. SQLite is canonical; everything here is a
// lossy, regenerable projection of it.
package vault

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ArthurZizumbo/batten/internal/render"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// ErrNoVault is returned when a Writer has no root. The caller should simply not export when
// capabilities.obsidian.vault is unset; failing loudly beats writing a run note into the CWD.
var ErrNoVault = errors.New("batten: no Obsidian vault configured (capabilities.obsidian.vault)")

// Writer emits one project's notes into a vault.
type Writer struct {
	Root    string // vault path, already expanded (the caller resolves "~")
	Project string

	// WriteSets is optional: node_id -> the files that node owns (from store.WriteSet).
	// When it is nil the run note says the write-set was not recorded, rather than printing
	// "0 files" — "this agent owned nothing" and "nobody asked" are different facts, and
	// batten does not report the second as the first.
	WriteSets map[string][]string
}

func New(root, project string) *Writer {
	return &Writer{Root: root, Project: project}
}

// verdictNone is a value we KNOW, not a guess: this run has no verdict envelope at all.
// It has to be a real property value rather than a missing key, because the "blocked verdicts"
// dashboard must select it: a run with no verdict and a run whose verdict is `blocked` are the
// same operational fact — the close gate will deny the commit.
const verdictNone = "none"

// ---------- paths ----------

// Layout: <vault>/batten/<project>/runs/<UNIT>.{md,canvas} and <vault>/batten/<project>/*.base
func (w *Writer) projectDir() string {
	return filepath.Join(w.Root, "batten", safeName(w.Project))
}

func (w *Writer) runsDir() string { return filepath.Join(w.projectDir(), "runs") }

// RunNotePath is where the run note for a unit lives (absolute).
func (w *Writer) RunNotePath(unitID string) string {
	if w.Root == "" {
		return ""
	}
	return filepath.Join(w.runsDir(), safeName(unitID)+".md")
}

// CanvasPath is where the caller should write the unit's .canvas (absolute). The canvas
// package owns rendering it; the vault only owns deciding where it goes, so that the run note
// can embed it with a link that resolves.
func (w *Writer) CanvasPath(unitID string) string {
	if w.Root == "" {
		return ""
	}
	return filepath.Join(w.runsDir(), safeName(unitID)+".canvas")
}

// CanvasRel is the same file as a vault-relative, slash-separated path — the exact form
// WriteRun's canvasRel argument wants, so the caller never has to do the string surgery.
func (w *Writer) CanvasRel(unitID string) string {
	return path.Join("batten", safeName(w.Project), "runs", safeName(unitID)+".canvas")
}

// safeName keeps a unit id or project name usable as a filename on Windows, a first-class
// target here: <>:"/\|?* are illegal there, and an embedded separator or ".." would let a unit
// id escape the vault folder entirely.
var unsafeName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func safeName(s string) string {
	s = unsafeName.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Trim(s, ". ") // Windows also refuses trailing dots and spaces
	if s == "" {
		return "unnamed"
	}
	return s
}

// ---------- frontmatter ----------

// frontmatter is the typed property block Obsidian reads and the .base dashboards query.
//
// Every "unknown" field is a pointer or a zero-able type with omitempty, and that is
// load-bearing: a run whose transcript was never ingested has not spent zero tokens, we simply
// did not measure it. An omitted property renders as empty (honest); `tokens: 0` would be a
// number we invented. Same for evidence_count when there is no verdict to count evidence in.
// A note on the two verdict properties. `verdict` is the REVIEWER's — the judgement of the work
// against its acceptance criteria — and `batten_verdict` is batten's own proof that the declared
// checks were run. They are separate because the gate requires both, from two different
// producers, and because they answer different questions. Collapsing them into "the latest row"
// let `batten check` set `verdict: ok` on a run nobody had reviewed, which took that run straight
// out of the "needs a human" dashboard.
type frontmatter struct {
	Unit          string     `yaml:"unit"`
	Project       string     `yaml:"project"`
	Status        string     `yaml:"status"`
	Phase         string     `yaml:"phase,omitempty"`
	Verdict       string     `yaml:"verdict"`
	BattenVerdict string     `yaml:"batten_verdict"`
	EvidenceCount *int       `yaml:"evidence_count,omitempty"`
	Tokens        *int64     `yaml:"tokens,omitempty"`
	ImputedUSD    *float64   `yaml:"imputed_usd,omitempty"`
	BaseSHA       string     `yaml:"base_sha,omitempty"`
	Started       time.Time  `yaml:"started,omitempty"`
	Ended         *time.Time `yaml:"ended,omitempty"`
	Domains       []string   `yaml:"domains"`
}

// ---------- the run note ----------

// WriteRun writes the run note. canvasRel is the vault-relative path of the .canvas the caller
// already emitted (or "" if none), so the note can embed it.
//
// reviewer and batten are the gate's two verdicts, from its two required producers; either may
// be nil. See the frontmatter comment for why they are not one field.
func (w *Writer) WriteRun(r *store.Run, nodes []store.Node, edges []store.Edge,
	reviewer, batten *store.Verdict, usage map[string]store.Usage, canvasRel string) error {
	if w.Root == "" {
		return ErrNoVault
	}
	if r == nil {
		return errors.New("batten: vault.WriteRun: nil run")
	}

	fm := frontmatter{
		Unit:          r.UnitID,
		Project:       w.Project,
		Status:        r.Status,
		Phase:         r.Phase,
		Verdict:       verdictNone,
		BattenVerdict: verdictNone,
		BaseSHA:       r.BaseSHA,
		Domains:       domainsOf(nodes),
	}
	if r.StartedAt > 0 {
		fm.Started = time.Unix(r.StartedAt, 0).UTC()
	}
	if r.EndedAt != nil && *r.EndedAt > 0 {
		t := time.Unix(*r.EndedAt, 0).UTC()
		fm.Ended = &t
	}
	if reviewer != nil {
		fm.Verdict = reviewer.Result
		n := len(reviewer.Evidence)
		fm.EvidenceCount = &n // may legitimately be 0 — that is the failure, and it must be visible
	}
	if batten != nil {
		fm.BattenVerdict = batten.Result
	}
	if r.TokensSpent > 0 {
		t := r.TokensSpent
		fm.Tokens = &t
	}
	if r.ImputedUSD > 0 {
		// Rounded so the same run always serializes to the same bytes; float noise would
		// otherwise rewrite the note (and re-trigger Obsidian's indexer) for no reason.
		u := round4(r.ImputedUSD)
		fm.ImputedUSD = &u
	}

	head, err := yaml.Marshal(fm)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(head)
	b.WriteString("---\n\n")
	w.renderBody(&b, r, nodes, edges, reviewer, batten, usage, canvasRel)

	return writeIfChanged(w.RunNotePath(r.UnitID), []byte(b.String()))
}

func (w *Writer) renderBody(b *strings.Builder, r *store.Run, nodes []store.Node,
	edges []store.Edge, reviewer, batten *store.Verdict, usage map[string]store.Usage, canvasRel string) {

	fmt.Fprintf(b, "# %s\n\n", r.UnitID)

	line := fmt.Sprintf("status `%s`", r.Status)
	if r.Phase != "" {
		line += fmt.Sprintf(" · phase `%s`", r.Phase)
	}
	if r.BaseSHA != "" {
		line += fmt.Sprintf(" · base `%s`", shortSHA(r.BaseSHA))
	}
	line += fmt.Sprintf(" · run `%s`", r.RunID)
	b.WriteString(line + "\n\n")

	// Cost, stated for what it is. On a subscription the marginal dollar cost of a token is
	// zero, so this is not a bill: it is what those tokens WOULD have cost on the API.
	switch {
	case r.TokensSpent > 0 && r.ImputedUSD > 0:
		fmt.Fprintf(b, "**%s tokens** · **%s imputed** (what this would have cost on the API; never billed)\n\n",
			humanTokens(r.TokensSpent), render.ImputedShort(r.ImputedUSD, r.UnpricedTokens, r.TokensSpent))
		if r.UnpricedTokens > 0 {
			fmt.Fprintf(b, "The imputed figure is a **floor, not a total**: %d%% of the tokens are on "+
				"models with no published rate.\n\n", render.UnpricedShare(r.UnpricedTokens, r.TokensSpent))
		}
	case r.TokensSpent > 0:
		// Tokens were measured but no rate was available (e.g. an unpriced model). Report the
		// count and say the price is unknown — never print "$0.00", which would price the
		// unpriceable as free. Same rule as the per-node "not priced" cell.
		fmt.Fprintf(b, "**%s tokens** · imputed cost **not priced** (no API rate for this run's model)\n\n",
			humanTokens(r.TokensSpent))
	default:
		b.WriteString("Usage **not measured** for this run (no transcript ingested). Not zero — unknown.\n\n")
	}

	w.renderVerdict(b, r, reviewer, batten)
	w.renderFanout(b, nodes, usage)
	renderRelations(b, nodes, edges)
	renderCanvas(b, canvasRel)
	w.renderNeighbours(b, r.UnitID)

	b.WriteString("\n---\n\n")
	b.WriteString("_Written by batten. SQLite is canonical; this note is a projection of it and is regenerated._\n")
}

// renderVerdict writes the gate's state: BOTH verdicts, each under its producer's heading.
//
// Rendering only the newest row is how this note came to hide the very thing a reviewer opens it
// for. `batten check` writes a source=batten verdict, which is then the latest one, so its check
// output stood in for the reviewer's evidence — and a run nobody had reviewed read as a pass.
func (w *Writer) renderVerdict(b *strings.Builder, r *store.Run, reviewer, batten *store.Verdict) {
	b.WriteString("## Verdict\n\n")

	if reviewer == nil && batten == nil {
		// The loudest thing in the note, because it is the one fact that changes what the
		// human can do next: the close gate will refuse the commit.
		fmt.Fprintf(b, "> [!danger] No verdict — the close gate will DENY a commit\n"+
			"> `%s` has no verdict envelope. `git commit` is denied until the closing phase emits one,\n"+
			"> because an approval that cites nothing is not an approval.\n"+
			"> Escape hatch, recorded in the audit log: `batten override %s --reason \"...\"`\n\n",
			r.UnitID, r.UnitID)
		return
	}

	renderOneVerdict(b, "Reviewer", reviewer,
		"> [!warning] No reviewer verdict\n"+
			"> `batten check` proves the declared checks RAN. It does not judge whether the work meets\n"+
			"> its acceptance criteria — only a verdict from the verify phase does that.\n"+
			"> Record one: `batten verdict --file v.json`\n\n")

	renderOneVerdict(b, "batten check", batten,
		"> [!warning] No batten-verified pass\n"+
			"> Nothing was RUN to verify this unit; the gate's checks were asserted at best.\n"+
			"> Run: `batten check "+r.UnitID+"`\n\n")
}

// renderOneVerdict renders a single producer's envelope, or says plainly that it is missing.
// A missing half is a fact about the gate, not an empty section to skip.
func renderOneVerdict(b *strings.Builder, heading string, v *store.Verdict, missing string) {
	fmt.Fprintf(b, "### %s\n\n", heading)
	if v == nil {
		b.WriteString(missing)
		return
	}

	if v.Result == "ok" && len(v.Evidence) == 0 {
		// store.SaveVerdict refuses this and the commit hook denies it. If one reached the
		// vault anyway, the note is the last place it can still be seen for what it is.
		fmt.Fprintf(b, "> [!danger] result `ok` with an EMPTY evidence[]\n"+
			"> This is the single failure batten exists to make impossible: an approval that points at\n"+
			"> nothing. Treat this run as **blocked**, not as passing.\n\n")
	}

	fmt.Fprintf(b, "**result** `%s`", v.Result)
	if v.Gate != "" {
		fmt.Fprintf(b, " · **gate** `%s`", v.Gate)
	}
	if v.CheckID != "" {
		fmt.Fprintf(b, " · **check** `%s`", v.CheckID)
	}
	if v.RequiresConfirmation {
		b.WriteString(" · **requires confirmation**")
	}
	b.WriteString("\n\n")

	if v.Why != "" {
		fmt.Fprintf(b, "%s\n\n", v.Why)
	}

	b.WriteString("#### Evidence\n\n")
	if len(v.Evidence) == 0 {
		b.WriteString("_none cited._\n\n")
	} else {
		for _, e := range v.Evidence {
			fmt.Fprintf(b, "- %s\n", strings.TrimSpace(e))
		}
		b.WriteString("\n")
	}

	if v.SafeNextStep != "" {
		fmt.Fprintf(b, "**safe_next_step**: %s\n\n", v.SafeNextStep)
	}
}

func (w *Writer) renderFanout(b *strings.Builder, nodes []store.Node, usage map[string]store.Usage) {
	b.WriteString("## Fan-out\n\n")

	var subs []store.Node
	for _, n := range nodes {
		if n.Kind == "subagent" {
			subs = append(subs, n)
		}
	}
	if len(subs) == 0 {
		b.WriteString("_no subagents recorded for this run._\n\n")
		return
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].StartedAt < subs[j].StartedAt })

	b.WriteString("| agent | domain | status | write-set | tokens | imputed |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, n := range subs {
		label := n.Label
		if label == "" {
			label = n.NodeID
		}
		fmt.Fprintf(b, "| %s | %s | `%s` | %s | %s | %s |\n",
			cell(label), cell(orDash(n.Domain)), cell(n.Status),
			w.writeSetCell(n.NodeID), tokensCell(usage, n.NodeID), imputedCell(usage, n.NodeID))
	}
	b.WriteString("\n")

	// Files, as inline code — deliberately NOT wikilinks. graphify's vault export names its
	// code notes by its own rules, which we cannot know from here, and a wikilink to a note
	// that does not exist is worse than no wikilink: it plants a phantom node in the graph
	// view and reads as a fact we never established.
	if files := w.allFiles(subs); len(files) > 0 {
		b.WriteString("**Files touched**\n\n")
		for _, f := range files {
			fmt.Fprintf(b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}
}

func (w *Writer) writeSetCell(nodeID string) string {
	if w.WriteSets == nil {
		return "not recorded"
	}
	n := len(w.WriteSets[nodeID])
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func (w *Writer) allFiles(subs []store.Node) []string {
	if w.WriteSets == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range subs {
		for _, f := range w.WriteSets[n.NodeID] {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

// renderRelations surfaces the typed edges. spawn is omitted because the fan-out table already
// shows it; the interesting ones are exactly the relations a flat trace cannot express —
// retry_of, supersedes, rollback, depends_on — which is why the store keeps them at all.
func renderRelations(b *strings.Builder, nodes []store.Node, edges []store.Edge) {
	label := map[string]string{}
	for _, n := range nodes {
		if n.Label != "" {
			label[n.NodeID] = n.Label
		}
	}
	name := func(id string) string {
		if l, ok := label[id]; ok {
			return l
		}
		return id
	}

	var rels []store.Edge
	for _, e := range edges {
		if e.Rel != "spawn" {
			rels = append(rels, e)
		}
	}
	if len(rels) == 0 {
		return
	}
	sort.Slice(rels, func(i, j int) bool {
		if rels[i].Rel != rels[j].Rel {
			return rels[i].Rel < rels[j].Rel
		}
		return rels[i].Src < rels[j].Src
	})

	b.WriteString("## Relations\n\n")
	for _, e := range rels {
		fmt.Fprintf(b, "- `%s`: %s → %s\n", e.Rel, name(e.Src), name(e.Dst))
	}
	b.WriteString("\n")
}

func renderCanvas(b *strings.Builder, canvasRel string) {
	b.WriteString("## Run graph\n\n")
	if canvasRel == "" {
		b.WriteString("_no canvas emitted for this run._\n\n")
		return
	}
	// The basename is enough: the .canvas sits in the same folder as this note, and Obsidian
	// resolves a same-folder link first. We do not stat it — canvasRel is the caller's
	// assertion that it already wrote the file, and silently dropping the embed because of a
	// call-ordering detail would be worse than trusting it.
	fmt.Fprintf(b, "![[%s]]\n\n", path.Base(filepath.ToSlash(canvasRel)))
}

func (w *Writer) renderNeighbours(b *strings.Builder, unitID string) {
	prev, ok := w.prevUnitNote(unitID)
	if !ok {
		return
	}
	b.WriteString("## Neighbours\n\n")
	fmt.Fprintf(b, "Previous unit: [[%s]]\n\n", prev)
}

// prevUnitNote derives the preceding unit id from a numeric suffix, but returns it ONLY if that
// note actually exists in the vault.
//
// Deriving "US-033" from "US-034" is a guess. The vault is the one place we can turn the guess
// into a fact — os.Stat — so we do, and emit nothing otherwise. This is the same rule as the
// files above: never link to a note that cannot exist.
func (w *Writer) prevUnitNote(unitID string) (string, bool) {
	for _, cand := range prevUnitCandidates(unitID) {
		if st, err := os.Stat(w.RunNotePath(cand)); err == nil && !st.IsDir() {
			return cand, true
		}
	}
	return "", false
}

var trailingDigits = regexp.MustCompile(`^(.*?)(\d+)$`)

// prevUnitCandidates offers both the zero-padded and the bare form ("US-100" -> "US-099",
// "US-99"), because we cannot know whether the project's ids are fixed-width. The existence
// check decides; if neither note exists, neither is linked.
func prevUnitCandidates(unitID string) []string {
	m := trailingDigits.FindStringSubmatch(unitID)
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 1 {
		return nil // no unit zero, and no unit before the first
	}
	prefix, width := m[1], len(m[2])
	padded := fmt.Sprintf("%s%0*d", prefix, width, n-1)
	bare := fmt.Sprintf("%s%d", prefix, n-1)
	if padded == bare {
		return []string{padded}
	}
	return []string{padded, bare}
}

// ---------- helpers ----------

func domainsOf(nodes []store.Node) []string {
	seen := map[string]bool{}
	out := []string{} // never nil: an empty list serializes as `domains: []`, not `domains: null`
	for _, n := range nodes {
		if n.Domain != "" && !seen[n.Domain] {
			seen[n.Domain] = true
			out = append(out, n.Domain)
		}
	}
	sort.Strings(out)
	return out
}

// tokensCell and imputedCell distinguish three states that a naive "%d" would collapse into a
// zero: not measured, measured but unpriced, and measured. batten never reports an unknown as 0.
func tokensCell(usage map[string]store.Usage, nodeID string) string {
	u, ok := usage[nodeID]
	if !ok {
		return "not measured"
	}
	return humanTokens(u.Tokens())
}

func imputedCell(usage map[string]store.Usage, nodeID string) string {
	u, ok := usage[nodeID]
	if !ok {
		return "not measured"
	}
	if u.ImputedUSD <= 0 {
		return "not priced"
	}
	return fmt.Sprintf("$%.2f", u.ImputedUSD)
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.FormatInt(n, 10)
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// cell keeps a value from breaking out of a markdown table row.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// writeIfChanged skips a write whose bytes are already on disk. Obsidian watches the vault and
// reindexes on mtime; regenerating identical notes on every hook would churn the graph view
// (and the user's git diff) for nothing.
func writeIfChanged(dst string, data []byte) error {
	if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
