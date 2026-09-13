package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestVerifyHappyPathAllPass(t *testing.T) {
	r := NewRouter()
	w := postJSON(t, r, validBody([]string{"9.8", "9.9", "10.0", "10.1"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, float64(4), body["sample_count"])
	assert.Equal(t, float64(1), body["lowest_count"])
	assert.Equal(t, []any{"pa"}, body["lowest_ids"])
	assert.Equal(t, "9.8", body["lowest_mean_lph"])
	assert.Equal(t, "9.95", body["overall_mean_lph"])
	assert.Equal(t, "98.49", body["du_percent"])
	assert.Equal(t, "pass", body["verdict"])
}

func TestVerifyReviewAndFailVerdicts(t *testing.T) {
	r := NewRouter()

	review := postJSON(t, r, validBody([]string{"8.5", "10", "10", "10"}))
	require.Equal(t, http.StatusOK, review.Code, review.Body.String())
	assert.Equal(t, "88.31", decodeBody(t, review)["du_percent"])
	assert.Equal(t, "review", decodeBody(t, review)["verdict"])

	fail := postJSON(t, r, validBody([]string{"2", "10", "10", "10"}))
	require.Equal(t, http.StatusOK, fail.Code, fail.Body.String())
	assert.Equal(t, "fail", decodeBody(t, fail)["verdict"])
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
