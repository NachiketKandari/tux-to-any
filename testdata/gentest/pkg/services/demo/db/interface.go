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

// NewDemoStore keeps the handle it is given. Dropping it (the earlier
// `return &store{}`) left every generated test running against a nil
// *sqlx.DB, so the suite panicked before reaching its first assertion — which
// is how a wrong no-rows expectation stayed invisible: the byte-pinned golden
// was never actually executed.
func NewDemoStore(db *sqlx.DB) DemoStore { return &store{db: db} }
