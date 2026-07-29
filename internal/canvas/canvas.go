// Package canvas renders a run graph as a JSON Canvas 1.0 file.
//
// This is the module that replaced a TUI. The v2 design budgeted weeks for a Bubbletea
// graph viewer with a hand-rolled Sugiyama→ASCII layout, on the correct observation that
// no Go library does terminal node-link layout. The observation was right and the
// conclusion was wrong: Obsidian already renders JSON Canvas, so the graph view costs
// an exporter, not a project.
//
// Spec: https://jsoncanvas.org/spec/1.0/
package canvas

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ArthurZizumbo/batten/internal/store"
)

type Canvas struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

type Node struct {
	ID     string `json:"id"`
	Type   string `json:"type"` // text|file|link|group
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Color  string `json:"color,omitempty"`

	Text  string `json:"text,omitempty"`  // type=text
	Label string `json:"label,omitempty"` // type=group
	File  string `json:"file,omitempty"`  // type=file
}

type Edge struct {
	ID       string `json:"id"`
	FromNode string `json:"fromNode"`
	FromSide string `json:"fromSide,omitempty"`
	ToNode   string `json:"toNode"`
	ToSide   string `json:"toSide,omitempty"`
	Color    string `json:"color,omitempty"`
	Label    string `json:"label,omitempty"`
}

// JSON Canvas preset colors. The spec leaves the exact hues to the app, which is
// what we want: they follow the reader's theme.
const (
	colRed    = "1"
	colOrange = "2"
	colYellow = "3"
	colGreen  = "4"
	colPurple = "6"
)

func statusColor(s string) string {
	switch s {
	case "ok":
		return colGreen
	case "blocked":
		return colRed
	case "failed":
		return colRed
	case "warn":
		return colYellow
	case "running":
		return colPurple
	}
	return ""
}

func relColor(rel string) string {
	switch rel {
	case "retry_of":
		return colOrange
	case "rollback":
		return colRed
	case "depends_on":
		return colYellow
	}
	return ""
}

const (
	nodeW  = 300
	nodeH  = 120
	gapX   = 420
	gapY   = 170
	padTop = 60
)

