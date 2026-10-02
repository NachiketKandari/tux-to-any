package db

import (
	"context"

	"demo-be/pkg/services/demo/models"

	"github.com/jmoiron/sqlx"
)

type DemoStore interface {
	GetOrderDetails(context.Context, string) ([]*models.OrderDetails, error)
	GetOrderResponses(ctx context.Context, compCd string) ([]*models.OrderResponse, error)
	GetOrderCount(ctx context.Context, matchAccount string) (int64, error)
	GetOrderMarks(ctx context.Context, compCd string) (*models.MarksResponse, error)
	// GetDB takes no context, which is what makes it a compile check for the
	// controller template's unconditional gomock.Any() matcher.
	GetDB() *sqlx.DB
	AddOrder(ctx context.Context, compCd, compName string) error
	// EditOrder returns ONLY an error, like riskprofile's EditMarks. Every
	// row-shaped mock payload carries two elements (value, error), which is
	// wrong here: the controller suite died on "wrong number of arguments to
	// Return for MockDemoStore.EditOrder: got 2, want 1".
	EditOrder(ctx context.Context, compCd string) error
	DeleteOrder(ctx context.Context, tx *sqlx.Tx, compCd string) error
}

// DemoAuditStore is the SECOND dependency the controller constructor takes,
// standing in for the corpus's cross-package commonDB.UserStore. It exists so
// the controller ctor is not single-argument: the generator emitted exactly one
// argument regardless, and every controller suite failed to compile with "not
// enough arguments in call".
type DemoAuditStore interface {
	RecordAudit(ctx context.Context, action string) error
}

// NewDemoStore keeps the handles it is given. Dropping them (the earlier
// `return &store{}`) left every generated test running against a nil *sqlx.DB,
// so the suite panicked before reaching its first assertion — which is how a
// wrong no-rows expectation stayed invisible: the byte-pinned golden was never
// actually executed.
//
// Two handles, deliberately: the connection goes to whichever one most methods
// run on, not to whichever comes first.
func NewDemoStore(db *sqlx.DB, writeDb *sqlx.DB) DemoStore {
	return &store{db: db, writeDb: writeDb}
}

func NewDemoAuditStore() DemoAuditStore { return &auditStore{} }

type auditStore struct{}

func (a *auditStore) RecordAudit(ctx context.Context, action string) error { return nil }
