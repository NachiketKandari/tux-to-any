package csdraft

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/cschk"
	"tux-to-any/internal/csgen"
	"tux-to-any/internal/csplan"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
)

// The draft round trip (B1/B2): render a draft for the CUSE-shaped
// fixture, load it through the strict mapping loader (zero hand-written
// yaml), build the plan, and generate a gate-clean tree. This is the
// TUX_CS_CORPUS smoke's structural path over a tracked fixture.
func TestDraftRoundTrip(t *testing.T) {
	const fixture = "../../testdata/fixtures/cs/SVC_CUST_GET_DTL.pc"
	irf, err := ir.ExtractFileOpts(fixture, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := flow.ScanForIR(string(src), irf)
	if err != nil {
		t.Fatal(err)
	}
	tree := flow.Build(src, facts, irf.Entry, irf)

	// Without config defaults: placeholders stay editable TODOs.
	draft := Render(irf, tree, src, Options{})
	for _, want := range []string{
		"namespace: CHANGE_ME_ROOT_NAMESPACE",
		"component: CustGetDtl",
		"scenarioRef: \"c_arm_flg=C\"",
		"name: \"CustGetDtlC\"",
		"route: \"cust_get_dtl_C\"",
		"requestFields:",
		"paramNames:",
		"dbMethods:",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q", want)
		}
	}

	// With config defaults (C1): the namespace/area placeholders fill in.
	draft = Render(irf, tree, src, Options{Namespace: "Acme.Api", Area: "OAO.Area"})
	if !strings.Contains(draft, "namespace: Acme.Api") || !strings.Contains(draft, "area: OAO.Area") {
		t.Errorf("config defaults did not fill the draft:\n%s", draft)
	}

	dir := t.TempDir()
	draftPath := filepath.Join(dir, "SVC_CUST_GET_DTL.cs.mapping.yaml")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	mapping, err := csplan.LoadMapping(draftPath)
	if err != nil {
		t.Fatalf("draft does not load: %v", err)
	}
	plan, err := csplan.Build(csplan.Options{Main: irf, Source: string(src), Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Endpoints) != 1 || plan.Endpoints[0].Name != "CustGetDtlC" {
		t.Fatalf("plan endpoints = %v, want [CustGetDtlC]", plan.Endpoints)
	}
	res, err := csgen.Generate(t.Context(), csgen.Options{Plan: plan, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range res.Order {
		for _, is := range cschk.Check(rel, res.Files[rel], "") {
			t.Errorf("gate: %s", is.Error())
		}
	}
	for _, is := range cschk.SQLFidelity(plan, res.Files) {
		t.Errorf("gate: %s", is.Error())
	}
}

// TestComponentOf pins the deterministic class-stem derivation.
// TestDraftGuardOnlySuggestions pins the guard-only draft: no census-
// qualifying candidate (the lone arm's FML reads sit in the preamble), no
// dispatch axis — the draft still suggests the lone condition, commented,
// so the file is never silently unmappable.
func TestDraftGuardOnlySuggestions(t *testing.T) {
	src := `#include <atmi.h>

void SVC_ONE_ARM(TPSVCINFO *rqst)
{
    char c_flag;
    if (strcmp(c_flag, "CUSE") == 0)
    {
        EXEC SQL SELECT MAR_FORM_NO INTO :sql_form_no FROM MAR_MBL_ACCOPN_RQST;
        Fadd32(ptr_fml_Obuffer, FML_FORM_NO, (char *)sql_form_no.arr, 0);
    }
    tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_ONE_ARM.pc")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	irf, err := ir.ExtractFile(path)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := flow.ScanForIR(src, irf)
	if err != nil {
		t.Fatal(err)
	}
	tree := flow.Build([]byte(src), facts, irf.Entry, irf)

	draft := Render(irf, tree, []byte(src), Options{})
	for _, want := range []string{
		"# no API candidates found (1 condition(s) inspected, no dispatch axis)",
		"# - condition: 1              # lone arm, lines 6-10",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
}

func TestComponentOf(t *testing.T) {
	cases := map[string]string{
		"SVC_CUST_GET_DTL": "CustGetDtl",
		"SVC_MF_NAV_LIST":  "MfNavList",
		"BAT_DEMO_RT":      "BatDemoRt",
		"":                 "Component",
	}
	for in, want := range cases {
		if got := ComponentOf(in); got != want {
			t.Errorf("ComponentOf(%q) = %q, want %q", in, got, want)
		}
	}
}
