// Package tui is the review surface: read a run without leaving the terminal.
//
// It is deliberately a tree and a detail pane, not a node-link graph. Terminal graph
// layout (Sugiyama into ASCII) is buildable and is the wrong trade: the topology view is
// the .canvas file, which Obsidian renders for free. What a terminal is actually good at
// is the other job — scanning runs, reading a verdict, seeing which fanned-out agent
// failed, checking a budget. That is what this renders, and nothing else.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ArthurZizumbo/batten/internal/render"
	"github.com/ArthurZizumbo/batten/internal/spec"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// Bubble Tea v2 changed the Model interface out from under v1 code: Init lost its model
// return, and the alt-screen moved off ProgramOption onto the View. tea.NewProgram takes
// an interface, so a regression there fails at runtime with a blank screen rather than at
// build time. This assertion drags that failure back to compile time.
var _ tea.Model = (*Model)(nil)

var (
	cGreen  = lipgloss.Color("2")
	cRed    = lipgloss.Color("1")
	cYellow = lipgloss.Color("3")
	cBlue   = lipgloss.Color("4")
	cGray   = lipgloss.Color("8")
	cWhite  = lipgloss.Color("7")

	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(cWhite)
	stDim      = lipgloss.NewStyle().Foreground(cGray)
	stSelected = lipgloss.NewStyle().Bold(true).Foreground(cBlue)
	stAlarm    = lipgloss.NewStyle().Bold(true).Foreground(cRed)
	stWarn     = lipgloss.NewStyle().Foreground(cYellow)
	stPane     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cGray).Padding(0, 1)
	stHelp     = lipgloss.NewStyle().Foreground(cGray)
)

// Layout constants.
//
// The border/padding arithmetic is the thing that silently tears a frame, and lipgloss v2
// changed it from v1: Style.Width(n) is now the TOTAL block width, borders included — it
// subtracts the border, wraps and aligns the text to what is left, then draws the border
// back around. So two panes joined horizontally occupy exactly leftW+rightW columns, and
// the text inside one has paneW - paneBorder - panePadX cells to live in. Treating Width as
// "content width, borders extra" (the v1 rule) overflows the terminal by four columns.
const (
	leftW      = 38
	paneBorder = 2 // RoundedBorder: one cell each side, and one line top and bottom
	panePadX   = 2 // Padding(0,1)
	barW       = 8
	blockH     = 3 // lines per run row in the list: header, ceiling bar, totals
	headFootH  = 2 // the title line above the panes and the help line below them
	listHeadH  = 2 // "runs" heading and the blank line under it
	scrollHint = 2 // the "↑ N more" / "↓ N more" lines, reserved whether or not they show
)

// contentW is the cells a pane's text actually gets. contentH is the lines it gets.
func contentW(paneW int) int { return paneW - paneBorder - panePadX }
func contentH(paneH int) int { return paneH - paneBorder }

func statusStyle(s string) lipgloss.Style {
	switch s {
	case "ok":
		return lipgloss.NewStyle().Foreground(cGreen)
	case "blocked", "failed", "rolled_back":
		return lipgloss.NewStyle().Foreground(cRed)
	case "warn":
		return lipgloss.NewStyle().Foreground(cYellow)
	case "running":
		return lipgloss.NewStyle().Foreground(cBlue)
	}
	return stDim
}

func glyph(s string) string {
	switch s {
	case "ok":
		return "✓"
	case "blocked":
		return "⨯"
	case "failed":
		return "✗"
	case "rolled_back":
		return "↶"
	case "warn":
		return "!"
	case "running":
		return "◐"
	}
	return "·"
}

type detail struct {
	run   store.Run
	nodes []store.Node
	edges []store.Edge
	// verdict is the reviewer's judgment; battenVerdict is the mechanical pass. The gate
	// needs both, so both are carried rather than collapsed to "the latest one".
	verdict       *store.Verdict
	battenVerdict *store.Verdict
	usage         map[string]store.Usage // node_id -> that node's share of the ledger
	ceils         []store.Ceiling
	criteria      []store.Criterion // the run's acceptance criteria; empty = none seeded
	// writesets[nodeID] = the files that node owns
	writesets map[string][]string
}

