package dripdu

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnoseNoBlockageUniformBranch(t *testing.T) {
	// Every emitter delivers the same flow: the median equals the flows, no
	// point is suspected and the section list is empty but present.
	res, err := DiagnoseBlockage(mkPoints(t,
		[]string{"10", "10", "10", "10", "10", "10"},
		"a", "b", "c", "d", "e", "f"))
	require.NoError(t, err)

	assert.Equal(t, BasisMeasuredFlow, res.Basis)
	assert.Equal(t, 0, res.Median.Cmp(big.NewRat(10, 1)))
	require.Len(t, res.Points, 6)
	for i, p := range res.Points {
		assert.Equal(t, []string{"a", "b", "c", "d", "e", "f"}[i], p.ID)
		assert.False(t, p.Suspected, "point %s", p.ID)
		assert.Equal(t, 0, p.BaselineRatio.Cmp(big.NewRat(100, 1)))
		assert.Equal(t, "100.00", p.BaselineRatioRounded)
	}
	assert.NotNil(t, res.Sections, "sections must be an empty list, not null")
	assert.Empty(t, res.Sections)
}

func TestDiagnoseSingleSectionMergesAdjacentPoints(t *testing.T) {
	// n=7 -> odd median is the 4th sorted value. Four healthy 10 lph points
	// pin the median to 10 and the cutoff to 8.5; the three lows at indices
	// 2..4 form one continuous section.
	res, err := DiagnoseBlockage(mkPoints(t,
		[]string{"10", "10", "8", "7.5", "8", "10", "10"},
		"p0", "p1", "p2", "p3", "p4", "p5", "p6"))
	require.NoError(t, err)

	assert.Equal(t, 0, res.Median.Cmp(big.NewRat(10, 1)))
	wantSuspected := []bool{false, false, true, true, true, false, false}
	wantPercent := []int64{100, 100, 80, 75, 80, 100, 100}
	for i, p := range res.Points {
		assert.Equal(t, wantSuspected[i], p.Suspected, "point %s", p.ID)
		assert.Equal(t, 0, p.BaselineRatio.Cmp(big.NewRat(wantPercent[i], 1)), "point %s", p.ID)
	}

	require.Len(t, res.Sections, 1)
	s := res.Sections[0]
	assert.Equal(t, "p2", s.StartID)
	assert.Equal(t, "p4", s.EndID)
	assert.Equal(t, []string{"p2", "p3", "p4"}, s.MemberIDs)
}

func TestDiagnoseTwoSectionsAndRunsAtEdges(t *testing.T) {
	// n=9: five 10 lph points pin the median (the 5th sorted value) to 10,
	// so the cutoff is 8.5. Two low runs touch the start and end of the
	// branch; the healthy middle keeps them apart.
	res, err := DiagnoseBlockage(mkPoints(t,
		[]string{"8", "8", "10", "10", "10", "10", "10", "7", "7"},
		"q0", "q1", "q2", "q3", "q4", "q5", "q6", "q7", "q8"))
	require.NoError(t, err)

	require.Len(t, res.Sections, 2)
	assert.Equal(t, BlockageSection{
		StartID:   "q0",
		EndID:     "q1",
		MemberIDs: []string{"q0", "q1"},
	}, res.Sections[0])
	assert.Equal(t, BlockageSection{
		StartID:   "q7",
		EndID:     "q8",
		MemberIDs: []string{"q7", "q8"},
	}, res.Sections[1])

	// Healthy points between and beyond the runs must not be swallowed.
	assert.False(t, res.Points[2].Suspected)
	assert.False(t, res.Points[6].Suspected)
	assert.True(t, res.Points[7].Suspected)
}

func TestDiagnoseThresholdIsStrictlyBelow85Percent(t *testing.T) {
	// n=4, even median (10+10)/2 = 10; the cutoff is exactly 8.5. A point at
	// the cutoff is still healthy; one thousandth lower is suspected.
	atCutoff, err := DiagnoseBlockage(mkPoints(t,
		[]string{"8.5", "10", "10", "10"}, "a", "b", "c", "d"))
	require.NoError(t, err)
	assert.False(t, atCutoff.Points[0].Suspected, "exactly 85.00% is not below the threshold")
	assert.Equal(t, 0, atCutoff.Points[0].BaselineRatio.Cmp(big.NewRat(85, 1)))
	assert.Empty(t, atCutoff.Sections)

	belowCutoff, err := DiagnoseBlockage(mkPoints(t,
		[]string{"8.499", "10", "10", "10"}, "a", "b", "c", "d"))
	require.NoError(t, err)
	assert.True(t, belowCutoff.Points[0].Suspected, "84.99% is below the threshold")
	assert.Equal(t, 0, belowCutoff.Points[0].BaselineRatio.Cmp(big.NewRat(8499, 100)))
	require.Len(t, belowCutoff.Sections, 1)
	assert.Equal(t, "a", belowCutoff.Sections[0].StartID)
}

