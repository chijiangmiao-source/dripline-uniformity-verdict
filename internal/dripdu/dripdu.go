// Package dripdu computes the distribution uniformity (DU) verdict for a
// drip-irrigation branch acceptance run.
//
// Every calculation is performed with exact rational arithmetic: flow values
// are decimal fractions with at most three places, so sums are exact and the
// two means and DU are carried as exact fractions. Only the final DU value is
// rounded, using decimal ROUND_HALF_UP to two decimal places. The verdict is
// then derived from that single rounded value, which guarantees exactly one
// acceptance conclusion for borderline branches.
package dripdu

import (
	"math/big"
	"sort"
)

// Verdict is the acceptance decision for one branch.
type Verdict string

const (
	// VerdictPass means DU >= 90.00.
	VerdictPass Verdict = "pass"
	// VerdictReview means 80.00 <= DU <= 89.99.
	VerdictReview Verdict = "review"
	// VerdictFail means DU < 80.00.
	VerdictFail Verdict = "fail"
)

// Measurement is one validated flow measurement point.
type Measurement struct {
	ID   string
	Flow *big.Rat // flow in litres per hour, strictly greater than 0 and at most 100
	// Rated is the emitter's rated flow in litres per hour, or nil when the
	// request omitted rated_flow_lph. Within one evaluation either every
	// point carries a rated flow or none does; the API layer rejects mixed
	// requests before they reach this package.
	Rated *big.Rat
}

// CalculationBasis names the quantity the lowest group, the means and DU
// were computed from.
type CalculationBasis string

const (
	// BasisMeasuredFlow ranks points by their measured flow (requests
	// without rated_flow_lph).
	BasisMeasuredFlow CalculationBasis = "measured_flow"
	// BasisSupplyRatio ranks points by the ratio of measured flow to rated
	// flow (requests with rated_flow_lph on every point).
	BasisSupplyRatio CalculationBasis = "supply_ratio"
)

// Result is the fully recomputable adjudication of one acceptance request.
type Result struct {
	SampleCount int
	LowestCount int
	// LowestIDs are the lowest-group point ids, ordered by the ranked
	// quantity and then by input position, so ties enter the group in input
	// order.
	LowestIDs []string
	// LowestMean and OverallMean are the means of the measured flows (of
	// the lowest group and of all points), regardless of Basis.
	LowestMean  *big.Rat
	OverallMean *big.Rat
	// DU is the exact distribution uniformity in percent (lowest mean /
	// overall mean * 100 over the ranked quantity), carried without prior
	// rounding.
	DU *big.Rat
	// DURounded is DU rounded with decimal ROUND_HALF_UP to two places,
	// always rendered with exactly two digits after the decimal point.
	DURounded string
	Verdict   Verdict
	// Basis reports whether the lowest group and DU were computed from
	// measured flows or from supply ratios.
	Basis CalculationBasis
	// LowestMeanRatio and OverallMeanRatio are the exact means of the
	// per-point supply ratios (Flow/Rated) for the lowest group and for all
	// points. Both are nil unless Basis is BasisSupplyRatio.
	LowestMeanRatio  *big.Rat
	OverallMeanRatio *big.Rat
}

// Evaluate adjudicates a set of 4..64 validated measurements.
func Evaluate(points []Measurement) Result {
	n := len(points)

	// ceil(n/4): size of the lowest quarter.
	k := (n + 3) / 4

	// Rated mode applies only when every point carries a rated flow; mixed
	// requests are rejected during request validation.
	rated := true
	for _, p := range points {
		if p.Rated == nil {
			rated = false
			break
		}
	}

	// ranked is the quantity points are ordered and averaged by: the supply
	// ratio Flow/Rated in rated mode, the measured flow otherwise.
	ranked := make([]*big.Rat, n)
	for i, p := range points {
		if rated {
			ranked[i] = new(big.Rat).Quo(p.Flow, p.Rated)
		} else {
			ranked[i] = p.Flow
		}
	}

	// Stable ordering keeps equal ranked values in input order, satisfying
	// the rule that tied lowest values enter the lowest group in input
	// sequence.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return ranked[order[a]].Cmp(ranked[order[b]]) < 0
	})
	lowest := order[:k]

	total := new(big.Rat)
	rankedTotal := new(big.Rat)
	for i, p := range points {
		total.Add(total, p.Flow)
		rankedTotal.Add(rankedTotal, ranked[i])
	}
	lowestSum := new(big.Rat)
	lowestRankedSum := new(big.Rat)
	ids := make([]string, 0, k)
	for _, idx := range lowest {
		lowestSum.Add(lowestSum, points[idx].Flow)
		lowestRankedSum.Add(lowestRankedSum, ranked[idx])
		ids = append(ids, points[idx].ID)
	}

	lowestMean := new(big.Rat).Quo(lowestSum, big.NewRat(int64(k), 1))
	overallMean := new(big.Rat).Quo(total, big.NewRat(int64(n), 1))

	// DU = lowest mean / overall mean * 100 over the ranked quantity, kept
	// as an exact fraction.
	basis := BasisMeasuredFlow
	var lowestMeanRatio, overallMeanRatio *big.Rat
	lowestRanked := lowestMean
	overallRanked := overallMean
	if rated {
		basis = BasisSupplyRatio
		lowestMeanRatio = new(big.Rat).Quo(lowestRankedSum, big.NewRat(int64(k), 1))
		overallMeanRatio = new(big.Rat).Quo(rankedTotal, big.NewRat(int64(n), 1))
		lowestRanked = lowestMeanRatio
		overallRanked = overallMeanRatio
	}
	du := new(big.Rat).Quo(lowestRanked, overallRanked)
	du.Mul(du, big.NewRat(100, 1))

	cents := roundHalfUpScaled(du, 2)

	return Result{
		SampleCount:      n,
		LowestCount:      k,
		LowestIDs:        ids,
		LowestMean:       lowestMean,
		OverallMean:      overallMean,
		DU:               du,
		DURounded:        formatScaled(cents, 2),
		Verdict:          verdictFromCents(cents),
		Basis:            basis,
		LowestMeanRatio:  lowestMeanRatio,
		OverallMeanRatio: overallMeanRatio,
	}
}

