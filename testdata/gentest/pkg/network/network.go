package network

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HttpResponse is the envelope the generated handler tests unmarshal. Its
// Status string is what the generated cases assert: the numeric code is
// GinContext's business, and a test that guessed it was wrong.
type HttpResponse struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error,omitempty"`
}

// GinContext wraps a gin context with the envelope writers a converted handler
// calls. The status codes below are the corpus's own mapping, copied so the
// fixture behaves like the service it stands in for:
//
//	SuccessJSON     200
//	FailureJSON     404   — NOT 500, which the old template asserted
//	BadRequestJSON  400
type GinContext struct {
	*gin.Context
}

func (g *GinContext) SuccessJSON(data any) {
	g.JSON(http.StatusOK, HttpResponse{Status: "success", Data: data})
}

func (g *GinContext) FailureJSON(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	g.JSON(http.StatusNotFound, HttpResponse{
		Status: "failure",
		Error: struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		}{Code: http.StatusNotFound, Description: msg},
	})
}

func (g *GinContext) BadRequestJSON(err error, _ any) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	g.JSON(http.StatusBadRequest, HttpResponse{
		Status: "failure",
		Error: struct {
			Code        int    `json:"code"`
			Description string `json:"description"`
		}{Code: http.StatusBadRequest, Description: msg},
	})
}
