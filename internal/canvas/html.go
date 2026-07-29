package canvas

// A run graph as ONE HTML file, openable in any browser.
//
// The JSON Canvas export needs Obsidian, which is a fine assumption for the person who already
// runs Obsidian and a wall for everyone else. This is the same graph with no reader required —
// what graphify's `graph.html` is, and what people actually screenshot and post.
//
// TWO CONDITIONS, both of which shape the code below:
//
//   - ONE FILE. No CDN, no external font, no asset directory. Everything — the CSS, the script,
//     the data — is inline. A "self-contained" artifact that fetches a stylesheet is not
//     self-contained; it is a page that breaks on the plane, in a locked-down network, and in
//     the airgapped environments where a governance tool is most likely to be wanted.
//   - GENERABLE MID-RUN, not only at Stop. An artifact that exists only once the work is over
//     does not get shared at the moment the person is excited about it.
//
// The honesty rules that govern every other surface apply here too, and this is the surface most
// tempted to break them because it is the one people look at: a node whose usage was never
// ingested says NOT MEASURED, never 0 tokens. An agent that claimed nothing says the write-set
// was not recorded, never "0 files".

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ArthurZizumbo/batten/internal/render"
	"github.com/ArthurZizumbo/batten/internal/store"
)

// Detail is the per-node information the HTML shows on hover and that the .canvas file has no
// room for. Every field is allowed to be absent, and absence is rendered as absence.
type Detail struct {
	Kind       string
	Domain     string
	AgentID    string
	AgentType  string
	Status     string
	Started    string
	Ended      string
	WriteSet   []string
	Tokens     int64 // 0 means NOT MEASURED, which the template says out loud
	ImputedUSD float64
	Priced     bool
}

// HTMLInput is everything the page needs. It is a struct rather than eight parameters because
// every one of them is optional in some real run, and a positional call would hide that.
type HTMLInput struct {
	Run      *store.Run
	Details  map[string]Detail // keyed by node id
	Reviewer *store.Verdict
	Batten   *store.Verdict
	Retries  int
	// Override, when set, means the gate stands open no matter what the verdicts say — and
	// the header line must say that instead of promising a denial that will not happen (#10).
	Override *store.OverrideDetail
}

// WriteHTML renders the canvas as a standalone page.
func (c *Canvas) WriteHTML(path string, in HTMLInput) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	page, err := c.HTML(in)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(page), 0o644)
}

// HTML builds the page. Split from WriteHTML so a test can assert on the bytes without a
// filesystem, and so a future `batten pr` or a server could serve it.
func (c *Canvas) HTML(in HTMLInput) (string, error) {
	type wireNode struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
		W      int    `json:"w"`
		H      int    `json:"h"`
		Color  string `json:"color"`
		Label  string `json:"label"`
		Text   string `json:"text"`
		Detail string `json:"detail"`
	}
	type wireEdge struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label"`
		Color string `json:"color"`
	}

	nodes := make([]wireNode, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		nodes = append(nodes, wireNode{
			ID: n.ID, Type: n.Type, X: n.X, Y: n.Y, W: n.Width, H: n.Height,
			Color: n.Color, Label: n.Label, Text: n.Text,
			Detail: detailText(in.Details[n.ID]),
		})
	}
	edges := make([]wireEdge, 0, len(c.Edges))
	for _, e := range c.Edges {
		edges = append(edges, wireEdge{From: e.FromNode, To: e.ToNode, Label: e.Label, Color: e.Color})
	}

	data, err := json.Marshal(struct {
		Nodes []wireNode `json:"nodes"`
		Edges []wireEdge `json:"edges"`
	}{nodes, edges})
	if err != nil {
		return "", err
	}
	// The data is embedded in a <script type="application/json"> block, so the only character
	// that can break out is a literal "</script>". Nothing here is user-controlled today, but a
	// domain name or a commit message could carry one tomorrow.
	safe := strings.ReplaceAll(string(data), "</", `<\/`)

	var b strings.Builder
	fmt.Fprintf(&b, htmlHead, html.EscapeString(runTitle(in.Run)))
	fmt.Fprintf(&b, "<header><h1>%s</h1><div class=meta>%s</div></header>\n",
		html.EscapeString(runTitle(in.Run)), headerMeta(in))
	b.WriteString(`<div id=stage><svg id=wires></svg><div id=nodes></div></div>` + "\n")
	b.WriteString(`<div id=tip></div>` + "\n")
	fmt.Fprintf(&b, "<script type=\"application/json\" id=\"g\">%s</script>\n", safe)
	b.WriteString(htmlScript)
	b.WriteString(htmlFoot)
	return b.String(), nil
}

