package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postStability(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stability", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// stabilityBody builds a stability request; each round is a list of
// {id, flow} pairs so per-round order, missing ids and foreign ids can all be
// exercised. Flow values are inserted as raw JSON tokens.
func stabilityBody(rounds ...[][2]string) string {
	var buf bytes.Buffer
	buf.WriteString(`{"rounds":[`)
	for r, round := range rounds {
		if r > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"measurements":[`)
		for i, p := range round {
			if i > 0 {
				buf.WriteByte(',')
			}
			idJSON, _ := json.Marshal(p[0])
			buf.WriteString(`{"id":`)
			buf.Write(idJSON)
			buf.WriteString(`,"flow_lph":`)
			buf.WriteString(p[1])
			buf.WriteByte('}')
		}
		buf.WriteString(`]}`)
	}
	buf.WriteString(`]}`)
	return buf.String()
}

// uniformRounds builds rounds where every point has the same flow in every
// round: flows[p] is point p's constant flow.
func uniformRounds(ids []string, flows []string, roundCount int) [][][2]string {
	rounds := make([][][2]string, roundCount)
	for r := range rounds {
		round := make([][2]string, len(ids))
		for i, id := range ids {
			round[i] = [2]string{id, flows[i]}
		}
		rounds[r] = round
	}
	return rounds
}

// varyingRounds builds 3 rounds where point 0 takes flows[0..2] and the rest
// stay constant at 10.
func varyingRounds(flows [3]string) [][][2]string {
	ids := []string{"A", "B", "C", "D"}
	rounds := make([][][2]string, 3)
	for r := range rounds {
		round := [][2]string{{ids[0], flows[r]}}
		for _, id := range ids[1:] {
			round = append(round, [2]string{id, "10"})
		}
		rounds[r] = round
	}
	return rounds
}

func stabilityPoints(t *testing.T, body map[string]any) []any {
	t.Helper()
	points, ok := body["points"].([]any)
	require.True(t, ok, "points is not an array: %v", body["points"])
	return points
}

func pointViewByID(t *testing.T, body map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range stabilityPoints(t, body) {
		p := raw.(map[string]any)
		if p["id"] == id {
			return p
		}
	}
	t.Fatalf("point %q not in response", id)
	return nil
}

func TestStabilityStableSample(t *testing.T) {
	r := NewRouter()
	w := postStability(t, r, stabilityBody(
		[][2]string{{"A", "9.80"}, {"B", "10.00"}, {"C", "10.10"}, {"D", "9.90"}},
		[][2]string{{"A", "9.90"}, {"B", "10.05"}, {"C", "10.00"}, {"D", "9.85"}},
		[][2]string{{"A", "9.85"}, {"B", "10.02"}, {"C", "10.05"}, {"D", "9.95"}},
	))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	assert.Equal(t, float64(3), body["round_count"])
	assert.Equal(t, float64(4), body["point_count"])
	assert.Equal(t, "stable", body["stability"])
	assert.Equal(t, "稳定", body["stability_text"])

	// Point A: 9.80, 9.90, 9.85 -> median 9.85, range 0.10,
	// percent = 0.1/9.85*100 = 200/197 ≈ 1.0152, the worst of the four.
	a := pointViewByID(t, body, "A")
	median := decimalField(t, a, "median_lph")
	assert.Equal(t, "9.85", median["decimal"])
	assert.Equal(t, "197/20", median["exact_fraction"])
	assert.Equal(t, true, median["terminating"])
	spread := decimalField(t, a, "range_lph")
	assert.Equal(t, "0.1", spread["decimal"])
	assert.Equal(t, "1/10", spread["exact_fraction"])
	pct := decimalField(t, a, "fluctuation_percent")
	assert.Equal(t, "200/197", pct["exact_fraction"])
	assert.Equal(t, "1.02", pct["rounded"])

	assert.Equal(t, "A", body["worst_point_id"])
	worst := decimalField(t, body, "worst_fluctuation_percent")
	assert.Equal(t, "200/197", worst["exact_fraction"])
	assert.Equal(t, "1.02", worst["rounded"])
}

func TestStabilityEvenRoundMedianEndToEnd(t *testing.T) {
	r := NewRouter()
	w := postStability(t, r, stabilityBody(
		[][2]string{{"P1", "10"}, {"P2", "10"}, {"P3", "10"}, {"P4", "10"}},
		[][2]string{{"P1", "11"}, {"P2", "10"}, {"P3", "10"}, {"P4", "10"}},
		[][2]string{{"P1", "12"}, {"P2", "10"}, {"P3", "10"}, {"P4", "10"}},
		[][2]string{{"P1", "13"}, {"P2", "10"}, {"P3", "10"}, {"P4", "10"}},
	))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	assert.Equal(t, float64(4), body["round_count"])
	p1 := pointViewByID(t, body, "P1")
	median := decimalField(t, p1, "median_lph")
	assert.Equal(t, "11.5", median["decimal"], "mean of the two middle values")
	assert.Equal(t, "23/2", median["exact_fraction"])
	assert.Equal(t, true, median["terminating"])
	pct := decimalField(t, p1, "fluctuation_percent")
	assert.Equal(t, "600/23", pct["exact_fraction"])
	assert.Equal(t, "26.09", pct["rounded"])

	assert.Equal(t, "P1", body["worst_point_id"])
	assert.Equal(t, "volatile", body["stability"])
	assert.Equal(t, "波动", body["stability_text"])
}

func TestStabilityThresholdsBothSides(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name      string
		flows     [3]string
		stability string
		text      string
		rounded   string
	}{
		{"exactly five percent is stable", [3]string{"10", "10", "10.5"}, "stable", "稳定", "5.00"},
		{"just above five percent is watch", [3]string{"10", "10", "10.51"}, "watch", "关注", "5.10"},
		{"exactly ten percent is watch", [3]string{"10", "10", "11"}, "watch", "关注", "10.00"},
		{"just above ten percent is volatile", [3]string{"10", "10", "11.01"}, "volatile", "波动", "10.10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postStability(t, r, stabilityBody(varyingRounds(tc.flows)...))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, tc.stability, body["stability"])
			assert.Equal(t, tc.text, body["stability_text"])
			assert.Equal(t, "A", body["worst_point_id"])
			worst := decimalField(t, body, "worst_fluctuation_percent")
			assert.Equal(t, tc.rounded, worst["rounded"])
		})
	}
}

// TestStabilityRoundTwoMissingPointLocated pins the precise error location
// when the second round does not reproduce the first round's point set.
func TestStabilityRoundTwoMissingPointLocated(t *testing.T) {
	r := NewRouter()

	cases := []struct {
		name        string
		body        string
		wantField   string
		wantMessage string
	}{
		{
			name: "second round replaces a first-round point",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"E", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField:   "rounds[1].measurements",
			wantMessage: `point id "D" from the first round is missing`,
		},
		{
			name: "second round drops a first-round point",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}, {"E", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}, {"E", "10"}},
			),
			wantField:   "rounds[1].measurements",
			wantMessage: `point id "E" from the first round is missing`,
		},
		{
			name: "later round adds a foreign point",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}, {"X", "10"}},
			),
			wantField:   "rounds[2].measurements",
			wantMessage: `point id "X" was not present in the first round`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postStability(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			assert.Equal(t, tc.wantMessage, errBody["message"])
			// No partial analysis may leak into the error response.
			full := decodeBody(t, w)
			assert.NotContains(t, full, "points")
			assert.NotContains(t, full, "stability")
			assert.NotContains(t, full, "worst_point_id")
		})
	}
}

func TestStabilityRoundCountBoundaries(t *testing.T) {
	r := NewRouter()
	ids := []string{"A", "B", "C", "D"}
	flows := []string{"10", "10", "10", "10"}

	for _, n := range []int{2, 11} {
		w := postStability(t, r, stabilityBody(uniformRounds(ids, flows, n)...))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "rounds=%d", n)
		errBody := decodeBody(t, w)["error"].(map[string]any)
		assert.Equal(t, "rounds", errBody["field"], "rounds=%d", n)
		assert.Equal(t, "rounds must contain between 3 and 10 rounds", errBody["message"])
	}

	for _, n := range []int{3, 10} {
		w := postStability(t, r, stabilityBody(uniformRounds(ids, flows, n)...))
		require.Equal(t, http.StatusOK, w.Code, "rounds=%d body=%s", n, w.Body.String())
		assert.Equal(t, float64(n), decodeBody(t, w)["round_count"])
	}
}

func TestStabilityPointCountBoundaries(t *testing.T) {
	r := NewRouter()
	mkIDs := func(n int) ([]string, []string) {
		ids := make([]string, n)
		flows := make([]string, n)
		for i := range ids {
			ids[i] = fmt.Sprintf("p%d", i)
			flows[i] = "10"
		}
		return ids, flows
	}

	for _, n := range []int{3, 65} {
		ids, flows := mkIDs(n)
		w := postStability(t, r, stabilityBody(uniformRounds(ids, flows, 3)...))
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, "points=%d", n)
		errBody := decodeBody(t, w)["error"].(map[string]any)
		assert.Equal(t, "rounds[0].measurements", errBody["field"], "points=%d", n)
	}

	for _, n := range []int{4, 64} {
		ids, flows := mkIDs(n)
		w := postStability(t, r, stabilityBody(uniformRounds(ids, flows, 3)...))
		require.Equal(t, http.StatusOK, w.Code, "points=%d body=%s", n, w.Body.String())
		assert.Equal(t, float64(n), decodeBody(t, w)["point_count"])
	}
}

func TestStabilityFieldPathPointsAtFirstViolation(t *testing.T) {
	r := NewRouter()

	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name: "invalid flow in second round",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "0"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[1].measurements[2].flow_lph",
		},
		{
			name: "flow above 100 in first round",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "100.001"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[0].measurements[1].flow_lph",
		},
		{
			name: "four decimal places rejected",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10.1234"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[0].measurements[3].flow_lph",
		},
		{
			name: "duplicate id within one round",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"B", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[1].measurements[2].id",
		},
		{
			name: "empty id",
			body: stabilityBody(
				[][2]string{{"", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[0].measurements[0].id",
		},
		{
			name: "flow as string rejected",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", `"10"`}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[1].measurements[1].flow_lph",
		},
		{
			name: "scientific notation rejected",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "1e1"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[2].measurements[0].flow_lph",
		},
		{
			name: "leading zero flow rejected",
			body: stabilityBody(
				[][2]string{{"A", "10"}, {"B", "01"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
				[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
			),
			wantField: "rounds[0].measurements[1].flow_lph",
		},
		{
			name: "repeated rounds array rejected as ambiguous",
			body: `{"rounds":[{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]}],` +
				`"rounds":[{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]}]}`,
			wantField: "rounds",
		},
		{
			name: "repeated measurements in one round",
			body: `{"rounds":[` +
				`{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}],` +
				`"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]},` +
				`{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]},` +
				`{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]}]}`,
			wantField: "rounds[0].measurements",
		},
		{
			name: "repeated flow_lph in one point",
			body: `{"rounds":[` +
				`{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":0,"flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]},` +
				`{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]},` +
				`{"measurements":[{"id":"A","flow_lph":10},{"id":"B","flow_lph":10},{"id":"C","flow_lph":10},{"id":"D","flow_lph":10}]}]}`,
			wantField: "rounds[0].measurements[1].flow_lph",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postStability(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			full := decodeBody(t, w)
			assert.NotContains(t, full, "points")
			assert.NotContains(t, full, "stability")
		})
	}
}

