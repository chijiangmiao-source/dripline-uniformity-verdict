package dripdu

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustFlow(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, err := ParseFlow(s)
	require.NoError(t, err, "ParseFlow(%q)", s)
	return r
}

func mkPoints(t *testing.T, flows []string, ids ...string) []Measurement {
	t.Helper()
	if len(ids) == 0 {
		ids = make([]string, len(flows))
		for i := range flows {
			ids[i] = "p" + string(rune('a'+i))
		}
	}
	points := make([]Measurement, len(flows))
	for i, f := range flows {
		points[i] = Measurement{ID: ids[i], Flow: mustFlow(t, f)}
	}
	return points
}

func TestLowestGroupSizeIsCeilQuarter(t *testing.T) {
	cases := []struct {
		n, want int
	}{
		{4, 1}, {5, 2}, {6, 2}, {7, 2}, {8, 2},
		{9, 3}, {12, 3}, {13, 4}, {16, 4}, {63, 16}, {64, 16},
	}
	for _, tc := range cases {
		flows := make([]string, tc.n)
		for i := range flows {
			flows[i] = "10.000"
		}
		res := Evaluate(mkPoints(t, flows))
		assert.Equal(t, tc.want, res.LowestCount, "n=%d", tc.n)
		assert.Len(t, res.LowestIDs, tc.want, "n=%d", tc.n)
		assert.Equal(t, tc.n, res.SampleCount, "n=%d", tc.n)
	}
}

func TestLowestGroupPicksSmallestFlows(t *testing.T) {
	// n=5 -> k=2; ids travel with their flows regardless of input position.
	res := Evaluate(mkPoints(t,
		[]string{"9.000", "8.500", "8.600", "9.500", "10.000"},
		"hi", "lo1", "lo2", "x", "y"))
	assert.Equal(t, []string{"lo1", "lo2"}, res.LowestIDs)

	lowestMean := new(big.Rat).Add(mustFlow(t, "8.500"), mustFlow(t, "8.600"))
	lowestMean.Quo(lowestMean, big.NewRat(2, 1))
	assert.Equal(t, 0, res.LowestMean.Cmp(lowestMean))
	assert.Equal(t, "8.55", MeanString(res.LowestMean))

	// total = 9.000+8.500+8.600+9.500+10.000 = 45.600; mean = 45.6/5 = 9.12.
	overall := big.NewRat(45600, 5000)
	assert.Equal(t, 0, res.OverallMean.Cmp(overall))
	assert.Equal(t, "9.12", MeanString(res.OverallMean))
}

func TestTiesEnterLowestGroupInInputOrder(t *testing.T) {
	// n=8 -> k=2, but three points share the minimum flow 7.000; the first
	// two of those in input order must make the group.
	flows := []string{"7.000", "9.000", "8.000", "7.000", "9.500", "7.000", "8.200", "8.100"}
	ids := []string{"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7"}
	res := Evaluate(mkPoints(t, flows, ids...))
	assert.Equal(t, []string{"t0", "t3"}, res.LowestIDs)
}

func TestTiesAtCutoffKeepInputOrder(t *testing.T) {
	// n=4 -> k=1, two points equal to the minimum; the earlier one wins.
	res := Evaluate(mkPoints(t,
		[]string{"5.000", "5.000", "6.000", "6.000"},
		"first", "second", "c", "d"))
	assert.Equal(t, []string{"first"}, res.LowestIDs)
}

func TestUniformFlowsAreOneHundredPass(t *testing.T) {
	res := Evaluate(mkPoints(t, []string{"12.345", "12.345", "12.345", "12.345"}))
	assert.Equal(t, "100.00", res.DURounded)
	assert.Equal(t, VerdictPass, res.Verdict)
}

func TestHalfUpRoundingBoundary90_005(t *testing.T) {
	// low=54.003, other three=61.999:
	// DU = 400*54003/(54003+3*61999) = 21601200/240000 = 90.005 exactly.
	res := Evaluate(mkPoints(t, []string{"54.003", "61.999", "61.999", "61.999"}))

	wantExact := new(big.Rat).Quo(big.NewRat(21601200, 1), big.NewRat(240000, 1))
	assert.Equal(t, 0, res.DU.Cmp(wantExact), "exact DU before rounding: %s", res.DU.FloatString(6))
	assert.Equal(t, "90.01", res.DURounded, "ROUND_HALF_UP must push 90.005 up")
	assert.Equal(t, VerdictPass, res.Verdict, "verdict follows the rounded DU")
}

