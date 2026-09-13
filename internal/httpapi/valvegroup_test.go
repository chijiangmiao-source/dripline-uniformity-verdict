package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postValveGroupPlan(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/valve-group-plan", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// valvePlanBody builds a request with the given capacity and design flows,
// assigning ids e0, e1, ... unless explicit ids are supplied.
func valvePlanBody(capacity string, flows []string, ids ...string) string {
	if len(ids) == 0 {
		ids = make([]string, len(flows))
		for i := range flows {
			ids[i] = fmt.Sprintf("e%d", i)
		}
	}
	var buf bytes.Buffer
	buf.WriteString(`{"pump_capacity_lph":`)
	buf.WriteString(capacity)
	buf.WriteString(`,"emitters":[`)
	for i := range flows {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`{"id":`)
		idJSON, _ := json.Marshal(ids[i])
		buf.Write(idJSON)
		buf.WriteString(`,"design_flow_lph":`)
		buf.WriteString(flows[i])
		buf.WriteByte('}')
	}
	buf.WriteString(`]}`)
	return buf.String()
}

// manyFlows returns n copies of "1", for count-boundary requests.
func manyFlows(n int) []string {
	flows := make([]string, n)
	for i := range flows {
		flows[i] = "1"
	}
	return flows
}

func valveGroups(t *testing.T, body map[string]any) []any {
	t.Helper()
	groups, ok := body["groups"].([]any)
	require.True(t, ok, "groups is not an array: %v", body["groups"])
	return groups
}

func valveGroupAt(t *testing.T, body map[string]any, idx int) map[string]any {
	t.Helper()
	g, ok := valveGroups(t, body)[idx].(map[string]any)
	require.True(t, ok, "group %d is not an object", idx)
	return g
}

