package handler

import (
	"demo-be/pkg/services/demo/controller"
	"demo-be/pkg/services/demo/models"

	"github.com/gin-gonic/gin"
)

type demoHandler struct {
	controller controller.DemoController
}

func (f *demoHandler) OrderList(c *gin.Context) {
	var request models.OrderRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	data, err := f.controller.OrderList(c, &request)
	if err != nil {
		return
	}
	if data == nil {
		return
	}
	c.JSON(200, data)
}

// OrderMarksFetch binds a request but does NOT pass it: the controller method
// it calls takes only a context. The generated EXPECT used to be
// OrderMarksFetch(ctx, &request), which is over-arity against
// OrderMarks(ctx).
func (f *demoHandler) OrderMarksFetch(c *gin.Context) {
	var request models.MarksRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	data, err := f.controller.OrderMarks(c)
	if err != nil {
		return
	}
	if data == nil {
		return
	}
	c.JSON(200, data)
}

func (f *demoHandler) OrderMarkList(c *gin.Context) {
	var request models.MarksRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	data, err := f.controller.OrderMarkList(c, &request)
	if err != nil {
		return
	}
	if data == nil {
		return
	}
	c.JSON(200, data)
}

// OrderEither routes to one of TWO controller methods, mirroring the corpus's
// ViewQuestions:
//
//	if request.RequestType == "B" { data, err = f.controller.ViewQuestions(c, &request) }
//	if request.RequestType == "L" { data, err = f.controller.ListSection(c, &request) }
//
// The generator merged the two call sites' arguments and emitted
// ListSection(c, &request, &request) — three arguments to a method taking two,
// so every handler suite failed to compile. One representative here is what
// keeps that from coming back.
func (f *demoHandler) OrderEither(c *gin.Context) {
	var request models.OrderRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	var data any
	var err error
	if request.CompCode == "B" {
		data, err = f.controller.OrderList(c, &request)
	}
	if request.CompCode == "L" {
		data, err = f.controller.OrderDirect(c, &request)
	}
	if err != nil {
		return
	}
	c.JSON(200, data)
}

func (f *demoHandler) OrderAudit(c *gin.Context) {
	var request models.OrderRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	data, err := f.controller.OrderAudit(c, &request)
	if err != nil {
		return
	}
	if data == nil {
		return
	}
	c.JSON(200, data)
}
