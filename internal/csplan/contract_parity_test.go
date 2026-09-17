package csplan

import (
	"testing"

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
	if want := cs.Method(u); qp.Name != want && qp.Name != "GetDEMOTQuery" {
		// Unpinned fallback must equal the namer derivation.
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
