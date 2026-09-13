package httpapi

import (
	"net/http"

	"github.com/example/drip-du/internal/dripdu"
	"github.com/gin-gonic/gin"
)

// verifyResponse is the adjudication returned for one valid request. Every
// numeric field is rendered as a string so exact decimal values survive JSON
// transport without any float64 interpretation.
type verifyResponse struct {
	SampleCount int      `json:"sample_count"`
	LowestCount int      `json:"lowest_count"`
	LowestIDs   []string `json:"lowest_ids"`
	LowestMean  string   `json:"lowest_mean_lph"`
	OverallMean string   `json:"overall_mean_lph"`
	DU          string   `json:"du_percent"`
	Verdict     string   `json:"verdict"`
	VerdictText string   `json:"verdict_text"`
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
	return r
}

func handleVerify(c *gin.Context) {
	// 64 points with short ids never approach 1 MiB; this only guards abuse.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	body, err := c.GetRawData()
	if err != nil || len(body) == 0 {
		writeValidationError(c, http.StatusBadRequest, "", "request body must be a non-empty JSON document no larger than 1 MiB")
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

	c.JSON(http.StatusOK, verifyResponse{
		SampleCount: result.SampleCount,
		LowestCount: result.LowestCount,
		LowestIDs:   result.LowestIDs,
		LowestMean:  dripdu.MeanString(result.LowestMean),
		OverallMean: dripdu.MeanString(result.OverallMean),
		DU:          result.DURounded,
		Verdict:     string(result.Verdict),
		VerdictText: verdictText(result.Verdict),
	})
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
