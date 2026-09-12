package db

import (
	"context"

	"demo-be/pkg/services/demo/models"
)

type store struct {
	db *sqlx.DB
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
