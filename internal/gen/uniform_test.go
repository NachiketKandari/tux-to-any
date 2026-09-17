package gen

import (
	"testing"

	"tux-to-any/internal/ir"
)

func TestGoTypeFor(t *testing.T) {
	cases := []struct {
		hv   ir.HostVar
		want string
	}{
		{ir.HostVar{Name: "a", CType: "char"}, "string"},
		{ir.HostVar{Name: "a", CType: "VARCHAR"}, "string"},
		{ir.HostVar{Name: "a", CType: "long"}, "int64"},
		{ir.HostVar{Name: "a", CType: "int"}, "int"},
		{ir.HostVar{Name: "a", CType: "double"}, "float64"},
		{ir.HostVar{Name: "a", GoHint: "string"}, "string"},
	}
	for _, c := range cases {
		if got := GoTypeFor(c.hv); got != c.want {
			t.Errorf("GoTypeFor(%+v) = %q, want %q", c.hv, got, c.want)
		}
	}
}

func TestTemplateFor(t *testing.T) {
	if got := TemplateFor(ir.QuerySelectSingle, false); got != ir.TemplateSelectSingle {
		t.Errorf("single = %q", got)
	}
	if got := TemplateFor(ir.QuerySelectSingle, true); got != "db_method_select_single_tx" {
		t.Errorf("single tx = %q", got)
	}
	if got := TemplateFor(ir.QueryInsert, false); got != "db_method_dml_plain" {
		t.Errorf("insert plain = %q", got)
	}
	if got := TemplateFor(ir.QueryInsert, true); got != ir.TemplateInsertTx {
		t.Errorf("insert tx = %q", got)
	}
}
