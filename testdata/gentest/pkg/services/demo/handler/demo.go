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
