package canvas

import (
	"strings"
	"testing"

	"github.com/ArthurZizumbo/batten/internal/store"
)

func htmlFixture(t *testing.T) (string, HTMLInput) {
	t.Helper()
	run, nodes, edges, rv, bv := fixture()
	in := HTMLInput{
		Run: run, Reviewer: rv, Batten: bv, Retries: 1,
		Details: map[string]Detail{
			"n-a1": {Kind: "subagent", Domain: "api", AgentID: "api-1", Status: "failed"},
			"n-a2": {Kind: "subagent", Domain: "api", AgentID: "api-2", Status: "ok",
				WriteSet: []string{"api/rate.go"}, Tokens: 412_000, ImputedUSD: 3.2, Priced: true},
			"n-b1": {Kind: "subagent", Domain: "web", AgentID: "web-1", Status: "running"},
		},
	}
	page, err := Render(run, nodes, edges, rv, bv).HTML(in)
	if err != nil {
		t.Fatal(err)
	}
	return page, in
}

// TestTheHTMLIsGenuinelyOneFile is the whole promise of the format.
//
// A "self-contained" artifact that fetches a stylesheet is not self-contained: it is a page that
// breaks on a plane, on a locked-down corporate network, and in the airgapped environments where
// a governance tool is most likely to be wanted. Verified in a real browser too — the only
// request the page provoked was the browser's own favicon probe.
func TestTheHTMLIsGenuinelyOneFile(t *testing.T) {
	page, _ := htmlFixture(t)

	for _, forbidden := range []string{
		"<script src", "<link ", "@import", "cdn.", "googleapis", "unpkg", "jsdelivr",
		"src=\"http", "url(http",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page reaches outside itself (%q); it is not self-contained", forbidden)
		}
	}
	// The pieces that make it work have to actually be inline.
	for _, want := range []string{"<style>", "<script", "application/json"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page is missing its inline %q", want)
		}
	}
	if !strings.HasPrefix(page, "<!doctype html>") {
		t.Error("a browser needs a doctype to render this in standards mode")
	}
}

// The honesty rules apply hardest here, because this is the surface people screenshot.
func TestTheHTMLNeverInventsANumber(t *testing.T) {
	page, _ := htmlFixture(t)

	if !strings.Contains(page, "NOT MEASURED") {
		t.Errorf("the fixture run has no ingested usage, so the header must say so:\n%s", page)
	}
	if strings.Contains(page, ">0 tokens") || strings.Contains(page, "$0.00") {
		t.Error("the page printed a zero for usage nobody measured")
	}
	// A subagent that claimed nothing says so, rather than showing an empty list that reads as
	// "owned nothing".
	if !strings.Contains(page, "write-set: NOT RECORDED") {
		t.Errorf("an unclaimed write-set must be reported as unrecorded:\n%s", page)
	}
	// And one that DID claim shows what it owns.
	if !strings.Contains(page, "api/rate.go") {
		t.Error("a claimed write-set is missing from the detail panel")
	}
}

// The header's gate line is the first thing read, so it must agree with the gate itself.
func TestTheHeaderGateLineIsHonest(t *testing.T) {
	run, nodes, edges, rv, bv := fixture()

	cases := []struct {
		name     string
		reviewer *store.Verdict
		batten   *store.Verdict
		want     string
	}{
		{"nothing at all", nil, nil, "no verdict"},
		{"checks never run", rv, nil, "checks never run"},
		{"nobody reviewed", nil, bv, "nobody judged the work"},
		{"reviewer blocked", rv, bv, "reviewer said blocked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, err := Render(run, nodes, edges, c.reviewer, c.batten).
				HTML(HTMLInput{Run: run, Reviewer: c.reviewer, Batten: c.batten})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(page, c.want) {
				t.Errorf("the header does not say %q:\n%s", c.want, headerOf(page))
			}
			if strings.Contains(page, ">batten-verified<") {
				t.Errorf("claimed batten-verified with %s", c.name)
			}
		})
	}
}

// A run graph carrying "</script>" in a label would otherwise close the data block early and
// break the page — or worse. Nothing user-controlled reaches it today; a commit message or a
// domain name could tomorrow.
func TestTheEmbeddedDataCannotCloseItsOwnScriptTag(t *testing.T) {
	run := &store.Run{RunID: "r1", Project: "p", UnitID: "US-1", Status: "running"}
	nodes := []store.Node{{
		NodeID: "n1", RunID: "r1", Kind: "subagent",
		Domain: `evil</script><script>alert(1)</script>`, Status: "ok",
	}}
	page, err := Render(run, nodes, nil, nil, nil).HTML(HTMLInput{Run: run})
	if err != nil {
		t.Fatal(err)
	}
	// One opening data block and the page's own script; no injected third one.
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Errorf("a node label closed the data block and injected a script:\n%s", page)
	}
}

func headerOf(page string) string {
	i := strings.Index(page, "<header>")
	if i < 0 {
		return page
	}
	rest := page[i:]
	if j := strings.Index(rest, "</header>"); j > 0 {
		return rest[:j]
	}
	return rest
}
