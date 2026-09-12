package handler

import (
	"demo-be/pkg/services/demo/controller"
	"demo-be/pkg/services/demo/models"

	"github.com/gin-gonic/gin"
)

type DemoHandler interface {
	OrderList(c *gin.Context)
}

func NewDemoHandler(controller controller.DemoController) DemoHandler {
	return &demoHandler{controller: controller}
}
