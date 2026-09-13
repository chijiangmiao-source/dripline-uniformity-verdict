package httpapi

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ratedBody builds a verify request carrying flow_lph and, when rated is
// non-nil, rated_flow_lph on every measurement. Flow and rated values are
// inserted as raw JSON tokens so invalid literals can be exercised too.
func ratedBody(flows, rated []string, ids ...string) string {
	if len(ids) == 0 {
		ids = make([]string, len(flows))
		for i := range flows {
			ids[i] = "p" + string(rune('a'+i))
		}
	}
	var buf bytes.Buffer
	buf.WriteString(`{"measurements":[`)
	for i := range flows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"id":`)
		idJSON, _ := json.Marshal(ids[i])
		buf.Write(idJSON)
		buf.WriteString(`,"flow_lph":`)
		buf.WriteString(flows[i])
		if rated != nil {
			buf.WriteString(`,"rated_flow_lph":`)
			buf.WriteString(rated[i])
		}
		buf.WriteByte('}')
	}
	buf.WriteString(`]}`)
	return buf.String()
}

func TestRatedModeResponseContract(t *testing.T) {
	r := NewRouter()
	// Ratios: 1, 1/2, 1, 1 -> B is relatively under-supplied although its
	// measured flow (9) is not the lowest flow.
	w := postJSON(t, r, ratedBody(
		[]string{"8", "9", "10", "10"},
		[]string{"8", "18", "10", "10"},
		"A", "B", "C", "D"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	assert.Equal(t, "supply_ratio", body["calculation_basis"])
	assert.Equal(t, float64(4), body["sample_count"])
	assert.Equal(t, float64(1), body["lowest_count"])
	assert.Equal(t, []any{"B"}, body["lowest_ids"])

	// The original fields are preserved: measured-flow means of the
	// ratio-selected group and of all points.
	lowest := decimalField(t, body, "lowest_mean_lph")
	assert.Equal(t, "9", lowest["decimal"])
	assert.Equal(t, "9/1", lowest["exact_fraction"])
	assert.Equal(t, true, lowest["terminating"])
	overall := decimalField(t, body, "overall_mean_lph")
	assert.Equal(t, "9.25", overall["decimal"])
	assert.Equal(t, "37/4", overall["exact_fraction"])
	assert.Equal(t, true, overall["terminating"])

	// The rated-mode means follow the decimal/exact_fraction/terminating
	// contract.
	lowestRatio := decimalField(t, body, "lowest_mean_ratio")
	assert.Equal(t, "0.5", lowestRatio["decimal"])
	assert.Equal(t, "1/2", lowestRatio["exact_fraction"])
	assert.Equal(t, true, lowestRatio["terminating"])
	overallRatio := decimalField(t, body, "overall_mean_ratio")
	assert.Equal(t, "0.875", overallRatio["decimal"])
	assert.Equal(t, "7/8", overallRatio["exact_fraction"])
	assert.Equal(t, true, overallRatio["terminating"])

	// DU comes from the ratio means: (1/2)/(7/8)*100 = 400/7.
	du := decimalField(t, body, "du_percent")
	assert.Equal(t, "400/7", du["exact_fraction"])
	assert.Equal(t, "57.14", du["rounded"])
	assert.Equal(t, "fail", body["verdict"])
	assert.Equal(t, "不通过", body["verdict_text"])
}

func TestRatedValuesChangeLowestGroupEndToEnd(t *testing.T) {
	r := NewRouter()
	flows := []string{"8", "9", "10", "10"}
	ids := []string{"A", "B", "C", "D"}

	// Legacy request on the same flows: the lowest measured flow decides.
	legacy := postJSON(t, r, ratedBody(flows, nil, ids...))
	require.Equal(t, http.StatusOK, legacy.Code, legacy.Body.String())
	legacyBody := decodeBody(t, legacy)
	assert.Equal(t, []any{"A"}, legacyBody["lowest_ids"])
	assert.Equal(t, "86.49", decimalField(t, legacyBody, "du_percent")["rounded"])
	assert.Equal(t, "review", legacyBody["verdict"])
	assert.NotContains(t, legacyBody, "calculation_basis")

	// With rated values the same branch flips its lowest group and verdict.
	rated := postJSON(t, r, ratedBody(flows, []string{"8", "18", "10", "10"}, ids...))
	require.Equal(t, http.StatusOK, rated.Code, rated.Body.String())
	ratedBody := decodeBody(t, rated)
	assert.Equal(t, []any{"B"}, ratedBody["lowest_ids"])
	assert.Equal(t, "57.14", decimalField(t, ratedBody, "du_percent")["rounded"])
	assert.Equal(t, "fail", ratedBody["verdict"])
}

func TestRatedModeTiesKeepInputOrderEndToEnd(t *testing.T) {
	r := NewRouter()
	// n=8 -> k=2; indices 1, 2, 3 and 5 all tie at ratio 1/2.
	w := postJSON(t, r, ratedBody(
		[]string{"10", "4", "8", "6", "12", "8", "9", "7"},
		[]string{"10", "8", "16", "12", "12", "16", "9", "7"},
		"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, []any{"t1", "t2"}, body["lowest_ids"], "tied ratios enter the group in input order")
	assert.Equal(t, "66.67", decimalField(t, body, "du_percent")["rounded"])
}

// TestRatedModeRecomputableFromResponseAlone mirrors the legacy
// recomputation test: repeating ratio means still let the client reproduce
// the exact DU and verdict from the response alone.
func TestRatedModeRecomputableFromResponseAlone(t *testing.T) {
	r := NewRouter()
	w := postJSON(t, r, ratedBody(
		[]string{"1", "10", "10", "10"},
		[]string{"3", "10", "10", "10"}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	lowest := decimalField(t, body, "lowest_mean_ratio")
	assert.Equal(t, "1/3", lowest["exact_fraction"])
	assert.Equal(t, "0.333333333333", lowest["decimal"], "12-place preview only")
	assert.Equal(t, false, lowest["terminating"])

	overall := decimalField(t, body, "overall_mean_ratio")
	assert.Equal(t, "5/6", overall["exact_fraction"])
	assert.Equal(t, "0.833333333333", overall["decimal"])
	assert.Equal(t, false, overall["terminating"])

	// Independent recomputation of DU from the two exact ratio fractions.
	du := new(big.Rat).Quo(
		parseFraction(t, lowest["exact_fraction"].(string)),
		parseFraction(t, overall["exact_fraction"].(string)))
	du.Mul(du, big.NewRat(100, 1))

	served := decimalField(t, body, "du_percent")
	assert.Equal(t, 0, du.Cmp(parseFraction(t, served["exact_fraction"].(string))))
	assert.Equal(t, "40.00", roundHalfUpTwo(du))
	assert.Equal(t, "40.00", served["rounded"])
	assert.Equal(t, "fail", body["verdict"])
}

func TestRatedModeMixedPresenceLocated(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name:      "first point missing rated",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10,"rated_flow_lph":8},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[0].rated_flow_lph",
		},
		{
			name:      "second point missing rated",
			body:      `{"measurements":[{"id":"a","flow_lph":10,"rated_flow_lph":8},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[1].rated_flow_lph",
		},
		{
			name:      "only middle point rated",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10,"rated_flow_lph":8},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].rated_flow_lph",
		},
		{
			name:      "last point missing rated",
			body:      `{"measurements":[{"id":"a","flow_lph":10,"rated_flow_lph":8},{"id":"b","flow_lph":10,"rated_flow_lph":8},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[3].rated_flow_lph",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postJSON(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"], "the error must locate the first point that omitted rated_flow_lph")
			assert.Contains(t, errBody["message"], "rated_flow_lph")
			// A rejected request produces no adjudication at all.
			full := decodeBody(t, w)
			assert.NotContains(t, full, "du_percent")
			assert.NotContains(t, full, "lowest_mean_ratio")
			assert.NotContains(t, full, "overall_mean_ratio")
		})
	}
}

// TestRatedModeMixedMissingOutranksLaterViolations pins the document-order
// rule: the all-or-nothing rated_flow_lph error sits at the first point that
// omitted the field, so it is reported ahead of any per-measurement violation
// at a later index — and behind any violation at an earlier index.
func TestRatedModeMixedMissingOutranksLaterViolations(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name:      "first point missing rated beats later invalid flow",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":0,"rated_flow_lph":8},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[0].rated_flow_lph",
		},
		{
			name:      "first point missing rated beats later invalid rated value",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10,"rated_flow_lph":0},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[0].rated_flow_lph",
		},
		{
			name:      "first point missing rated beats later duplicate id",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10,"rated_flow_lph":8},{"id":"b","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[0].rated_flow_lph",
		},
		{
			name:      "earlier invalid flow beats later missing rated",
			body:      `{"measurements":[{"id":"a","flow_lph":0,"rated_flow_lph":8},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[0].flow_lph",
		},
		{
			name:      "earlier empty id beats later missing rated",
			body:      `{"measurements":[{"id":"","flow_lph":10,"rated_flow_lph":8},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField: "measurements[0].id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postJSON(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			assert.NotContains(t, decodeBody(t, w), "du_percent")
		})
	}
}

func TestRatedModeInvalidValuesLocated(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name        string
		rated       string
		wantMessage string
	}{
		{"zero", "0", "rated_flow_lph must be greater than 0 and at most 100"},
		{"above 100", "100.001", "rated_flow_lph must be greater than 0 and at most 100"},
		{"four decimal places", "1.2345", "rated_flow_lph must be a decimal number with at most three decimal places"},
		{"negative", "-1", "rated_flow_lph must be a decimal number with at most three decimal places"},
		{"scientific notation", "1e1", "rated_flow_lph must be a decimal number with at most three decimal places"},
		{"string", `"8"`, "rated_flow_lph must be a JSON number"},
		{"null", "null", "rated_flow_lph must be a JSON number"},
		{"boolean", "true", "rated_flow_lph must be a JSON number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// All four points carry rated_flow_lph; the one at index 2 is
			// invalid, so the error must point exactly there.
			body := ratedBody(
				[]string{"10", "10", "10", "10"},
				[]string{"8", "8", tc.rated, "8"})
			w := postJSON(t, r, body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, "measurements[2].rated_flow_lph", errBody["field"])
			assert.Equal(t, tc.wantMessage, errBody["message"])
			assert.NotContains(t, decodeBody(t, w), "du_percent")
		})
	}
}

// TestLegacyResponseShapeUnchanged pins the documented README example: a
// request without rated_flow_lph must produce exactly the long-standing
// response, with no rated-mode fields added.
func TestLegacyResponseShapeUnchanged(t *testing.T) {
	r := NewRouter()
	w := postJSON(t, r, `{"measurements":[
		{"id":"A-01","flow_lph":9.82},{"id":"A-02","flow_lph":9.95},
		{"id":"A-03","flow_lph":10.00},{"id":"A-04","flow_lph":10.05},
		{"id":"A-05","flow_lph":9.90},{"id":"A-06","flow_lph":10.10},
		{"id":"A-07","flow_lph":9.88},{"id":"A-08","flow_lph":10.02}]}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"sample_count": 8,
		"lowest_count": 2,
		"lowest_ids": ["A-01", "A-07"],
		"lowest_mean_lph": {"decimal": "9.85", "exact_fraction": "197/20", "terminating": true},
		"overall_mean_lph": {"decimal": "9.965", "exact_fraction": "1993/200", "terminating": true},
		"du_percent": {"rounded": "98.85", "exact_fraction": "197000/1993"},
		"verdict": "pass",
		"verdict_text": "通过"
	}`), &want))
	assert.Equal(t, want, decodeBody(t, w))
}

// TestExamplePayloadsKeepTheirVerdicts posts the shipped example files
// unchanged: the three legacy payloads keep their documented outcomes and
// grow no new fields, while the rated example is adjudicated on ratios.
func TestExamplePayloadsKeepTheirVerdicts(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		file      string
		verdict   string
		du        string
		basis     string // empty for legacy payloads
		lowestIDs []any
	}{
		{"../../examples/acceptance-pass.json", "pass", "98.85", "", []any{"A-01", "A-07"}},
		{"../../examples/acceptance-review.json", "review", "86.00", "", []any{"B-03", "B-08"}},
		{"../../examples/acceptance-fail.json", "fail", "66.64", "", []any{"C-01", "C-05"}},
		{"../../examples/acceptance-rated.json", "review", "89.16", "supply_ratio", []any{"R-07", "R-04"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			w := postJSON(t, r, string(data))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, tc.verdict, body["verdict"])
			assert.Equal(t, tc.du, decimalField(t, body, "du_percent")["rounded"])
			assert.Equal(t, tc.lowestIDs, body["lowest_ids"])
			if tc.basis == "" {
				assert.NotContains(t, body, "calculation_basis")
				assert.NotContains(t, body, "lowest_mean_ratio")
				assert.NotContains(t, body, "overall_mean_ratio")
			} else {
				assert.Equal(t, tc.basis, body["calculation_basis"])
			}
		})
	}
}
