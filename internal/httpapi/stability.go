package httpapi

import (
	"fmt"
	"math/big"
	"net/http"

	"github.com/example/drip-du/internal/dripdu"
	"github.com/gin-gonic/gin"
)

// percentView carries one exact percentage two ways: ExactFraction
// ("num/den") is authoritative and always enough to recompute the stability
// conclusion; Rounded is the two-decimal ROUND_HALF_UP rendering shown to
// humans. Rounding is display-only: the conclusion derives from the exact
// fraction, never from Rounded.
type percentView struct {
	Rounded       string `json:"rounded"`
	ExactFraction string `json:"exact_fraction"`
}

// stabilityPointView is the per-point fluctuation profile across all rounds.
type stabilityPointView struct {
	ID                 string      `json:"id"`
	Median             decimalView `json:"median_lph"`
	Range              decimalView `json:"range_lph"`
	FluctuationPercent percentView `json:"fluctuation_percent"`
}

// stabilityResponse is the fluctuation analysis returned for one valid
// repeated-measurement request. Points are listed in first-round order and
// every numeric result carries its exact fraction, so the branch conclusion
// can be recomputed from this document alone.
type stabilityResponse struct {
	RoundCount              int                  `json:"round_count"`
	PointCount              int                  `json:"point_count"`
	Points                  []stabilityPointView `json:"points"`
	WorstPointID            string               `json:"worst_point_id"`
	WorstFluctuationPercent percentView          `json:"worst_fluctuation_percent"`
	Stability               string               `json:"stability"`
	StabilityText           string               `json:"stability_text"`
}

// stabilityRequest is the parsed request shape:
//
//	{ "rounds": [ {"measurements": [ {"id": "p1", "flow_lph": 12.34}, ... ]}, ... ] }
//
// Rounds[i] holds the i-th round's validated measurements; every round
// carries exactly the first round's set of point ids.
type stabilityRequest struct {
	Rounds [][]dripdu.Measurement
}

func handleStability(c *gin.Context) {
	// Same policy as the adjudication endpoint: no fixed body cap, so long
	// ids across many rounds cannot be rejected by an artificial limit.
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		writeValidationError(c, http.StatusBadRequest, "", "request body must be a non-empty JSON document")
		return
	}

	req, verr := parseStabilityRequest(body)
	if verr != nil {
		status := http.StatusUnprocessableEntity
		if verr.badRequest {
			status = http.StatusBadRequest
		}
		// 422 for semantic violations (round count, point sets, ids, flows);
		// 400 when the body is not even a JSON object. Either way only the
		// first offending field path is named and no partial analysis leaks.
		writeValidationError(c, status, verr.Field, verr.Message)
		return
	}

	result := dripdu.AnalyzeStability(req.Rounds)

	resp := stabilityResponse{
		RoundCount:   result.RoundCount,
		PointCount:   result.PointCount,
		Points:       make([]stabilityPointView, 0, len(result.Points)),
		WorstPointID: result.WorstPointID,
		WorstFluctuationPercent: percentView{
			Rounded:       result.WorstPercentRounded,
			ExactFraction: fractionString(result.WorstPercent),
		},
		Stability:     string(result.Stability),
		StabilityText: stabilityText(result.Stability),
	}
	for _, p := range result.Points {
		resp.Points = append(resp.Points, stabilityPointView{
			ID:     p.ID,
			Median: toDecimalView(p.Median),
			Range:  toDecimalView(p.Range),
			FluctuationPercent: percentView{
				Rounded:       p.PercentRounded,
				ExactFraction: fractionString(p.Percent),
			},
		})
	}
	c.JSON(http.StatusOK, resp)
}