func TestStabilityMalformedBodies(t *testing.T) {
	r := NewRouter()

	cases := []struct {
		name   string
		body   string
		field  string
		status int
	}{
		{"not json", `{"rounds":`, "", http.StatusBadRequest},
		{"array root", `[1,2,3]`, "", http.StatusBadRequest},
		{"empty", ``, "", http.StatusBadRequest},
		{"missing rounds", `{"points":[]}`, "rounds", http.StatusUnprocessableEntity},
		{"rounds not array", `{"rounds":{}}`, "rounds", http.StatusUnprocessableEntity},
		{"round not object", `{"rounds":[1,2,3]}`, "rounds[0]", http.StatusUnprocessableEntity},
		{"round missing measurements", `{"rounds":[{},{},{}]}`, "rounds[0].measurements", http.StatusUnprocessableEntity},
		{"measurements not array", `{"rounds":[{"measurements":{}},{"measurements":[]},{"measurements":[]}]}`, "rounds[0].measurements", http.StatusUnprocessableEntity},
		{"item not object", `{"rounds":[{"measurements":[1,2,3,4]},{"measurements":[]},{"measurements":[]}]}`, "rounds[0].measurements[0]", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postStability(t, r, tc.body)
			assert.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusUnprocessableEntity {
				errBody := decodeBody(t, w)["error"].(map[string]any)
				assert.Equal(t, tc.field, errBody["field"])
			}
		})
	}
}

