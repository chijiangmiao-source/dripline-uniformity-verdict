package dripdu

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustRated(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, err := ParseRatedFlow(s)
	require.NoError(t, err, "ParseRatedFlow(%q)", s)
	return r
}

func mkRatedPoints(t *testing.T, flows, rated []string, ids ...string) []Measurement {
	t.Helper()
	require.Equal(t, len(flows), len(rated), "flows and rated must pair up")
	if len(ids) == 0 {
		ids = make([]string, len(flows))
		for i := range flows {
			ids[i] = "p" + string(rune('a'+i))
		}
	}
	points := make([]Measurement, len(flows))
	for i := range flows {
		points[i] = Measurement{ID: ids[i], Flow: mustFlow(t, flows[i]), Rated: mustRated(t, rated[i])}
	}
	return points
}

func TestRatedValuesChangeLowestGroup(t *testing.T) {
	// One branch, one set of measured flows, three adjudications: only the
	// rated values differ, and they decide which point is relatively
	// under-supplied.
	flows := []string{"8", "9", "10", "10"}
	ids := []string{"a", "b", "c", "d"}

	// Without rated values the lowest measured flow decides: a (8 lph).
	legacy := Evaluate(mkPoints(t, flows, ids...))
	assert.Equal(t, BasisMeasuredFlow, legacy.Basis)
	assert.Equal(t, []string{"a"}, legacy.LowestIDs)
	assert.Nil(t, legacy.LowestMeanRatio)
	assert.Nil(t, legacy.OverallMeanRatio)

	// Rated values equal to the measured flows make every ratio 1: a tie
	// that keeps input order, so the group is still a.
	uniform := Evaluate(mkRatedPoints(t, flows, []string{"8", "9", "10", "10"}, ids...))
	assert.Equal(t, BasisSupplyRatio, uniform.Basis)
	assert.Equal(t, []string{"a"}, uniform.LowestIDs)

	// b is rated 18 but delivers 9: ratio 1/2, the lowest although its
	// measured flow is not the lowest. The lowest group changes.
	mixed := Evaluate(mkRatedPoints(t, flows, []string{"8", "18", "10", "10"}, ids...))
	assert.Equal(t, BasisSupplyRatio, mixed.Basis)
	assert.Equal(t, []string{"b"}, mixed.LowestIDs)

	// Ratios: 1, 1/2, 1, 1 -> means 1/2 and 7/8, DU = (1/2)/(7/8)*100.
	assert.Equal(t, 0, mixed.LowestMeanRatio.Cmp(big.NewRat(1, 2)))
	assert.Equal(t, 0, mixed.OverallMeanRatio.Cmp(big.NewRat(7, 8)))
	assert.Equal(t, 0, mixed.DU.Cmp(big.NewRat(400, 7)))
	assert.Equal(t, "57.14", mixed.DURounded)
	assert.Equal(t, VerdictFail, mixed.Verdict)

	// The measured-flow means are still reported: they describe the
	// ratio-selected group (b alone) and all points.
	assert.Equal(t, 0, mixed.LowestMean.Cmp(big.NewRat(9, 1)))
	assert.Equal(t, 0, mixed.OverallMean.Cmp(big.NewRat(37, 4)))
}

func TestRatedModeTiesKeepInputOrder(t *testing.T) {
	// n=8 -> k=2. Four points tie at ratio 1/2 (indices 1, 2, 3, 5); the
	// first two of them in input order make the lowest group.
	flows := []string{"10", "4", "8", "6", "12", "8", "9", "7"}
	rated := []string{"10", "8", "16", "12", "12", "16", "9", "7"}
	ids := []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7"}
	res := Evaluate(mkRatedPoints(t, flows, rated, ids...))

	assert.Equal(t, []string{"t1", "t2"}, res.LowestIDs)
	assert.Equal(t, 0, res.LowestMeanRatio.Cmp(big.NewRat(1, 2)))
	assert.Equal(t, 0, res.OverallMeanRatio.Cmp(big.NewRat(3, 4)))
	// DU = (1/2)/(3/4)*100 = 200/3 = 66.666..., rounded half up.
	assert.Equal(t, 0, res.DU.Cmp(big.NewRat(200, 3)))
	assert.Equal(t, "66.67", res.DURounded)
	assert.Equal(t, VerdictFail, res.Verdict)
}

func TestRatedModeNonTerminatingRatioMeans(t *testing.T) {
	// Ratios 1/3, 1, 1, 1: both ratio means are repeating decimals and must
	// follow the exact-decimal contract (fraction authoritative, decimal a
	// 12-place preview, terminating=false).
	res := Evaluate(mkRatedPoints(t,
		[]string{"1", "10", "10", "10"},
		[]string{"3", "10", "10", "10"}))

	assert.Equal(t, []string{"pa"}, res.LowestIDs)
	assert.Equal(t, 0, res.LowestMeanRatio.Cmp(big.NewRat(1, 3)))
	assert.Equal(t, 0, res.OverallMeanRatio.Cmp(big.NewRat(5, 6)))

	// DU = (1/3)/(5/6)*100 = 40 exactly, carried as a fraction throughout.
	assert.Equal(t, 0, res.DU.Cmp(big.NewRat(40, 1)))
	assert.Equal(t, "40.00", res.DURounded)
	assert.Equal(t, VerdictFail, res.Verdict)

	lowest := AsExactDecimal(res.LowestMeanRatio)
	assert.False(t, lowest.Terminating)
	assert.Equal(t, "1/3", lowest.Fraction)
	assert.Equal(t, "0.333333333333", lowest.Decimal)

	overall := AsExactDecimal(res.OverallMeanRatio)
	assert.False(t, overall.Terminating)
	assert.Equal(t, "5/6", overall.Fraction)
	assert.Equal(t, "0.833333333333", overall.Decimal)
}

func TestRatedModeUniformRatiosScoreHundred(t *testing.T) {
	// Every point delivers exactly its rated flow: DU = 100.00 -> pass,
	// and the all-tied lowest group keeps input order.
	res := Evaluate(mkRatedPoints(t,
		[]string{"7.5", "15", "7.5", "15", "7.5"},
		[]string{"7.5", "15", "7.5", "15", "7.5"},
		"a", "b", "c", "d", "e"))
	assert.Equal(t, []string{"a", "b"}, res.LowestIDs)
	assert.Equal(t, "100.00", res.DURounded)
	assert.Equal(t, VerdictPass, res.Verdict)
	assert.Equal(t, 0, res.LowestMeanRatio.Cmp(big.NewRat(1, 1)))
	assert.Equal(t, 0, res.OverallMeanRatio.Cmp(big.NewRat(1, 1)))
}
