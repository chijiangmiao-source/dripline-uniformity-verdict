package httpapi

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/example/drip-du/internal/dripdu"
	"github.com/gin-gonic/gin"
)

// valveGroupView is one consecutive valve group: its 1-based number, the
// member emitter ids in installation order, and the exact total design flow
// and remaining capacity. Both flows are sums of three-decimal values, so
// their decimal renderings are always exact (terminating is always true).
type valveGroupView struct {
	Number            int         `json:"group_number"`
	MemberIDs         []string    `json:"member_ids"`
	TotalDesignFlow   decimalView `json:"total_design_flow_lph"`
	RemainingCapacity decimalView `json:"remaining_capacity_lph"`
}

// valveGroupPlanResponse is the grouping plan returned for one valid
// request. Every flow carries its exact fraction, so the grouping can be
// recomputed from this document alone by accumulating the emitters in input
// order against pump_capacity_lph.
type valveGroupPlanResponse struct {
	PumpCapacity decimalView      `json:"pump_capacity_lph"`
	EmitterCount int              `json:"emitter_count"`
	GroupCount   int              `json:"group_count"`
	Groups       []valveGroupView `json:"groups"`
}

// valveGroupPlanRequest is the parsed request shape:
//
//	{ "pump_capacity_lph": 100,
//	  "emitters": [ {"id": "e1", "design_flow_lph": 30}, ... ] }
//
// Capacity is the pump's available flow; Emitters holds the 4..64 ordered
// emitters, each individually within capacity.
type valveGroupPlanRequest struct {
	Capacity *big.Rat
	Emitters []dripdu.ValveEmitter
}

func handleValveGroupPlan(c *gin.Context) {
	// Same policy as the other endpoints: no fixed body cap, so long ids
	// across 64 emitters cannot be rejected by an artificial limit.
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		writeValidationError(c, http.StatusBadRequest, "", "request body must be a non-empty JSON document")
		return
	}

	req, verr := parseValveGroupPlanRequest(body)
	if verr != nil {
		status := http.StatusUnprocessableEntity
		if verr.badRequest {
			status = http.StatusBadRequest
		}
		// 422 for semantic violations (capacity, count, ids, flows, an
		// emitter that alone exceeds the pump); 400 when the body is not
		// even a JSON object. Either way only the first offending field
		// path is named and no partial grouping leaks.
		writeValidationError(c, status, verr.Field, verr.Message)
		return
	}

	groups := dripdu.PlanValveGroups(req.Capacity, req.Emitters)

	resp := valveGroupPlanResponse{
		PumpCapacity: toDecimalView(req.Capacity),
		EmitterCount: len(req.Emitters),
		GroupCount:   len(groups),
		Groups:       make([]valveGroupView, 0, len(groups)),
	}
	for _, g := range groups {
		resp.Groups = append(resp.Groups, valveGroupView{
			Number:            g.Number,
			MemberIDs:         g.MemberIDs,
			TotalDesignFlow:   toDecimalView(g.TotalDesignFlow),
			RemainingCapacity: toDecimalView(g.RemainingCapacity),
		})
	}
	c.JSON(http.StatusOK, resp)
}

