package gen

import (
	"strings"
	"testing"

	"tux-to-any/internal/common"
	"tux-to-any/internal/contract"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/namer"
)

// TestRowFieldNamingDelegation pins the Phase 4 cutover: row field names
// come from GoNamer.Prop and row types from GoNamer.FieldType. The old
// inline derivation is preserved below as the oracle on corpus-style
// inputs — any divergence fails loudly instead of drifting goldens.
func TestRowFieldNamingDelegation(t *testing.T) {
	goNamer := namer.GoNamer{}
	shapes := []string{
		"sql_demo_comp_cd",
		"sql_mf_nav_comp_cd",
		"st_gst.d_cgst_amt",
		"mf_jthldr",
		"sql_acct_no",
	}
	for _, shape := range shapes {
		// Oracle: the pre-Phase-4 inline derivation.
		hvName := shape
		if j := strings.LastIndex(hvName, "."); j >= 0 {
			hvName = hvName[j+1:]
		}
		if j := strings.IndexAny(hvName, " \t"); j > 0 {
			hvName = hvName[:j]
		}
		want := common.Export(common.CamelLowerGo(strings.TrimPrefix(hvName, "sql_")))
		if got := goNamer.Prop(shape); got != want {
			t.Errorf("GoNamer.Prop(%q) = %q, want inline %q", shape, got, want)
		}
	}
	if got := goNamer.FieldType(contract.Field{Kind: contract.FieldRow}); got != "sql.NullString" {
		t.Errorf("GoNamer.FieldType(row) = %q, want sql.NullString", got)
	}
	// End to end through rowFields on a synthetic query.
	svc := &Service{}
	q := &ir.Query{ID: "q1", Type: ir.QuerySelectSingle, RowShape: shapes[:2]}
	fields, err := svc.rowFields("q1", q)
	if err != nil {
		t.Fatalf("rowFields: %v", err)
	}
	for i, f := range fields {
		if want := goNamer.Prop(shapes[i]); f.Name != want {
			t.Errorf("rowFields[%d].Name = %q, want namer %q", i, f.Name, want)
		}
		if f.Type != "sql.NullString" {
			t.Errorf("rowFields[%d].Type = %q, want sql.NullString", i, f.Type)
		}
	}
}
