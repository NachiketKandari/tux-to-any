package db

import (
	"context"

	"demo-be/pkg/services/demo/models"

	"github.com/jmoiron/sqlx"
)

type DemoStore interface {
	GetOrderDetails(context.Context, string) ([]*models.OrderDetails, error)
	GetOrderCount(ctx context.Context, matchAccount string) (int64, error)
}

func NewDemoStore(db *sqlx.DB) DemoStore { return &store{} }