// TestStabilityFollowsFirstRoundOrderEndToEnd pins that a later round listing
// the same ids in a different order is accepted, matched by id, and reported
// in first-round order.
func TestStabilityFollowsFirstRoundOrderEndToEnd(t *testing.T) {
	r := NewRouter()
	w := postStability(t, r, stabilityBody(
		[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
		[][2]string{{"D", "10"}, {"C", "10"}, {"B", "11"}, {"A", "10"}},
		[][2]string{{"C", "10"}, {"B", "10.5"}, {"A", "10"}, {"D", "10"}},
	))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	points := stabilityPoints(t, body)
	order := make([]string, 0, len(points))
	for _, raw := range points {
		order = append(order, raw.(map[string]any)["id"].(string))
	}
	assert.Equal(t, []string{"A", "B", "C", "D"}, order)

	b := pointViewByID(t, body, "B")
	median := decimalField(t, b, "median_lph")
	assert.Equal(t, "10.5", median["decimal"])
	assert.Equal(t, "21/2", median["exact_fraction"])
	pct := decimalField(t, b, "fluctuation_percent")
	assert.Equal(t, "200/21", pct["exact_fraction"])
	assert.Equal(t, "9.52", pct["rounded"])
	assert.Equal(t, "B", body["worst_point_id"])
	assert.Equal(t, "watch", body["stability"])
}

// TestStabilityRecomputableFromResponseAlone proves the branch conclusion can
// be reproduced from the response's exact fractions alone: recompute every
// point's percentage as range/median*100, take the exact maximum and apply
// the 5/10 thresholds to it.
func TestStabilityRecomputableFromResponseAlone(t *testing.T) {
	r := NewRouter()
	w := postStability(t, r, stabilityBody(
		[][2]string{{"A", "9.80"}, {"B", "10.00"}, {"C", "10.10"}, {"D", "9.90"}},
		[][2]string{{"A", "9.90"}, {"B", "10.05"}, {"C", "10.00"}, {"D", "9.85"}},
		[][2]string{{"A", "9.85"}, {"B", "10.02"}, {"C", "10.05"}, {"D", "9.95"}},
	))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	var maxPct *big.Rat
	worstID := ""
	for _, raw := range stabilityPoints(t, body) {
		p := raw.(map[string]any)
		median := parseFraction(t, decimalField(t, p, "median_lph")["exact_fraction"].(string))
		spread := parseFraction(t, decimalField(t, p, "range_lph")["exact_fraction"].(string))
		served := parseFraction(t, decimalField(t, p, "fluctuation_percent")["exact_fraction"].(string))

		recomputed := new(big.Rat).Quo(spread, median)
		recomputed.Mul(recomputed, big.NewRat(100, 1))
		assert.Equal(t, 0, recomputed.Cmp(served), "point %s percent", p["id"])
		assert.Equal(t, roundHalfUpTwo(recomputed), decimalField(t, p, "fluctuation_percent")["rounded"])

		if maxPct == nil || recomputed.Cmp(maxPct) > 0 {
			maxPct = recomputed
			worstID = p["id"].(string)
		}
	}

	assert.Equal(t, worstID, body["worst_point_id"])
	worst := decimalField(t, body, "worst_fluctuation_percent")
	assert.Equal(t, roundHalfUpTwo(maxPct), worst["rounded"])

	want := "volatile"
	if maxPct.Cmp(big.NewRat(5, 1)) <= 0 {
		want = "stable"
	} else if maxPct.Cmp(big.NewRat(10, 1)) <= 0 {
		want = "watch"
	}
	assert.Equal(t, want, body["stability"])
}

// TestVerifyEndpointUnchangedByStability pins that the adjudication endpoint
// keeps responding identically with the stability route registered.
func TestVerifyEndpointUnchangedByStability(t *testing.T) {
	r := NewRouter()
	w := postJSON(t, r, validBody([]string{"9.8", "9.9", "10.0", "10.1"}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, "98.49", decimalField(t, body, "du_percent")["rounded"])
	assert.Equal(t, "pass", body["verdict"])
	assert.NotContains(t, body, "stability")
}
