package dripdu

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkRounds builds rounds of measurements sharing one id list: rounds[r][i] is
// the flow of ids[i] in round r.
func mkRounds(t *testing.T, ids []string, rounds ...[]string) [][]Measurement {
	t.Helper()
	out := make([][]Measurement, len(rounds))
	for r, flows := range rounds {
		require.Len(t, flows, len(ids), "round %d flow count", r)
		round := make([]Measurement, len(ids))
		for i, f := range flows {
			round[i] = Measurement{ID: ids[i], Flow: mustFlow(t, f)}
		}
		out[r] = round
	}
	return out
}

func pointByID(t *testing.T, res StabilityResult, id string) StabilityPoint {
	t.Helper()
	for _, p := range res.Points {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("point %q not in result", id)
	return StabilityPoint{}
}

func TestStabilityMedianOddRounds(t *testing.T) {
	// 3 rounds: the median is the middle value itself, no averaging.
	res := AnalyzeStability(mkRounds(t,
		[]string{"a", "b", "c", "d"},
		[]string{"9.5", "10", "10", "10"},
		[]string{"10", "10", "10", "10"},
		[]string{"10.2", "10", "10", "10"},
	))

	a := pointByID(t, res, "a")
	assert.Equal(t, 0, a.Median.Cmp(big.NewRat(10, 1)), "median of 9.5,10,10.2")
	assert.Equal(t, 0, a.Range.Cmp(big.NewRat(7, 10)), "range 10.2-9.5")
	// 0.7 / 10 * 100 = 7 exactly.
	assert.Equal(t, 0, a.Percent.Cmp(big.NewRat(7, 1)))
	assert.Equal(t, "7.00", a.PercentRounded)
	assert.Equal(t, StabilityWatch, res.Stability)
	assert.Equal(t, "a", res.WorstPointID)
}

func TestStabilityMedianEvenRounds(t *testing.T) {
	// 4 rounds: the median is the mean of the two middle values.
	res := AnalyzeStability(mkRounds(t,
		[]string{"a", "b", "c", "d"},
		[]string{"10", "10", "10", "10"},
		[]string{"11", "10", "10", "10"},
		[]string{"12", "10", "10", "10"},
		[]string{"13", "10", "10", "10"},
	))

	a := pointByID(t, res, "a")
	assert.Equal(t, 0, a.Median.Cmp(big.NewRat(23, 2)), "median (11+12)/2")
	assert.Equal(t, 0, a.Range.Cmp(big.NewRat(3, 1)))
	// 3 / (23/2) * 100 = 600/23, a repeating decimal kept exact.
	assert.Equal(t, 0, a.Percent.Cmp(big.NewRat(600, 23)))
	assert.Equal(t, "26.09", a.PercentRounded)
	assert.Equal(t, StabilityVolatile, res.Stability)
}

func TestStabilityClassificationBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		flows       []string // the varying point across 3 (or 4) rounds
		want        Stability
		wantRounded string
	}{
		{"exactly five percent is stable", []string{"10", "10", "10.5"}, StabilityStable, "5.00"},
		{"just above five percent is watch", []string{"10", "10", "10.51"}, StabilityWatch, "5.10"},
		{"exactly ten percent is watch", []string{"10", "10", "11"}, StabilityWatch, "10.00"},
		{"just above ten percent is volatile", []string{"10", "10", "11.01"}, StabilityVolatile, "10.10"},
		{"zero range is stable", []string{"10", "10", "10"}, StabilityStable, "0.00"},
		// Exact 10000/1999 = 5.0025...% displays as "5.00" but the conclusion
		// uses the exact fraction, so it is watch, not stable.
		{"display rounding never reclassifies", []string{"9.99", "9.99", "10", "10.49"}, StabilityWatch, "5.00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rounds := make([][]string, len(tc.flows))
			for r, f := range tc.flows {
				rounds[r] = []string{f, "10", "10", "10"}
			}
			res := AnalyzeStability(mkRounds(t, []string{"a", "b", "c", "d"}, rounds...))
			assert.Equal(t, tc.want, res.Stability)
			assert.Equal(t, "a", res.WorstPointID)
			assert.Equal(t, tc.wantRounded, res.WorstPercentRounded)
		})
	}
}

