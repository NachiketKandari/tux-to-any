package csplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
)

// csFilterSrc is the filter fixture: an H/F/default dispatch ladder where
// both H and F arms carry SQL (the merge carries both actions into one
// endpoint).
const csFilterSrc = `void SVC_CSF(TPSVCINFO *rqst) {
	char c_flag;
	if (c_flag == 'H') {
		EXEC SQL INSERT INTO TH VALUES (:a);
		Fadd32(obuf, FML_H_OUT, (char *)&a, 0);
	} else if (c_flag == 'F') {
		EXEC SQL INSERT INTO TF VALUES (:b);
		Fadd32(obuf, FML_F_OUT, (char *)&b, 0);
	} else {
		EXEC SQL INSERT INTO TD VALUES (:c);
		Fadd32(obuf, FML_D_OUT, (char *)&c, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`

func csFilterFixture(t *testing.T) (*ir.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_CSF.pc")
	if err := os.WriteFile(path, []byte(csFilterSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f, csFilterSrc
}

// TestCSPlanScenarioFilterValidation pins the 4th key's load contract for
// the C# mapping: syntax at load, exactly-one-of, duplicates rejected.
func TestCSPlanScenarioFilterValidation(t *testing.T) {
	base := func(e Endpoint) *Mapping {
		return &Mapping{Namespace: "Tux", Component: "C", Endpoints: []Endpoint{e}}
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag == 'F'", Name: "X", Route: "x"}).Validate(); err != nil {
		t.Errorf("valid scenarioFilter rejected: %v", err)
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag > 'F'", Name: "X", Route: "x"}).Validate(); err == nil {
		t.Error("unsupported filter syntax must be rejected at load")
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag == 'F'", ScenarioRef: "c_flag=F", Name: "X", Route: "x"}).Validate(); err == nil {
		t.Error("scenarioFilter + scenarioRef must be rejected")
	}
	if err := base(Endpoint{Condition: 1, Name: "X", Route: "x"}).Validate(); err != nil {
		t.Errorf("plain condition endpoint rejected: %v", err)
	}
	if err := base(Endpoint{Condition: 1, ScenarioFilter: "c_flag == 'F'", Name: "X", Route: "x"}).Validate(); err == nil {
		t.Error("condition + scenarioFilter must be rejected")
	}
}

// TestCSPlanBuildScenarioFilter pins the filter endpoint's resolution: one
// action for the merged F/H filter, both arms' queries planned, the covered
// arms un-warned by the KeptLines coverage check, the else arm advisory.
func TestCSPlanBuildScenarioFilter(t *testing.T) {
	f, src := csFilterFixture(t)
	m := &Mapping{
		Namespace: "Tux", Component: "CustFilter",
		Endpoints: []Endpoint{{ScenarioFilter: "c_flag == 'H' || c_flag == 'F'", Name: "HistOrFull", Route: "hist-full"}},
	}
	p, err := Build(Options{Main: f, Source: src, Mapping: m})
	if err != nil {
		t.Fatalf("filter endpoint rejected: %v", err)
	}
	if len(p.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1 merged action", len(p.Endpoints))
	}
	if got := p.Endpoints[0].Scenario; got != "c_flag in {F,H}" {
		t.Errorf("scenario = %q, want c_flag in {F,H}", got)
	}
	if len(p.Endpoints[0].QueryIDs) != 2 {
		t.Errorf("endpoint queries = %v, want both arms' inserts", p.Endpoints[0].QueryIDs)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "dispatch arm c_flag=F") || strings.Contains(w, "dispatch arm c_flag=H") {
			t.Errorf("filter-covered arm warned: %s", w)
		}
	}
	defaultWarned := false
	for _, w := range p.Warnings {
		if strings.Contains(w, "dispatch arm c_flag=default") {
			defaultWarned = true
		}
	}
	if !defaultWarned {
		t.Errorf("the uncovered default arm must warn, warnings = %v", p.Warnings)
	}
}

// TestCSPlanBuildScenarioFilterErrors pins the loud rejects on the C# path.
func TestCSPlanBuildScenarioFilterErrors(t *testing.T) {
	f, src := csFilterFixture(t)
	build := func(text string) error {
		m := &Mapping{
			Namespace: "Tux", Component: "CustFilter",
			Endpoints: []Endpoint{{ScenarioFilter: text, Name: "X", Route: "x"}},
		}
		_, err := Build(Options{Main: f, Source: src, Mapping: m})
		return err
	}
	for _, tc := range []struct{ text, want string }{
		{"bogus_axis == 'F'", "unknown axis"},
		{"c_flag == 'X'", "matches nothing"},
		{"c_flag == 'H' && c_flag == 'F'", "contradictory literals"},
	} {
		err := build(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.text, err, tc.want)
		}
	}
}
