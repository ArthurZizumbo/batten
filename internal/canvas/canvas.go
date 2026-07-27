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
func Render(run *store.Run, nodes []store.Node, edges []store.Edge, verdict *store.Verdict) *Canvas {
	c := &Canvas{Nodes: []Node{}, Edges: []Edge{}}

	// Group nodes by phase. A subagent's phase is the phase node that spawned it.
	parent := map[string]string{}
	for _, e := range edges {
		if e.Rel == "spawn" {
			parent[e.Dst] = e.Src
		}
	}

	var phases []store.Node
	byPhase := map[string][]store.Node{}
	for _, n := range nodes {
		if n.Kind == "phase" {
			phases = append(phases, n)
			continue
		}
		byPhase[parent[n.NodeID]] = append(byPhase[parent[n.NodeID]], n)
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
			body := fmt.Sprintf("**%s**\n`%s`", k.Label, k.Status)
			if k.Domain != "" {
				body = fmt.Sprintf("**%s**\ndomain: `%s`\n`%s`", k.Label, k.Domain, k.Status)
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
		if _, ok := pos[e.Src]; !ok {
			continue
		}
		if _, ok := pos[e.Dst]; !ok {
			continue
		}
		lbl := ""
		if e.Rel != "spawn" {
			lbl = e.Rel // spawn is obvious from the layout; the others are the interesting ones
		}
		c.Edges = append(c.Edges, Edge{
			ID: fmt.Sprintf("e%d", i), FromNode: e.Src, ToNode: e.Dst,
			FromSide: "right", ToSide: "left", Label: lbl, Color: relColor(e.Rel),
		})
	}

	// The verdict is the point of the whole run: give it a node of its own.
	if verdict != nil {
		x := len(phases) * gapX
		body := fmt.Sprintf("## verdict: %s\n\n**%s**\n\n", verdict.Result, verdict.Why)
		if len(verdict.Evidence) == 0 {
			body += "_no evidence_ — an approval must cite something"
		} else {
			for _, e := range verdict.Evidence {
				body += "- " + e + "\n"
			}
		}
		c.Nodes = append(c.Nodes, Node{
			ID: "verdict", Type: "text", X: x, Y: 0, Width: nodeW + 80, Height: nodeH + 80,
			Color: statusColor(verdict.Result), Text: body,
		})
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