type Model struct {
	spec  *spec.Spec
	store *store.Store

	runs []store.Run
	sel  int
	det  *detail
	// ceils is only populated for the runs currently on screen. store.Budget costs a few
	// queries per run and the list polls; there is no reason to price runs nobody can see.
	ceils map[string][]store.Ceiling
	err   error

	w, h int
}

func New(sp *spec.Spec, st *store.Store) *Model {
	return &Model{spec: sp, store: st, ceils: map[string][]store.Ceiling{}}
}

type tickMsg time.Time

// The refresh cursor. Not fsnotify: WAL makes file-watching unreliable, and the writers
// here are separate hook processes anyway. Polling the DB is the honest mechanism.
// One second, not milliseconds — this is a review surface, and the hooks it watches are
// competing with it for the same SQLite file.
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init returns only a Cmd in Bubble Tea v2; the model is already the receiver.
func (m *Model) Init() tea.Cmd {
	m.reload()
	return tick()
}

func (m *Model) reload() {
	runs, err := m.store.ListRuns(m.spec.Project, 100)
	if err != nil {
		m.err = err
		return
	}
	m.err = nil
	m.runs = runs
	if m.sel >= len(runs) {
		m.sel = max(0, len(runs)-1)
	}
	m.det = nil
	m.ceils = map[string][]store.Ceiling{}
	if len(runs) == 0 {
		return
	}

	b := m.spec.Budget
	lo, hi := m.window()
	for _, r := range runs[lo:hi] {
		cs, err := m.store.Budget(r.RunID, b.TokensPerRun, b.ImputedUSDPerRun, b.QuotaPctPerRun)
		if err == nil {
			m.ceils[r.RunID] = cs
		}
	}

	r := runs[m.sel]
	d := &detail{run: r, writesets: map[string][]string{}, ceils: m.ceils[r.RunID]}
	d.nodes, _ = m.store.Nodes(r.RunID)
	d.edges, _ = m.store.Edges(r.RunID)
	d.usage, _ = m.store.UsageByNode(r.RunID)
	// Both verdicts, for the same reason `batten show` needs both: the gate wants one from
	// batten proving the checks ran and one from a reviewer judging the work, and rendering
	// only the newest row lets `batten check` hide the reviewer's evidence behind its own
	// check output. A screen that shows half a two-half rule is a screen you can misread.
	d.battenVerdict, _ = m.store.LatestVerdictBySource(r.RunID, "", "batten")
	d.verdict, _ = m.store.LatestVerdictNotBySource(r.RunID, "", "batten")
	d.criteria, _ = m.store.Criteria(r.RunID)
	for _, n := range d.nodes {
		if ws, err := m.store.WriteSet(r.RunID, n.NodeID); err == nil && len(ws) > 0 {
			d.writesets[n.NodeID] = ws
		}
	}
	m.det = d
}

// window is the slice of runs the list can actually show, scrolled to keep the selection
// visible. reload() and renderRuns() must agree on it, or the pane prices rows it does
// not draw and draws rows it did not price.
// Style.Height() is a MINIMUM, not a clamp: a pane whose content overruns it simply grows,
// the frame grows with it, and in altscreen the header scrolls off the top. So the scroll
// hints are reserved unconditionally rather than added on top of a full row budget.
func (m *Model) window() (lo, hi int) {
	rows := (contentH(m.bodyH()) - listHeadH - scrollHint) / blockH
	if rows < 1 {
		rows = 1
	}
	lo = 0
	if m.sel >= rows {
		lo = m.sel - rows + 1
	}
	hi = min(lo+rows, len(m.runs))
	return lo, hi
}

// bodyH is the total height of the panes, borders included (see the const block).
func (m *Model) bodyH() int {
	h := m.h
	if h == 0 {
		h = 30
	}
	b := h - headFootH
	if b < 10 {
		b = 10
	}
	return b
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Reload, do not just record: the height decides how many runs are on screen, and
		// only the runs on screen get priced. Without this, a resize leaves the rows that
		// scrolled into view unpriced until the next tick.
		m.w, m.h = msg.Width, msg.Height
		m.reload()
	case tickMsg:
		m.reload()
		return m, tick()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "j", "down":
			if m.sel < len(m.runs)-1 {
				m.sel++
				m.reload()
			}
		case "k", "up":
			if m.sel > 0 {
				m.sel--
				m.reload()
			}
		case "g", "home":
			m.sel = 0
			m.reload()
		case "G", "end":
			m.sel = max(0, len(m.runs)-1)
			m.reload()
		case "r":
			m.reload()
		}
	}
	return m, nil
}

