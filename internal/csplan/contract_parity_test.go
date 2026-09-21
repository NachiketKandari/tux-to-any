package csplan

import (
	"testing"

	"tux-to-any/internal/common"
	"tux-to-any/internal/contract"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/namer"
)

// TestContractProjectionParity proves the C# projection can be derived from
// the uniform contract without drift: contract SQL == cleanSQL, contract
// binds (host-var filtered) == buildQueryPlan params, contract row fields
// == RowProps via the CsNamer.
func TestContractProjectionParity(t *testing.T) {
	q := &ir.Query{
		ID: "q1", Type: ir.QuerySelectSingle,
		SQL:    "select a, b into :h1, :h2 from DEMO_T where x = :sql_comp_cd;",
		Tables: []string{"DEMO_T"}, Binds: []string{"sql_comp_cd"},
		RowShape: []string{"h1", "h2"}, StartLine: 1, EndLine: 3,
	}
	m := &Mapping{DBMethods: map[string]MethodPin{}, ParamNames: map[string]string{}, RequestFields: map[string]string{}}
	hosts := map[string]bool{"sql_comp_cd": true}
	qp, err := buildQueryPlan(q, m, hosts)
	if err != nil {
		t.Fatalf("buildQueryPlan: %v", err)
	}
	u := contract.QueryUnitFor(q, nil, false)
	if qp.SQL != u.SQL {
		t.Errorf("SQL drift: plan %q vs contract %q", qp.SQL, u.SQL)
	}
	cs := namer.CsNamer{}
	// Unpinned fallback equals the namer derivation by construction
	// (DefaultQueryName delegates to CsNamer — Phase 4).
	if want := cs.Method(u); qp.Name != want {
		t.Errorf("method drift: plan %q vs namer %q", qp.Name, want)
	}
	if len(qp.Params) != len(u.Params) {
		t.Errorf("params drift: plan %v vs contract %v", qp.Params, u.Params)
	}
	if len(qp.RowProps) != len(u.RowFields) {
		t.Errorf("rowprops drift: plan %v vs contract %v", qp.RowProps, u.RowFields)
	}
	for i, p := range qp.RowProps {
		if want := cs.Prop(u.RowFields[i].HostVar); p.Name != want {
			t.Errorf("rowprop %d: plan %q vs namer %q", i, p.Name, want)
		}
	}
}

// TestNamingDelegation pins the Phase 4 naming cutover: the plan's helpers
// are the shared rules (common.Pascal/UpperSnake, CsNamer.Method over the
// contract kind), so the draft pins and the unpinned fallback agree by
// construction across every query type.
func TestNamingDelegation(t *testing.T) {
	for _, bind := range []string{"sql_cst_pan_no", ":vc_acct", "sql_mar_form_no", "sql_17dim_val", "ST_GST.D_CGST_AMT"} {
		if got := pascalOf(bind); got != common.Pascal(bind) {
			t.Errorf("pascalOf(%q) = %q, want common.Pascal %q", bind, got, common.Pascal(bind))
		}
		if got := propNameOf(bind); got != common.UpperSnake(bind) {
			t.Errorf("propNameOf(%q) = %q, want common.UpperSnake %q", bind, got, common.UpperSnake(bind))
		}
	}
	cs := namer.CsNamer{}
	for _, qt := range []ir.QueryType{ir.QuerySelectSingle, ir.QuerySelectMulti, ir.QueryInsert, ir.QueryUpdate, ir.QueryDelete, ir.QueryMerge} {
		q := &ir.Query{ID: "q1", Type: qt, Tables: []string{"CST_Txn"}}
		want := cs.Method(contract.QueryUnit{Kind: contract.QueryKindOf(qt), Tables: q.Tables})
		if got := DefaultQueryName(q, qt.IsDML()); got != want {
			t.Errorf("DefaultQueryName(%s) = %q, want namer %q", qt, got, want)
		}
	}
	// Empty-table guard: both spell the Row fallback.
	q := &ir.Query{ID: "q1", Type: ir.QuerySelectSingle}
	if got, want := DefaultQueryName(q, false), cs.Method(contract.QueryUnit{Kind: contract.QuerySelectOne}); got != want {
		t.Errorf("DefaultQueryName(empty) = %q, want %q", got, want)
	}
}
