package convert

import (
	"strings"
	"testing"

	"tux-to-any/internal/flow"
)

func orCensus() []flow.CensusCond {
	return []flow.CensusCond{
		{Line: 429, Cond: "c_d2u_active_flg == 'Y'", Skeleton: `# == "Y"`, Idents: []string{"c_d2u_active_flg"}, Effects: []string{"i_cnt_d2us"}},
		{Line: 440, Cond: "cnt_d2u > 0 || i_cnt_d2us > 0", Skeleton: "# > 0 || # > 0", Idents: []string{"cnt_d2u", "i_cnt_d2us"}, Effects: []string{"c_enable_d2u_flg"}},
	}
}

// TestEmptyIfErrs pins the unconditional reject: an `if cond {}` with no
// body and no else fails loudly.
func TestEmptyIfErrs(t *testing.T) {
	body := "getDmmD2uMatchMppngMstr := 0\nif getDmmD2uMatchMppngMstr != 0 {\n}\nreturn data, err"
	if errs := emptyIfErrs(body); len(errs) == 0 {
		t.Error("empty if passed — want an unconditional reject")
	}
	if errs := emptyIfErrs("if x > 0 {\n\tx := 1\n\t_ = x\n}\nreturn data, err"); len(errs) != 0 {
		t.Errorf("non-empty if rejected: %v", errs)
	}
}

// TestConditionPresenceSplitOr pins the staged-bug shape: a body carrying
// only one OR arm (with its effect) still misses the merged census entry,
// and the note names the lost source line for the retry.
func TestConditionPresenceSplitOr(t *testing.T) {
	census := orCensus()
	// One arm only (+ the flag block dropped): the merged OR entry must miss.
	body := "cntD2u := 1\ncEnableD2uFlg := \"N\"\nif cntD2u > 0 {\n\tcEnableD2uFlg = \"Y\"\n}\nreturn data, err"
	errs := conditionPresenceErrs(census, body)
	if len(errs) == 0 {
		t.Fatal("split-OR body passed — want a gate error + retry note")
	}
	joined := strings.Join(errs, "; ")
	if !strings.Contains(joined, "line 429") && !strings.Contains(joined, "line 440") {
		t.Errorf("retry note names no lost source line: %v", errs)
	}
	if !strings.Contains(joined, "lost — implement the branch") {
		t.Errorf("retry note missing the targeted hint: %v", errs)
	}
}

// TestConditionPresenceMergedPass pins the accept: the correctly merged
// body (both arms, both effects) passes.
func TestConditionPresenceMergedPass(t *testing.T) {
	census := orCensus()
	body := "cntD2u := 1\niCntD2us := 0\ncD2uActiveFlg := \"N\"\nif cD2uActiveFlg == \"Y\" {\n\tiCntD2us = 1\n}\ncEnableD2uFlg := \"N\"\nif cntD2u > 0 || iCntD2us > 0 {\n\tcEnableD2uFlg = \"Y\"\n} else {\n\tcEnableD2uFlg = \"N\"\n}\nreturn data, err"
	if errs := conditionPresenceErrs(census, body); len(errs) != 0 {
		t.Errorf("merged body rejected: %v", errs)
	}
}

// TestConditionPresenceRenamedPass pins the rename-robust fallback: C
// snake_case and Go camelCase match through the CamelLowerGo normalization,
// skeleton or ident overlap.
func TestConditionPresenceRenamedPass(t *testing.T) {
	census := []flow.CensusCond{
		{Line: 10, Cond: "cnt_d2u > 0 || i_cnt_d2us > 0", Skeleton: "# > 0 || # > 0", Idents: []string{"cnt_d2u", "i_cnt_d2us"}, Effects: []string{"c_enable_d2u_flg"}},
	}
	// Single-arm ident overlap still needs the effect: renamed arm + effect passes presence.
	body := "cntD2u := 1\ncEnableD2uFlg := \"N\"\nif cntD2u > 0 {\n\tcEnableD2uFlg = \"Y\"\n}\nreturn data, err"
	if errs := conditionPresenceErrs(census, body); len(errs) != 0 {
		t.Errorf("renamed single-arm body rejected (ident-overlap fallback): %v", errs)
	}
}
