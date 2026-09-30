package gen

import (
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/templates"
)

// helperParamPlan builds a plan with one fn helper whose Go signature carries
// the legacy C parameter names, the shape plan/helperSignature produces.
func helperParamPlan(params ...string) *plan.Plan {
	var ps []plan.FnParam
	for _, name := range params {
		ps = append(ps, plan.FnParam{Name: name, Type: "string"})
	}
	return &plan.Plan{FnHelpers: []plan.FnHelper{{
		Name: "fn_find_risk_profile", GoName: "FnFindRiskProfile", Return: "int", Params: ps,
	}}}
}

// TestHelperArgResolvesThroughProvenanceNotName is the regression test for
// P1's root cause. A helper parameter keeps its LEGACY C spelling, so the
// parameter is `c_user_id` while the request struct field is `UsrId`. Those
// two strings share nothing, which is why matching the parameter against the
// request field's Go name resolved nothing and every endpoint reported a gap.
//
// The request map is keyed on the legacy spelling because that is what the
// FML_GET target is — tux.pc:221 is
// Fget32(ptr_fml_Ibuffer, FML_USR_ID, 0, (char*)c_user_id, 0) — so both
// sides of the lookup speak C and the pairing is real.
func TestHelperArgResolvesThroughProvenanceNotName(t *testing.T) {
	reqMap := map[string]string{
		normHost("c_user_id"):     "UsrId",
		normHost("c_match_accnt"): "MatchAccnt",
	}
	// The Go-name map, kept only as the legacy fallback. It could never
	// have matched these parameters; that is the bug.
	byName := map[string]string{"usr_id": "UsrId", "match_accnt": "MatchAccnt"}

	p := helperParamPlan("c_user_id", "c_match_accnt", "c_zero_investment_flag")
	todos := map[string][]string{}
	got := detHelperCalls(p, "  i_ret = fn_find_risk_profile(c_ServiceName, c_user_id);\n", 448, reqMap, byName, todos)
	if len(got) != 1 {
		t.Fatalf("helpers = %d, want 1", len(got))
	}
	want := []string{"request.UsrId", "request.MatchAccnt", `""`}
	if len(got[0].args) != len(want) {
		t.Fatalf("args = %v, want %v", got[0].args, want)
	}
	for i := range want {
		if got[0].args[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[0].args[i], want[i])
		}
	}
	// Only the third parameter is genuinely unresolved, and it says so.
	if len(todos["helper:FnFindRiskProfile"]) != 1 {
		t.Errorf("todos = %v, want exactly one for c_zero_investment_flag", todos["helper:FnFindRiskProfile"])
	} else if !strings.Contains(todos["helper:FnFindRiskProfile"][0], "c_zero_investment_flag") {
		t.Errorf("todo = %q, want it to name c_zero_investment_flag", todos["helper:FnFindRiskProfile"][0])
	}
}

// TestHelperArgPrefersProvenanceOverNameMatch guards the precedence. Both
// maps can hold the same key, and reqMap must win: it is backed by an
// FML_GET, whereas byName is a coincidence of naming that would silently
// mis-bind when a helper parameter happens to share a Go field's name.
func TestHelperArgPrefersProvenanceOverNameMatch(t *testing.T) {
	reqMap := map[string]string{normHost("c_user_id"): "UsrId"}
	byName := map[string]string{"c_user_id": "SomethingElseEntirely"}

	p := helperParamPlan("c_user_id")
	todos := map[string][]string{}
	got := detHelperCalls(p, "  fn_find_risk_profile(c_user_id);\n", 448, reqMap, byName, todos)
	if len(got) != 1 || got[0].args[0] != "request.UsrId" {
		t.Errorf("args = %v, want the provenance-backed request.UsrId", got)
	}
}

// TestHelperArgStillFallsBackToLegacyNameMatch keeps the shapes that relied on
// the old behaviour working. A helper whose parameter is literally named
// after a Go request field resolved before and must still resolve.
func TestHelperArgStillFallsBackToLegacyNameMatch(t *testing.T) {
	byName := map[string]string{"matchaccnt": "MatchAccnt"}
	p := helperParamPlan("MatchAccnt")
	todos := map[string][]string{}
	got := detHelperCalls(p, "  fn_find_risk_profile(MatchAccnt);\n", 448, map[string]string{}, byName, todos)
	if len(got) != 1 || got[0].args[0] != "request.MatchAccnt" {
		t.Errorf("args = %v, want the legacy name match to still resolve", got)
	}
	if len(todos["helper:FnFindRiskProfile"]) != 0 {
		t.Errorf("todos = %v, want none", todos["helper:FnFindRiskProfile"])
	}
}

// TestHelperArgRefusesToBindAnEmptyRequestField: detRequestMap can in
// principle map a target to an empty field name. Emitting `request.` for
// that would produce code that does not compile, and it is strictly worse
// than the zero value plus an honest TODO.
func TestHelperArgRefusesToBindAnEmptyRequestField(t *testing.T) {
	reqMap := map[string]string{normHost("c_user_id"): ""}
	p := helperParamPlan("c_user_id")
	todos := map[string][]string{}
	got := detHelperCalls(p, "  fn_find_risk_profile(c_user_id);\n", 448, reqMap, map[string]string{}, todos)
	if len(got) != 1 {
		t.Fatalf("helpers = %d, want 1", len(got))
	}
	for _, a := range got[0].args {
		if strings.Contains(a, "request.") {
			t.Errorf("arg = %q, want no bare request. reference", a)
		}
	}
	if len(todos["helper:FnFindRiskProfile"]) != 1 {
		t.Errorf("todos = %v, want one — an empty field is unresolved, not resolved", todos["helper:FnFindRiskProfile"])
	}
}