func runTitle(r *store.Run) string {
	if r == nil {
		return "batten run"
	}
	return r.UnitID
}

// headerMeta is the one-line summary, and the place the "never invent a number" rule bites
// hardest: this is what someone reads before anything else.
func headerMeta(in HTMLInput) string {
	if in.Run == nil {
		return ""
	}
	var parts []string
	parts = append(parts, "status <b>"+html.EscapeString(in.Run.Status)+"</b>")
	if in.Run.Phase != "" {
		parts = append(parts, "phase <b>"+html.EscapeString(in.Run.Phase)+"</b>")
	}
	if in.Retries > 0 {
		parts = append(parts, fmt.Sprintf("<b>%d</b> retry/retries", in.Retries))
	}
	switch {
	case in.Run.TokensSpent > 0 && in.Run.ImputedUSD > 0:
		parts = append(parts, fmt.Sprintf("<b>%s</b> tokens · <b>%s</b> imputed <span class=q>(never billed)</span>",
			humanTokens(in.Run.TokensSpent), render.ImputedShort(in.Run.ImputedUSD, in.Run.UnpricedTokens, in.Run.TokensSpent)))
	case in.Run.TokensSpent > 0:
		parts = append(parts, fmt.Sprintf("<b>%s</b> tokens · imputed <b>not priced</b>", humanTokens(in.Run.TokensSpent)))
	default:
		parts = append(parts, `usage <b>NOT MEASURED</b> <span class=q>(not zero — unknown)</span>`)
	}
	if in.Run.BaseSHA != "" {
		parts = append(parts, "anchor <code>"+html.EscapeString(shortSHA(in.Run.BaseSHA))+"</code>")
	}
	parts = append(parts, gateLine(in))
	return strings.Join(parts, " · ")
}

// gateLine says whether this run could land, in the same words every other surface uses.
// The override is checked first because that is the order the hook decides in: an open
// override makes every other answer here moot.
func gateLine(in HTMLInput) string {
	if in.Override != nil {
		return `<span class=warn>gate OPEN by override — a commit lands unverified (audited)</span>`
	}
	switch {
	case in.Reviewer == nil && in.Batten == nil:
		return `<span class=bad>no verdict — a commit would be denied</span>`
	case in.Batten == nil:
		return `<span class=warn>checks never run</span>`
	case in.Batten.Result != "ok":
		return `<span class=bad>checks failed</span>`
	case in.Reviewer == nil:
		return `<span class=warn>nobody judged the work</span>`
	case len(in.Reviewer.Evidence) == 0:
		return `<span class=bad>the approval cites nothing</span>`
	case in.Reviewer.Result != "ok":
		return `<span class=bad>reviewer said ` + html.EscapeString(in.Reviewer.Result) + `</span>`
	}
	return `<span class=good>batten-verified</span>`
}

