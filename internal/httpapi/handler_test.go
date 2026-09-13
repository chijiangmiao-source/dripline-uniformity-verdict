package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func postJSON(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), "body: %s", w.Body.String())
	return out
}

func validBody(flows []string, ids ...string) string {
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
		buf.WriteByte('}')
	}
	buf.WriteString(`]}`)
	return buf.String()
}

func decimalField(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := body[key].(map[string]any)
	require.True(t, ok, "field %s is not an object: %v", key, body[key])
	return v
}

func TestVerifyHappyPathAllPass(t *testing.T) {
	r := NewRouter()
	w := postJSON(t, r, validBody([]string{"9.8", "9.9", "10.0", "10.1"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, float64(4), body["sample_count"])
	assert.Equal(t, float64(1), body["lowest_count"])
	assert.Equal(t, []any{"pa"}, body["lowest_ids"])

	lowest := decimalField(t, body, "lowest_mean_lph")
	assert.Equal(t, "9.8", lowest["decimal"])
	assert.Equal(t, "49/5", lowest["exact_fraction"])
	assert.Equal(t, true, lowest["terminating"])

	overall := decimalField(t, body, "overall_mean_lph")
	assert.Equal(t, "9.95", overall["decimal"])
	assert.Equal(t, true, overall["terminating"])

	du := decimalField(t, body, "du_percent")
	assert.Equal(t, "98.49", du["rounded"])
	assert.Equal(t, "pass", body["verdict"])

	// The response must let the client recompute DU exactly from fractions:
	// lowest/overall*100, rounded half up. Here 49/5 / 199/20 * 100 = 19600/199.
	assert.Equal(t, "19600/199", du["exact_fraction"])
}

func TestVerifyReviewAndFailVerdicts(t *testing.T) {
	r := NewRouter()

	review := postJSON(t, r, validBody([]string{"8.5", "10", "10", "10"}))
	require.Equal(t, http.StatusOK, review.Code, review.Body.String())
	reviewBody := decodeBody(t, review)
	assert.Equal(t, "88.31", decimalField(t, reviewBody, "du_percent")["rounded"])
	assert.Equal(t, "review", reviewBody["verdict"])

	fail := postJSON(t, r, validBody([]string{"2", "10", "10", "10"}))
	require.Equal(t, http.StatusOK, fail.Code, fail.Body.String())
	assert.Equal(t, "fail", decodeBody(t, fail)["verdict"])
}

func TestVerifyRepeatingMeanIsExactlyRecomputable(t *testing.T) {
	// n=7: one 99.999 and six 100.000 -> overall mean 699999/7000, a
	// repeating decimal. The response must carry the exact fraction and flag
	// the decimal rendering as non-terminating/approximate.
	r := NewRouter()
	w := postJSON(t, r, validBody(
		[]string{"99.999", "100", "100", "100", "100", "100", "100"}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	overall := decimalField(t, body, "overall_mean_lph")
	assert.Equal(t, "699999/7000", overall["exact_fraction"], "exact overall mean")
	assert.Equal(t, "99.999857142857", overall["decimal"], "12-place preview")
	assert.Equal(t, false, overall["terminating"])

	lowest := decimalField(t, body, "lowest_mean_lph")
	assert.Equal(t, "199999/2000", lowest["exact_fraction"])
	assert.Equal(t, true, lowest["terminating"])

	du := decimalField(t, body, "du_percent")
	assert.Equal(t, "69999650/699999", du["exact_fraction"], "exact unrounded DU")
	assert.Equal(t, "100.00", du["rounded"])
	assert.Equal(t, "pass", body["verdict"])
}

func TestVerifyTiesKeepInputOrderEndToEnd(t *testing.T) {
	r := NewRouter()
	// n=8 -> lowest group size 2; three points tie at the minimum.
	body := validBody(
		[]string{"7", "9", "8", "7", "9.5", "7", "8.2", "8.1"},
		"t0", "t1", "t2", "t3", "t4", "t5", "t6", "t7")
	w := postJSON(t, r, body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []any{"t0", "t3"}, decodeBody(t, w)["lowest_ids"])
}

func TestVerifyCountBoundaries(t *testing.T) {
	r := NewRouter()
	makeFlows := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "10"
		}
		return out
	}

	for n, status := range map[int]int{3: http.StatusUnprocessableEntity, 65: http.StatusUnprocessableEntity} {
		w := postJSON(t, r, validBody(makeFlows(n)))
		assert.Equal(t, status, w.Code, "n=%d", n)
		assert.Equal(t, "measurements", decodeBody(t, w)["error"].(map[string]any)["field"], "n=%d", n)
	}

	// 4 and 64 are both accepted.
	for _, n := range []int{4, 64} {
		w := postJSON(t, r, validBody(makeFlows(n)))
		assert.Equal(t, http.StatusOK, w.Code, "n=%d body=%s", n, w.Body.String())
	}
}

func TestVerifyFieldPathPointsAtFirstViolation(t *testing.T) {
	r := NewRouter()

	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name:      "first point empty id",
			body:      `{"measurements":[{"id":"","flow_lph":10},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].id",
		},
		{
			name:      "whitespace id is empty",
			body:      `{"measurements":[{"id":"  ","flow_lph":10},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].id",
		},
		{
			name:      "duplicate id reported at second occurrence",
			body:      `{"measurements":[{"id":"x","flow_lph":10},{"id":"b","flow_lph":10},{"id":"x","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[2].id",
		},
		{
			name:      "first invalid flow located",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":0},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[1].flow_lph",
		},
		{
			name:      "four decimal places rejected",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10.1234},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[2].flow_lph",
		},
		{
			name:      "flow above 100 rejected",
			body:      `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":100.001},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[1].flow_lph",
		},
		{
			name:      "flow as string rejected",
			body:      `{"measurements":[{"id":"a","flow_lph":"10"},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].flow_lph",
		},
		{
			name:      "flow null rejected",
			body:      `{"measurements":[{"id":"a","flow_lph":null},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].flow_lph",
		},
		{
			name:      "id missing",
			body:      `{"measurements":[{"flow_lph":10},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].id",
		},
		{
			name:      "flow missing",
			body:      `{"measurements":[{"id":"a"},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField: "measurements[0].flow_lph",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postJSON(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			// No partial adjudication fields may leak into the error response.
			full := decodeBody(t, w)
			assert.NotContains(t, full, "du_percent")
			assert.NotContains(t, full, "lowest_mean_lph")
			assert.NotContains(t, full, "overall_mean_lph")
		})
	}
}

func TestVerifyMalformedBodies(t *testing.T) {
	r := NewRouter()

	cases := []struct {
		name   string
		body   string
		field  string
		status int
	}{
		{"not json", `{"measurements":`, "", http.StatusBadRequest},
		{"array root", `[1,2,3]`, "", http.StatusBadRequest},
		{"empty", ``, "", http.StatusBadRequest},
		{"missing measurements", `{"points":[]}`, "measurements", http.StatusUnprocessableEntity},
		{"measurements not array", `{"measurements":{}}`, "measurements", http.StatusUnprocessableEntity},
		{"item not object", `{"measurements":[1,2,3,4]}`, "measurements[0]", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postJSON(t, r, tc.body)
			assert.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusUnprocessableEntity {
				errBody := decodeBody(t, w)["error"].(map[string]any)
				assert.Equal(t, tc.field, errBody["field"])
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	r := NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestParseRejectsScientificNotation(t *testing.T) {
	// Even a syntactically valid JSON number with an exponent is rejected
	// because decimal place count cannot be read off a scientific literal.
	r := NewRouter()
	w := postJSON(t, r, `{"measurements":[{"id":"a","flow_lph":1e1},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.Equal(t, "measurements[0].flow_lph", decodeBody(t, w)["error"].(map[string]any)["field"])
}

// TestLeadingZeroNumberLocated pins that a plain number with a leading zero
// (not valid JSON, but a plain decimal in intent) is rejected by field-level
// validation — 422 naming the field — rather than failing the whole body as
// malformed JSON with no location.
func TestLeadingZeroNumberLocated(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name        string
		body        string
		wantField   string
		wantMessage string
	}{
		{
			name:        "leading zero flow",
			body:        `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":01},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField:   "measurements[1].flow_lph",
			wantMessage: "flow_lph must be a decimal number with at most three decimal places",
		},
		{
			name:        "leading zero flow with fraction",
			body:        `{"measurements":[{"id":"a","flow_lph":007.5},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField:   "measurements[0].flow_lph",
			wantMessage: "flow_lph must be a decimal number with at most three decimal places",
		},
		{
			name:        "leading zero rated flow",
			body:        `{"measurements":[{"id":"a","flow_lph":10,"rated_flow_lph":8},{"id":"b","flow_lph":10,"rated_flow_lph":08},{"id":"c","flow_lph":10,"rated_flow_lph":8},{"id":"d","flow_lph":10,"rated_flow_lph":8}]}`,
			wantField:   "measurements[1].rated_flow_lph",
			wantMessage: "rated_flow_lph must be a decimal number with at most three decimal places",
		},
		{
			name:        "leading zero number as id is a type error",
			body:        `{"measurements":[{"id":"a","flow_lph":10},{"id":01,"flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`,
			wantField:   "measurements[1].id",
			wantMessage: "id must be a string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postJSON(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			assert.Equal(t, tc.wantMessage, errBody["message"])
			assert.NotContains(t, decodeBody(t, w), "du_percent")
		})
	}

	// Other malformed JSON still fails the whole body as 400 with no field.
	for _, body := range []string{`01`, `{"measurements":`, `{"measurements":[{"id":"a","flow_lph":1.}]}`, `[1,2,3]`} {
		w := postJSON(t, r, body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", body)
	}
}

// TestVeryLongIDsAccepted pins that the request body has no fixed size cap:
// four points with very long non-empty unique ids and valid rated data are
// adjudicated normally.
func TestVeryLongIDsAccepted(t *testing.T) {
	r := NewRouter()
	var b strings.Builder
	b.WriteString(`{"measurements":[`)
	for i := 0; i < 4; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		id := strings.Repeat(string(rune('a'+i)), 300*1024)
		idJSON, _ := json.Marshal(id)
		fmt.Fprintf(&b, `{"id":%s,"flow_lph":10,"rated_flow_lph":8}`, idJSON)
	}
	b.WriteString(`]}`)

	w := postJSON(t, r, b.String())
	require.Equal(t, http.StatusOK, w.Code, "%.200s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, float64(4), body["sample_count"])
	assert.Equal(t, "supply_ratio", body["calculation_basis"])
	assert.Equal(t, "100.00", decimalField(t, body, "du_percent")["rounded"])
	assert.Equal(t, "pass", body["verdict"])
}

// parseFraction reads an "num/den" string from the response without importing
// the domain package: this mirrors what an external acceptance tool can do.
func parseFraction(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	require.True(t, ok, "fraction %q", s)
	return r
}

// roundHalfUpTwo recomputes ROUND_HALF_UP to two places purely from an exact
// fraction, as an external auditor would from the JSON response.
func roundHalfUpTwo(r *big.Rat) string {
	scaled := new(big.Int).Mul(r.Num(), big.NewInt(100))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(scaled, r.Denom(), rem)
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	whole := new(big.Int).Quo(q, big.NewInt(100))
	frac := new(big.Int).Mod(q, big.NewInt(100))
	return whole.String() + "." + padTwo(frac.String())
}

func padTwo(s string) string {
	for len(s) < 2 {
		s = "0" + s
	}
	return s
}

// TestVerdictRecomputableFromResponseAlone proves a seven-point run whose
// overall mean is a repeating decimal still yields a verdict the client can
// reproduce exactly from the response's exact_fraction fields, with no access
// to server internals.
func TestVerdictRecomputableFromResponseAlone(t *testing.T) {
	r := NewRouter()
	w := postJSON(t, r, validBody(
		[]string{"99.999", "100", "100", "100", "100", "100", "100"}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	lowest := parseFraction(t, decimalField(t, body, "lowest_mean_lph")["exact_fraction"].(string))
	overall := parseFraction(t, decimalField(t, body, "overall_mean_lph")["exact_fraction"].(string))

	// Independent recomputation of DU from the two exact mean fractions.
	du := new(big.Rat).Quo(lowest, overall)
	du.Mul(du, big.NewRat(100, 1))

	// It must exactly equal the exact DU fraction the server reported.
	servedDU := parseFraction(t, decimalField(t, body, "du_percent")["exact_fraction"].(string))
	assert.Equal(t, 0, du.Cmp(servedDU))

	// Rounding the independently computed fraction must reproduce du_percent.
	assert.Equal(t, "100.00", roundHalfUpTwo(du))
	assert.Equal(t, "100.00", decimalField(t, body, "du_percent")["rounded"])

	// And the threshold rule applied to that value reproduces the verdict.
	cents, _ := new(big.Int).SetString(strings.ReplaceAll(roundHalfUpTwo(du), ".", ""), 10)
	wantVerdict := "fail"
	if cents.Cmp(big.NewInt(9000)) >= 0 {
		wantVerdict = "pass"
	} else if cents.Cmp(big.NewInt(8000)) >= 0 {
		wantVerdict = "review"
	}
	assert.Equal(t, wantVerdict, body["verdict"])
}