// parseStabilityRequest validates the whole request and returns the first
// violation in document order, addressed by a field path such as
// "rounds[1].measurements[2].flow_lph". Rounds are checked one at a time in
// submission order; a later round's point-set mismatch with the first round
// is located at that round's measurements array.
func parseStabilityRequest(body []byte) (stabilityRequest, *validationError) {
	root, err := parseJSONObject(body)
	if err != nil || root == nil {
		return stabilityRequest{}, syntaxError("request body must be a JSON object")
	}

	rawRounds, ok := root["rounds"]
	if !ok {
		return stabilityRequest{}, &validationError{
			Field:   "rounds",
			Message: "rounds is required",
		}
	}

	rawList, err := parseJSONArray(rawRounds)
	if err != nil {
		return stabilityRequest{}, &validationError{
			Field:   "rounds",
			Message: "rounds must be an array",
		}
	}

	if len(rawList) < 3 || len(rawList) > 10 {
		return stabilityRequest{}, &validationError{
			Field:   "rounds",
			Message: "rounds must contain between 3 and 10 rounds",
		}
	}

	rounds := make([][]dripdu.Measurement, 0, len(rawList))
	var firstIDs []string
	var firstSet map[string]struct{}

	for i, raw := range rawList {
		roundPath := fmt.Sprintf("rounds[%d]", i)

		fields, err := parseJSONObject(raw)
		if err != nil || fields == nil {
			return stabilityRequest{}, &validationError{
				Field:   roundPath,
				Message: "round must be a JSON object",
			}
		}

		rawItems, ok := fields["measurements"]
		if !ok {
			return stabilityRequest{}, &validationError{
				Field:   roundPath + ".measurements",
				Message: "measurements is required",
			}
		}
		items, err := parseJSONArray(rawItems)
		if err != nil {
			return stabilityRequest{}, &validationError{
				Field:   roundPath + ".measurements",
				Message: "measurements must be an array",
			}
		}
		if len(items) < 4 || len(items) > 64 {
			return stabilityRequest{}, &validationError{
				Field:   roundPath + ".measurements",
				Message: "measurements must contain between 4 and 64 points",
			}
		}

		seen := make(map[string]struct{}, len(items))
		points := make([]dripdu.Measurement, 0, len(items))
		for j, rawItem := range items {
			itemPath := fmt.Sprintf("%s.measurements[%d]", roundPath, j)

			itemFields, err := parseJSONObject(rawItem)
			if err != nil || itemFields == nil {
				return stabilityRequest{}, &validationError{
					Field:   itemPath,
					Message: "measurement must be a JSON object",
				}
			}

			id, verr := parseID(itemFields["id"], seen, itemPath+".id")
			if verr != nil {
				return stabilityRequest{}, verr
			}

			flow, verr := parseFlowField(itemFields["flow_lph"], itemPath+".flow_lph")
			if verr != nil {
				return stabilityRequest{}, verr
			}

			points = append(points, dripdu.Measurement{ID: id, Flow: flow})
		}

		if i == 0 {
			// The first round fixes both the canonical point order and the id
			// set every later round must reproduce exactly.
			firstIDs = make([]string, 0, len(points))
			firstSet = make(map[string]struct{}, len(points))
			for _, p := range points {
				firstIDs = append(firstIDs, p.ID)
				firstSet[p.ID] = struct{}{}
			}
		} else {
			// A first-round id absent here is reported before any foreign id,
			// so a renamed point surfaces as the missing original.
			for _, id := range firstIDs {
				if _, present := seen[id]; !present {
					return stabilityRequest{}, &validationError{
						Field:   roundPath + ".measurements",
						Message: fmt.Sprintf("point id %q from the first round is missing", id),
					}
				}
			}
			for _, p := range points {
				if _, present := firstSet[p.ID]; !present {
					return stabilityRequest{}, &validationError{
						Field:   roundPath + ".measurements",
						Message: fmt.Sprintf("point id %q was not present in the first round", p.ID),
					}
				}
			}
		}

		rounds = append(rounds, points)
	}

	return stabilityRequest{Rounds: rounds}, nil
}

func fractionString(r *big.Rat) string {
	return r.Num().String() + "/" + r.Denom().String()
}

func stabilityText(s dripdu.Stability) string {
	switch s {
	case dripdu.StabilityStable:
		return "稳定"
	case dripdu.StabilityWatch:
		return "关注"
	default:
		return "波动"
	}
}
