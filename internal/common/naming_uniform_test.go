package common

import "testing"

func TestPascalUpperSnakeSnake(t *testing.T) {
	if got := Pascal("sql_cst_pan_no"); got != "CstPanNo" {
		t.Errorf("Pascal = %q, want CstPanNo", got)
	}
	if got := Pascal(":sql_x"); got != "X" {
		t.Errorf("Pascal(:sql_x) = %q", got)
	}
	if got := UpperSnake("sql_mar_form_no"); got != "MAR_FORM_NO" {
		t.Errorf("UpperSnake = %q", got)
	}
	if got := UpperSnake("sql_17dim_val"); got != "_17DIM_VAL" {
		t.Errorf("UpperSnake digit escape = %q", got)
	}
	if got := Snake("FML_MF-NAV"); got != "fml_mf_nav" {
		t.Errorf("Snake = %q", got)
	}
	if got := StripHungarianPrefix("sql_vc_x"); got != "x" {
		t.Errorf("StripHungarianPrefix stacked = %q", got)
	}
}
