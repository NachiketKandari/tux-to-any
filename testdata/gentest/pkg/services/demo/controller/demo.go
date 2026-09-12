package controller

import (
	"context"

	"demo-be/pkg/services/demo/db"
	"demo-be/pkg/services/demo/models"
)

type demoController struct {
	store db.DemoStore
}

func (s *demoController) OrderList(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error) {
	result, err := s.store.GetOrderDetails(ctx, request.CompCode)
	for _, row := range result {
		data = append(data, &models.OrderResponse{
			CompCode: row.CompCd.String,
			CompName: row.CompName.String,
		})
	}
	return data, err
}

func (s *demoController) OrderDirect(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error) {
	return s.store.GetOrderDetails(ctx, request.CompCode)
}
