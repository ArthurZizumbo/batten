package mcp

// The model reads prose; the client reads JSON. Sending each of them the other one's format is
// what this file exists to stop.
//
// An MCP tool result has two halves, and they are for two different readers. `content` is what
// reaches the MODEL. `structuredContent` is for the CLIENT — it renders a widget with it — and
// is not something the model has to read to act.
//
// batten was sending the same bytes to both, and not on purpose. Every handler returned a nil
// *CallToolResult, and the go-sdk fills in what the handler leaves empty:
//
//	res.StructuredContent = outJSON
//	if res.Content == nil {
//	    res.Content = []Content{&TextContent{Text: string(outJSON)}}   // server.go:386
//	}
//
// So the full JSON went out twice, byte for byte identical — 932/932 bytes for batten_runs,
// 1176/1176 for run_graph, 885/885 for verdict_status, 1466/1466 for spec. The SDK is explicit
// that this is the fallback: set Content and it leaves it alone.
//
// The half that costs tokens is `content`, and it was carrying a serialized struct — arrays of
// nulls, RFC3339 timestamps, schema-shaped keys — to a reader that wanted a sentence. The answer
// to "can I commit?" is three lines, not 885 characters of JSON:
//
//	US-034 · gate qa · BLOCKED
//	No batten-verified pass: the gate's checks were never run.
//	Run: batten check US-034
//
// The structured half keeps everything, so nothing is lost for a client that wants to draw it.
//
// This also answers the TOON question without adopting TOON: the saving comes from sending LESS,
// not from encoding the same thing more densely.

import (
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// summarizer is what every tool output implements: the compact, model-facing rendering of
// itself. Keeping it on the output type rather than in the handler means the summary cannot
// drift away from the data it summarizes.
type summarizer interface {
	summary() string
}

// reply pairs a compact `content` for the model with the full `structuredContent` for the
// client. Every handler returns through here.
func reply[T summarizer](out T) (*sdk.CallToolResult, T, error) {
	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: strings.TrimSpace(out.summary())}},
	}, out, nil
}

// ---------- the summaries ----------

