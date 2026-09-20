package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
)

// TestMappingScenarioFilterValidation pins the 4th endpoint key's load
// contract: syntax validated at load (like scenarioRef), exactly-one-of
// enforced, duplicates rejected.
func TestMappingScenarioFilterValidation(t *testing.T) {
	base := func(e Endpoint) *Mapping {
		return &Mapping{Service: "svcs", Module: "app/pkg/services/svcs", Endpoints: []Endpoint{e}}
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag == 'F' || c_flag == 'I'", Name: "X", Route: "/x"}).Validate(); err != nil {
		t.Errorf("valid scenarioFilter rejected: %v", err)
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag > 'F'", Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("unsupported filter syntax must be rejected at load")
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag == 'F'", Condition: 1, Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("scenarioFilter + condition must be rejected")
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag == 'F'", ScenarioRef: "c_flag=F", Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("scenarioFilter + scenarioRef must be rejected")
	}
	if err := base(Endpoint{ScenarioFilter: "c_flag == 'F'", ConditionRef: "c1", Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("scenarioFilter + conditionRef must be rejected")
	}
	if err := (&Mapping{Service: "svcs", Endpoints: []Endpoint{
		{ScenarioFilter: "c_flag == 'F'", Name: "X", Route: "/x"},
		{ScenarioFilter: "c_flag == 'F'", Name: "Y", Route: "/y"},
	}}).Validate(); err == nil {
		t.Error("duplicate scenarioFilter must be rejected")
	}
	if got := (Endpoint{ScenarioFilter: "c_flag == 'F'"}).RefOrIndex(); got != "scenarioFilter c_flag == 'F'" {
		t.Errorf("RefOrIndex = %q", got)
	}
}

// TestPlanBuildScenarioFilter pins the filter endpoint's resolution on the
// braced-ladder fixture: one endpoint covers the F and H arms (no arm
// warnings for them), carries both queries, and leaves the else arm loudly
// advisory.
func TestPlanBuildScenarioFilter(t *testing.T) {
	f, src := defaultArmFixture(t)
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{{ScenarioFilter: "c_flag == 'H' || c_flag == 'F'", Name: "HistOrFull", Route: "/hist-full"}},
	}
	p, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatalf("filter endpoint rejected: %v", err)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "has no endpoint") && !strings.Contains(w, "else arm") {
			t.Errorf("filter-covered arm warned: %s", w)
		}
	}
	if defaultWarned := func() bool {
		for _, w := range p.Warnings {
			if strings.Contains(w, "else arm") && strings.Contains(w, "has no endpoint") {
				return true
			}
		}
		return false
	}(); !defaultWarned {
		t.Errorf("the uncovered else arm must warn, warnings = %v", p.Warnings)
	}
	ctrls := 0
	var qids []string
	for _, u := range p.Units {
		if u.Kind != KindControllerMethod {
			continue
		}
		ctrls++
		qids = append(qids, u.QueryIDs...)
	}
	if ctrls != 1 {
		t.Errorf("controller units = %d, want 1 (one merged filter endpoint)", ctrls)
	}
	// The fixture's F arm carries no SQL; the H insert is the endpoint's
	// one query (the nested-fixture test covers the multi-query merge).
	if len(qids) != 1 {
		t.Errorf("filter endpoint queries = %v, want the H insert", qids)
	}
}

// TestPlanBuildScenarioFilterErrors pins the loud rejects: unknown axes
// (with the registry suggestion), out-of-domain values, contradictory
// literals, and the cross-axis reachability prune.
func TestPlanBuildScenarioFilterErrors(t *testing.T) {
	f, src := defaultArmFixture(t)
	build := func(text string) error {
		m := &Mapping{
			Service: "svcs", Module: "app/pkg/services/svcs",
			Endpoints: []Endpoint{{ScenarioFilter: text, Name: "X", Route: "/x"}},
		}
		_, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
		return err
	}
	cases := []struct{ text, want string }{
		{"bogus_axis == 'F'", "unknown axis"},
		{"c_flag == 'X'", "matches nothing"},
		{"c_flag == 'H' && c_flag == 'F'", "contradictory literals"},
	}
	for _, tc := range cases {
		err := build(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.text, err, tc.want)
		}
	}
}

// filterNestedSrc nests the secondary chain inside the H arm — the
// reachability fixture (`K` never co-occurs with `F`).
const filterNestedSrc = `void SVC_N(TPSVCINFO *rqst) {
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
		EXEC SQL INSERT INTO TF VALUES (:b);
		Fadd32(obuf, FML_F_OUT, (char *)&b, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`

func filterNestedFixture(t *testing.T) (*ir.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_N.pc")
	if err := os.WriteFile(path, []byte(filterNestedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f, filterNestedSrc
}

// TestPlanBuildScenarioFilterIntersection pins the && case across the
// nested axis: H && K resolves (both witnessed), F && K is a loud
// reachability reject (K lives only under H).
func TestPlanBuildScenarioFilterIntersection(t *testing.T) {
	f, src := filterNestedFixture(t)
	build := func(text string) error {
		m := &Mapping{
			Service: "svcs", Module: "app/pkg/services/svcs",
			Endpoints: []Endpoint{{ScenarioFilter: text, Name: "X", Route: "/x"}},
		}
		_, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
		return err
	}
	if err := build("c_flag == 'H' && new_flag == 'K'"); err != nil {
		t.Fatalf("H && K must resolve: %v", err)
	}
	err := build("c_flag == 'F' && new_flag == 'K'")
	if err == nil || !strings.Contains(err.Error(), "no reachable arm") {
		t.Errorf("F && K: error = %v, want the reachability reject", err)
	}
	// The union merge carries both arms' SQL in one controller unit.
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{{ScenarioFilter: "c_flag == 'H' || c_flag == 'F'", Name: "HF", Route: "/hf"}},
	}
	p, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	qids := 0
	for _, u := range p.Units {
		if u.Kind == KindControllerMethod {
			qids = len(u.QueryIDs)
		}
	}
	if qids != 2 {
		t.Errorf("merged filter queries = %d, want both arms' inserts", qids)
	}
}
