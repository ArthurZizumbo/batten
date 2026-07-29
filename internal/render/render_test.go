package render

import (
	"strings"
	"testing"
)

// Finding #33: a run with 38% of its tokens on an unpriced model printed `$0.39` as though it
// were a total. The dollars for that share do not exist — the figure is a floor, and both
// renderings must read as one. The controls pin the two honest neighbours: a fully priced
// figure stays a plain total, and a fully unpriced one gets no dollar figure at all.
func TestImputedRendersPartialPricingAsAFloor(t *testing.T) {
	if got := ImputedShort(0.39, 80_000, 210_000); got != "≥$0.39" {
		t.Errorf("partially priced short = %q, want ≥$0.39", got)
	}
	long := ImputedLong(0.39, 80_000, 210_000)
	if !strings.Contains(long, "floor") || !strings.Contains(long, "38%") {
		t.Errorf("partially priced long = %q; must call itself a floor and size the gap", long)
	}

	// Control: fully priced stays a plain total.
	if got := ImputedShort(0.39, 0, 210_000); got != "$0.39" {
		t.Errorf("fully priced short = %q, want $0.39", got)
	}

	// Control: fully unpriced gets no dollar figure — "$0.00" would price it as free.
	if got := ImputedShort(0, 210_000, 210_000); got != "not priced" {
		t.Errorf("fully unpriced short = %q, want 'not priced'", got)
	}
	if long := ImputedLong(0, 210_000, 210_000); strings.Contains(long, "$") {
		t.Errorf("fully unpriced long = %q; must not contain a dollar figure", long)
	}
}

// TestTokens is the canonical case table. Five packages carried a private copy of this
// renderer and a sixth hand-rolled `%.1fM` — which is how a measured 42,600 tokens became
// "0.0M" in the session brief while `batten runs` printed 42.6k from the same row (#36).
func TestTokens(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{42_600, "42.6k"},
		{1_400_000, "1.4M"},
	}
	for _, c := range cases {
		if got := Tokens(c.in); got != c.want {
			t.Errorf("Tokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnpricedShare(t *testing.T) {
	cases := []struct {
		unpriced, total int64
		want            int
	}{
		{0, 100, 0},
		{38, 100, 38},
		{80_000, 210_000, 38},
		{1, 3, 33},
		{100, 100, 100},
		{5, 0, 0}, // no total, no share — never divide by zero
	}
	for _, c := range cases {
		if got := UnpricedShare(c.unpriced, c.total); got != c.want {
			t.Errorf("UnpricedShare(%d, %d) = %d, want %d", c.unpriced, c.total, got, c.want)
		}
	}
}
