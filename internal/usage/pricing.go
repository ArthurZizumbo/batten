package usage

import (
	"strings"
	"time"
)

// Published list prices, USD per MILLION tokens. This table is the only place a number
// is allowed to be asserted; everything else in this package derives from it.
//
// A model we have no entry for is UNPRICED, not free. rateFor returns ok == false and the
// caller emits the row with ImputedUSD == 0 plus the model id, so the surface can say
// "we could not price this" instead of quietly reporting a cost that is too low. Guessing
// a rate for an unrecognized model would corrupt the budget ledger in the one direction
// that matters — downward — so we refuse to.
type rate struct {
	in, out float64

	// Fast mode is an Opus-only tier and is priced separately, not derived. A model with
	// no published fast rate leaves these at 0 and falls back to the standard rate rather
	// than inventing a premium.
	fastIn, fastOut float64
}

func (r rate) hasFast() bool { return r.fastIn > 0 && r.fastOut > 0 }

// Model ids carry date suffixes (claude-haiku-4-5-20251001), so entries are PREFIXES.
// rateFor takes the LONGEST match, which keeps the table correct if a future id is ever a
// prefix of another rather than depending on the order lines happen to be written in.
type modelRate struct {
	prefix string
	rate   rate
}

const (
	opusIn, opusOut         = 5.0, 25.0
	opusFastIn, opusFastOut = 10.0, 50.0
)

var opus = rate{in: opusIn, out: opusOut, fastIn: opusFastIn, fastOut: opusFastOut}

var rates = []modelRate{
	{"claude-opus-4-8", opus},
	{"claude-opus-4-7", opus},
	{"claude-opus-4-6", opus},
	{"claude-opus-4-5", opus},

	{"claude-sonnet-4-6", rate{in: 3, out: 15}},
	{"claude-sonnet-4-5", rate{in: 3, out: 15}},

	{"claude-haiku-4-5", rate{in: 1, out: 5}},

	{"claude-fable-5", rate{in: 10, out: 50}},
	{"claude-mythos-5", rate{in: 10, out: 50}},

	// claude-sonnet-5 is deliberately absent here: its rate depends on the date, so it is
	// resolved in rateFor. See sonnet5Repricing.
}

// sonnet5Repricing is the day the published Sonnet 5 rate goes from $2/$10 to $3/$15.
//
// Pricing is keyed off the RECORD's own timestamp rather than wall-clock time. A transcript
// re-parsed after the cutover must still be priced at the rate it was actually billed at,
// otherwise every historical run silently reprices itself the moment the calendar rolls over
// and the ledger stops matching what happened.
var sonnet5Repricing = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

var (
	sonnet5Before = rate{in: 2, out: 10}
	sonnet5After  = rate{in: 3, out: 15}
)

// Derived multipliers on the INPUT price. Cache writes are more expensive than input because
// the write is persisted; reads are a tenth of it. The 5m and 1h buckets are NOT
// interchangeable and must never be collapsed into one "cache write" number.
const (
	cacheWrite5mMul = 1.25
	cacheWrite1hMul = 2.0
	cacheReadMul    = 0.1

	// US-pinned inference carries a premium on the request.
	geoUSMul = 1.1

	// Server-side web search: $10 per 1,000 requests.
	webSearchUSD = 0.01
)

// rateFor resolves a model id to its published rate at time at.
// ok == false means "we do not know what this costs", which is a fact the caller must be
// able to report — not a zero to fold silently into a total.
func rateFor(model string, at time.Time) (rate, bool) {
	if strings.HasPrefix(model, "claude-sonnet-5") {
		if at.Before(sonnet5Repricing) {
			return sonnet5Before, true
		}
		return sonnet5After, true
	}

	var best modelRate
	for _, m := range rates {
		if strings.HasPrefix(model, m.prefix) && len(m.prefix) > len(best.prefix) {
			best = m
		}
	}
	if best.prefix == "" {
		return rate{}, false
	}
	return best.rate, true
}

// Price returns the imputed USD for one request: what it WOULD have cost on the API.
// On a subscription this is not a bill — it is the value pulled out of the plan.
// ok == false means the model is unpriced; cost is then 0.
//
// This prices at TODAY's rate. Parse does not call it: Parse prices each record at the
// record's own timestamp (see priceAt), so re-parsing an old transcript stays correct across
// the Sonnet 5 repricing. Use Price for "what would this cost me now" questions.
func Price(model, speed string, inTok, outTok, cw5m, cw1h, cacheRead, webSearches int64, geoUS bool) (usd float64, ok bool) {
	return priceAt(model, speed, inTok, outTok, cw5m, cw1h, cacheRead, webSearches, geoUS, time.Now().UTC())
}

// priceAt is Price with the pricing date made explicit, which is what makes an old transcript
// reprice to the same number it did the day it was written.
func priceAt(model, speed string, inTok, outTok, cw5m, cw1h, cacheRead, webSearches int64, geoUS bool, at time.Time) (float64, bool) {
	r, ok := rateFor(model, at)
	if !ok {
		// Do not guess. The tokens are still real and the caller still records them; only the
		// price is unknown, and an unknown price is reported as unknown.
		return 0, false
	}

	in, out := r.in, r.out
	// "fast" on a model with no published fast rate falls back to standard rather than
	// inventing a premium: an invented multiplier is a fabricated number.
	if speed == "fast" && r.hasFast() {
		in, out = r.fastIn, r.fastOut
	}

	// Every cache multiplier is relative to the INPUT rate of the tier actually used, so fast
	// mode stacks: a fast 1h cache write is 2x the fast input price, not 2x the standard one.
	usd := (float64(inTok)*in +
		float64(cw5m)*in*cacheWrite5mMul +
		float64(cw1h)*in*cacheWrite1hMul +
		float64(cacheRead)*in*cacheReadMul +
		float64(outTok)*out) / 1e6

	usd += float64(webSearches) * webSearchUSD

	if geoUS {
		usd *= geoUSMul
	}
	return usd, true
}