func (o runsOutput) summary() string {
	if o.Note != "" && len(o.Runs) == 0 {
		return o.Note
	}
	if len(o.Runs) == 0 {
		return "no runs recorded for " + o.Project
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d run(s) in %s, newest first:\n", len(o.Runs), o.Project)
	for i, r := range o.Runs {
		if i == 8 {
			fmt.Fprintf(&b, "  … and %d older\n", len(o.Runs)-i)
			break
		}
		fmt.Fprintf(&b, "  %s %s/%s · %s · %s\n", r.Unit, r.Status, orDash(r.Phase),
			verdictPhrase(r.Verdict), tokenPhrase(r.Tokens, r.ImputedUSD))
	}
	if o.Note != "" {
		b.WriteString(o.Note + "\n")
	}
	return b.String()
}

func (o graphOutput) summary() string {
	if o.Run == nil {
		return orDefault(o.Note, "no run to graph")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s/%s · %d nodes, %d edges", o.Run.Unit, o.Run.Status,
		orDash(o.Run.Phase), len(o.Nodes), len(o.Edges))
	if o.Retries > 0 {
		// The one number that separates the plan from the path actually taken.
		fmt.Fprintf(&b, " · %d retry/retries", o.Retries)
	}
	fmt.Fprintf(&b, " · %s\n", tokenPhrase(o.Run.Tokens, o.Run.ImputedUSD))

	// Name only what needs attention. A healthy fan-out does not need to be listed: the counts
	// above already say it went fine, and the caller can ask for the structured half.
	var bad []string
	for _, n := range o.Nodes {
		switch n.Status {
		case "failed", "blocked":
			label := n.Label
			if n.Domain != "" {
				label += " (" + n.Domain + ")"
			}
			bad = append(bad, label+": "+n.Status)
		}
	}
	if len(bad) > 0 {
		fmt.Fprintf(&b, "needs attention: %s\n", strings.Join(bad, "; "))
	}
	if o.UnattributedUsage != nil {
		fmt.Fprintf(&b, "%d tokens could not be attributed to any node (reported, not folded in)\n",
			o.UnattributedUsage.TotalTokens)
	}
	if o.Note != "" {
		b.WriteString(o.Note + "\n")
	}
	return b.String()
}

func (o verdictOutput) summary() string {
	if o.RunID == "" {
		return orDefault(o.Note, "no run to check")
	}
	var b strings.Builder
	state := "COMMIT ALLOWED"
	if o.CommitDenied {
		state = "COMMIT DENIED"
	}
	fmt.Fprintf(&b, "%s · gate %s · %s\n", o.Unit, orDash(o.Gate), state)
	if o.DenyReason != "" {
		b.WriteString(firstLine(o.DenyReason) + "\n")
	}
	if o.HowToFix != "" {
		fmt.Fprintf(&b, "Fix: %s\n", firstLine(o.HowToFix))
	}
	if o.Note != "" {
		b.WriteString(o.Note + "\n")
	}
	return b.String()
}

func (o budgetOutput) summary() string {
	if !o.Declared {
		return orDefault(o.Note, "no budget declared in batten.yaml: nothing is enforced")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · on_exceed=%s", orDash(o.Unit), orDash(o.OnExceed))
	if o.MaxIterations > 0 {
		fmt.Fprintf(&b, " · iterations %d/%d", o.Iterations, o.MaxIterations)
	}
	b.WriteString("\n")
	for _, c := range o.Ceilings {
		switch {
		case !c.Available:
			// Never "0 of N". A ceiling nobody could sample is unmeasured, and reporting it as
			// spent-nothing is the invented number principle #1 exists to forbid.
			fmt.Fprintf(&b, "  %s: NOT MEASURABLE — %s\n", c.Kind, c.UnavailableReason)
		case c.Exceeded:
			fmt.Fprintf(&b, "  %s: OVER — %.4g of %.4g\n", c.Kind, deref(c.Spent), c.Cap)
		default:
			fmt.Fprintf(&b, "  %s: %.4g of %.4g\n", c.Kind, deref(c.Spent), c.Cap)
		}
	}
	if o.Note != "" {
		b.WriteString(o.Note + "\n")
	}
	return b.String()
}

func (o writeSetOutput) summary() string {
	var b strings.Builder
	switch {
	case !o.Owned:
		fmt.Fprintf(&b, "%s is not claimed by anyone: writing it is allowed.\n", o.Path)
	case o.OwnedByYou:
		fmt.Fprintf(&b, "%s is yours (%s): writing it is allowed.\n", o.Path, orDash(o.OwnerLabel))
	default:
		fmt.Fprintf(&b, "%s is owned by %s. A Write or Edit here would be DENIED by the "+
			"write-set guard — hand the change to its owner.\n", o.Path, orDash(o.OwnerLabel))
	}
	if o.Domain != "" {
		fmt.Fprintf(&b, "domain: %s\n", o.Domain)
	}
	if len(o.YourWriteSet) > 0 {
		fmt.Fprintf(&b, "you own %d file(s): %s\n", len(o.YourWriteSet), joinCapped(o.YourWriteSet, 6))
	}
	if o.Note != "" {
		b.WriteString(o.Note + "\n")
	}
	return b.String()
}

// ---------- shared phrasing ----------

// tokenPhrase never prints a zero it did not measure. "0 tokens" and "nobody ingested a
// transcript" are opposite facts, and the second one is the common one.
func tokenPhrase(tokens int64, usd float64) string {
	if tokens <= 0 {
		return "usage NOT MEASURED"
	}
	if usd > 0 {
		return fmt.Sprintf("%s tokens, $%.2f imputed", humanTokens(tokens), usd)
	}
	return fmt.Sprintf("%s tokens, imputed cost not priced", humanTokens(tokens))
}

func verdictPhrase(v *verdictBrief) string {
	if v == nil {
		return "NO verdict (a commit would be denied)"
	}
	return fmt.Sprintf("verdict %s, %d evidence", v.Result, v.EvidenceCount)
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func joinCapped(ss []string, n int) string {
	if len(ss) <= n {
		return strings.Join(ss, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(ss[:n], ", "), len(ss)-n)
}