// View builds the frame. AltScreen is a field on the View in v2, not a program option:
// tea.WithAltScreen no longer exists.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "batten — " + m.spec.Project
	return v
}

func (m *Model) render() string {
	if m.err != nil {
		return stAlarm.Render("error: "+m.err.Error()) + "\n"
	}
	if len(m.runs) == 0 {
		return stDim.Render("no runs yet — run `batten phase <unit> <phase>` to start one") + "\n"
	}

	w := m.w
	if w == 0 {
		w = 100
	}
	bodyH := m.bodyH()

	// Borders live inside Style.Width in v2, so the panes tile to exactly leftW + rightW.
	rightW := w - leftW
	if rightW < 34 {
		rightW = 34
	}

	left := stPane.Width(leftW).Height(bodyH).Render(m.renderRuns())
	right := stPane.Width(rightW).Height(bodyH).
		Render(m.renderDetail(contentW(rightW), contentH(bodyH)))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	head := stTitle.Render("batten") + stDim.Render(headerLine(m.spec.Project, len(m.runs)))
	help := stHelp.Render("j/k move · r refresh · q quit")
	return lipgloss.JoinVertical(lipgloss.Left, head, body, help)
}

// headerLine is the title bar's right half. plural(), because the same package already owned
// the helper while this line printed '1 runs' (#48).
func headerLine(project string, runs int) string {
	return fmt.Sprintf("  %s · %s", project, plural(runs, "run"))
}

// ---------- left pane: the run list ----------

func (m *Model) renderRuns() string {
	var b strings.Builder
	b.WriteString(stTitle.Render("runs") + "\n\n")

	lo, hi := m.window()
	if lo > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  ↑ %d more", lo)) + "\n")
	}
	for i := lo; i < hi; i++ {
		r := m.runs[i]
		g := statusStyle(r.Status).Render(glyph(r.Status))
		head := fmt.Sprintf("%s %-9s %s", g, r.UnitID, stDim.Render(r.Phase))
		if i == m.sel {
			b.WriteString(stSelected.Render("▸ ") + head + "\n")
		} else {
			b.WriteString("  " + head + "\n")
		}
		// The budget bar: the ceiling closest to being blown, because that is the one that
		// will actually stop the run. The full breakdown lives in the detail pane.
		b.WriteString("    " + m.bindingLine(r) + "\n")
		b.WriteString("    " + stDim.Render(totals(r)) + "\n")
	}
	if hi < len(m.runs) {
		b.WriteString(stDim.Render(fmt.Sprintf("  ↓ %d more", len(m.runs)-hi)) + "\n")
	}
	return b.String()
}

// bindingLine is the one-line budget bar for a run in the list. Every branch here is a
// different claim, and they must not be confused: no bar means no ceiling was declared,
// n/a means one was declared and cannot be measured, and a bar at 0% means measured and
// empty. An empty bar is a claim of headroom — we draw it only when we can back it up.
func (m *Model) bindingLine(r store.Run) string {
	if !m.spec.Budget.Set() {
		return "" // batten.yaml declares no ceiling; there is nothing to draw
	}
	cs, priced := m.ceils[r.RunID]
	if !priced {
		return stDim.Render("…") // scrolled into view but not yet priced this refresh
	}
	c, ok := binding(cs)
	if !ok {
		return stDim.Render(strings.Repeat("─", barW) + " n/a  " + naReason(firstUnmeasurable(cs)))
	}
	frac := c.Spent / c.Cap
	return fracStyle(frac).Render(fmt.Sprintf("%s %3.0f%% %s", bar(frac, barW), frac*100, kindLabel(c.Kind)))
}

// totals is the exactly-measured pair. "imputed" is load-bearing: on a subscription these
// dollars are not a bill, they are the value being pulled out of the plan. The dollar half
// comes from the shared renderer so a partially unpriced run reads as a floor here too.
func totals(r store.Run) string {
	usd := render.ImputedShort(r.ImputedUSD, r.UnpricedTokens, r.TokensSpent)
	if usd == "not priced" {
		return fmt.Sprintf("%s · imputed not priced (no published rate)", render.Tokens(r.TokensSpent))
	}
	return fmt.Sprintf("%s · %s imputed", render.Tokens(r.TokensSpent), usd)
}

// ---------- right pane: the selected run ----------

