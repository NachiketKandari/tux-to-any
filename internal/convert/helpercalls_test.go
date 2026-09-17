package convert

import (
	"strings"
	"testing"

	"tux-to-any/internal/plan"
)

// sameFileHelper mirrors the plan record a risk.pc-style helper produces:
// c_user_id and c_match_accnt survive the signature; the service name,
// session id, and error buffer do not.
func sameFileHelper() plan.FnHelper {
	return plan.FnHelper{
		Name:   "fn_save_risk_profile",
		GoName: "FnSaveRiskProfile",
		Params: []plan.FnParam{
			{Name: "c_user_id", Type: "string"},
			{Name: "c_match_accnt", Type: "string"},
			{Name: "sql_ura_uniq_nmbr", Type: "string"},
			{Name: "c_flg_using", Type: "string"},
			{Name: "sql_urf_debt_prsrv_asset_prcnt", Type: "*float64"},
			{Name: "sql_urf_eq_growth_asset_prcnt", Type: "*float64"},
			{Name: "sql_urf_alternates_asset_prcnt", Type: "*float64"},
		},
		Return: "int",
	}
}

// TestRewriteHelperCalls pins the same-file helper call-site rewrite: the
// callee becomes the generated controller method, middleware-owned args are
// dropped, kept args stay verbatim, and multi-line call sites rewrite whole
// (the corpus wraps long argument lists).
func TestRewriteHelperCalls(t *testing.T) {
	helpers := map[string]plan.FnHelper{"fn_save_risk_profile": sameFileHelper()}

	single := "      i_ret = fn_save_risk_profile(c_ServiceName, c_user_id, c_match_accnt, sql_ura_uniq_nmbr, c_flg_using, &sql_urf_debt_prsrv_asset_prcnt, &sql_urf_eq_growth_asset_prcnt, &sql_urf_alternates_asset_prcnt, l_sssn_id, c_errmsg);"
	got, n := rewriteHelperCalls(single, helpers)
	if n != 1 {
		t.Fatalf("single-line rewrites = %d, want 1:\n%s", n, got)
	}
	want := "      i_ret = s.FnSaveRiskProfile(c_user_id, c_match_accnt, sql_ura_uniq_nmbr, c_flg_using, &sql_urf_debt_prsrv_asset_prcnt, &sql_urf_eq_growth_asset_prcnt, &sql_urf_alternates_asset_prcnt);"
	if got != want {
		t.Errorf("single-line rewrite:\n got %q\nwant %q", got, want)
	}

	multi := "i_ret = fn_save_risk_profile(\n" +
		"          c_ServiceName,\n" +
		"          c_user_id,\n" +
		"          c_match_accnt,\n" +
		"          sql_ura_uniq_nmbr,\n" +
		"          c_flg_using,\n" +
		"          &sql_urf_debt_prsrv_asset_prcnt,\n" +
		"          &sql_urf_eq_growth_asset_prcnt,\n" +
		"          &sql_urf_alternates_asset_prcnt,\n" +
		"          l_sssn_id,\n" +
		"          c_errmsg);\n" +
		"if(i_ret != 1)\n"
	got, n = rewriteHelperCalls(multi, helpers)
	if n != 1 {
		t.Fatalf("multi-line rewrites = %d, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "i_ret = s.FnSaveRiskProfile(c_user_id, c_match_accnt, sql_ura_uniq_nmbr, c_flg_using, ") {
		t.Errorf("multi-line call not rewritten:\n%s", got)
	}
	if strings.Contains(got, "c_ServiceName") || strings.Contains(got, "l_sssn_id") || strings.Contains(got, "c_errmsg") {
		t.Errorf("middleware-owned args survived:\n%s", got)
	}
	if !strings.Contains(got, "if(i_ret != 1)") {
		t.Errorf("trailing source mangled:\n%s", got)
	}

	// A different fn_* call (an external stub) is untouched.
	other, n := rewriteHelperCalls("x = fn_other(c_ServiceName);", helpers)
	if n != 0 || other != "x = fn_other(c_ServiceName);" {
		t.Errorf("non-helper call altered: %q (n=%d)", other, n)
	}
}

// TestLegacyHelpersSameFile pins the prompt mapping for a same-file helper:
// the line names the generated method with its exact signature, and it
// survives both the pre-rewrite legacy spelling and the rewritten Go call
// in the view (the chunked path filters by chunk text).
func TestLegacyHelpersSameFile(t *testing.T) {
	p := &plan.Plan{FnHelpers: []plan.FnHelper{sameFileHelper()}}
	pre := "i_ret = fn_save_risk_profile(c_ServiceName, c_user_id);"
	post := "i_ret = s.FnSaveRiskProfile(c_user_id);"
	for _, view := range []string{pre, post} {
		lines := legacyHelpers(p, view)
		if len(lines) != 1 {
			t.Fatalf("view %q: helper lines = %v, want 1", view, lines)
		}
		line := lines[0]
		for _, want := range []string{"fn_save_risk_profile", "s.FnSaveRiskProfile(c context.Context, c_user_id string", "int", "controller/fns.go"} {
			if !strings.Contains(line, want) {
				t.Errorf("view %q: helper line %q missing %q", view, line, want)
			}
		}
		if strings.Contains(line, "drop the call") {
			t.Errorf("same-file helper must never map to drop-the-call: %q", line)
		}
	}
	if lines := legacyHelpers(p, "nothing here"); len(lines) != 0 {
		t.Errorf("absent helper still mapped: %v", lines)
	}
}

// TestRequiredHelperCalls pins the orchestration contract extension: a
// helper call the rewritten view shows is required in the body exactly like
// a store call, and the chunk filter matches the rewritten spelling.
func TestRequiredHelperCalls(t *testing.T) {
	view := "i_ret = s.FnSaveRiskProfile(c_user_id, c_match_accnt)\n" +
		"rows, err := s.store.GetRpQuestionSections(c)"
	calls := requiredCalls(view, "s.store.")
	joined := strings.Join(calls, ",")
	for _, want := range []string{"s.store.GetRpQuestionSections", "s.FnSaveRiskProfile"} {
		if !strings.Contains(joined, want) {
			t.Errorf("required calls %v missing %s", calls, want)
		}
	}
	errs := requiredCallErrs(view, "if err := s.store.GetRpQuestionSections(c); err != nil { return nil, err }", "s.store.")
	if len(errs) != 1 || !strings.Contains(errs[0], "s.FnSaveRiskProfile") {
		t.Errorf("errs = %v, want exactly the dropped helper call", errs)
	}

	line := helperCallLine(sameFileHelper())
	if got := filterHelpers([]string{line}, view); len(got) != 1 {
		t.Errorf("filterHelpers dropped the rewritten helper line %q for view %q", line, view)
	}
}

// TestFnHelperGateSignatureContract pins the signature gate for service
// helpers: the prescribed parameter list is enforced when Fixed (callers
// were generated against it) and left free otherwise (fn-lib parity).
func TestFnHelperGateSignatureContract(t *testing.T) {
	h := sameFileHelper()
	h.Fixed = true
	good := "func (s *demoController) FnSaveRiskProfile(c context.Context, c_user_id string, c_match_accnt string, sql_ura_uniq_nmbr string, c_flg_using string, sql_urf_debt_prsrv_asset_prcnt *float64, sql_urf_eq_growth_asset_prcnt *float64, sql_urf_alternates_asset_prcnt *float64) int {\n\treturn 1\n}"
	if errs := fnHelperGate(good, h, "demoController", "", "s.store."); len(errs) != 0 {
		t.Errorf("prescribed signature rejected: %v", errs)
	}
	bad := strings.Replace(good, "c_match_accnt string", "matchAccnt string", 1)
	bad = strings.Replace(bad, "c_user_id string", "userID string", 1)
	errs := fnHelperGate(bad, h, "demoController", "", "s.store.")
	if joined := strings.Join(errs, "; "); !strings.Contains(joined, "signature contract") {
		t.Errorf("mismatched parameter list not caught: %v", errs)
	}

	free := h
	free.Fixed = false
	if errs := fnHelperGate(good, free, "demoController", "", "s.store."); len(errs) != 0 {
		t.Errorf("fn-lib helper gate must not enforce a signature: %v", errs)
	}
}