func TestStabilityExactPercentAtDisplayBoundary(t *testing.T) {
	// 9.99, 9.99, 10, 10.49 -> median (9.99+10)/2 = 9.995, range 0.5,
	// percent = 0.5/9.995*100 = 10000/1999 > 5: watch although the two-place
	// rendering is "5.00".
	res := AnalyzeStability(mkRounds(t,
		[]string{"a", "b", "c", "d"},
		[]string{"9.99", "10", "10", "10"},
		[]string{"9.99", "10", "10", "10"},
		[]string{"10", "10", "10", "10"},
		[]string{"10.49", "10", "10", "10"},
	))
	a := pointByID(t, res, "a")
	assert.Equal(t, 0, a.Median.Cmp(big.NewRat(1999, 200)))
	assert.Equal(t, 0, a.Percent.Cmp(big.NewRat(10000, 1999)))
	assert.Equal(t, "5.00", a.PercentRounded)
	assert.Equal(t, StabilityWatch, res.Stability)
}

func TestStabilityWorstPointTieBreaksToFirstRoundOrder(t *testing.T) {
	// x and y both fluctuate by exactly 6%; the earlier point in first-round
	// order is reported as the worst.
	res := AnalyzeStability(mkRounds(t,
		[]string{"x", "y", "c", "d"},
		[]string{"10", "10", "10", "10"},
		[]string{"10", "10", "10", "10"},
		[]string{"10.6", "10.6", "10", "10"},
	))
	assert.Equal(t, "x", res.WorstPointID)
	assert.Equal(t, 0, res.WorstPercent.Cmp(big.NewRat(6, 1)))
	assert.Equal(t, StabilityWatch, res.Stability)
}

func TestStabilityFollowsFirstRoundOrder(t *testing.T) {
	// Later rounds may list points in any order; values are matched by id and
	// the report keeps the first round's order.
	ids := []string{"A", "B", "C", "D"}
	round1 := []Measurement{
		{ID: "A", Flow: mustFlow(t, "10")},
		{ID: "B", Flow: mustFlow(t, "10")},
		{ID: "C", Flow: mustFlow(t, "10")},
		{ID: "D", Flow: mustFlow(t, "10")},
	}
	round2 := []Measurement{
		{ID: "D", Flow: mustFlow(t, "10")},
		{ID: "C", Flow: mustFlow(t, "10")},
		{ID: "B", Flow: mustFlow(t, "11")},
		{ID: "A", Flow: mustFlow(t, "10")},
	}
	round3 := []Measurement{
		{ID: "C", Flow: mustFlow(t, "10")},
		{ID: "B", Flow: mustFlow(t, "10.5")},
		{ID: "A", Flow: mustFlow(t, "10")},
		{ID: "D", Flow: mustFlow(t, "10")},
	}
	res := AnalyzeStability([][]Measurement{round1, round2, round3})

	gotOrder := make([]string, 0, len(res.Points))
	for _, p := range res.Points {
		gotOrder = append(gotOrder, p.ID)
	}
	assert.Equal(t, ids, gotOrder, "report order follows the first round")

	b := pointByID(t, res, "B")
	assert.Equal(t, 0, b.Median.Cmp(big.NewRat(21, 2)), "median of 10,11,10.5")
	assert.Equal(t, 0, b.Range.Cmp(big.NewRat(1, 1)))
	// 1 / 10.5 * 100 = 200/21, a repeating decimal kept exact.
	assert.Equal(t, 0, b.Percent.Cmp(big.NewRat(200, 21)))
	assert.Equal(t, "9.52", b.PercentRounded)
	assert.Equal(t, "B", res.WorstPointID)
	assert.Equal(t, StabilityWatch, res.Stability)
}

func TestStabilityResultCounts(t *testing.T) {
	res := AnalyzeStability(mkRounds(t,
		[]string{"a", "b", "c", "d"},
		[]string{"10", "10", "10", "10"},
		[]string{"10", "10", "10", "10"},
		[]string{"10", "10", "10", "10"},
	))
	assert.Equal(t, 3, res.RoundCount)
	assert.Equal(t, 4, res.PointCount)
	assert.Len(t, res.Points, 4)
	assert.Equal(t, "0.00", res.WorstPercentRounded)
	assert.Equal(t, StabilityStable, res.Stability)
}