func TestValveGroupPlanSingleGroup(t *testing.T) {
	// 4 × 20 = 80 <= 100: the whole branch runs as one valve group.
	r := NewRouter()
	w := postValveGroupPlan(t, r, valvePlanBody("100", []string{"20", "20", "20", "20"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, float64(4), body["emitter_count"])
	assert.Equal(t, float64(1), body["group_count"])

	capacity := decimalField(t, body, "pump_capacity_lph")
	assert.Equal(t, "100", capacity["decimal"])
	assert.Equal(t, "100/1", capacity["exact_fraction"])
	assert.Equal(t, true, capacity["terminating"])

	g := valveGroupAt(t, body, 0)
	assert.Equal(t, float64(1), g["group_number"])
	assert.Equal(t, []any{"e0", "e1", "e2", "e3"}, g["member_ids"])

	total := decimalField(t, g, "total_design_flow_lph")
	assert.Equal(t, "80", total["decimal"])
	assert.Equal(t, "80/1", total["exact_fraction"])
	assert.Equal(t, true, total["terminating"])

	remaining := decimalField(t, g, "remaining_capacity_lph")
	assert.Equal(t, "20", remaining["decimal"])
	assert.Equal(t, "20/1", remaining["exact_fraction"])
}

func TestValveGroupPlanBoundaryCapacityDeterministicGrouping(t *testing.T) {
	// 40+30+30 = 100 saturates the pump exactly and stays in group 1; the
	// 25 opens group 2, the 50 joins it (75 <= 100), and the final 50 would
	// reach 125, so it forms group 3 alone.
	r := NewRouter()
	w := postValveGroupPlan(t, r, valvePlanBody("100",
		[]string{"40", "30", "30", "25", "50", "50"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, float64(6), body["emitter_count"])
	assert.Equal(t, float64(3), body["group_count"])

	g1 := valveGroupAt(t, body, 0)
	assert.Equal(t, float64(1), g1["group_number"])
	assert.Equal(t, []any{"e0", "e1", "e2"}, g1["member_ids"])
	assert.Equal(t, "100", decimalField(t, g1, "total_design_flow_lph")["decimal"])
	// Exactly saturated: no remaining capacity.
	assert.Equal(t, "0", decimalField(t, g1, "remaining_capacity_lph")["decimal"])
	assert.Equal(t, "0/1", decimalField(t, g1, "remaining_capacity_lph")["exact_fraction"])

	g2 := valveGroupAt(t, body, 1)
	assert.Equal(t, float64(2), g2["group_number"])
	assert.Equal(t, []any{"e3", "e4"}, g2["member_ids"])
	assert.Equal(t, "75", decimalField(t, g2, "total_design_flow_lph")["decimal"])
	assert.Equal(t, "25", decimalField(t, g2, "remaining_capacity_lph")["decimal"])

	g3 := valveGroupAt(t, body, 2)
	assert.Equal(t, float64(3), g3["group_number"])
	assert.Equal(t, []any{"e5"}, g3["member_ids"])
	assert.Equal(t, "50", decimalField(t, g3, "total_design_flow_lph")["decimal"])
	assert.Equal(t, "50", decimalField(t, g3, "remaining_capacity_lph")["decimal"])
}

func TestValveGroupPlanExactThousandthsAreRecomputable(t *testing.T) {
	// 3.333+3.333+3.334 = 10.000 exactly; the response fractions let the
	// client recompute the grouping without any float detour.
	r := NewRouter()
	w := postValveGroupPlan(t, r, valvePlanBody("10.2",
		[]string{"3.333", "3.333", "3.334", "0.5"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)

	capacity := decimalField(t, body, "pump_capacity_lph")
	assert.Equal(t, "10.2", capacity["decimal"])
	assert.Equal(t, "51/5", capacity["exact_fraction"])

	assert.Equal(t, float64(2), body["group_count"])
	g1 := valveGroupAt(t, body, 0)
	total := decimalField(t, g1, "total_design_flow_lph")
	assert.Equal(t, "10", total["decimal"])
	assert.Equal(t, "10/1", total["exact_fraction"])
	remaining := decimalField(t, g1, "remaining_capacity_lph")
	assert.Equal(t, "0.2", remaining["decimal"])
	assert.Equal(t, "1/5", remaining["exact_fraction"])

	g2 := valveGroupAt(t, body, 1)
	assert.Equal(t, []any{"e3"}, g2["member_ids"])
	assert.Equal(t, "1/2", decimalField(t, g2, "total_design_flow_lph")["exact_fraction"])
	assert.Equal(t, "97/10", decimalField(t, g2, "remaining_capacity_lph")["exact_fraction"])
}

func TestValveGroupPlanRejectsEmitterExceedingCapacity(t *testing.T) {
	// The second emitter alone draws 60 > 50: no grouping can hold it, so
	// the request is rejected at that point's design_flow_lph.
	r := NewRouter()
	w := postValveGroupPlan(t, r, valvePlanBody("50", []string{"20", "60", "20", "20"}))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	errBody := decodeBody(t, w)["error"].(map[string]any)
	assert.Equal(t, "emitters[1].design_flow_lph", errBody["field"])
	assert.Equal(t, "design_flow_lph exceeds the pump capacity", errBody["message"])
	// No partial grouping leaks alongside the error.
	assert.NotContains(t, decodeBody(t, w), "groups")
}

func TestValveGroupPlanAcceptsEmitterExactlyAtCapacity(t *testing.T) {
	// An emitter equal to the capacity is not "exceeding" it: it forms a
	// saturated single-member group.
	r := NewRouter()
	w := postValveGroupPlan(t, r, valvePlanBody("50", []string{"50", "50", "50", "50"}))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, float64(4), body["group_count"])
	for i := 0; i < 4; i++ {
		g := valveGroupAt(t, body, i)
		assert.Equal(t, float64(i+1), g["group_number"])
		assert.Equal(t, "0", decimalField(t, g, "remaining_capacity_lph")["decimal"])
	}
}

func TestValveGroupPlanRejectsDuplicateFields(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			// Two pump capacities must not resolve to the second one.
			name: "duplicate pump capacity rejected as ambiguous",
			body: `{"pump_capacity_lph":50,"pump_capacity_lph":100,` +
				`"emitters":[{"id":"a","design_flow_lph":20},{"id":"b","design_flow_lph":20},` +
				`{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantField: "pump_capacity_lph",
		},
		{
			name: "duplicate emitters array rejected as ambiguous",
			body: `{"pump_capacity_lph":100,` +
				`"emitters":[{"id":"a","design_flow_lph":60},{"id":"b","design_flow_lph":60},` +
				`{"id":"c","design_flow_lph":60},{"id":"d","design_flow_lph":60}],` +
				`"emitters":[{"id":"a","design_flow_lph":20},{"id":"b","design_flow_lph":20},` +
				`{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantField: "emitters",
		},
		{
			name: "invalid then valid design_flow_lph located at the point",
			body: `{"pump_capacity_lph":100,"emitters":[{"id":"a","design_flow_lph":20},` +
				`{"id":"b","design_flow_lph":0,"design_flow_lph":20},` +
				`{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantField: "emitters[1].design_flow_lph",
		},
		{
			name: "repeated id located at the point",
			body: `{"pump_capacity_lph":100,"emitters":[{"id":"a","design_flow_lph":20},` +
				`{"id":"b","id":"c","design_flow_lph":20},` +
				`{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantField: "emitters[1].id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postValveGroupPlan(t, r, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			assert.Equal(t, tc.wantField, errBody["field"])
			assert.Contains(t, errBody["message"], "is duplicated")
			assert.NotContains(t, decodeBody(t, w), "groups")
		})
	}
}

func TestValveGroupPlanValidationErrors(t *testing.T) {
	r := NewRouter()
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantField  string
	}{
		{
			name:       "capacity missing",
			body:       `{"emitters":[{"id":"a","design_flow_lph":20},{"id":"b","design_flow_lph":20},{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "pump_capacity_lph",
		},
		{
			name:       "capacity zero",
			body:       valvePlanBody("0", []string{"20", "20", "20", "20"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "pump_capacity_lph",
		},
		{
			name:       "capacity over three decimals",
			body:       valvePlanBody("100.0001", []string{"20", "20", "20", "20"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "pump_capacity_lph",
		},
		{
			name:       "capacity not a number",
			body:       `{"pump_capacity_lph":"100","emitters":[{"id":"a","design_flow_lph":20},{"id":"b","design_flow_lph":20},{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "pump_capacity_lph",
		},
		{
			name:       "emitters missing",
			body:       `{"pump_capacity_lph":100}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters",
		},
		{
			name:       "too few emitters",
			body:       valvePlanBody("100", []string{"20", "20", "20"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters",
		},
		{
			name:       "too many emitters",
			body:       valvePlanBody("100", manyFlows(65)),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters",
		},
		{
			name:       "design flow zero",
			body:       valvePlanBody("100", []string{"20", "0", "20", "20"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[1].design_flow_lph",
		},
		{
			name:       "design flow over three decimals",
			body:       valvePlanBody("100", []string{"20", "20.0001", "20", "20"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[1].design_flow_lph",
		},
		{
			name:       "design flow scientific notation",
			body:       `{"pump_capacity_lph":100,"emitters":[{"id":"a","design_flow_lph":20},{"id":"b","design_flow_lph":2e1},{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[1].design_flow_lph",
		},
		{
			name:       "design flow missing",
			body:       `{"pump_capacity_lph":100,"emitters":[{"id":"a","design_flow_lph":20},{"id":"b"},{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[1].design_flow_lph",
		},
		{
			name:       "duplicate emitter id",
			body:       valvePlanBody("100", []string{"20", "20", "20", "20"}, "a", "b", "a", "c"),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[2].id",
		},
		{
			name:       "empty emitter id",
			body:       valvePlanBody("100", []string{"20", "20", "20", "20"}, "a", "", "c", "d"),
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[1].id",
		},
		{
			name:       "emitter not an object",
			body:       `{"pump_capacity_lph":100,"emitters":[{"id":"a","design_flow_lph":20},7,{"id":"c","design_flow_lph":20},{"id":"d","design_flow_lph":20}]}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantField:  "emitters[1]",
		},
		{
			name:       "malformed body is a 400",
			body:       `{"pump_capacity_lph":`,
			wantStatus: http.StatusBadRequest,
			wantField:  "",
		},
		{
			name:       "non-object body is a 400",
			body:       `[1,2,3]`,
			wantStatus: http.StatusBadRequest,
			wantField:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postValveGroupPlan(t, r, tc.body)
			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			errBody := decodeBody(t, w)["error"].(map[string]any)
			if tc.wantField == "" {
				assert.NotContains(t, errBody, "field")
			} else {
				assert.Equal(t, tc.wantField, errBody["field"])
			}
			// No partial grouping leaks alongside an error.
			assert.NotContains(t, decodeBody(t, w), "groups")
		})
	}
}

func TestValveGroupPlanBoundaryEmitterCounts(t *testing.T) {
	r := NewRouter()

	four := postValveGroupPlan(t, r, valvePlanBody("100", []string{"10", "10", "10", "10"}))
	require.Equal(t, http.StatusOK, four.Code, four.Body.String())
	assert.Equal(t, float64(4), decodeBody(t, four)["emitter_count"])

	sixtyFour := postValveGroupPlan(t, r, valvePlanBody("100", manyFlows(64)))
	require.Equal(t, http.StatusOK, sixtyFour.Code, sixtyFour.Body.String())
	body := decodeBody(t, sixtyFour)
	assert.Equal(t, float64(64), body["emitter_count"])
	// 64 × 1 = 64 <= 100: a single group holds every emitter.
	assert.Equal(t, float64(1), body["group_count"])
	assert.Equal(t, "36", decimalField(t, valveGroupAt(t, body, 0), "remaining_capacity_lph")["decimal"])
}

// TestValveGroupPlanDoesNotChangeExistingEndpoints pins compatibility: adding
// the grouping route leaves the three analysis routes and the health check
// registered and answering.
func TestValveGroupPlanDoesNotChangeExistingEndpoints(t *testing.T) {
	r := NewRouter()

	health := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthW := httptest.NewRecorder()
	r.ServeHTTP(healthW, health)
	require.Equal(t, http.StatusOK, healthW.Code)
	assert.Equal(t, "ok", decodeBody(t, healthW)["status"])

	verify := postJSON(t, r, validBody([]string{"9.8", "9.9", "10", "10.1"}))
	require.Equal(t, http.StatusOK, verify.Code)
	assert.Equal(t, "pass", decodeBody(t, verify)["verdict"])

	stabilityReq := httptest.NewRequest(http.MethodPost, "/api/v1/stability", bytes.NewBufferString(stabilityBody(
		[][2]string{{"A", "9.9"}, {"B", "10"}, {"C", "10.1"}, {"D", "10"}},
		[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
		[][2]string{{"A", "10"}, {"B", "10"}, {"C", "10"}, {"D", "10"}},
	)))
	stabilityReq.Header.Set("Content-Type", "application/json")
	stabilityW := httptest.NewRecorder()
	r.ServeHTTP(stabilityW, stabilityReq)
	assert.Equal(t, http.StatusOK, stabilityW.Code, stabilityW.Body.String())

	blockage := postBlockage(t, r, validBody([]string{"9.9", "10", "10.1", "10"}))
	require.Equal(t, http.StatusOK, blockage.Code, blockage.Body.String())
}
