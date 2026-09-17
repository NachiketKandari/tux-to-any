package contract

import (
	"encoding/json"
	"testing"

	"tux-to-any/internal/ir"
)

func TestBuildServiceFromIR(t *testing.T) {
	main := &ir.File{
		Path:  "svc.pc",
		Entry: "SVC_ENTRY",
		HostVars: []ir.HostVar{
			{Name: "sql_comp_cd", CType: "char"},
			{Name: "sql_acct_no", CType: "varchar"},
		},
	}
	q := &ir.Query{
		ID: "q1", Type: ir.QuerySelectSingle,
		SQL:    "select a into :h1 from t where x = :sql_comp_cd",
		Tables: []string{"t"}, Binds: []string{"sql_comp_cd"},
		RowShape: []string{"h1"}, StartLine: 10, EndLine: 12,
	}
	cond := &ir.Condition{Index: 1, Kind: "if", StartLine: 5, EndLine: 20,
		FmlOps: []ir.FmlOp{
			{Kind: ir.FmlGet, Field: "FML_COMP_CD", Target: "sql_comp_cd", Line: 6},
			{Kind: ir.FmlAdd, Field: "FML_ACCT_NO", Target: "sql_acct_no", Line: 15},
		},
		QueryIDs: []string{"q1"},
	}
	svc := Build(BuildOptions{
		Name: "svc", Entry: "SVC_ENTRY", Source: "svc.pc", Main: main,
		Endpoints: []EndpointInput{{
			Name: "GetAcct", Condition: cond,
			Queries:  []QueryInput{{Query: q}},
			LineSpan: [2]int{5, 20},
		}},
	})
	if len(svc.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1", len(svc.Endpoints))
	}
	ep := svc.Endpoints[0]
	if len(ep.Request.Fields) == 0 {
		t.Errorf("request fields empty")
	}
	if len(ep.Response.Fields) == 0 {
		t.Errorf("response fields empty")
	}
	if len(ep.Queries) != 1 || ep.Queries[0].SQL == "" {
		t.Errorf("query unit missing canonical SQL: %+v", ep.Queries)
	}
	// JSON-stable: marshal twice, byte-identical.
	a, _ := json.Marshal(svc)
	b, _ := json.Marshal(Build(BuildOptions{
		Name: "svc", Entry: "SVC_ENTRY", Source: "svc.pc", Main: main,
		Endpoints: []EndpointInput{{
			Name: "GetAcct", Condition: cond,
			Queries:  []QueryInput{{Query: q}},
			LineSpan: [2]int{5, 20},
		}},
	}))
	if string(a) != string(b) {
		t.Errorf("Build not deterministic")
	}
}

func TestQueryUnitForCanonicalSQL(t *testing.T) {
	q := &ir.Query{
		ID: "q1", Type: ir.QuerySelectSingle,
		SQL:    "select a, b into :h1, :h2 from t where x = : x;",
		Tables: []string{"t"}, Binds: []string{"x"},
		RowShape: []string{"h1", "h2"},
	}
	u := QueryUnitFor(q, nil, false)
	if u.Kind != QuerySelectOne {
		t.Errorf("kind = %q", u.Kind)
	}
	for _, b := range u.Binds {
		if b == "h1" || b == "h2" {
			t.Errorf("INTO target leaked into binds: %v", u.Binds)
		}
	}
	if len(u.RowFields) != 2 {
		t.Errorf("row fields = %d, want 2", len(u.RowFields))
	}
}
