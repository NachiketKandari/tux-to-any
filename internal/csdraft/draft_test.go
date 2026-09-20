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
		"# - scenarioFilter: \"c_arm_flg == 'C' || c_arm_flg == 'P'\"",
		"#     merges to c_arm_flg in {C,P} | matched: c_arm_flg=C, c_arm_flg=P",
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
	// Both arms map now: C carries FML reads+writes; P is the message-only
	// shape (reads the shared preamble, emits no FML) — the census rubric
	// accepts it as an endpoint whose handler returns the message string.
	if len(plan.Endpoints) != 2 || plan.Endpoints[0].Name != "CustGetDtlC" || plan.Endpoints[1].Name != "CustGetDtlP" {
		t.Fatalf("plan endpoints = %v, want [CustGetDtlC CustGetDtlP]", plan.Endpoints)
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
// TestDraftGuardOnlySuggestions pins the lone-arm draft: the arm's FML
// reads sit in the preamble (none in-arm), so the old reads+writes rubric
// passed nothing and the draft could only suggest the lone condition,
// commented. Return-anchored promotion (writes + shared tail terminal)
// now emits it as a real emit-only endpoint — the draft maps the file
// directly, and the mapping loads and plans.
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
		"- condition: 1",
		"writes: FML_FORM_NO",
		"name: \"OneArmAction1\"",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft missing %q:\n%s", want, draft)
		}
	}
	if strings.Contains(draft, "no API candidates found") {
		t.Errorf("lone arm must promote to a real endpoint, not the commented suggestion:\n%s", draft)
	}

	// The promoted endpoint loads and plans — the draft is convertible.
	dir2 := t.TempDir()
	draftPath := filepath.Join(dir2, "SVC_ONE_ARM.cs.mapping.yaml")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	mapping, err := csplan.LoadMapping(draftPath)
	if err != nil {
		t.Fatalf("draft does not load: %v", err)
	}
	plan, err := csplan.Build(csplan.Options{Main: irf, Source: src, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Endpoints) != 1 {
		t.Fatalf("plan endpoints = %v, want the promoted lone arm", plan.Endpoints)
	}
}

// TestDraftDisambiguatesQueryNames pins the merged-filter pin fix: two
// arms whose SQL both name MF_COMPANIES derive the same DefaultQueryName,
// so the draft's dbMethods pins must carry the numeric suffix the plan
// fallback uses — colliding consts do not compile.
func TestDraftDisambiguatesQueryNames(t *testing.T) {
	src := `void SVC_CC(TPSVCINFO *rqst) {
	char c_flag;
	long a;
	long b;
	if (c_flag == 'H') {
		EXEC SQL SELECT NVL(MAX(X), 0) INTO :a FROM MF_COMPANIES WHERE A = :sql_x;
		Fadd32(obuf, FML_H_OUT, (char *)&a, 0);
	} else if (c_flag == 'F') {
		EXEC SQL SELECT NVL(MAX(Y), 0) INTO :b FROM MF_COMPANIES WHERE B = :sql_y;
		Fadd32(obuf, FML_F_OUT, (char *)&b, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`
	path := filepath.Join(t.TempDir(), "SVC_CC.pc")
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
		"GetMFCOMPANIESQuery}",
		"GetMFCOMPANIESQuery2}",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft pins missing %q:\n%s", want, draft)
		}
	}
	draftPath := filepath.Join(t.TempDir(), "SVC_CC.cs.mapping.yaml")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := csplan.LoadMapping(draftPath); err != nil {
		t.Fatalf("draft does not load: %v", err)
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