func (m *Model) renderDetail(w, h int) string {
	d := m.det
	if d == nil {
		return stDim.Render("—")
	}
	head := m.detailHead(d)
	tree := m.detailTree(d)
	verd := m.detailVerdict(d, w)

	// The verdict is what the run is FOR, so it is never what gets cut. When the pane is
	// too short, the tree loses lines and says how many; the verdict always survives whole.
	room := h - len(head) - len(verd) - 1
	if room < 1 {
		room = 1
	}
	if len(tree) > room {
		keep := max(0, room-1)
		hidden := len(tree) - keep
		tree = append(tree[:keep:keep], stDim.Render(fmt.Sprintf("… %d more lines", hidden)))
	}

	out := append([]string{}, head...)
	out = append(out, tree...)
	out = append(out, "")
	out = append(out, verd...)

	// Last resort. A verdict citing dozens of pieces of evidence can outgrow the pane even
	// with the tree gone, and an overgrown pane grows the frame past the terminal. Clamp —
	// but say so, loudly: evidence that vanished without a word is precisely the failure
	// this whole tool exists to prevent, and the review surface must not commit it either.
	if len(out) > h {
		keep := max(0, h-1)
		hidden := len(out) - keep
		out = append(out[:keep:keep],
			stAlarm.Render(fmt.Sprintf("… %d more lines hidden — widen the terminal", hidden)))
	}
	return strings.Join(out, "\n")
}

func (m *Model) detailHead(d *detail) []string {
	r := d.run
	out := []string{
		stTitle.Render(r.UnitID) + "  " + statusStyle(r.Status).Render(r.Status) +
			"  " + stDim.Render(r.Phase),
	}
	if r.BaseSHA != "" {
		out = append(out, stDim.Render("anchor "+shortSHA(r.BaseSHA)))
	}
	out = append(out, stDim.Render(totals(r)))

	// Every declared ceiling, including the ones we cannot see. Silence about an
	// unenforced ceiling reads as "enforced and fine", which is the lie we must not tell.
	for _, c := range d.ceils {
		out = append(out, ceilingLine(c))
	}
	if mi := m.spec.Budget.MaxIterations; mi > 0 {
		out = append(out, stDim.Render(fmt.Sprintf("iters   %d / %d", r.Iterations, mi)))
	}
	if line := criteriaLine(d.criteria); line != "" {
		out = append(out, line)
	}
	out = append(out, "")
	return out
}

// criteriaLine is the compliance glance (ítem 21): which acceptance criteria an approving
// verdict has cited, and which are still open. Empty when none were seeded — a run with no
// criteria on record must not show a satisfied empty scoreboard.
func criteriaLine(cs []store.Criterion) string {
	if len(cs) == 0 {
		return ""
	}
	covered := 0
	var marks []string
	for _, c := range cs {
		if c.Status == store.StatusCovered {
			covered++
			marks = append(marks, fmt.Sprintf("AC-%d✓", c.Idx))
		} else {
			marks = append(marks, fmt.Sprintf("AC-%d·", c.Idx))
		}
	}
	line := fmt.Sprintf("criteria %d/%d covered  %s", covered, len(cs), strings.Join(marks, " "))
	if covered < len(cs) {
		return stWarn.Render(line)
	}
	return stDim.Render(line)
}

