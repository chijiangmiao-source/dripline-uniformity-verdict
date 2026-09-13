package httpapi

import (
	"math/big"
	"net/http"

	"github.com/example/drip-du/internal/dripdu"
	"github.com/gin-gonic/gin"
)

// decimalView carries one exact rational value two ways: ExactFraction
// ("num/den") is authoritative and always enough to recompute the verdict;
// Decimal is a human-readable rendering. Terminating reports whether Decimal
// equals the fraction exactly (true) or is only a 12-place preview (false).
type decimalView struct {
	Decimal       string `json:"decimal"`
	ExactFraction string `json:"exact_fraction"`
	Terminating   bool   `json:"terminating"`
}

// duView carries DU both as the unrounded exact fraction and as the only
// rounded value in the whole response (ROUND_HALF_UP, two decimal places).
type duView struct {
	Rounded       string `json:"rounded"`
	ExactFraction string `json:"exact_fraction"`
}

// verifyResponse is the adjudication returned for one valid request. Every
// numeric result includes its exact rational fraction, so the DU and verdict
// can be recomputed from this document alone even when a mean is a repeating
// decimal (e.g. seven measurements divided by 7).
//
// Requests that carry rated_flow_lph on every measurement are adjudicated on
// the measured/rated supply ratio; the response then also reports
// calculation_basis and the two ratio means. Legacy requests omit all three
// fields, keeping the long-standing response shape byte-compatible.
type verifyResponse struct {
	SampleCount int         `json:"sample_count"`
	LowestCount int         `json:"lowest_count"`
	LowestIDs   []string    `json:"lowest_ids"`
	LowestMean  decimalView `json:"lowest_mean_lph"`
	OverallMean decimalView `json:"overall_mean_lph"`
	DU          duView      `json:"du_percent"`
	Verdict     string      `json:"verdict"`
	VerdictText string      `json:"verdict_text"`
	// Rated-mode additions, omitted entirely for legacy requests.
	CalculationBasis string       `json:"calculation_basis,omitempty"`
	LowestMeanRatio  *decimalView `json:"lowest_mean_ratio,omitempty"`
	OverallMeanRatio *decimalView `json:"overall_mean_ratio,omitempty"`
}

// errorResponse is returned with 4xx/5xx statuses.
type errorResponse struct {
	Error struct {
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	} `json:"error"`
}

// NewRouter builds the Gin engine exposing the verification endpoint.
func NewRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.POST("/api/v1/verify", handleVerify)
	r.POST("/api/v1/stability", handleStability)
	r.POST("/api/v1/blockage-diagnosis", handleBlockageDiagnosis)
	return r
}

func handleVerify(c *gin.Context) {
	// The contract sets no size limit: ids are arbitrary non-empty strings,
	// so a legitimate request with long ids must still be adjudicated rather
	// than rejected by a fixed body cap.
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		writeValidationError(c, http.StatusBadRequest, "", "request body must be a non-empty JSON document")
		return
	}

	req, verr := parseRequest(body)
	if verr != nil {
		status := http.StatusUnprocessableEntity
		if verr.badRequest {
			// Body was not even a syntactically valid JSON object.
			status = http.StatusBadRequest
		}
		// 422 Unprocessable Entity: syntactically valid JSON whose semantics
		// (count, ids or flows) violate the acceptance contract. Either way
		// the error names only the first offending field path and never
		// partial means.
		writeValidationError(c, status, verr.Field, verr.Message)
		return
	}

	result := dripdu.Evaluate(req.Measurements)

	resp := verifyResponse{
		SampleCount: result.SampleCount,
		LowestCount: result.LowestCount,
		LowestIDs:   result.LowestIDs,
		LowestMean:  toDecimalView(result.LowestMean),
		OverallMean: toDecimalView(result.OverallMean),
		DU: duView{
			Rounded:       result.DURounded,
			ExactFraction: result.DU.Num().String() + "/" + result.DU.Denom().String(),
		},
		Verdict:     string(result.Verdict),
		VerdictText: verdictText(result.Verdict),
	}
	if result.Basis == dripdu.BasisSupplyRatio {
		resp.CalculationBasis = string(result.Basis)
		lowestRatio := toDecimalView(result.LowestMeanRatio)
		overallRatio := toDecimalView(result.OverallMeanRatio)
		resp.LowestMeanRatio = &lowestRatio
		resp.OverallMeanRatio = &overallRatio
	}
	c.JSON(http.StatusOK, resp)
}

func toDecimalView(r *big.Rat) decimalView {
	v := dripdu.AsExactDecimal(r)
	return decimalView{
		Decimal:       v.Decimal,
		ExactFraction: v.Fraction,
		Terminating:   v.Terminating,
	}
}

func writeValidationError(c *gin.Context, status int, field, message string) {
	resp := errorResponse{}
	resp.Error.Message = message
	resp.Error.Field = field
	c.AbortWithStatusJSON(status, resp)
}

func verdictText(v dripdu.Verdict) string {
	switch v {
	case dripdu.VerdictPass:
		return "通过"
	case dripdu.VerdictReview:
		return "复查"
	default:
		return "不通过"
	}
}
