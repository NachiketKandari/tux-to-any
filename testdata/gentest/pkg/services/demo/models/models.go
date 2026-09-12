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
