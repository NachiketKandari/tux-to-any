package models

import "database/sql"

type OrderRequest struct {
	CompCode string `json:"FML_COMP_CD" binding:"required"`
}

type OrderResponse struct {
	CompCode string `json:"FML_COMP_CD,omitempty"`
	CompName string `json:"FML_COMP_NAME,omitempty"`
}

type OrderDetails struct {
	CompCd   sql.NullString `db:"COMP_CD"`
	CompName sql.NullString `db:"COMP_NAME"`
}

// MarksRequest carries a SLICE field. The corpus has five of these
// (AnswerID []string, Marks []string, …) and the generator used to declare
// every case-struct field `string`, so the generated controller/handler suite
// failed with "cannot use testCase.AnswerID (variable of type string) as
// []string value". One representative here is what keeps F5 from regressing:
// nothing else in this fixture would notice.
type MarksRequest struct {
	CompCode string   `json:"FML_COMP_CD" binding:"required"`
	Marks    []string `json:"FML_MARKS" binding:"required"`
}

// MarksResponse mirrors the request so the no-request controller method
// (OrderMarks) has a typed response to assert on.
type MarksResponse struct {
	CompCode string   `json:"FML_COMP_CD,omitempty"`
	Marks    []string `json:"FML_MARKS,omitempty"`
}