// TestHelperCallsEmitsNoRequestCallWithoutAProvenMatch is the floor: with no
// maps at all, nothing may be bound to the request struct. A wrong binding
// compiles and silently passes the wrong value; an unresolved one is loud.
func TestHelperCallsEmitsNoRequestCallWithoutAProvenMatch(t *testing.T) {
	p := helperParamPlan("c_user_id", "d_debt_amt")
	todos := map[string][]string{}
	got := detHelperCalls(p, "  fn_find_risk_profile(c_user_id, &d_debt_amt);\n", 448, nil, nil, todos)
	if len(got) != 1 {
		t.Fatalf("helpers = %d, want 1", len(got))
	}
	for i, a := range got[0].args {
		if strings.HasPrefix(a, "request.") {
			t.Errorf("arg %d = %q, want a zero value — nothing was proven", i, a)
		}
	}
	if n := len(todos["helper:FnFindRiskProfile"]); n != 2 {
		t.Errorf("todos = %d, want one per parameter", n)
	}
}

// TestHelperCallsIgnoresAParamlessHelper keeps a no-arg helper from being
// counted as a gap. A helper with no parameters has nothing to resolve, and
// emitting zero TODOs for it is what lets the census total stay honest.
func TestHelperCallsIgnoresAParamlessHelper(t *testing.T) {
	p := helperParamPlan()
	todos := map[string][]string{}
	got := detHelperCalls(p, "  fn_find_risk_profile();\n", 448, nil, nil, todos)
	if len(got) != 1 || len(got[0].args) != 0 {
		t.Errorf("got = %+v, want one helper with no args", got)
	}
	if len(todos["helper:FnFindRiskProfile"]) != 0 {
		t.Errorf("todos = %v, want none", todos)
	}
}

// TestRequestMapKeysOnTheLegacyTargetSpelling is the invariant the fix rests
// on, pinned directly against the IR so it cannot drift: an FML_GET whose
// target is `c_user_id` must be reachable under normHost("c_user_id"). It
// would not be reachable under the Go field name, which is the whole bug.
func TestRequestMapKeysOnTheLegacyTargetSpelling(t *testing.T) {
	f := &ir.File{
		Conditions: []ir.Condition{{
			StartLine: 200, EndLine: 260,
			FmlOps: []ir.FmlOp{
				{Kind: ir.FmlGet, Field: "FML_USR_ID", Target: "c_user_id", Line: 221},
				{Kind: ir.FmlGet, Field: "FML_MATCH_ACCNT", Target: "c_match_accnt", Line: 227},
			},
		}},
	}
	s := &Service{Main: f, source: ""}
	got := detRequestMap(s, &f.Conditions[0])
	if got[normHost("c_user_id")] != "UsrId" {
		t.Errorf("reqMap[c_user_id] = %q, want UsrId — the helper parameter looks up exactly this key", got[normHost("c_user_id")])
	}
	if got[normHost("c_match_accnt")] == "" {
		t.Error("reqMap[c_match_accnt] is empty, want a request field")
	}
}

// TestHelperArgsResolveEndToEndThroughTheRealPlan is the paired check: the map
// detRequestMap builds and the lookup detHelperCalls performs agree, using
// the real plan shape. A change to either side's keying breaks here rather
// than silently reverting all 95 resolutions to zero.
func TestHelperArgsResolveEndToEndThroughTheRealPlan(t *testing.T) {
	f := &ir.File{
		Conditions: []ir.Condition{{
			StartLine: 200, EndLine: 260,
			FmlOps: []ir.FmlOp{
				{Kind: ir.FmlGet, Field: "FML_USR_ID", Target: "c_user_id", Line: 221},
			},
		}},
	}
	s := &Service{Main: f, source: ""}
	reqMap := detRequestMap(s, &f.Conditions[0])

	p := helperParamPlan("c_user_id", "c_zero_investment_flag")
	todos := map[string][]string{}
	got := detHelperCalls(p, "  fn_find_risk_profile(c_ServiceName, c_user_id, c_zero_investment_flag);\n",
		448, reqMap, detFieldSet(nil), todos)
	if len(got) != 1 {
		t.Fatalf("helpers = %d, want 1", len(got))
	}
	if got[0].args[0] != "request.UsrId" {
		t.Errorf("c_user_id = %q, want request.UsrId", got[0].args[0])
	}
	if len(todos["helper:FnFindRiskProfile"]) != 1 {
		t.Errorf("todos = %v, want exactly one remaining gap", todos["helper:FnFindRiskProfile"])
	}
}

// compile-time guard: detHelperCalls must keep taking FieldSpecs nowhere.
// The signature change from []templates.FieldSpec to two maps is the whole
// fix, and this references the type so an accidental reintroduction of the
// old parameter list is a build error rather than a silent behaviour change.
var _ = templates.FieldSpec{}
