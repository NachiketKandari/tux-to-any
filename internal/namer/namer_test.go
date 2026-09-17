package namer

import (
	"testing"

	"tux-to-any/internal/contract"
)

func TestGoCsPyMethodParity(t *testing.T) {
	q := contract.QueryUnit{ID: "q1", Kind: contract.QuerySelectMany, Tables: []string{"DEMO_T"}}
	if got := (GoNamer{}).Method(q); got != "GetDemoT" {
		t.Errorf("Go method = %q", got)
	}
	if got := (CsNamer{}).Method(q); got != "GetDEMOTQuery" {
		t.Errorf("Cs method = %q, want GetDEMOTQuery", got)
	}
	if got := (PyNamer{}).Method(q); got == "" {
		t.Errorf("Py method empty")
	}
}

func TestFieldTypes(t *testing.T) {
	if got := (GoNamer{}).FieldType(contract.Field{Kind: contract.FieldRow}); got != "sql.NullString" {
		t.Errorf("Go row type = %q", got)
	}
	if got := (CsNamer{}).FieldType(contract.Field{CType: "long"}); got != "long" {
		t.Errorf("Cs long = %q", got)
	}
	if got := (PyNamer{}).FieldType(contract.Field{CType: "int"}); got != "int" {
		t.Errorf("Py int = %q", got)
	}
	if got := (GoNamer{}).Param(":sql_mf_nav_comp_cd"); got == "" || got == ":sql_mf_nav_comp_cd" {
		t.Errorf("Go param not derived: %q", got)
	}
	if got := (CsNamer{}).Prop("sql_mar_form_no"); got != "MAR_FORM_NO" {
		t.Errorf("Cs prop = %q", got)
	}
}