// verdictFromCents classifies the already rounded DU, so the thresholds refer
// to one unambiguous two-decimal value (>= 9000 = 90.00, >= 8000 = 80.00).
func verdictFromCents(cents *big.Int) Verdict {
	switch {
	case cents.Cmp(big.NewInt(9000)) >= 0:
		return VerdictPass
	case cents.Cmp(big.NewInt(8000)) >= 0:
		return VerdictReview
	default:
		return VerdictFail
	}
}

// roundHalfUpScaled returns r * 10^places rounded with decimal ROUND_HALF_UP
// (ties go away from zero) as an exact integer.
func roundHalfUpScaled(r *big.Rat, places uint) *big.Int {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaledNum := new(big.Int).Mul(new(big.Int).Abs(r.Num()), scale)

	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(scaledNum, r.Denom(), rem)

	twice := new(big.Int).Mul(rem, big.NewInt(2))
	if twice.Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if r.Num().Sign() < 0 {
		q.Neg(q)
	}
	return q
}

// ExactDecimal is an API-facing view of an exact rational value. Fraction is
// the canonical "num/den" form and always carries the full precision; Decimal
// is shown for convenience and is itself exact only when Terminating is true
// (the reduced denominator has no prime factors other than 2 and 5). When
// Terminating is false, Decimal is a 12-place ROUND_HALF_UP preview and the
// fraction must be used to recompute any result exactly.
type ExactDecimal struct {
	Fraction    string
	Decimal     string
	Terminating bool
}

// AsExactDecimal renders an exact rational value for transport. The DU value
// must always be computed from the *big.Rat itself, never from Decimal.
func AsExactDecimal(r *big.Rat) ExactDecimal {
	view := ExactDecimal{
		Fraction: r.Num().String() + "/" + r.Denom().String(),
	}
	if s, ok := exactDecimal(r); ok {
		view.Decimal = s
		view.Terminating = true
	} else {
		view.Decimal = roundedDecimal(r, 12)
		view.Terminating = false
	}
	return view
}

// exactDecimal returns the exact finite decimal form of r, or ok=false if the
// fraction has no terminating decimal expansion.
func exactDecimal(r *big.Rat) (string, bool) {
	rest := new(big.Int).Set(r.Denom())
	twos, fives := 0, 0
	for new(big.Int).Mod(rest, big.NewInt(2)).Sign() == 0 {
		rest.Quo(rest, big.NewInt(2))
		twos++
	}
	for new(big.Int).Mod(rest, big.NewInt(5)).Sign() == 0 {
		rest.Quo(rest, big.NewInt(5))
		fives++
	}
	if rest.Sign() != 0 && rest.Cmp(big.NewInt(1)) != 0 {
		return "", false
	}

	places := twos
	if fives > places {
		places = fives
	}
	tenPow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	multiplier := new(big.Int).Quo(tenPow, r.Denom())
	scaled := new(big.Int).Mul(r.Num(), multiplier)

	return formatScaled(scaled, places), true
}

// roundedDecimal renders r with exactly places digits after the decimal point,
// rounding the final digit with ROUND_HALF_UP.
func roundedDecimal(r *big.Rat, places uint) string {
	return formatScaled(roundHalfUpScaled(r, places), int(places))
}

// formatScaled renders an integer equal to value * 10^places with exactly
// places digits after the decimal point.
func formatScaled(scaled *big.Int, places int) string {
	neg := scaled.Sign() < 0
	s := new(big.Int).Abs(scaled).String()
	if places > 0 {
		if len(s) <= places {
			s = padLeft(s, places+1)
		}
		s = s[:len(s)-places] + "." + s[len(s)-places:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func padLeft(s string, length int) string {
	for len(s) < length {
		s = "0" + s
	}
	return s
}
