package controller

import (
	"context"

	"demo-be/pkg/services/demo/db"
	"demo-be/pkg/services/demo/models"

	"github.com/jmoiron/sqlx"
)

type DemoController interface {
	OrderList(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error)
	OrderDirect(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error)
	// OrderMarks takes NO request. The extractor required a *models.X and
	// returned nil for anything else, which made this endpoint `unsupported` —
	// the one method the log route missed.
	OrderMarks(ctx context.Context) (data []*models.MarksResponse, err error)
	// OrderMarkList takes a request with a []string field, so the generated
	// case struct has to declare it as a slice.
	OrderMarkList(ctx context.Context, request *models.MarksRequest) (data []*models.MarksResponse, err error)
	// OrderAudit is the only endpoint that touches the second dependency, so a
	// controller suite can prove the nil passed for it is deliberate.
	OrderAudit(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error)
	// OrderHandle returns the write handle itself. It is a passthrough over a
	// NO-ARGUMENT store method, which is the only shape that puts GetDB's
	// EXPECT into a rendered controller suite — the unconditional
	// gomock.Any() matcher was over-arity against GetDB().
	OrderHandle(ctx context.Context) (data *sqlx.DB, err error)
	// OrderEdit returns only an error, mirroring riskprofile's EditMarks path:
	// the store method has one result, so its EXPECT's Return takes one value.
	OrderEdit(ctx context.Context, request *models.OrderRequest) (err error)
	// OrderStatus returns a scalar whose success value is a constant in the
	// body, so a scalar response is asserted from the body rather than the
	// type's zero value.
	OrderStatus(ctx context.Context, request *models.OrderRequest) (string, error)
}

// NewDemoController takes TWO dependencies. The generator emitted exactly one
// argument regardless, and every controller suite failed to compile with "not
// enough arguments in call".
func NewDemoController(store db.DemoStore, audit db.DemoAuditStore) DemoController {
	return &demoController{store: store, audit: audit}
}
