package controller

import (
	"context"

	"demo-be/pkg/services/demo/db"
	"demo-be/pkg/services/demo/models"
)

type DemoController interface {
	OrderList(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error)
	OrderDirect(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error)
}

func NewDemoController(store db.DemoStore) DemoController { return &demoController{store: store} }