// detailText builds the hover panel for one node. Absence is spelled out rather than skipped:
// a blank line where a number should be reads as zero.
func detailText(d Detail) string {
	if d.Kind == "" {
		return ""
	}
	var b strings.Builder
	if d.Domain != "" {
		fmt.Fprintf(&b, "domain: %s\n", d.Domain)
	}
	if d.AgentID != "" {
		fmt.Fprintf(&b, "agent: %s", d.AgentID)
		if d.AgentType != "" {
			fmt.Fprintf(&b, " (%s)", d.AgentType)
		}
		b.WriteString("\n")
	}
	if d.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", d.Status)
	}

	if d.Kind == "subagent" {
		switch len(d.WriteSet) {
		case 0:
			b.WriteString("write-set: NOT RECORDED\n")
		default:
			fmt.Fprintf(&b, "write-set (%d):\n", len(d.WriteSet))
			files := append([]string(nil), d.WriteSet...)
			sort.Strings(files)
			for _, f := range files {
				fmt.Fprintf(&b, "  %s\n", f)
			}
		}
	}

	switch {
	case d.Tokens > 0 && d.Priced:
		fmt.Fprintf(&b, "tokens: %s · $%.3f imputed\n", humanTokens(d.Tokens), d.ImputedUSD)
	case d.Tokens > 0:
		fmt.Fprintf(&b, "tokens: %s · not priced\n", humanTokens(d.Tokens))
	default:
		// The rule that matters most on the surface people look at.
		b.WriteString("usage: NOT MEASURED (not zero — unknown)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

const htmlHead = `<!doctype html>
<html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>%s — batten</title>
<style>
:root{--bg:#0d1117;--fg:#e6edf3;--dim:#8b949e;--line:#30363d;--card:#161b22;
--ok:#1a7f37;--bad:#cf222e;--warn:#9a6700;--run:#0969da;--purple:#8250df}
@media(prefers-color-scheme:light){:root{--bg:#fff;--fg:#1f2328;--dim:#59636e;--line:#d1d9e0;--card:#f6f8fa}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);
font:14px/1.5 ui-sans-serif,-apple-system,Segoe UI,Roboto,sans-serif}
header{padding:16px 20px;border-bottom:1px solid var(--line)}
h1{margin:0 0 4px;font-size:20px}
.meta{color:var(--dim);font-size:13px}
.meta b{color:var(--fg);font-weight:600}
.q{opacity:.75;font-style:italic}
.good{color:#3fb950;font-weight:600}.bad{color:#f85149;font-weight:600}.warn{color:#d29922;font-weight:600}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}
#stage{position:relative;overflow:hidden;height:calc(100vh - 110px);cursor:grab}
#stage.drag{cursor:grabbing}
#wires{position:absolute;inset:0;width:100%%;height:100%%;pointer-events:none;overflow:visible}
#nodes{position:absolute;inset:0;transform-origin:0 0}
.n{position:absolute;border:1px solid var(--line);border-radius:8px;background:var(--card);
padding:10px 12px;overflow:hidden;font-size:13px}
.n.group{background:transparent;border-style:dashed;opacity:.65}
.n h2{margin:0 0 4px;font-size:14px}
.n .st{font-family:ui-monospace,monospace;font-size:11px;color:var(--dim)}
.n.ok{border-color:var(--ok)}.n.fail{border-color:var(--bad)}
.n.warn{border-color:var(--warn)}.n.running{border-color:var(--run)}
.n.ok h2::after{content:" ✓";color:#3fb950}
.n.fail h2::after{content:" ✗";color:#f85149}
#tip{position:fixed;display:none;max-width:460px;background:var(--card);color:var(--fg);
border:1px solid var(--line);border-radius:8px;padding:10px 12px;font-size:12px;
font-family:ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;
box-shadow:0 8px 24px rgba(0,0,0,.4);pointer-events:none;z-index:9}
footer{position:fixed;bottom:0;left:0;right:0;padding:6px 20px;font-size:11px;color:var(--dim);
border-top:1px solid var(--line);background:var(--bg)}
footer a{color:inherit}
</style></head><body>
`

const htmlScript = `<script>
(function(){
 var g=JSON.parse(document.getElementById('g').textContent);
 var wrap=document.getElementById('nodes'),svg=document.getElementById('wires'),
     stage=document.getElementById('stage'),tip=document.getElementById('tip');
 var NS='http://www.w3.org/2000/svg';
 var cls={'1':'fail','2':'warn','3':'warn','4':'ok','6':'running'};
 var pos={};

 // Groups first, so a phase box sits behind the nodes it contains rather than over them.
 g.nodes.slice().sort(function(a,b){return (a.type==='group'?0:1)-(b.type==='group'?0:1)})
  .forEach(function(n){
   pos[n.id]=n;
   var d=document.createElement('div');
   d.className='n '+(n.type==='group'?'group ':'')+(cls[n.color]||'');
   d.style.left=n.x+'px';d.style.top=n.y+'px';d.style.width=n.w+'px';d.style.height=n.h+'px';
   var t=n.label||n.text||'';
   var lines=String(t).split('\n').filter(Boolean);
   var head=(lines.shift()||'').replace(/^#+\s*/,'').replace(/\*\*/g,'');
   d.innerHTML='<h2></h2><div class=st></div>';
   d.querySelector('h2').textContent=head;
   // \x60 is a backtick: written as an escape because this whole script lives inside a Go raw
   // string literal, which a literal backtick would close.
   d.querySelector('.st').textContent=lines.join('\n').replace(/[\x60*]/g,'');
   if(n.detail){
     d.addEventListener('mousemove',function(e){
       tip.textContent=n.detail;tip.style.display='block';
       var x=e.clientX+14,y=e.clientY+14;
       if(x+470>innerWidth)x=e.clientX-470;
       if(y+160>innerHeight)y=e.clientY-160;
       tip.style.left=x+'px';tip.style.top=y+'px';
     });
     d.addEventListener('mouseleave',function(){tip.style.display='none'});
   }
   wrap.appendChild(d);
 });

 g.edges.forEach(function(e){
   var a=pos[e.from],b=pos[e.to];if(!a||!b)return;
   var l=document.createElementNS(NS,'line');
   l.setAttribute('x1',a.x+a.w);l.setAttribute('y1',a.y+a.h/2);
   l.setAttribute('x2',b.x);l.setAttribute('y2',b.y+b.h/2);
   l.setAttribute('stroke',e.color==='2'?'#d29922':e.color==='1'?'#f85149':'#6e7681');
   l.setAttribute('stroke-width','2');
   if(e.label)l.setAttribute('stroke-dasharray','6 4');
   svg.appendChild(l);
   if(e.label){
     var tx=document.createElementNS(NS,'text');
     tx.setAttribute('x',(a.x+a.w+b.x)/2);tx.setAttribute('y',(a.y+a.h/2+b.y+b.h/2)/2-6);
     tx.setAttribute('fill','#d29922');tx.setAttribute('font-size','11');
     tx.setAttribute('text-anchor','middle');tx.textContent=e.label;
     svg.appendChild(tx);
   }
 });

 // Fit the whole graph on load, then let the reader pan and zoom.
 var xs=g.nodes.map(function(n){return n.x}),ys=g.nodes.map(function(n){return n.y});
 var xe=g.nodes.map(function(n){return n.x+n.w}),ye=g.nodes.map(function(n){return n.y+n.h});
 var minX=Math.min.apply(null,xs.concat([0])),minY=Math.min.apply(null,ys.concat([0]));
 var w=Math.max.apply(null,xe.concat([1]))-minX,h=Math.max.apply(null,ye.concat([1]))-minY;
 var k=Math.min(stage.clientWidth/(w+80),stage.clientHeight/(h+80),1);
 var tx=-minX*k+40,ty=-minY*k+40;
 function apply(){
   var m='translate('+tx+'px,'+ty+'px) scale('+k+')';
   wrap.style.transform=m;svg.style.transform=m;svg.style.transformOrigin='0 0';
 }
 apply();
 var down=false,px=0,py=0;
 stage.addEventListener('mousedown',function(e){down=true;px=e.clientX;py=e.clientY;stage.classList.add('drag')});
 addEventListener('mouseup',function(){down=false;stage.classList.remove('drag')});
 addEventListener('mousemove',function(e){if(!down)return;tx+=e.clientX-px;ty+=e.clientY-py;px=e.clientX;py=e.clientY;apply()});
 stage.addEventListener('wheel',function(e){
   e.preventDefault();
   var f=e.deltaY<0?1.1:1/1.1,nk=Math.min(3,Math.max(.15,k*f));
   var r=stage.getBoundingClientRect(),mx=e.clientX-r.left,my=e.clientY-r.top;
   tx=mx-(mx-tx)*(nk/k);ty=my-(my-ty)*(nk/k);k=nk;apply();
 },{passive:false});
})();
</script>
`

const htmlFoot = `<footer>Generated by <a href="https://github.com/ArthurZizumbo/batten">batten</a>
— one file, no network. The graph is the path the run actually took, read from its record.</footer>
</body></html>
`
