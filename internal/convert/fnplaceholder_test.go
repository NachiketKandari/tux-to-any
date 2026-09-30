package convert

import (
	"errors"
	"strings"
	"testing"

	"tux-to-any/internal/gen"
	"tux-to-any/internal/goast"
	"tux-to-any/internal/plan"
)

// fixedHelper is the shape a service-run helper takes: a prescribed signature
// that callers were generated against, so the method must exist whatever
// happens to its body.
func fixedHelper() plan.FnHelper {
	return plan.FnHelper{
		Name: "fn_save_risk_profile", GoName: "FnSaveRiskProfile", Fixed: true, Return: "int",
		Params: []plan.FnParam{
			{Name: "c_user_id", Type: "string"},
			{Name: "c_match_accnt", Type: "string"},
			{Name: "sql_urf_debt_prsrv_asset_prcnt", Type: "*float64"},
			{Name: "sql_urf_eq_growth_asset_prcnt", Type: "*float64"},
		},
	}
}

// TestPlaceholderFnHelperMethodParses is the load-bearing assertion. The
// whole point of the placeholder is that the emitted package keeps compiling
// when a deterministic body is gated out, so a placeholder that does not parse
// would reproduce the exact failure it exists to prevent.
func TestPlaceholderFnHelperMethodParses(t *testing.T) {
	method := placeholderFnHelperMethod(fixedHelper(), "tuxController", errors.New("23:41: expected ';', found ':='"))
	if _, err := goast.Emit("convert: placeholder", "package controller\n\n"+method); err != nil {
		t.Fatalf("placeholder does not parse: %v\n%s", err, method)
	}
}

// TestPlaceholderCarriesThePrescribedSignature is what makes it a usable
// stand-in: the caller in tux.go was generated against plan's signature, not
// against whatever survived, so a placeholder that drifted from it would
// reintroduce the compile break at the call site.
func TestPlaceholderCarriesThePrescribedSignature(t *testing.T) {
	h := fixedHelper()
	method := placeholderFnHelperMethod(h, "tuxController", errors.New("gated out"))
	want := fnHelperDecl(h, "tuxController")
	if !strings.HasPrefix(method, want+" {") {
		t.Errorf("placeholder does not start with the prescribed declaration:\nwant prefix %q\ngot %q", want+" {", method)
	}
}

// TestPlaceholderNamesTheReasonCode is what lets the census see it. Without a
// tuxgo:TODO the gap is invisible again — which is the hole this whole
// change was opened to close.
func TestPlaceholderNamesTheReasonCode(t *testing.T) {
	method := placeholderFnHelperMethod(fixedHelper(), "tuxController", errors.New("body did not parse"))
	if !strings.Contains(method, gen.NoStoreCallsMark) {
		t.Errorf("placeholder carries no %s marker:\n%s", gen.NoStoreCallsMark, method)
	}
	if !strings.Contains(method, "did not parse") {
		t.Errorf("placeholder does not record why:\n%s", method)
	}
}

// TestPlaceholderZeroesItsOutParams keeps the int-status contract: a caller
// that passed addresses must not be left holding a value the placeholder
// never wrote.
func TestPlaceholderZeroesItsOutParams(t *testing.T) {
	method := placeholderFnHelperMethod(fixedHelper(), "tuxController", errors.New("gated out"))
	for _, p := range []string{"sql_urf_debt_prsrv_asset_prcnt", "sql_urf_eq_growth_asset_prcnt"} {
		if !strings.Contains(method, "*"+p+" = 0") {
			t.Errorf("out-param %s is not zeroed:\n%s", p, method)
		}
	}
	// And not the value params, which are caller-owned.
	if strings.Contains(method, "*c_user_id") {
		t.Errorf("value param was treated as an out-param:\n%s", method)
	}
}

// TestPlaceholderReturnsTheLegacyFailureStatus: the helpers are called and
// their int result compared against -1, so a placeholder that returned 0
// would tell the caller the work succeeded.
func TestPlaceholderReturnsTheLegacyFailureStatus(t *testing.T) {
	method := placeholderFnHelperMethod(fixedHelper(), "tuxController", errors.New("gated out"))
	if !strings.Contains(method, "return -1") {
		t.Errorf("placeholder does not return the failure status:\n%s", method)
	}
	// A void helper must not return -1 — that does not compile.
	void := fixedHelper()
	void.Return = ""
	vm := placeholderFnHelperMethod(void, "tuxController", errors.New("gated out"))
	if strings.Contains(vm, "return -1") {
		t.Errorf("void placeholder returns a value:\n%s", vm)
	}
	if _, err := goast.Emit("convert: void placeholder", "package controller\n\n"+vm); err != nil {
		t.Errorf("void placeholder does not parse: %v\n%s", err, vm)
	}
}

// TestPlaceholderReasonSurvivesAMultilineCause is defensive: the reason is
// interpolated into a // comment, and a newline there would silently comment
// out the rest of the body.
func TestPlaceholderReasonSurvivesAMultilineCause(t *testing.T) {
	method := placeholderFnHelperMethod(fixedHelper(), "tuxController",
		errors.New("line one\nline two\r\nline three"))
	reason := ""
	for _, ln := range strings.Split(method, "\n") {
		if strings.Contains(ln, "// Reason:") {
			reason = ln
		}
	}
	if reason == "" {
		t.Fatalf("no reason line:\n%s", method)
	}
	if strings.ContainsAny(reason, "\r\n") {
		t.Errorf("reason line carries a line break: %q", reason)
	}
	if !strings.Contains(reason, "line one") || !strings.Contains(reason, "line three") {
		t.Errorf("reason lost content: %q", reason)
	}
	if _, err := goast.Emit("convert: multiline", "package controller\n\n"+method); err != nil {
		t.Errorf("a multiline reason broke the placeholder: %v\n%s", err, method)
	}
}

// TestOneLineCollapsesEveryLineBreak is the unit behind the above.
func TestOneLineCollapsesEveryLineBreak(t *testing.T) {
	for _, in := range []string{"a\nb", "a\r\nb", "a\n\nb", "  a\nb  "} {
		got := oneLine(in)
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("oneLine(%q) = %q, still has a line break", in, got)
		}
	}
}

// The companion regression for the same defect — the missing newline in
// detEmitFnStoreCall — lives in internal/gen/fnstorecall_test.go, because
// that emitter is a gen one.
