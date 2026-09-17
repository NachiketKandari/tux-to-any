package plan

import (
	"testing"

	scanner "tux-to-any/internal/tsscan"
)

// TestHelperSignature pins the deterministic Go signature derivation for
// same-file helpers: params in legacy order, middleware-owned session
// params dropped, char*/varchar → string, long → int64, int → int,
// double* → *float64, arrays/pointers handled, void → no return.
func TestHelperSignature(t *testing.T) {
	src := `int fn_save_risk_profile(char *c_ServiceName,
                         char *c_user_id,
                         char *c_match_accnt,
                         char *sql_ura_uniq_nmbr,
                         char c_flg_using,
                         double* sql_urf_debt_prsrv_asset_prcnt,
                         double* sql_urf_eq_growth_asset_prcnt,
                         double* sql_urf_alternates_asset_prcnt,
                         long l_sssn_id,
                         char *c_err_msg)
{
	return 1;
}
`
	def := scanner.FunctionDef{Name: "fn_save_risk_profile", ReturnType: "int", StartLine: 1, BodyStartLine: 11, BodyEndLine: 13}
	params, ret, ok := helperSignature(def, src)
	if !ok {
		t.Fatal("signature derivation failed")
	}
	want := []FnParam{
		{"c_user_id", "string"},
		{"c_match_accnt", "string"},
		{"sql_ura_uniq_nmbr", "string"},
		{"c_flg_using", "string"},
		{"sql_urf_debt_prsrv_asset_prcnt", "*float64"},
		{"sql_urf_eq_growth_asset_prcnt", "*float64"},
		{"sql_urf_alternates_asset_prcnt", "*float64"},
	}
	if len(params) != len(want) {
		t.Fatalf("params = %+v, want %+v", params, want)
	}
	for i := range want {
		if params[i] != want[i] {
			t.Errorf("param %d = %+v, want %+v", i, params[i], want[i])
		}
	}
	if ret != "int" {
		t.Errorf("return = %q, want int", ret)
	}
}

func TestHelperSignatureEdgeCases(t *testing.T) {
	src := `void fn_void_helper(long l_x, long l_sssn_id)
{
}
int fn_bad(FBFR32 *ptr)
{
}
int fn_unnamed(char *)
{
}
int fn_ok(char c_flg, int i, double d, float f)
{
}
`
	// fn_void_helper: decl line 1, brace line 2 → void return, l_sssn_id dropped.
	params, ret, ok := helperSignature(scanner.FunctionDef{Name: "fn_void_helper", ReturnType: "void", StartLine: 1, BodyStartLine: 2}, src)
	if !ok || ret != "" || len(params) != 1 || params[0] != (FnParam{"l_x", "int64"}) {
		t.Errorf("void helper = %+v/%q/%v, want [l_x int64]/\"\"/true", params, ret, ok)
	}
	// fn_bad: unknown FBFR32 type → not derivable.
	if _, _, ok := helperSignature(scanner.FunctionDef{Name: "fn_bad", ReturnType: "int", StartLine: 4, BodyStartLine: 5}, src); ok {
		t.Error("FBFR32 parameter must not derive a signature")
	}
	// fn_unnamed: name-less parameter → not derivable.
	if _, _, ok := helperSignature(scanner.FunctionDef{Name: "fn_unnamed", ReturnType: "int", StartLine: 7, BodyStartLine: 8}, src); ok {
		t.Error("unnamed parameter must not derive a signature")
	}
	// scalars.
	params, ret, ok = helperSignature(scanner.FunctionDef{Name: "fn_ok", ReturnType: "int", StartLine: 10, BodyStartLine: 11}, src)
	if !ok || ret != "int" {
		t.Fatalf("fn_ok = %+v/%q/%v", params, ret, ok)
	}
	want := []FnParam{{"c_flg", "string"}, {"i", "int"}, {"d", "float64"}, {"f", "float32"}}
	if len(params) != len(want) {
		t.Fatalf("fn_ok params = %+v, want %+v", params, want)
	}
	for i := range want {
		if params[i] != want[i] {
			t.Errorf("fn_ok param %d = %+v, want %+v", i, params[i], want[i])
		}
	}
}
