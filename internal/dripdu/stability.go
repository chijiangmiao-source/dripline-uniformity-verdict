package dripdu

import (
	"math/big"
	"sort"
)

// Stability is the branch-level conclusion of a repeated-measurement
// fluctuation analysis, derived from the single worst point.
type Stability string

const (
	// StabilityStable means every point's range is at most 5% of its median.
	StabilityStable Stability = "stable"
	// StabilityWatch means the worst point's range exceeds 5% but stays
	// within 10% of its median.
	StabilityWatch Stability = "watch"
	// StabilityVolatile means the worst point's range exceeds 10% of its
	// median.
	StabilityVolatile Stability = "volatile"
)

// StabilityPoint is the per-point fluctuation profile across all rounds.
type StabilityPoint struct {
	ID string
	// Median is the exact median of the point's per-round flows; for an even
	// round count it is the mean of the two middle values.
	Median *big.Rat
	// Range is the exact spread (max - min) of the point's flows.
	Range *big.Rat
	// Percent is Range / Median * 100 carried as an exact fraction; it is
	// never rounded before the branch conclusion is derived from it.
	Percent *big.Rat
	// PercentRounded is Percent rendered with decimal ROUND_HALF_UP to two
	// places, for display only.
	PercentRounded string
}

// StabilityResult is the fully recomputable fluctuation analysis of one
// repeated-measurement run.
type StabilityResult struct {
	RoundCount int
	PointCount int
	// Points holds one entry per measurement point, in first-round order.
	Points []StabilityPoint
	// WorstPointID is the point with the highest exact Percent; ties resolve
	// to the earliest point in first-round order.
	WorstPointID string
	// WorstPercent is the exact fluctuation percentage of WorstPointID and
	// the value the branch conclusion is derived from.
	WorstPercent        *big.Rat
	WorstPercentRounded string
	Stability           Stability
}

// AnalyzeStability evaluates 3..10 rounds of 4..64 validated measurements.
// Every round must carry the same set of point ids (enforced during request
// validation); the first round fixes the point order of the report.
func AnalyzeStability(rounds [][]Measurement) StabilityResult {
	m := len(rounds)
	n := len(rounds[0])

	// Index each round's flows by point id so later rounds may list the
	// points in any order; the report always follows the first round.
	byID := make([]map[string]*big.Rat, m)
	for i, round := range rounds {
		byID[i] = make(map[string]*big.Rat, len(round))
		for _, p := range round {
			byID[i][p.ID] = p.Flow
		}
	}

	points := make([]StabilityPoint, 0, n)
	worstIdx := -1
	var worstPercent *big.Rat
	for j, first := range rounds[0] {
		values := make([]*big.Rat, m)
		for i := range rounds {
			values[i] = byID[i][first.ID]
		}
		sort.Slice(values, func(a, b int) bool {
			return values[a].Cmp(values[b]) < 0
		})

		var median *big.Rat
		if m%2 == 1 {
			median = new(big.Rat).Set(values[m/2])
		} else {
			median = new(big.Rat).Add(values[m/2-1], values[m/2])
			median.Quo(median, big.NewRat(2, 1))
		}

		spread := new(big.Rat).Sub(values[m-1], values[0])
		percent := new(big.Rat).Quo(spread, median)
		percent.Mul(percent, big.NewRat(100, 1))

		points = append(points, StabilityPoint{
			ID:             first.ID,
			Median:         median,
			Range:          spread,
			Percent:        percent,
			PercentRounded: roundedDecimal(percent, 2),
		})
		// Strictly greater keeps the earliest point on ties.
		if worstPercent == nil || percent.Cmp(worstPercent) > 0 {
			worstPercent = percent
			worstIdx = j
		}
	}

	return StabilityResult{
		RoundCount:          m,
		PointCount:          n,
		Points:              points,
		WorstPointID:        points[worstIdx].ID,
		WorstPercent:        worstPercent,
		WorstPercentRounded: roundedDecimal(worstPercent, 2),
		Stability:           stabilityFromPercent(worstPercent),
	}
}

// stabilityFromPercent classifies the exact (unrounded) worst fluctuation
// percentage: within 5% is stable, above 5% and at most 10% needs attention,
// above 10% is volatile. The two-decimal rendering in the response is display
// only and never feeds this decision.
func stabilityFromPercent(percent *big.Rat) Stability {
	switch {
	case percent.Cmp(big.NewRat(5, 1)) <= 0:
		return StabilityStable
	case percent.Cmp(big.NewRat(10, 1)) <= 0:
		return StabilityWatch
	default:
		return StabilityVolatile
	}
}