func TestHalfUpRoundingBoundary89_995Becomes90(t *testing.T) {
	// low=17.999, other three=20.667:
	// DU = 400*17999/(17999+3*20667) = 7199600/80000 = 89.995 exactly,
	// which ROUND_HALF_UP turns into 90.00 -> pass.
	res := Evaluate(mkPoints(t, []string{"17.999", "20.667", "20.667", "20.667"}))
	assert.Equal(t, "90.00", res.DURounded)
	assert.Equal(t, VerdictPass, res.Verdict)
}

func TestVerdictThresholdBoundaries(t *testing.T) {
	// For n=4, DU = 400*x/(x+3*y) where x is the lowest flow and y the
	// other three (all in thousandths). Each case below hits the stated
	// percentage exactly, exercising the rounded-value thresholds.
	cases := []struct {
		name     string
		flows    []string
		wantDU   string
		wantVerd Verdict
	}{
		{
			name:     "exactly 80.00 is review not fail",
			flows:    []string{"0.003", "0.004", "0.004", "0.004"}, // 4x=3y
			wantDU:   "80.00",
			wantVerd: VerdictReview,
		},
		{
			name:     "exactly 79.99 is fail",
			flows:    []string{"7.999", "10.667", "10.667", "10.667"}, // 32001x=23997y
			wantDU:   "79.99",
			wantVerd: VerdictFail,
		},
		{
			name:     "exactly 89.99 is review not pass",
			flows:    []string{"26.997", "31.001", "31.001", "31.001"}, // 31001x=26997y
			wantDU:   "89.99",
			wantVerd: VerdictReview,
		},
		{
			name:     "exactly 90.00 is pass",
			flows:    []string{"0.027", "0.031", "0.031", "0.031"}, // 31x=27y
			wantDU:   "90.00",
			wantVerd: VerdictPass,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Evaluate(mkPoints(t, tc.flows))
			assert.Equal(t, tc.wantDU, res.DURounded)
			assert.Equal(t, tc.wantVerd, res.Verdict)
		})
	}
}

func TestReviewBandMiddle(t *testing.T) {
	// DU about 85: low=8.5, others=10 -> 400*8500/(8500+30000)=88.31...
	res := Evaluate(mkPoints(t, []string{"8.500", "10.000", "10.000", "10.000"}))
	assert.Equal(t, "88.31", res.DURounded)
	assert.Equal(t, VerdictReview, res.Verdict)
}

func TestClearFail(t *testing.T) {
	// One emitter close to blocked: 2.000 among 10s.
	res := Evaluate(mkPoints(t, []string{"2.000", "10.000", "10.000", "10.000"}))
	assert.Equal(t, "25.00", res.DURounded)
	assert.Equal(t, VerdictFail, res.Verdict)
}

func TestMeansAreNotRoundedBeforeDU(t *testing.T) {
	// n=7 -> k=2. Flows: one 99.999 and six 100.000.
	// overall mean = 699999/7000 = 99.999857142... (non-terminating decimal).
	flows := []string{"99.999", "100.000", "100.000", "100.000", "100.000", "100.000", "100.000"}
	res := Evaluate(mkPoints(t, flows))

	require.Equal(t, []string{"pa", "pb"}, res.LowestIDs)

	// Exact means carried as fractions.
	assert.Equal(t, 0, res.LowestMean.Cmp(big.NewRat(199999, 2000)), "lowest mean = 99.9995")
	assert.Equal(t, 0, res.OverallMean.Cmp(big.NewRat(699999, 7000)))

	// Exact DU = 199999/2000 * 7000/699999 * 100 = 69999650/699999.
	assert.Equal(t, 0, res.DU.Cmp(big.NewRat(69999650, 699999)))
	// Rounded only at the end; the 6th decimal pushes 99.9996... to 100.00.
	assert.Equal(t, "100.00", res.DURounded)

	// Transport rendering: terminating decimals are exact; the repeating one
	// is shown at 12 places but never feeds back into DU.
	assert.Equal(t, "99.9995", MeanString(res.LowestMean))
	assert.Equal(t, "99.999857142857", MeanString(res.OverallMean))
}

func TestMeanStringExactFiniteDecimals(t *testing.T) {
	cases := map[string]string{
		"1/4":    "0.25",
		"4001/4": "1000.25",
		"1/2000": "0.0005",
		"3/8":    "0.375",
		"7/125":  "0.056",
		"100/1":  "100",
	}
	for frac, want := range cases {
		r, ok := new(big.Rat).SetString(frac)
		require.True(t, ok)
		assert.Equal(t, want, MeanString(r), frac)
	}
}
