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
}

// Result is the fully recomputable adjudication of one acceptance request.
type Result struct {
	SampleCount int
	LowestCount int
	// LowestIDs are the lowest-group point ids, ordered by ascending flow and
	// then by input position, so ties enter the group in input order.
	LowestIDs   []string
	LowestMean  *big.Rat
	OverallMean *big.Rat
	// DU is the exact distribution uniformity in percent (lowest mean /
	// overall mean * 100), carried without prior rounding.
	DU *big.Rat
	// DURounded is DU rounded with decimal ROUND_HALF_UP to two places,
	// always rendered with exactly two digits after the decimal point.
	DURounded string
	Verdict   Verdict
}

// Evaluate adjudicates a set of 4..64 validated measurements.
func Evaluate(points []Measurement) Result {
	n := len(points)

	// ceil(n/4): size of the lowest-flow quarter.
	k := (n + 3) / 4

	// Stable ordering keeps equal flows in input order, satisfying the rule
	// that tied lowest values enter the lowest group in input sequence.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return points[order[a]].Flow.Cmp(points[order[b]].Flow) < 0
	})
	lowest := order[:k]

	total := new(big.Rat)
	for _, p := range points {
		total.Add(total, p.Flow)
	}
	lowestSum := new(big.Rat)
	ids := make([]string, 0, k)
	for _, idx := range lowest {
		lowestSum.Add(lowestSum, points[idx].Flow)
		ids = append(ids, points[idx].ID)
	}

	lowestMean := new(big.Rat).SetFrac(big.NewInt(1), big.NewInt(1))
	lowestMean.Quo(lowestSum, big.NewRat(int64(k), 1))
	overallMean := new(big.Rat).Quo(total, big.NewRat(int64(n), 1))

	// DU = lowest mean / overall mean * 100, kept as an exact fraction.
	du := new(big.Rat).Quo(lowestMean, overallMean)
	du.Mul(du, big.NewRat(100, 1))

	cents := roundHalfUpScaled(du, 2)

	return Result{
		SampleCount: n,
		LowestCount: k,
		LowestIDs:   ids,
		LowestMean:  lowestMean,
		OverallMean: overallMean,
		DU:          du,
		DURounded:   formatScaled(cents, 2),
		Verdict:     verdictFromCents(cents),
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

// MeanString renders an exact mean for the API response. Means of decimal
// inputs with a denominator containing only factors 2 and 5 are emitted as
// exact finite decimals (no rounding at all). A mean whose denominator has
// other prime factors has no finite decimal expansion; it is shown with 12
// decimal places using ROUND_HALF_UP for transport only. The DU value is
// always computed from the exact fraction, never from this rendering.
func MeanString(r *big.Rat) string {
	if s, ok := exactDecimal(r); ok {
		return s
	}
	return roundedDecimal(r, 12)
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
