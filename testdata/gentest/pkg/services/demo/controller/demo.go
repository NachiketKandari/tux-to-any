package controller

import (
	"context"

	"demo-be/pkg/services/demo/db"
	"demo-be/pkg/services/demo/models"

	"github.com/jmoiron/sqlx"
)

type demoController struct {
	store db.DemoStore
	audit db.DemoAuditStore
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

// OrderDirect is a true passthrough: one store call, no field mapping. Under
// -no-llm that is the only controller shape that renders deterministically, so
// the fixture needs at least one or the controller layer produces no suite at
// all. It previously did `return s.store.GetOrderDetails(...)`, which did not
// compile — that returns []*models.OrderDetails while the method declares
// []*models.OrderResponse. A byte-pinned golden cannot catch a fixture that
// does not build, which is the same class of gap as F3.
func (s *demoController) OrderDirect(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error) {
	return s.store.GetOrderResponses(ctx, request.CompCode)
}

// OrderMarks takes no request and calls a no-arg store method, so it exercises
// both F9 (a controller method with no request) and F8 (a store method whose
// EXPECT must take no matcher and must hand back the live handle).
func (s *demoController) OrderMarks(ctx context.Context) (data []*models.MarksResponse, err error) {
	writeDB := s.store.GetDB()
	if writeDB == nil {
		return nil, nil
	}
	marks, err := s.store.GetOrderMarks(ctx, "")
	if err != nil {
		return nil, err
	}
	return []*models.MarksResponse{marks}, nil
}

func (s *demoController) OrderMarkList(ctx context.Context, request *models.MarksRequest) (data []*models.MarksResponse, err error) {
	marks, err := s.store.GetOrderMarks(ctx, request.CompCode)
	if err != nil {
		return nil, err
	}
	marks.Marks = request.Marks
	return []*models.MarksResponse{marks}, nil
}

func (s *demoController) OrderAudit(ctx context.Context, request *models.OrderRequest) (data []*models.OrderResponse, err error) {
	if err := s.audit.RecordAudit(ctx, "order_audit"); err != nil {
		return nil, err
	}
	return nil, nil
}

// OrderHandle is a passthrough over a no-argument store method: no field
// mapping, one store call. That combination is what makes it renderable under
// -no-llm, and it is the only way GetDB's EXPECT reaches a generated suite.
func (s *demoController) OrderHandle(ctx context.Context) (data *sqlx.DB, err error) {
	return s.store.GetDB(), nil
}

// OrderEdit is a passthrough over a store method returning ONLY an error, so
// its EXPECT's Return arity is exercised by a rendered suite rather than only
// by a unit pin.
func (s *demoController) OrderEdit(ctx context.Context, request *models.OrderRequest) (err error) {
	return s.store.EditOrder(ctx, request.CompCode)
}

// OrderStatus returns a plain string, and its success path is a CONSTANT. The
// generator knew the response type and nothing about the value, so the
// deterministic route asserted "" here:
//
//	expected: "order updated"
//	actual  : ""
//
// and the log route passed because it supplies a real value. A scalar response
// has no fields to map, which is why the body's own literal is the only other
// source available.
func (s *demoController) OrderStatus(ctx context.Context, request *models.OrderRequest) (string, error) {
	if err := s.store.EditOrder(ctx, request.CompCode); err != nil {
		return "", err
	}
	return "order updated", nil
}
