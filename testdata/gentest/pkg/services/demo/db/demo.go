package db

import (
	"context"
	"errors"

	"demo-be/pkg/services/demo/models"

	"github.com/jmoiron/sqlx"
)

// store holds a read AND a write handle. The corpus does
// (NewRiskProfileStore(db, writeDb *sqlx.DB)) and the generator used to hand
// the mock connection to a fixed argument position, so every read ran against a
// nil *sqlx.DB and the suite panicked before its first assertion. The reads
// below deliberately outnumber the write so the majority vote has a majority to
// find — one read and one write would be a coin flip.
type store struct {
	db      *sqlx.DB
	writeDb *sqlx.DB
}

func (g *store) GetOrderDetails(c context.Context, compCd string) ([]*models.OrderDetails, error) {
	var orders []*models.OrderDetails
	query := `SELECT DEMO_ORDER_COMP_CD AS "COMP_CD", DEMO_ORDER_CO_NAME AS "COMP_NAME"
	          FROM DEMO_ORDER, DEMO_COMPANY
	          WHERE DEMO_ORDER_CO_ID = :1`
	err := g.db.SelectContext(c, &orders, query, compCd)
	if err != nil {
		return nil, err
	}
	return orders, nil
}

func (g *store) GetOrderCount(ctx context.Context, matchAccount string) (int64, error) {
	var count int64
	query := `SELECT COUNT(*) AS "count" FROM DEMO_ORDER_MAP WHERE DEMO_MATCH_ACC = :1`
	err := g.db.GetContext(ctx, &count, query, matchAccount)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetOrderMarks is a single-row read that TOLERATES no rows: it swallows the
// error and returns the zero value with a nil error. GetOrderCount above
// propagates it. Having both in one fixture is what makes F3 checkable — the
// tool cannot assume one contract for every read, and a fixture with only one
// kind would let an assumption pass.
func (g *store) GetOrderMarks(ctx context.Context, compCd string) (*models.MarksResponse, error) {
	var marks models.MarksResponse
	query := `SELECT DEMO_ORDER_MARKS AS "FML_MARKS" FROM DEMO_ORDER WHERE DEMO_ORDER_CO_ID = :1`
	if err := g.db.GetContext(ctx, &marks, query, compCd); err != nil {
		return &marks, nil
	}
	return &marks, nil
}

// GetOrderResponses returns the response type directly, so the controller that
// wraps it can be a true passthrough. That shape matters: a controller body
// with field mapping is StatusLLMNeeded under -no-llm, so a fixture whose only
// controller methods map fields generates no controller suite at all and the
// whole layer goes untested.
func (g *store) GetOrderResponses(ctx context.Context, compCd string) ([]*models.OrderResponse, error) {
	var orders []*models.OrderResponse
	query := `SELECT DEMO_ORDER_COMP_CD AS "FML_COMP_CD", DEMO_ORDER_CO_NAME AS "FML_COMP_NAME"
	          FROM DEMO_ORDER WHERE DEMO_ORDER_CO_ID = :1`
	err := g.db.SelectContext(ctx, &orders, query, compCd)
	if err != nil {
		return nil, err
	}
	return orders, nil
}

// GetDB takes NO arguments and returns the write handle. The controller
// template used to emit EXPECT().GetDB(gomock.Any()) for it, which is
// over-arity against this signature.
func (g *store) GetDB() *sqlx.DB { return g.writeDb }

// AddOrder is a DML method whose zero-rows case returns a DOMAIN error, not
// sql.ErrNoRows. The template hardcoded the sentinel, so the generated suite
// asserted an error this method never produces.
//
// The shape is the corpus's own: ExecContext yields a sql.Result, the count
// comes from result.RowsAffected(), and zero rows falls through to
// errors.New. Written any other way the fixture would not be the shape the
// extractor is meant to recognise.
//
// The bind parameters are scalars, as in the corpus. A struct-typed bind
// (request *models.OrderRequest) would be rendered by dbCallArgs as a literal
// nil, and the body would dereference it — a real defect the generator has
// today, but one outside this plan's scope. Taking scalars keeps the fixture
// honest about what the corpus looks like and keeps the golden executable;
// see the note on AddOrder's nil bind in docs/gentest-fix-plan.md.
func (g *store) AddOrder(ctx context.Context, compCd, compName string) error {
	query := `INSERT INTO DEMO_ORDER (DEMO_ORDER_CO_ID, DEMO_ORDER_CO_NAME) VALUES (:1, :2)`
	result, err := g.writeDb.ExecContext(ctx, query, compCd, compName)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count > 0 {
		return nil
	}
	return errors.New("unable to add the order")
}

// EditOrder returns ONLY an error, so its EXPECT takes Return(nil) and not the
// two-element (value, error) payload every read shape gets. riskprofile's
// EditMarks has this exact shape and its controller suite failed on the arity.
func (g *store) EditOrder(ctx context.Context, compCd string) error {
	query := `UPDATE DEMO_ORDER SET DEMO_ORDER_UPD = :1 WHERE DEMO_ORDER_CO_ID = :2`
	_, err := g.db.ExecContext(ctx, query, compCd, compCd)
	return err
}

// DeleteOrder is the DML-tx shape: it tolerates zero rows, so its zero-rows
// case must assert no error.
// The query is assigned to a variable rather than inlined because that is how
// the extractor reads it — an inline backtick literal yields an empty Query,
// and an empty Query defeats isDeleteTx's DELETE prefix test, so the case is
// emitted as an error instead of a tolerance. The corpus assigns to a var too.
func (g *store) DeleteOrder(ctx context.Context, tx *sqlx.Tx, compCd string) error {
	query := `DELETE FROM DEMO_ORDER WHERE DEMO_ORDER_CO_ID = :1`
	_, err := tx.ExecContext(ctx, query, compCd)
	return err
}
