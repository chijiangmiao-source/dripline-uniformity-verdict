package httpapi

import (
	"bytes"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postBlockage(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blockage-diagnosis", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func blockagePoints(body map[string]any) []any {
	return body["points"].([]any)
}

func blockagePointAt(t *testing.T, body map[string]any, idx int) map[string]any {
	t.Helper()
	p, ok := blockagePoints(body)[idx].(map[string]any)
	require.True(t, ok, "point %d is not an object", idx)
	return p
}

func baselinePercent(p map[string]any) map[string]any {
	return p["baseline_percent"].(map[string]any)
}

func TestBlockageNoBlockageReturnsEmptySectionsAndAllPoints(t *testing.T) {
	r := NewRouter()
	w := postBlockage(t, r, validBody([]string{"9.9", "10", "10.1", "10"}, "a", "b", "c", "d"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	assert.Equal(t, float64(4), body["sample_count"])
	median := decimalField(t, body, "median")
	// Sorted 9.9, 10, 10, 10.1 -> even median (10+10)/2 = 10 exactly.
	assert.Equal(t, "10", median["decimal"])
	assert.Equal(t, "10/1", median["exact_fraction"])
	assert.Equal(t, true, median["terminating"])

	points := blockagePoints(body)
	require.Len(t, points, 4, "every measurement is reported even with no blockage")
	for i, id := range []string{"a", "b", "c", "d"} {
		p := blockagePointAt(t, body, i)
		assert.Equal(t, id, p["id"], "points must follow input order")
		assert.Equal(t, false, p["suspected_low_flow"])
	}
	// 9.9/10*100 = 99%; 10.1/10*100 = 101%.
	assert.Equal(t, "99/1", baselinePercent(blockagePointAt(t, body, 0))["exact_fraction"])
	assert.Equal(t, "101/1", baselinePercent(blockagePointAt(t, body, 2))["exact_fraction"])

	assert.NotNil(t, body["sections"], "sections must be an empty array, not null")
	assert.Empty(t, body["sections"])
	assert.NotContains(t, body, "calculation_basis", "legacy responses carry no basis field")
}

func TestBlockageSingleSectionMergesAdjacentMarkedPoints(t *testing.T) {
	r := NewRouter()
	// Median 10, cutoff 8.5: indices 2..4 form one continuous low run.
	w := postBlockage(t, r, validBody(
		[]string{"10", "10", "8", "7.5", "8", "10", "10"},
		"p0", "p1", "p2", "p3", "p4", "p5", "p6"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	flags := []bool{false, false, true, true, true, false, false}
	for i, want := range flags {
		assert.Equal(t, want, blockagePointAt(t, body, i)["suspected_low_flow"], "point %d", i)
	}
	assert.Equal(t, "75.00", baselinePercent(blockagePointAt(t, body, 3))["rounded"])
	assert.Equal(t, "75/1", baselinePercent(blockagePointAt(t, body, 3))["exact_fraction"])

	sections := body["sections"].([]any)
	require.Len(t, sections, 1)
	section := sections[0].(map[string]any)
	assert.Equal(t, "p2", section["start_id"])
	assert.Equal(t, "p4", section["end_id"])
	assert.Equal(t, []any{"p2", "p3", "p4"}, section["member_ids"])
}

func TestBlockageTwoSectionsStaySeparate(t *testing.T) {
	r := NewRouter()
	// Median pinned to 10 by five healthy points; low runs at both ends.
	w := postBlockage(t, r, validBody(
		[]string{"8", "8", "10", "10", "10", "10", "10", "7", "7"},
		"q0", "q1", "q2", "q3", "q4", "q5", "q6", "q7", "q8"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	sections := body["sections"].([]any)
	require.Len(t, sections, 2)
	first := sections[0].(map[string]any)
	second := sections[1].(map[string]any)
	assert.Equal(t, "q0", first["start_id"])
	assert.Equal(t, "q1", first["end_id"])
	assert.Equal(t, []any{"q0", "q1"}, first["member_ids"])
	assert.Equal(t, "q7", second["start_id"])
	assert.Equal(t, "q8", second["end_id"])
	assert.Equal(t, []any{"q7", "q8"}, second["member_ids"])
}

func TestBlockageThresholdIsStrictAndRecomputable(t *testing.T) {
	r := NewRouter()

	// Exactly 85% of the median is still healthy.
	at := postBlockage(t, r, validBody([]string{"8.5", "10", "10", "10"}, "a", "b", "c", "d"))
	require.Equal(t, http.StatusOK, at.Code, at.Body.String())
	atBody := decodeBody(t, at)
	assert.Equal(t, false, blockagePointAt(t, atBody, 0)["suspected_low_flow"])
	assert.Empty(t, atBody["sections"])

	// 84.99% is below the strict threshold: one single-point section.
	below := postBlockage(t, r, validBody([]string{"8.499", "10", "10", "10"}, "a", "b", "c", "d"))
	require.Equal(t, http.StatusOK, below.Code, below.Body.String())
	belowBody := decodeBody(t, below)
	assert.Equal(t, true, blockagePointAt(t, belowBody, 0)["suspected_low_flow"])
	sections := belowBody["sections"].([]any)
	require.Len(t, sections, 1)
	assert.Equal(t, "a", sections[0].(map[string]any)["start_id"])

	// External recomputation: value/median*100 from the exact fractions alone
	// must reproduce the served exact percentage and its rounded display.
	served := baselinePercent(blockagePointAt(t, belowBody, 0))
	median := parseFraction(t, decimalField(t, belowBody, "median")["exact_fraction"].(string))
	recomputed := new(big.Rat).Quo(big.NewRat(8499, 1000), median)
	recomputed.Mul(recomputed, big.NewRat(100, 1))
	assert.Equal(t, 0, recomputed.Cmp(parseFraction(t, served["exact_fraction"].(string))))
	assert.Equal(t, "84.99", served["rounded"])
}

func TestBlockageRatedModeMovesTheSection(t *testing.T) {
	r := NewRouter()
	flows := []string{"8", "9", "10", "10", "10", "10"}
	ids := []string{"a", "b", "c", "d", "e", "f"}

	// Legacy: the 8 lph point is the only low point.
	legacy := postBlockage(t, r, ratedBody(flows, nil, ids...))
	require.Equal(t, http.StatusOK, legacy.Code, legacy.Body.String())
	legacyBody := decodeBody(t, legacy)
	legacySections := legacyBody["sections"].([]any)
	require.Len(t, legacySections, 1)
	assert.Equal(t, "a", legacySections[0].(map[string]any)["start_id"])
	assert.NotContains(t, legacyBody, "calculation_basis")

	// Rated: b delivers only 9/18 = 1/2 of its rated flow and becomes the
	// sole section, although a has the lower measured flow.
	rated := postBlockage(t, r, ratedBody(flows,
		[]string{"8", "18", "10", "10", "10", "10"}, ids...))
	require.Equal(t, http.StatusOK, rated.Code, rated.Body.String())
	ratedResponseBody := decodeBody(t, rated)
	assert.Equal(t, "supply_ratio", ratedResponseBody["calculation_basis"])
	assert.Equal(t, false, blockagePointAt(t, ratedResponseBody, 0)["suspected_low_flow"])
	assert.Equal(t, true, blockagePointAt(t, ratedResponseBody, 1)["suspected_low_flow"])
	ratedSections := ratedResponseBody["sections"].([]any)
	require.Len(t, ratedSections, 1)
	section := ratedSections[0].(map[string]any)
	assert.Equal(t, "b", section["start_id"])
	assert.Equal(t, "b", section["end_id"])
	assert.Equal(t, []any{"b"}, section["member_ids"])
}

func TestBlockageRatedModeRepeatingFractions(t *testing.T) {
	r := NewRouter()
	// Ratios 1/3, 2/3, 1, 1: median 5/6 is non-terminating; the first two
	// points (40% and 80% of baseline) merge into one section.
	w := postBlockage(t, r, ratedBody(
		[]string{"1", "2", "10", "10"},
		[]string{"3", "3", "10", "10"},
		"a", "b", "c", "d"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	median := decimalField(t, body, "median")
	assert.Equal(t, "5/6", median["exact_fraction"])
	assert.Equal(t, false, median["terminating"])
	assert.Equal(t, "0.833333333333", median["decimal"], "12-place preview only")

	assert.Equal(t, "40/1", baselinePercent(blockagePointAt(t, body, 0))["exact_fraction"])
	assert.Equal(t, "80/1", baselinePercent(blockagePointAt(t, body, 1))["exact_fraction"])
	assert.Equal(t, true, blockagePointAt(t, body, 0)["suspected_low_flow"])
	assert.Equal(t, true, blockagePointAt(t, body, 1)["suspected_low_flow"])

	sections := body["sections"].([]any)
	require.Len(t, sections, 1)
	assert.Equal(t, []any{"a", "b"}, sections[0].(map[string]any)["member_ids"])
}

func TestBlockageErrorsReuseVerifyEnvelopeAndFirstField(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantField  string
	}{
		{
			name:       "invalid flow located at index",
			body:       `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":0},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "measurements[1].flow_lph",
		},
		{
			name:       "invalid rated located exactly",
			body:       ratedBody([]string{"10", "10", "10", "10"}, []string{"8", "8", "100.001", "8"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "measurements[2].rated_flow_lph",
		},
		{
			name:       "mixed rated locates first omission",
			body:       `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10,"rated_flow_lph":8},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "measurements[0].rated_flow_lph",
		},
		{
			name:       "too few points located at measurements",
			body:       validBody([]string{"10", "10", "10"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "measurements",
		},
		{
			name:       "null measurements cannot form a median",
			body:       `{"measurements":null}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "measurements",
		},
		{
			name:       "malformed body is a 400",
			body:       `{"measurements":`,
			wantStatus: http.StatusBadRequest,
			wantField:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postBlockage(t, r, tc.body)
			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			if tc.wantField == "" {
				// Syntax-level 400s carry no field location, like /verify.
				assert.NotContains(t, errBody, "field")
			} else {
				assert.Equal(t, tc.wantField, errBody["field"])
			}
			// No partial diagnosis leaks alongside an error.
			full := decodeBody(t, w)
			assert.NotContains(t, full, "sections")
			assert.NotContains(t, full, "points")
			assert.NotContains(t, full, "median")
		})
	}
}

// TestBlockageRejectsDuplicateFields pins that a request whose JSON repeats a
// member name is rejected as ambiguous instead of silently keeping only the
// later value: two measurements arrays must not resolve to the second one, an
// invalid flow_lph must not be erased by a repeated valid one, and an invalid
// rated_flow_lph must not be erased either.
func TestBlockageRejectsDuplicateFields(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name: "two measurements arrays rejected as ambiguous",
			body: `{"measurements":[{"id":"a","flow_lph":8},{"id":"b","flow_lph":8},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}],` +
				`"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements",
		},
		{
			name:      "invalid then valid flow_lph located at the point",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":0,"flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[1].flow_lph",
		},
		{
			name:      "invalid then valid rated_flow_lph located at the point",
			body:      `{"measurements":[{"id":"a","flow_lph":10,"rated_flow_lph":8},{"id":"b","flow_lph":10,"rated_flow_lph":0,"rated_flow_lph":8},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[1].rated_flow_lph",
		},
		{
			name:      "repeated id located at the point",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","id":"c","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[1].id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postBlockage(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			// No partial diagnosis leaks alongside the error.
			full := decodeBody(t, w)
			assert.NotContains(t, full, "sections")
			assert.NotContains(t, full, "points")
			assert.NotContains(t, full, "median")
		})
	}
}

// TestBlockageDoesNotChangeExistingEndpoints pins compatibility: adding the
// diagnosis route leaves the verify and stability routes registered and
// answering.
func TestBlockageDoesNotChangeExistingEndpoints(t *testing.T) {
	r := NewRouter()

	verify := postJSON(t, r, validBody([]string{"9.8", "9.9", "10", "10.1"}))
	require.Equal(t, http.StatusOK, verify.Code)
	verifyBody := decodeBody(t, verify)
	assert.Equal(t, "pass", verifyBody["verdict"])
	assert.NotContains(t, verifyBody, "sections")

	stabilityReq := httptest.NewRequest(http.MethodPost, "/api/v1/stability", bytes.NewBufferString(stabilityBody(
		[][2]string{{"A", "9.9"}, {"B", "10"}, {"C", "10.1"}, {"D", "10"}},
		[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
		[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
	)))
	stabilityReq.Header.Set("Content-Type", "application/json")
	stabilityW := httptest.NewRecorder()
	r.ServeHTTP(stabilityW, stabilityReq)
	assert.Equal(t, http.StatusOK, stabilityW.Code, stabilityW.Body.String())
}
