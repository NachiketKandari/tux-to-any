package csgen

import (
	"strings"
	"testing"

	"tux-to-any/internal/csplan"
)

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #7) pinned
// the convertcs residue/scenario/span surfacing: EndpointPlan.Scenario,
// .Residue (the loud SCEN-D4 slice evidence) and .LineSpan were computed
// and dropped — no reader anywhere, and the seam re-derived the span by
// Sscanf-parsing the Span string. The seam prompt now carries all three.
func TestSeamPromptCarriesScenarioAndResidue(t *testing.T) {
	ep := EpData{
		Name: "GetCustF", Route: "/cust-f", DTOName: "CustF", RetType: "CustFDto",
		ReturnExpr: "response", Span: "40-120",
		Scenario: "c_flag=F",
		Residue:  []string{"L88: kept verbatim — touches the axis"},
		LineSpan: [2]int{40, 120},
	}
	p := &csplan.Plan{Component: "CustComponent", Service: "CustService"}
	prompt := userPrompt(p, ep, fileData{RepoField: "Repo", Service: "CustService"}, "src", nil)
	for _, want := range []string{
		"endpoint: GetCustF · scenario c_flag=F",
		"Slice residue (regions the planner kept verbatim",
		"L88: kept verbatim — touches the axis",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("seam prompt missing %q\ngot:\n%s", want, prompt)
		}
	}
	// A condition-sliced arm (no scenario, no residue) keeps the clean
	// header and renders no residue block.
	ep.Scenario = ""
	ep.Residue = nil
	prompt = userPrompt(p, ep, fileData{RepoField: "Repo", Service: "CustService"}, "src", nil)
	if strings.Contains(prompt, "scenario") || strings.Contains(prompt, "Slice residue") {
		t.Errorf("condition-sliced arm must not carry scenario/residue markers, got:\n%s", prompt)
	}
}

// TestSeamPromptCarriesFilterFacts pins the S6 convertcs twin: a
// scenarioFilter arm's seam prompt carries the expression and its
// matched/pruned assignment evidence (a scenarioRef arm stays clean).
func TestSeamPromptCarriesFilterFacts(t *testing.T) {
	ep := EpData{
		Name: "HistOrFull", Route: "hist-full", DTOName: "CustF", RetType: "CustFDto",
		ReturnExpr: "response", Span: "40-120",
		Scenario: "c_flag in {F,H}", Filter: "c_flag == 'H' || c_flag == 'F'",
		FilterMatched: []string{"c_flag=F", "c_flag=H"},
		FilterPruned:  []string{"c_flag=default && new_flag=K (no reachable new_flag)"},
		LineSpan:      [2]int{40, 120},
	}
	p := &csplan.Plan{Component: "CustComponent", Service: "CustService"}
	prompt := userPrompt(p, ep, fileData{RepoField: "Repo", Service: "CustService"}, "src", nil)
	for _, want := range []string{
		"Scenario filter: c_flag == 'H' || c_flag == 'F' — the arm is the re-fold under c_flag=F, c_flag=H",
		"pruned at plan time: c_flag=default && new_flag=K (no reachable new_flag)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("seam prompt missing %q\ngot:\n%s", want, prompt)
		}
	}
	ep.Filter, ep.FilterMatched, ep.FilterPruned = "", nil, nil
	prompt = userPrompt(p, ep, fileData{RepoField: "Repo", Service: "CustService"}, "src", nil)
	if strings.Contains(prompt, "Scenario filter") {
		t.Errorf("scenarioRef/condition arm must not carry filter facts, got:\n%s", prompt)
	}
}

func TestEpLineSpanReadsPlanSpan(t *testing.T) {
	if from, to := epLineSpan(EpData{LineSpan: [2]int{40, 120}}); from != 40 || to != 120 {
		t.Errorf("epLineSpan = %d-%d, want 40-120 (the plan's LineSpan, not a string re-parse)", from, to)
	}
	// Legacy fallback: EpData without a plan span still parses the string.
	if from, to := epLineSpan(EpData{Span: "7-9"}); from != 7 || to != 9 {
		t.Errorf("fallback epLineSpan = %d-%d, want 7-9", from, to)
	}
}

// End-to-end: the real fixture pipeline (csplan → buildEndpoints) feeds the
// seam prompt the scenario ref — the wiring this audit item fixed.
func TestSeamPromptCarriesScenarioEndToEnd(t *testing.T) {
	p, src := loadCustPlan(t)
	var scenEp *csplan.EndpointPlan
	for i := range p.Endpoints {
		if p.Endpoints[i].Scenario != "" {
			scenEp = &p.Endpoints[i]
		}
	}
	if scenEp == nil {
		t.Fatal("fixture lost its scenario-sliced endpoint — test premise broken")
	}
	var ep *EpData
	eps, _ := buildEndpoints(p)
	for i := range eps {
		if eps[i].Name == scenEp.Name {
			ep = &eps[i]
		}
	}
	if ep == nil || ep.Scenario != scenEp.Scenario || ep.LineSpan != scenEp.LineSpan {
		t.Fatalf("buildEndpoints must copy Scenario/LineSpan from the plan, got %+v", ep)
	}
	if !strings.Contains(userPrompt(p, *ep, fileData{RepoField: "Repo", Service: p.Service}, src, nil), "scenario "+scenEp.Scenario) {
		t.Errorf("seam prompt must carry the scenario ref %q", scenEp.Scenario)
	}
}