// detailTree renders phases and the subagents each phase spawned. Cross-edges (retry_of,
// supersedes, rollback) are annotations here rather than drawn edges: the .canvas is the
// topology view, and a terminal drawing arcs is a worse Obsidian.
func (m *Model) detailTree(d *detail) []string {
	parent := map[string]string{}
	extra := map[string][]string{}
	for _, e := range d.edges {
		if e.Rel == "spawn" {
			parent[e.Dst] = e.Src
			continue
		}
		extra[e.Dst] = append(extra[e.Dst], e.Rel+" "+e.Src)
	}

	var phases []store.Node
	present := map[string]bool{}
	for _, n := range d.nodes {
		if n.Kind == "phase" {
			present[n.NodeID] = true
		}
	}

	kids := map[string][]store.Node{}
	for _, n := range d.nodes {
		if n.Kind == "phase" {
			phases = append(phases, n)
			continue
		}
		// A parent id we cannot resolve in this run must fall through to the orphan list
		// below, not into a bucket nothing ever reads. Dropping it would hide the subagent
		// from the one screen a user opens to find it.
		p := parent[n.NodeID]
		if !present[p] {
			p = ""
		}
		kids[p] = append(kids[p], n)
	}

	var out []string
	for _, p := range phases {
		out = append(out, statusStyle(p.Status).Render(glyph(p.Status))+" "+stTitle.Render(p.Label))
		ks := kids[p.NodeID]
		for i, k := range ks {
			branch := "├─"
			if i == len(ks)-1 {
				branch = "└─"
			}
			line := " " + stDim.Render(branch) + " " +
				statusStyle(k.Status).Render(glyph(k.Status)) + " " + k.Label
			if k.Domain != "" {
				line += stDim.Render(" (" + k.Domain + ")")
			}
			if ws := d.writesets[k.NodeID]; len(ws) > 0 {
				line += stDim.Render(fmt.Sprintf("  %s", plural(len(ws), "file")))
			}
			// Per-node spend comes from the usage ledger, not from a guess. A node with no
			// usage row gets no number at all — an un-ingested agent is not a free agent.
			if u, ok := d.usage[k.NodeID]; ok {
				line += stDim.Render(fmt.Sprintf("  %s · $%.2f", render.Tokens(u.Tokens()), u.ImputedUSD))
			}
			out = append(out, line)
			for _, x := range extra[k.NodeID] {
				out = append(out, " "+stDim.Render("│")+"   "+stWarn.Render("↺ "+x))
			}
		}
	}

	// Nodes whose spawn edge never landed. Showing them as orphans is better than dropping
	// them: a subagent missing from the tree is exactly the one you are hunting for.
	if orph := kids[""]; len(orph) > 0 {
		out = append(out, "", stDim.Render("unattributed"))
		for _, k := range orph {
			out = append(out, "  "+statusStyle(k.Status).Render(glyph(k.Status))+" "+k.Label)
		}
	}
	if len(out) == 0 {
		out = append(out, stDim.Render("no nodes recorded yet"))
	}
	return out
}

