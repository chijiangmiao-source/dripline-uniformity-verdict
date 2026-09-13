// Package httpapi exposes the distribution-uniformity adjudication over HTTP.
package httpapi

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/example/drip-du/internal/dripdu"
)

// validationError pinpoints the first field that failed validation. Nothing
// derived from the request (no means or DU) is ever reported alongside it.
type validationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	// badRequest marks syntax-level failures (body is not a JSON object),
	// which must be 400 rather than the 422 used for semantic violations.
	badRequest bool
}

func syntaxError(message string) *validationError {
	return &validationError{Message: message, badRequest: true}
}

// verifyRequest is the accepted request shape:
//
//	{ "measurements": [ {"id": "p1", "flow_lph": 12.34}, ... ] }
//
// Every measurement may additionally carry "rated_flow_lph"; the field must
// be present on all measurements or on none.
type verifyRequest struct {
	Measurements []dripdu.Measurement
}

// parseRequest validates the whole request in document order and returns the
// first violation found, addressed by a field path such as
// "measurements[3].flow_lph".
func parseRequest(body []byte) (verifyRequest, *validationError) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || root == nil {
		return verifyRequest{}, syntaxError("request body must be a JSON object")
	}

	rawItems, ok := root["measurements"]
	if !ok {
		return verifyRequest{}, &validationError{
			Field:   "measurements",
			Message: "measurements is required",
		}
	}

	var rawList []json.RawMessage
	if err := json.Unmarshal(rawItems, &rawList); err != nil {
		return verifyRequest{}, &validationError{
			Field:   "measurements",
			Message: "measurements must be an array",
		}
	}

	n := len(rawList)
	if n < 4 || n > 64 {
		return verifyRequest{}, &validationError{
			Field:   "measurements",
			Message: "measurements must contain between 4 and 64 points",
		}
	}

	seenIDs := make(map[string]struct{}, n)
	points := make([]dripdu.Measurement, 0, n)
	ratedMode := false // whether the first measurement carries rated_flow_lph

	for i, raw := range rawList {
		itemPath := fmt.Sprintf("measurements[%d]", i)

		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return verifyRequest{}, &validationError{
				Field:   itemPath,
				Message: "measurement must be a JSON object",
			}
		}

		id, verr := parseID(fields["id"], seenIDs, itemPath+".id")
		if verr != nil {
			return verifyRequest{}, verr
		}

		flow, verr := parseFlowField(fields["flow_lph"], itemPath+".flow_lph")
		if verr != nil {
			return verifyRequest{}, verr
		}

		rated, hasRated, verr := parseRatedFlowField(fields, itemPath+".rated_flow_lph")
		if verr != nil {
			return verifyRequest{}, verr
		}
		if i == 0 {
			ratedMode = hasRated
		} else if hasRated != ratedMode {
			// rated_flow_lph is all-or-nothing across one request; the first
			// measurement set the expectation, so this item is the first
			// offender either way.
			return verifyRequest{}, &validationError{
				Field:   itemPath + ".rated_flow_lph",
				Message: "rated_flow_lph must be provided for every measurement or omitted for all",
			}
		}

		points = append(points, dripdu.Measurement{ID: id, Flow: flow, Rated: rated})
	}

	return verifyRequest{Measurements: points}, nil
}

func parseID(raw json.RawMessage, seen map[string]struct{}, path string) (string, *validationError) {
	if len(raw) == 0 {
		return "", &validationError{Field: path, Message: "id is required"}
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return "", &validationError{Field: path, Message: "id must be a string"}
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", &validationError{Field: path, Message: "id must be a string"}
	}
	if strings.TrimSpace(id) == "" {
		return "", &validationError{Field: path, Message: "id must be a non-empty string"}
	}
	if _, dup := seen[id]; dup {
		return "", &validationError{Field: path, Message: fmt.Sprintf("id %q is duplicated", id)}
	}
	seen[id] = struct{}{}
	return id, nil
}

func parseFlowField(raw json.RawMessage, path string) (*big.Rat, *validationError) {
	token := strings.TrimSpace(string(raw))
	if token == "" || token == "null" {
		return nil, &validationError{Field: path, Message: "flow_lph is required and must be a JSON number"}
	}
	// Only bare number literals reach decimal parsing: strings, booleans,
	// arrays and objects are rejected as type errors. Numeric literals with an
	// exponent pass this gate and are then rejected by ParseFlow, so the
	// caller gets the precise "at most three decimal places" rule rather than
	// a misleading type error.
	if !isNumericLiteral(token) {
		return nil, &validationError{Field: path, Message: "flow_lph must be a JSON number"}
	}
	flow, err := dripdu.ParseFlow(token)
	if err != nil {
		return nil, &validationError{Field: path, Message: err.Error()}
	}
	return flow, nil
}

// parseRatedFlowField validates the optional rated_flow_lph of one
// measurement. It reports whether the key was present at all so the caller
// can enforce the all-or-nothing rule across the request.
func parseRatedFlowField(fields map[string]json.RawMessage, path string) (*big.Rat, bool, *validationError) {
	raw, present := fields["rated_flow_lph"]
	if !present {
		return nil, false, nil
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || token == "null" {
		return nil, false, &validationError{Field: path, Message: "rated_flow_lph must be a JSON number"}
	}
	// Same gate as flow_lph: only bare number literals reach decimal
	// parsing; everything else is a type error.
	if !isNumericLiteral(token) {
		return nil, false, &validationError{Field: path, Message: "rated_flow_lph must be a JSON number"}
	}
	rated, err := dripdu.ParseRatedFlow(token)
	if err != nil {
		return nil, false, &validationError{Field: path, Message: err.Error()}
	}
	return rated, true, nil
}

// isNumericLiteral reports whether s has the shape of a JSON number token
// (without validating the decimal-place rule that ParseFlow enforces).
func isNumericLiteral(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[i] == '-' {
		i++
	}
	digits := 0
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		digits++
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		frac := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			frac++
		}
		if frac == 0 {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			exp++
		}
		if exp == 0 {
			return false
		}
	}
	return i == len(s)
}
