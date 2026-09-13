package dripdu

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlowAcceptsBoundaryValues(t *testing.T) {
	cases := map[string]string{
		// input  -> exact rational value
		"100":       "100/1",
		"100.000":   "100/1",
		"0.001":     "1/1000",
		"1":         "1/1",
		"0.5":       "1/2",
		"12.345":    "2469/200",
		"99.999":    "99999/1000",
		"00100.000": "", // leading zeros rejected: canonical JSON/decimal form only
	}
	for input, want := range cases {
		if want == "" {
			_, err := ParseFlow(input)
			assert.ErrorIs(t, err, ErrFlowFormat, input)
			continue
		}
		got, err := ParseFlow(input)
		require.NoError(t, err, input)
		wantRat, ok := new(big.Rat).SetString(want)
		require.True(t, ok)
		assert.Equal(t, 0, got.Cmp(wantRat), "input %s parsed as %s, want %s", input, got, wantRat)
	}
}

func TestParseFlowRejectsOutOfRange(t *testing.T) {
	for _, input := range []string{"0", "0.0", "0.000", "100.001", "100.01", "101", "999"} {
		_, err := ParseFlow(input)
		assert.ErrorIs(t, err, ErrFlowRange, "input %q", input)
	}
}

func TestParseFlowRejectsMalformedDecimals(t *testing.T) {
	for _, input := range []string{
		"", ".", ".5", "1.", "1.2345", "0.0001", " 1", "1 ", "1e2", "1E2",
		"+1", "-1", "-0.001", "0x10", "1,5", "1.2.3", "nan", "Inf", "00", "01",
	} {
		_, err := ParseFlow(input)
		assert.ErrorIs(t, err, ErrFlowFormat, "input %q", input)
	}
}

func TestParseRatedFlowMirrorsFlowRules(t *testing.T) {
	// Same accepted form and range as flow_lph; only the error messages
	// name the rated field.
	for input, want := range map[string]string{
		"8":     "8/1",
		"2.5":   "5/2",
		"100":   "100/1",
		"0.001": "1/1000",
		"16.25": "65/4",
	} {
		got, err := ParseRatedFlow(input)
		require.NoError(t, err, input)
		wantRat, ok := new(big.Rat).SetString(want)
		require.True(t, ok)
		assert.Equal(t, 0, got.Cmp(wantRat), "input %s", input)
	}

	for _, input := range []string{"0", "0.0", "100.001", "101"} {
		_, err := ParseRatedFlow(input)
		assert.ErrorIs(t, err, ErrRatedRange, "input %q", input)
		assert.Contains(t, err.Error(), "rated_flow_lph", "input %q", input)
	}
	for _, input := range []string{"1.2345", "01", "1e2", "-1", ".5", "1."} {
		_, err := ParseRatedFlow(input)
		assert.ErrorIs(t, err, ErrRatedFormat, "input %q", input)
		assert.Contains(t, err.Error(), "rated_flow_lph", "input %q", input)
	}
}

func TestRoundHalfUpScaled(t *testing.T) {
	cases := []struct {
		frac   string
		places uint
		want   string
	}{
		{"90005/1000", 2, "9001"}, // 90.005 -> 90.01
		{"89995/1000", 2, "9000"}, // 89.995 -> 90.00
		{"79994/1000", 2, "7999"}, // 79.994 -> 79.99
		{"80004/1000", 2, "8000"}, // 80.004 -> 80.00
		{"80006/1000", 2, "8001"}, // 80.006 -> 80.01
		{"1/8", 2, "13"},          // 0.125 -> 0.13, tie away from zero
		{"1/16", 2, "6"},          // 0.0625 -> 0.06
		{"-1/8", 2, "-13"},        // negative tie also away from zero
		{"100/1", 2, "10000"},     // 100 -> 100.00
		{"1/3", 4, "3333"},        // 0.33333... -> 0.3333
		{"2/3", 4, "6667"},        // 0.66666... -> 0.6667
	}
	for _, tc := range cases {
		r, ok := new(big.Rat).SetString(tc.frac)
		require.True(t, ok, tc.frac)
		got := roundHalfUpScaled(r, tc.places)
		assert.Equal(t, tc.want, got.String(), "frac=%s places=%d", tc.frac, tc.places)
	}
}

func TestEvaluateIsDeterministicAcrossRuns(t *testing.T) {
	flows := []string{"10.111", "9.222", "9.222", "11.333", "12.000", "10.500", "9.222", "8.001"}
	first := Evaluate(mkPoints(t, flows))
	second := Evaluate(mkPoints(t, flows))
	assert.Equal(t, first, second)
}
