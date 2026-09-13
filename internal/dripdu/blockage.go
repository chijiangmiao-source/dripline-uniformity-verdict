package dripdu

import (
	"errors"
	"math/big"
	"sort"
)

// ErrNoMedian marks a diagnosis that cannot establish its baseline because no
// measurements were supplied. The HTTP contract rejects such requests while
// parsing them, so this error only reaches callers that bypass validation.
var ErrNoMedian = errors.New("cannot form the median baseline without measurements")

// blockageCutoff is the share of the median below which a point is suspected
// of low flow. The comparison is strict: exactly 85.000...% is still healthy.
var blockageCutoff = big.NewRat(85, 100)

// DiagnosisPoint is one point's profile relative to the median baseline.
type DiagnosisPoint struct {
	ID string
	// Value is the exact diagnostic quantity: the supply ratio Flow/Rated in
	// rated mode, the measured flow otherwise.
	Value *big.Rat
	// BaselineRatio is Value / median * 100 carried as an exact fraction: the
	// point's exact percentage of the baseline. The suspected flag derives
	// from this fraction, never from its rounded display rendering.
	BaselineRatio *big.Rat
	// BaselineRatioRounded is BaselineRatio rendered with decimal
	// ROUND_HALF_UP to two places, for display only.
	BaselineRatioRounded string
	// Suspected marks points whose Value is strictly below 85% of the median.
	Suspected bool
}

// BlockageSection is one maximal run of adjacent suspected points, read in
// the branch's installation (input) order.
type BlockageSection struct {
	StartID   string
	EndID     string
	MemberIDs []string
}

// BlockageResult is the fully recomputable blocked-section diagnosis of one
// acceptance request.
type BlockageResult struct {
	// Basis reports whether the median and ratios were computed from measured
	// flows or from supply ratios.
	Basis CalculationBasis
	// Median is the exact median of all diagnostic values: the middle value
	// for an odd point count, the mean of the two middle values for an even
	// one.
	Median *big.Rat
	// Points holds one entry per measurement, in input order.
	Points []DiagnosisPoint
	// Sections lists the adjacent suspected runs in input order; it is empty
	// (never nil) when no point is suspected.
	Sections []BlockageSection
}

// DiagnoseBlockage locates concentrated low-flow regions along a validated
// branch. Each point's diagnostic value is its supply ratio Flow/Rated when
// every point carries a rated flow, its measured flow otherwise. The exact
// median of all values is the baseline: points strictly below 85% of it are
// suspected, and maximal runs of adjacent suspected points become sections.
func DiagnoseBlockage(points []Measurement) (BlockageResult, error) {
	n := len(points)
	if n == 0 {
		return BlockageResult{}, ErrNoMedian
	}

	// Same basis rule as the acceptance adjudication: rated mode applies only
	// when every point carries a rated flow; mixed requests are rejected
	// during request validation.
	rated := true
	for _, p := range points {
		if p.Rated == nil {
			rated = false
			break
		}
	}
	basis := BasisMeasuredFlow
	if rated {
		basis = BasisSupplyRatio
	}

	// values keeps input order and is what the per-point ratios and runs use;
	// ordered is only sorted to locate the median.
	values := make([]*big.Rat, n)
	for i, p := range points {
		if rated {
			values[i] = new(big.Rat).Quo(p.Flow, p.Rated)
		} else {
			values[i] = new(big.Rat).Set(p.Flow)
		}
	}

	ordered := make([]*big.Rat, n)
	copy(ordered, values)
	sort.Slice(ordered, func(a, b int) bool {
		return ordered[a].Cmp(ordered[b]) < 0
	})
	var median *big.Rat
	if n%2 == 1 {
		median = new(big.Rat).Set(ordered[n/2])
	} else {
		median = new(big.Rat).Add(ordered[n/2-1], ordered[n/2])
		median.Quo(median, big.NewRat(2, 1))
	}

	cutoff := new(big.Rat).Mul(median, blockageCutoff)
	hundred := big.NewRat(100, 1)

	out := make([]DiagnosisPoint, n)
	sections := make([]BlockageSection, 0)
	runStart := -1
	closeRun := func(end int) {
		members := make([]string, 0, end-runStart+1)
		for j := runStart; j <= end; j++ {
			members = append(members, points[j].ID)
		}
		sections = append(sections, BlockageSection{
			StartID:   points[runStart].ID,
			EndID:     points[end].ID,
			MemberIDs: members,
		})
	}

	for i := range points {
		ratio := new(big.Rat).Quo(values[i], median)
		ratio.Mul(ratio, hundred)
		suspected := values[i].Cmp(cutoff) < 0

		out[i] = DiagnosisPoint{
			ID:                   points[i].ID,
			Value:                values[i],
			BaselineRatio:        ratio,
			BaselineRatioRounded: roundedDecimal(ratio, 2),
			Suspected:            suspected,
		}

		switch {
		case suspected && runStart < 0:
			runStart = i
		case !suspected && runStart >= 0:
			closeRun(i - 1)
			runStart = -1
		}
	}
	if runStart >= 0 {
		closeRun(n - 1)
	}

	return BlockageResult{
		Basis:    basis,
		Median:   median,
		Points:   out,
		Sections: sections,
	}, nil
}