// Render lays the run out as columns: each phase is a group, its subagents stack below it.
// Deliberately simple — a run is small, and the layered structure is already in the data.
//
// It takes TWO verdicts because the gate demands two, from two different producers: `reviewer`
// is somebody's judgement of the work against its acceptance criteria, `batten` is batten's own
// proof that the declared checks were RUN. Rendering only the newest row — which is what this
// took before — meant `batten check` painted its check output over the reviewer's evidence, and
// the canvas showed one half of a two-half rule as though it were the whole gate.
func Render(run *store.Run, nodes []store.Node, edges []store.Edge, reviewer, batten *store.Verdict) *Canvas {
	c := &Canvas{Nodes: []Node{}, Edges: []Edge{}}

	// Group nodes by phase. A subagent's phase is the phase node that spawned it.
	parent := map[string]string{}
	for _, e := range edges {
		if e.Rel == "spawn" {
			parent[e.Dst] = e.Src
		}
	}

	var phases []store.Node
	present := map[string]bool{}
	for _, n := range nodes {
		if n.Kind == "phase" {
			present[n.NodeID] = true
		}
	}

	byPhase := map[string][]store.Node{}
	for _, n := range nodes {
		if n.Kind == "phase" {
			phases = append(phases, n)
			continue
		}
		// A spawn edge can name a phase that is not in this run's nodes — a run recorded before
		// phase ids were scoped, or one whose phase row was taken by another unit. Treating that
		// as "belongs to a phase we are not drawing" made the subagent disappear from the canvas
		// while `batten show` still listed it. An id we cannot resolve is exactly what the
		// unattributed column is for.
		p := parent[n.NodeID]
		if !present[p] {
			p = ""
		}
		byPhase[p] = append(byPhase[p], n)
	}
	sort.Slice(phases, func(i, j int) bool { return phases[i].StartedAt < phases[j].StartedAt })

	// Orphans (no spawning phase recorded) get their own column so nothing is silently dropped.
	if orphans := byPhase[""]; len(orphans) > 0 {
		phases = append(phases, store.Node{NodeID: "", Kind: "phase", Label: "unattributed", Status: ""})
	}

	pos := map[string]Node{}
	for i, p := range phases {
		x := i * gapX
		kids := byPhase[p.NodeID]

		groupH := padTop + nodeH + 40
		if len(kids) > 0 {
			groupH = padTop + len(kids)*gapY + 40
		}
		c.Nodes = append(c.Nodes, Node{
			ID: "g-" + p.NodeID, Type: "group",
			X: x - 30, Y: -padTop, Width: nodeW + 60, Height: groupH,
			Label: p.Label, Color: statusColor(p.Status),
		})

		if p.NodeID != "" {
			n := Node{
				ID: p.NodeID, Type: "text", X: x, Y: 0, Width: nodeW, Height: nodeH,
				Color: statusColor(p.Status),
				Text:  fmt.Sprintf("## %s\n`%s`", p.Label, p.Status),
			}
			c.Nodes = append(c.Nodes, n)
			pos[p.NodeID] = n
		}

		for j, k := range kids {
			y := padTop + j*gapY
			if p.NodeID == "" {
				y = j * gapY
			}
			// Domain first, then the agent id, and the agent TYPE last. Four subagents all
			// titled "general-purpose" tell the reader nothing about which box is which —
			// caught by rendering a real run to HTML and reading the headings.
			name := k.Domain
			if name == "" {
				name = k.AgentID
			}
			if name == "" {
				name = k.Label
			}
			// The attempt number, on retries only. Two `ml` cards — one red, one green — are
			// otherwise indistinguishable apart from colour, which is the exact complaint the
			// field test recorded: the reader cannot tell a retry from a second independent task.
			if k.Attempt > 1 {
				name = fmt.Sprintf("%s #%d", name, k.Attempt)
			}
			body := fmt.Sprintf("**%s**\n`%s`", name, k.Status)
			if k.Domain != "" && k.Label != "" && k.Label != k.Domain {
				body = fmt.Sprintf("**%s**\n%s\n`%s`", name, k.Label, k.Status)
			}
			if k.CostUSD > 0 {
				body += fmt.Sprintf("\n$%.3f", k.CostUSD)
			}
			n := Node{
				ID: k.NodeID, Type: "text", X: x, Y: y, Width: nodeW, Height: nodeH,
				Color: statusColor(k.Status), Text: body,
			}
			c.Nodes = append(c.Nodes, n)
			pos[k.NodeID] = n
		}
	}

	for i, e := range edges {
		src, ok := pos[e.Src]
		if !ok {
			continue
		}
		dst, ok := pos[e.Dst]
		if !ok {
			continue
		}
		lbl := ""
		if e.Rel != "spawn" {
			lbl = e.Rel // spawn is obvious from the layout; the others are the interesting ones
		}
		// Sides follow the layout, not the relation. `spawn` runs forwards, but the two relations
		// that matter most — `retry_of` and `depends_on` — point from the LATER node back at the
		// earlier one, and pinning them to right→left dragged the arrow all the way around the
		// card it started from.
		from, to := "right", "left"
		if src.X > dst.X {
			from, to = "left", "right"
		}
		c.Edges = append(c.Edges, Edge{
			ID: fmt.Sprintf("e%d", i), FromNode: e.Src, ToNode: e.Dst,
			FromSide: from, ToSide: to, Label: lbl, Color: relColor(e.Rel),
		})
	}

	// The verdict is the point of the whole run: give it a node of its own. Both of them, in
	// their own column — and once either exists the run is at the gate, so the half that is
	// still MISSING gets a node too. A canvas that draws one green pass and omits the absent
	// reviewer reads as a run that is ready to land; it is not.
	if reviewer != nil || batten != nil {
		x := len(phases) * gapX
		slots := []struct {
			id, title, missing string
			v                  *store.Verdict
		}{
			{"verdict", "reviewer", "No reviewer verdict. `batten check` proves the checks ran; " +
				"it does not judge whether the work meets its acceptance criteria.", reviewer},
			{"verdict-batten", "batten check", "No batten-verified pass. The gate's checks must " +
				"be RUN, not asserted.", batten},
		}
		for i, s := range slots {
			y := i * (nodeH + 120)
			body := fmt.Sprintf("## verdict · %s\n\n", s.title)
			color := colRed
			if s.v == nil {
				body += "**missing**\n\n" + s.missing
			} else {
				color = statusColor(s.v.Result)
				body += fmt.Sprintf("**%s**\n\n%s\n\n", s.v.Result, s.v.Why)
				if len(s.v.Evidence) == 0 {
					body += "_no evidence_ — an approval must cite something"
				} else {
					for _, e := range s.v.Evidence {
						body += "- " + e + "\n"
					}
				}
			}
			c.Nodes = append(c.Nodes, Node{
				ID: s.id, Type: "text", X: x, Y: y, Width: nodeW + 80, Height: nodeH + 80,
				Color: color, Text: body,
			})
		}
	}

	// Header: what this run cost and where it stands.
	head := fmt.Sprintf("# %s\n\nrun `%s`\nstatus `%s`\nphase `%s`",
		run.UnitID, run.RunID, run.Status, run.Phase)
	if run.TokensSpent > 0 {
		// Imputed, not billed: on a subscription this is the value pulled out of the plan.
		head += fmt.Sprintf("\ntokens **%s**\nimputed **$%.2f**",
			humanTokens(run.TokensSpent), run.ImputedUSD)
	}
	if run.BaseSHA != "" {
		head += fmt.Sprintf("\nbase `%s`", run.BaseSHA)
	}
	c.Nodes = append(c.Nodes, Node{
		ID: "header", Type: "text", X: -gapX, Y: 0, Width: nodeW, Height: nodeH + 60, Text: head,
	})

	return c
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func (c *Canvas) WriteFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