func TestDiagnoseEvenMedianIsMeanOfMiddlePair(t *testing.T) {
	// Sorted 7,8,9,10 -> median (8+9)/2 = 8.5, cutoff 7.225; the 7 point sits
	// below it, the 8 and 9 do not. This pins the even-count median rule.
	res, err := DiagnoseBlockage(mkPoints(t,
		[]string{"7", "9", "10", "8"}, "a", "b", "c", "d"))
	require.NoError(t, err)
	assert.Equal(t, 0, res.Median.Cmp(big.NewRat(17, 2)))
	assert.True(t, res.Points[0].Suspected)
	assert.False(t, res.Points[1].Suspected)
	assert.False(t, res.Points[2].Suspected)
	assert.False(t, res.Points[3].Suspected)
}

func TestDiagnoseRatedModeMovesSectionToUnderSuppliedPoint(t *testing.T) {
	flows := []string{"8", "9", "10", "10", "10", "10"}
	ids := []string{"a", "b", "c", "d", "e", "f"}

	// Measured-flow mode: median 10, only the 8 lph point is below 85%.
	legacy, err := DiagnoseBlockage(mkPoints(t, flows, ids...))
	require.NoError(t, err)
	assert.Equal(t, BasisMeasuredFlow, legacy.Basis)
	require.Len(t, legacy.Sections, 1)
	assert.Equal(t, "a", legacy.Sections[0].StartID)
	assert.False(t, legacy.Points[1].Suspected, "9 lph is 90% of the 10 lph median")

	// Rated mode with b rated at 18 lph: its supply ratio 9/18 = 1/2 is the
	// only low value; a delivers 8/8 = 1 and is healthy. The section moves.
	rated, err := DiagnoseBlockage(mkRatedPoints(t, flows,
		[]string{"8", "18", "10", "10", "10", "10"}, ids...))
	require.NoError(t, err)
	assert.Equal(t, BasisSupplyRatio, rated.Basis)
	assert.Equal(t, 0, rated.Median.Cmp(big.NewRat(1, 1)))
	assert.False(t, rated.Points[0].Suspected)
	assert.True(t, rated.Points[1].Suspected)
	assert.Equal(t, 0, rated.Points[1].BaselineRatio.Cmp(big.NewRat(50, 1)))
	require.Len(t, rated.Sections, 1)
	assert.Equal(t, "b", rated.Sections[0].StartID)
	assert.Equal(t, "b", rated.Sections[0].EndID)
	assert.Equal(t, []string{"b"}, rated.Sections[0].MemberIDs)
}

func TestDiagnoseRatedModeNonTerminatingMedian(t *testing.T) {
	// Ratios 1/3, 2/3, 1, 1 -> even median (2/3+1)/2 = 5/6, a repeating
	// decimal. Cutoff 17/24: both 1/3 (40%) and 2/3 (80%) are below it and
	// merge into one adjacent section; the exact fractions carry all
	// precision while the median decimal is a 12-place preview.
	res, err := DiagnoseBlockage(mkRatedPoints(t,
		[]string{"1", "2", "10", "10"},
		[]string{"3", "3", "10", "10"},
		"a", "b", "c", "d"))
	require.NoError(t, err)

	assert.Equal(t, 0, res.Median.Cmp(big.NewRat(5, 6)))
	median := AsExactDecimal(res.Median)
	assert.False(t, median.Terminating)
	assert.Equal(t, "0.833333333333", median.Decimal)

	assert.Equal(t, 0, res.Points[0].BaselineRatio.Cmp(big.NewRat(40, 1)))
	assert.Equal(t, 0, res.Points[1].BaselineRatio.Cmp(big.NewRat(80, 1)))
	assert.Equal(t, 0, res.Points[2].BaselineRatio.Cmp(big.NewRat(120, 1)))
	assert.True(t, res.Points[0].Suspected)
	assert.True(t, res.Points[1].Suspected)
	require.Len(t, res.Sections, 1)
	assert.Equal(t, []string{"a", "b"}, res.Sections[0].MemberIDs)
}

func TestDiagnoseWithoutMeasurementsHasNoMedian(t *testing.T) {
	_, err := DiagnoseBlockage(nil)
	assert.ErrorIs(t, err, ErrNoMedian)
}
