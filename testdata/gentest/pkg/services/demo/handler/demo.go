package handler

import (
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
