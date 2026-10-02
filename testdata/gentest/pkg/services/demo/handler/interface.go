package handler

import (
	"demo-be/pkg/services/demo/controller"
	"demo-be/pkg/services/demo/models"

	"github.com/gin-gonic/gin"
)

type DemoHandler interface {
	OrderList(c *gin.Context)
	// OrderMarksFetch is deliberately NOT named OrderMarks: it calls
	// controller.OrderMarks. The generated EXPECT used to be named after the
	// handler, producing a method the controller mock does not declare.
	OrderMarksFetch(c *gin.Context)
	OrderMarkList(c *gin.Context)
	OrderAudit(c *gin.Context)
}

func NewDemoHandler(controller controller.DemoController) DemoHandler {
	return &demoHandler{controller: controller}
}

var _ = models.OrderRequest{}
