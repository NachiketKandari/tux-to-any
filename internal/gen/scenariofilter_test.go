package gen

import (
	"os"
	"path/filepath"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
)

// genFilterSrc nests the secondary chain inside the H arm: the filter
// `c_flag == 'H' && new_flag == 'K'` is the co-occurrence case the gen
// plumbing must resolve to one controller body.
const genFilterSrc = `void SVC_GF(TPSVCINFO *rqst) {
	char c_flag;
	char new_flag;
	if (c_flag == 'H') {
		EXEC SQL INSERT INTO TH VALUES (:a);
		if (new_flag == 'K') {
			Fadd32(obuf, FML_K_OUT, (char *)&a, 0);
		} else if (new_flag == 'J') {
			Fadd32(obuf, FML_J_OUT, (char *)&a, 0);
		}
	} else if (c_flag == 'F') {
		Fadd32(obuf, FML_F_OUT, (char *)&b, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`

// TestServiceScenarioFilterPlumbing pins the gen re-derivation seam: a
// scenarioFilter endpoint resolves to the same re-folded slice the plan
// validated, feeds ScenarioOf, and synthesizes its condition — the
// controller body path consumes it exactly like a scenarioRef slice.
func TestServiceScenarioFilterPlumbing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SVC_GF.pc")
	if err := os.WriteFile(path, []byte(genFilterSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{
		Service: "gf", Module: "app/gf",
		Endpoints: []plan.Endpoint{{ScenarioFilter: "c_flag == 'H' && new_flag == 'K'", Name: "HistK", Route: "/hist-k"}},
	}
	p, err := plan.Build(plan.Options{Main: f, Source: genFilterSrc, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(Options{Plan: p, Main: f, Source: genFilterSrc})
	if err != nil {
		t.Fatal(err)
	}
	sc := svc.ScenarioOf("HistK")
	if sc == nil {
		t.Fatal("ScenarioOf returned nil for a scenarioFilter endpoint")
	}
	if sc.Key != "c_flag=H && new_flag=K" {
		t.Errorf("scenario key = %q, want c_flag=H && new_flag=K", sc.Key)
	}
	c := svc.conditionOf(m.Endpoints[0])
	if c == nil {
		t.Fatal("conditionOf returned nil for a scenarioFilter endpoint")
	}
	if c.Expr != sc.Key {
		t.Errorf("condition expr = %q, want the scenario key %q", c.Expr, sc.Key)
	}
	// The condition carries the H arm's SQL — the controller body's store
	// calls derive from it.
	found := false
	for _, id := range c.QueryIDs {
		if q := svc.Query(id); q != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("filter condition query ids = %v, want the H arm's query", c.QueryIDs)
	}
}
