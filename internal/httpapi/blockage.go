package httpapi

import (
	"errors"
	"net/http"

	"github.com/example/drip-du/internal/dripdu"
	"github.com/gin-gonic/gin"
)

// blockagePointView is one point's exact share of the median baseline.
type blockagePointView struct {
	ID               string      `json:"id"`
	BaselinePercent  percentView `json:"baseline_percent"`
	SuspectedLowFlow bool        `json:"suspected_low_flow"`
}

// blockageSectionView is one maximal run of adjacent suspected points,
// identified along the installation (input) order.
type blockageSectionView struct {
	StartID   string   `json:"start_id"`
	EndID     string   `json:"end_id"`
	MemberIDs []string `json:"member_ids"`
}

// blockageResponse is the blocked-section diagnosis returned for one valid
// request. Median and every baseline percentage carry their exact fractions,
// so the suspected flags and sections can be recomputed from this document
// alone. Rated-mode requests add calculation_basis, exactly like the
// adjudication endpoint.
type blockageResponse struct {
	SampleCount int                   `json:"sample_count"`
	Median      decimalView           `json:"median"`
	Points      []blockagePointView   `json:"points"`
	Sections    []blockageSectionView `json:"sections"`
	// Rated-mode addition, omitted entirely for legacy requests.
	CalculationBasis string `json:"calculation_basis,omitempty"`
}

func handleBlockageDiagnosis(c *gin.Context) {
	// The diagnosis reuses the acceptance payload verbatim — the same
	// measurements array, the same flow and optional rated_flow_lph rules —
	// so validation, the first-field paths and the 400/422 split are shared
	// with /api/v1/verify. No partial diagnosis ever accompanies an error.
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		writeValidationError(c, http.StatusBadRequest, "", "request body must be a non-empty JSON document")
		return
	}

	req, verr := parseRequest(body)
	if verr != nil {
		status := http.StatusUnprocessableEntity
		if verr.badRequest {
			status = http.StatusBadRequest
		}
		writeValidationError(c, status, verr.Field, verr.Message)
		return
	}

	result, err := dripdu.DiagnoseBlockage(req.Measurements)
	if err != nil {
		// Validation already guarantees 4..64 points; the only domain failure
		// is a baseline that cannot be formed, reported in the standard
		// envelope and located at the measurements array.
		field := ""
		if errors.Is(err, dripdu.ErrNoMedian) {
			field = "measurements"
		}
		writeValidationError(c, http.StatusUnprocessableEntity, field, err.Error())
		return
	}

	resp := blockageResponse{
		SampleCount: len(req.Measurements),
		Median:      toDecimalView(result.Median),
		Points:      make([]blockagePointView, 0, len(result.Points)),
		Sections:    make([]blockageSectionView, 0, len(result.Sections)),
	}
	for _, p := range result.Points {
		resp.Points = append(resp.Points, blockagePointView{
			ID: p.ID,
			BaselinePercent: percentView{
				Rounded:       p.BaselineRatioRounded,
				ExactFraction: fractionString(p.BaselineRatio),
			},
			SuspectedLowFlow: p.Suspected,
		})
	}
	for _, s := range result.Sections {
		resp.Sections = append(resp.Sections, blockageSectionView{
			StartID:   s.StartID,
			EndID:     s.EndID,
			MemberIDs: s.MemberIDs,
		})
	}
	if result.Basis == dripdu.BasisSupplyRatio {
		resp.CalculationBasis = string(result.Basis)
	}
	c.JSON(http.StatusOK, resp)
}
