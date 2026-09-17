package aggregate

import (
	"math"

	"github.com/Erik-Schuetze/league-of-legends/internal/aggmodel"
)

// Tier scoring.
//
// The vocabulary (S+ .. D) is frozen in aggmodel; the thresholds are the
// aggregation engineer's to choose and to document, so they live here, next to
// the reasoning, rather than in a config file.
//
// A cell is graded on the difference between its win rate and the window
// baseline win rate, in percentage points. Grading against the window baseline
// rather than against a fixed 50 percent is what makes the published ladder
// say something: every match has one winner, so a fixed 50 percent target
// would put half the champions "above average" through no merit of their own,
// and a patch where top lane is weak would be invisible.
//
// The bands are deliberately wide relative to the confidence half width at the
// default min_cell_n of 100 (0.98/sqrt(100) = 0.098, i.e. 9.8 percentage
// points at the worst case rate). A tier is therefore a statement about a
// champion's position in a patch window, not a significance claim about its
// win rate: ci95_half_width travels with every cell so a reader can see how
// much of a 0.6 point step is inside the noise. Grading on a shrunk estimate
// is the natural next step and is recorded as backlog in docs/aggregation.md.
type tierBand struct {
	tier aggmodel.Tier
	// minPP is the inclusive lower bound of the band, in percentage points
	// relative to the baseline win rate.
	minPP float64
}

// tierBands is ordered best to worst; the first band whose lower bound the
// difference meets or exceeds is the tier. The last band is unbounded below so
// that the function is total.
var tierBands = []tierBand{
	{tier: aggmodel.TierSPlus, minPP: 2.0},
	{tier: aggmodel.TierS, minPP: 1.2},
	{tier: aggmodel.TierA, minPP: 0.6},
	{tier: aggmodel.TierB, minPP: -0.6},
	{tier: aggmodel.TierC, minPP: -1.5},
	{tier: aggmodel.TierD, minPP: math.Inf(-1)},
}

// tierForRate grades a raw win rate against the window baseline.
//
// The unrounded rate is used: the published win_rate is rounded to four
// decimals for artifact stability, and grading the rounded value would make the
// tier of a cell sitting exactly on a boundary depend on the rounding rule.
func tierForRate(baseline, rate float64) aggmodel.Tier {
	diffPP := (rate - baseline) * 100
	for _, band := range tierBands {
		if diffPP >= band.minPP {
			return band.tier
		}
	}
	return aggmodel.TierD
}

// TierThresholds exposes the ladder for documentation and tests, in best to
// worst order.
func TierThresholds() []struct {
	Tier  aggmodel.Tier
	MinPP float64
} {
	out := make([]struct {
		Tier  aggmodel.Tier
		MinPP float64
	}, 0, len(tierBands))
	for _, band := range tierBands {
		out = append(out, struct {
			Tier  aggmodel.Tier
			MinPP float64
		}{Tier: band.tier, MinPP: band.minPP})
	}
	return out
}