// parseValveGroupPlanRequest validates the whole request and returns the
// first violation in document order, addressed by a field path such as
// "emitters[3].design_flow_lph". The capacity is checked before the emitter
// list because every per-emitter rule (including the fits-the-pump rule)
// depends on it; an emitter whose design flow alone exceeds the capacity is
// located at its own design_flow_lph field.
func parseValveGroupPlanRequest(body []byte) (valveGroupPlanRequest, *validationError) {
	root, err := parseJSONObject(body)
	if err != nil {
		if dup, ok := asDuplicateField(err); ok {
			// Two pump_capacity_lph members (or any repeated root member)
			// make the request ambiguous: reject rather than pick one
			// silently.
			return valveGroupPlanRequest{}, duplicateFieldViolation("", dup)
		}
		return valveGroupPlanRequest{}, syntaxError("request body must be a JSON object")
	}
	if root == nil {
		return valveGroupPlanRequest{}, syntaxError("request body must be a JSON object")
	}

	capacity, verr := parseCapacityField(root["pump_capacity_lph"], "pump_capacity_lph")
	if verr != nil {
		return valveGroupPlanRequest{}, verr
	}

	rawItems, ok := root["emitters"]
	if !ok {
		return valveGroupPlanRequest{}, &validationError{
			Field:   "emitters",
			Message: "emitters is required",
		}
	}

	rawList, err := parseJSONArray(rawItems)
	if err != nil {
		return valveGroupPlanRequest{}, &validationError{
			Field:   "emitters",
			Message: "emitters must be an array",
		}
	}

	n := len(rawList)
	if n < 4 || n > 64 {
		return valveGroupPlanRequest{}, &validationError{
			Field:   "emitters",
			Message: "emitters must contain between 4 and 64 points",
		}
	}

	seenIDs := make(map[string]struct{}, n)
	emitters := make([]dripdu.ValveEmitter, 0, n)
	for i, raw := range rawList {
		itemPath := fmt.Sprintf("emitters[%d]", i)

		fields, err := parseJSONObject(raw)
		if err != nil {
			if dup, ok := asDuplicateField(err); ok {
				// A repeated field inside one emitter (e.g. two
				// design_flow_lph values) is rejected at that field
				// instead of silently keeping the later value.
				return valveGroupPlanRequest{}, duplicateFieldViolation(itemPath, dup)
			}
			return valveGroupPlanRequest{}, &validationError{
				Field:   itemPath,
				Message: "emitter must be a JSON object",
			}
		}
		if fields == nil {
			return valveGroupPlanRequest{}, &validationError{
				Field:   itemPath,
				Message: "emitter must be a JSON object",
			}
		}

		id, verr := parseID(fields["id"], seenIDs, itemPath+".id")
		if verr != nil {
			return valveGroupPlanRequest{}, verr
		}

		flow, verr := parseDesignFlowField(fields["design_flow_lph"], itemPath+".design_flow_lph")
		if verr != nil {
			return valveGroupPlanRequest{}, verr
		}
		if flow.Cmp(capacity) > 0 {
			// One emitter alone exceeds the pump's available flow: no
			// grouping can ever accommodate it, so the whole request is
			// rejected at that point's design_flow_lph.
			return valveGroupPlanRequest{}, &validationError{
				Field:   itemPath + ".design_flow_lph",
				Message: "design_flow_lph exceeds the pump capacity",
			}
		}

		emitters = append(emitters, dripdu.ValveEmitter{ID: id, DesignFlow: flow})
	}

	return valveGroupPlanRequest{Capacity: capacity, Emitters: emitters}, nil
}

// parseCapacityField validates the pump's available flow. The accepted form
// matches flow_lph (plain decimal, at most three places) but carries no
// 100 lph ceiling: the pump may serve several groups.
func parseCapacityField(raw json.RawMessage, path string) (*big.Rat, *validationError) {
	token := strings.TrimSpace(string(raw))
	if token == "" || token == "null" {
		return nil, &validationError{Field: path, Message: "pump_capacity_lph is required and must be a JSON number"}
	}
	// Same gate as flow_lph: only bare number literals reach decimal
	// parsing; everything else is a type error.
	if !isNumericLiteral(token) {
		return nil, &validationError{Field: path, Message: "pump_capacity_lph must be a JSON number"}
	}
	capacity, err := dripdu.ParsePumpCapacity(token)
	if err != nil {
		return nil, &validationError{Field: path, Message: err.Error()}
	}
	return capacity, nil
}

// parseDesignFlowField validates one emitter's design flow. Like the pump
// capacity it is a plain positive decimal with at most three places and no
// 100 lph ceiling.
func parseDesignFlowField(raw json.RawMessage, path string) (*big.Rat, *validationError) {
	token := strings.TrimSpace(string(raw))
	if token == "" || token == "null" {
		return nil, &validationError{Field: path, Message: "design_flow_lph is required and must be a JSON number"}
	}
	if !isNumericLiteral(token) {
		return nil, &validationError{Field: path, Message: "design_flow_lph must be a JSON number"}
	}
	flow, err := dripdu.ParseDesignFlow(token)
	if err != nil {
		return nil, &validationError{Field: path, Message: err.Error()}
	}
	return flow, nil
}
