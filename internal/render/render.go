// Package render holds the small formatting rules that more than one batten surface must
// agree on. They live in one place because every divergence between two surfaces printing
// the same fact has, so far, become a field-test finding: four surfaces each rendered the
// imputed-dollar figure on their own and none of them could say part of the spend was
// unpriced (#33), and three private copies of the token formatter disagreed about 42.6k (#36).
package render

import (
	"fmt"
	"math"
)

// UnpricedShare is the percentage (0–100) of a run's tokens that carry no published rate,
// rounded to the nearest whole point.
func UnpricedShare(unpriced, total int64) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(float64(unpriced) * 100 / float64(total)))
}

// ImputedShort renders a run-level imputed-dollar figure for a table cell. A model with no
// published rate contributes tokens but no dollars, so a run that used one has a FLOOR, not
// a total — and the cell must read as one. A fully unpriced run gets no dollar figure at
// all: "$0.00" would price the unpriceable as free.
func ImputedShort(usd float64, unpriced, total int64) string {
	switch {
	case total > 0 && unpriced >= total:
		return "not priced"
	case unpriced > 0:
		return fmt.Sprintf("≥$%.2f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}

// ImputedLong is ImputedShort for prose: it names the gap instead of only marking it, so a
// skeptical reader doing the arithmetic reaches the same conclusion batten did.
func ImputedLong(usd float64, unpriced, total int64) string {
	switch {
	case total > 0 && unpriced >= total:
		return "imputed cost not priced (no published rate for this run's models; tokens exact)"
	case unpriced > 0:
		return fmt.Sprintf("≥$%.2f imputed — a floor, not a total: %d%% of the tokens have no published rate",
			usd, UnpricedShare(unpriced, total))
	}
	return fmt.Sprintf("$%.2f imputed (what this would have cost on the API)", usd)
}