func (m *Model) detailVerdict(d *detail, w int) []string {
	if d.verdict == nil && d.battenVerdict == nil {
		// The loudest thing on the screen, because it is the thing that will stop a commit
		// and the user would otherwise find out from a denied `git commit` ten minutes later.
		return []string{
			stAlarm.Render("NO VERDICT"),
			stAlarm.Render(m.closeGateNote()),
		}
	}

	var out []string
	for _, v := range []*store.Verdict{d.verdict, d.battenVerdict} {
		if v == nil {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		src := v.Source
		if src == "" {
			src = "agent"
		}
		out = append(out, stTitle.Render("verdict")+" "+
			statusStyle(v.Result).Render(strings.ToUpper(v.Result))+
			stDim.Render("  "+v.Gate+"/"+v.CheckID+"  ["+src+"]"))
		if v.Why != "" {
			out = append(out, stDim.Render(wrapIndent(v.Why, w, "")))
		}
		if len(v.Evidence) == 0 {
			// The one failure batten exists to kill. store.SaveVerdict refuses to write this
			// when the gate requires evidence; on screen anyway means the gate was not armed.
			out = append(out, stAlarm.Render("no evidence — this cannot be an approval"))
		}
		for _, e := range v.Evidence {
			out = append(out, statusStyle("ok").Render("· ")+wrapIndent(e, w-2, "  "))
		}
		if v.RequiresConfirmation {
			out = append(out, stWarn.Render("requires confirmation"))
		}
		if v.SafeNextStep != "" {
			out = append(out, stDim.Render("next: "+wrapIndent(v.SafeNextStep, w-6, "      ")))
		}
	}

	// Name whichever half is missing. A single green verdict on screen reads as "approved"
	// whichever one it is, and only one of the two can actually clear the gate alone.
	if d.battenVerdict == nil {
		out = append(out, "", stAlarm.Render("no batten-verified pass — the checks have not been RUN"))
	}
	if d.verdict == nil {
		out = append(out, "", stWarn.Render("no reviewer verdict — nothing has judged the criteria"))
	}
	return out
}

// closeGateNote names the phase and gate that will do the denying, so the warning is
// actionable rather than ominous.
func (m *Model) closeGateNote() string {
	p, ok := m.spec.ClosingPhase()
	if !ok {
		return "no phase declares requires_verdict — nothing is gating a commit"
	}
	g := p.Gate
	if g == "" {
		g = p.ID
	}
	return fmt.Sprintf("phase %q needs verdict %q from gate %q — git commit will be DENIED",
		p.ID, p.RequiresVerdict, g)
}

// ---------- budget rendering ----------

// binding returns the measurable ceiling closest to being blown. Unmeasurable ceilings are
// not candidates: a ceiling we cannot see cannot be the one we claim is binding.
func binding(cs []store.Ceiling) (store.Ceiling, bool) {
	var best store.Ceiling
	found := false
	for _, c := range cs {
		if !c.Available || c.Cap <= 0 {
			continue
		}
		if !found || c.Spent/c.Cap > best.Spent/best.Cap {
			best, found = c, true
		}
	}
	return best, found
}

// ceilingLine renders one declared ceiling. Overflow is left to the pane's Style.Width(),
// which word-wraps on visible cells; slicing the string here would cut a multi-byte bar
// glyph in half, since len() counts bytes and the terminal counts cells.
func ceilingLine(c store.Ceiling) string {
	label := kindLabel(c.Kind)
	if !c.Available {
		return stDim.Render(fmt.Sprintf("%-7s %s n/a  %s",
			label, strings.Repeat("─", barW), naReason(c)))
	}
	frac := 0.0
	if c.Cap > 0 {
		frac = c.Spent / c.Cap
	}
	st := fracStyle(frac)
	if c.Exceeded {
		st = stAlarm
	}
	return st.Render(fmt.Sprintf("%-7s %s %s", label, bar(frac, barW), amounts(c)))
}

func amounts(c store.Ceiling) string {
	switch c.Kind {
	case "tokens":
		return render.Tokens(int64(c.Spent)) + " / " + render.Tokens(int64(c.Cap))
	case "imputed_usd":
		return fmt.Sprintf("%s / $%.2f imputed", render.ImputedShort(c.Spent, c.UnpricedTokens, c.TotalTokens), c.Cap)
	case "quota_pct":
		return fmt.Sprintf("%.1f%% / %.1f%% of the 5h window", c.Spent, c.Cap)
	}
	return fmt.Sprintf("%.2f / %.2f", c.Spent, c.Cap)
}

func kindLabel(kind string) string {
	switch kind {
	case "tokens":
		return "tokens"
	case "imputed_usd":
		return "imputed"
	case "quota_pct":
		return "quota"
	}
	return kind
}

// naReason says WHY a ceiling is unmeasurable. Bare "n/a" reads as a bug in batten; the
// real cause is almost always that `batten statusline` — the only local surface that can
// sample the subscription quota — is not installed, and that is something the user can fix.
func naReason(c store.Ceiling) string {
	if c.Reason != "" {
		return "(" + c.Reason + ")"
	}
	kind := c.Kind
	if kind == "quota_pct" {
		return "(needs `batten statusline`)"
	}
	return "(not measurable)"
}

// unmeasurable returns the kind of the first ceiling we cannot see, for the n/a reason.
// firstUnmeasurable returns the first ceiling we cannot see, so the list row can say why.
func firstUnmeasurable(cs []store.Ceiling) store.Ceiling {
	for _, c := range cs {
		if !c.Available {
			return c
		}
	}
	return store.Ceiling{}
}

func fracStyle(frac float64) lipgloss.Style {
	switch {
	case frac >= 1:
		return stAlarm
	case frac >= 0.8:
		return stWarn
	}
	return stDim
}

func bar(frac float64, w int) string {
	if frac < 0 || frac != frac { // NaN guard: a 0/0 ceiling must not render as a full bar
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	f := int(frac * float64(w))
	return strings.Repeat("▓", f) + strings.Repeat("░", w-f)
}

// ---------- small helpers ----------

// wrapIndent wraps on visible cell width (lipgloss.Wrap, not len()) and indents the
// continuation lines so a long piece of evidence still reads as one list item.
func wrapIndent(s string, w int, pad string) string {
	if w < 16 {
		w = 16
	}
	lines := strings.Split(lipgloss.Wrap(s, w, " -/"), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// Run is the entry point main.go calls. Note there is no tea.WithAltScreen in Bubble Tea
// v2 — the alt-screen is requested by the View (see Model.View).
func Run(sp *spec.Spec, st *store.Store) error {
	_, err := tea.NewProgram(New(sp, st)).Run()
	return err
}
