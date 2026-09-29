package testgen

import (
	"strings"
	"testing"
)

// TestLogFixtureSourceValues pins the per-method value mapping: request and
// response fields from the logged bodies, db columns from the logged result,
// layer-pinned error attribution, per-field assumed fallback, and the
// unknown-method degrade.
func TestLogFixtureSourceValues(t *testing.T) {
	logText := `09-29-2026 14:58:15 DEBUG riskprofile.go:16 demo-be/pkg/services/svc/handler.(*handler).Fetch SERVICE-START {"requestID": "aaaa1111-0000-4000-8000-000000000000", "matchAccount": null}
09-29-2026 14:58:15 DEBUG backoffice.go:30 demo-be/pkg/services/svc/controller.(*controller).Fetch START {"requestID": "aaaa1111-0000-4000-8000-000000000000", "matchAccount": null}
09-29-2026 14:58:15 DEBUG riskprofile.go:64 demo-be/pkg/services/svc/db.(*store).Fetch Result: {"requestID": "aaaa1111-0000-4000-8000-000000000000", "result:": {"A":{"String":"logged-a","Valid":true},"B":{"String":"logged-b","Valid":true}}}
09-29-2026 14:58:15 DEBUG httpResponse.go:87 demo-be/pkg/network.(*GinContext).SuccessJSON data {"requestID": "aaaa1111-0000-4000-8000-000000000000", "data": {"FML_ONE":"logged-one"}}
09-29-2026 14:58:15 DEBUG riskprofile.go:46 demo-be/pkg/services/svc/handler.(*handler).Fetch SERVICE-END {"requestID": "aaaa1111-0000-4000-8000-000000000000", "matchAccount": null}
09-29-2026 14:58:15 INFO custom_logger.go:53 demo-be/pkg/middlewares.CustomLogger.func1 API Call End {"requestID": "aaaa1111-0000-4000-8000-000000000000", "matchAccount": null, "path": "/svc/fetch", "status": 200, "method": "POST", "requestBody": "{\"FML_ONE\": \"in-one\", \"FML_TWO\": \"in-two\"}", "responseBody": "{\"status\":\"success\"}"}
09-29-2026 15:04:06 DEBUG riskprofile.go:50 demo-be/pkg/services/svc/handler.(*handler).Fetch SERVICE-START {"requestID": "bbbb2222-0000-4000-8000-000000000000", "matchAccount": null}
09-29-2026 15:04:06 DEBUG backoffice.go:111 demo-be/pkg/services/svc/controller.(*controller).Fetch START {"requestID": "bbbb2222-0000-4000-8000-000000000000", "matchAccount": null}
09-29-2026 15:04:06 DEBUG riskprofile.go:153 demo-be/pkg/services/svc/db.(*store).Fetch The Value for Exists: {"requestID": "bbbb2222-0000-4000-8000-000000000000", "Exists:": {"Int16":1,"Valid":true}}
09-29-2026 15:04:06 ERROR backoffice.go:125 demo-be/pkg/services/svc/controller.(*controller).Fetch business rule failed {"requestID": "bbbb2222-0000-4000-8000-000000000000", "matchAccount": null}
09-29-2026 15:04:06 INFO custom_logger.go:53 demo-be/pkg/middlewares.CustomLogger.func1 API Call End {"requestID": "bbbb2222-0000-4000-8000-000000000000", "matchAccount": null, "path": "/svc/fetch", "status": 500, "method": "POST", "requestBody": "{\"FML_ONE\": \"failed-one\", \"FML_TWO\": \"failed-two\"}", "responseBody": ""}
`
	data := ParseLog("fixture.log", []byte(logText))
	models := &modelsInfo{Structs: map[string][]fieldInfo{
		"FetchRequest": {
			{Name: "One", Type: "string", JSON: "FML_ONE"},
			{Name: "Two", Type: "string", JSON: "FML_TWO"},
		},
		"FetchResponse": {
			{Name: "One", Type: "string", JSON: "FML_ONE,omitempty"},
			{Name: "Two", Type: "string", JSON: "FML_TWO,omitempty"},
		},
		"FetchRow": {
			{Name: "A", Type: "sql.NullString", DB: "A"},
			{Name: "B", Type: "sql.NullString", DB: "B"},
		},
	}}
	src := NewLogFixtureSource(data, models, "svc")

	// Success scope: logged request/response/db values win.
	fx := src.ForUnit("svc", "db", "Fetch")
	mv, ok := fx.(MethodLogValues)
	if !ok {
		t.Fatal("scoped source does not implement MethodLogValues")
	}
	if got := mv.Provenance(); got != "log aaaa1111" {
		t.Errorf("provenance = %q", got)
	}
	if got := mv.SuccessTrace(); got == nil || got.ID != "aaaa1111-0000-4000-8000-000000000000" {
		t.Errorf("success trace = %+v", got)
	}
	if got := fx.ColExpr("A", "sql.NullString"); got != `sql.NullString{String: "logged-a", Valid: true}` {
		t.Errorf("logged ColExpr = %q", got)
	}
	if got := fx.RowValues([]string{"A", "B"}); strings.Join(got, ",") != "logged-a,logged-b" {
		t.Errorf("logged RowValues = %v", got)
	}
	if got := fx.ZeroExpr("string"); got != `""` {
		t.Errorf("zero expr = %q", got)
	}
	req := fx.FieldValues("FetchRequest")
	if len(req) != 2 || req[0] != [2]string{"One", "in-one"} || req[1] != [2]string{"Two", "in-two"} {
		t.Errorf("logged request values = %v", req)
	}
	respSource, ok := fx.(ResponseFieldSource)
	if !ok {
		t.Fatal("scoped source does not implement ResponseFieldSource")
	}
	resp := respSource.ResponseFieldValues("FetchResponse")
	if len(resp) != 2 || resp[0] != [2]string{"One", "logged-one"} {
		t.Errorf("logged response values = %v", resp)
	}
	if resp[1] != [2]string{"Two", "fmltwo"} {
		t.Errorf("per-field assumed fallback = %v", resp[1])
	}

	// Failed scope: failed request values and the logged error, layer-pinned.
	cfx := src.ForUnit("svc", "controller", "Fetch")
	cmv := cfx.(MethodLogValues)
	if got := cmv.ErrorMsg(); got != "business rule failed" {
		t.Errorf("controller error = %q", got)
	}
	if fr, ok := cfx.(interface {
		FailedRequestValues(string) [][2]string
	}); ok {
		if got := fr.FailedRequestValues("FetchRequest"); len(got) != 2 || got[0] != [2]string{"One", "failed-one"} {
			t.Errorf("failed request values = %v", got)
		}
	} else {
		t.Error("scoped source does not implement FailedRequestValues")
	}
	// The same error must not attribute to the db layer's Fetch.
	dfx := src.ForUnit("svc", "db", "Fetch")
	if got := dfx.(MethodLogValues).ErrorMsg(); got != "" {
		t.Errorf("db layer error attribution leaked: %q", got)
	}
	if got := dfx.(MethodLogValues).FailedTrace(); got == nil || got.Status != 500 {
		t.Errorf("db layer failed trace = %+v", got)
	}

	// Unknown method: assumed fixture values, no trace.
	ufx := src.ForUnit("svc", "db", "Missing")
	umv := ufx.(MethodLogValues)
	if umv.SuccessTrace() != nil || umv.Provenance() != "assumed" {
		t.Errorf("unknown method: trace=%v provenance=%q", umv.SuccessTrace(), umv.Provenance())
	}
	if got := ufx.ColExpr("A", "sql.NullString"); got != `sql.NullString{String: "a", Valid: true}` {
		t.Errorf("unknown method ColExpr = %q", got)
	}
	if got := umv.ErrorMsg(); got != "" {
		t.Errorf("unknown method error = %q", got)
	}
}

// TestLogFixturePrimitives pins the value-rendering edge cases: sql.Null*
// ints, booleans, scalars, and escaping of embedding-unsafe text.
func TestLogFixturePrimitives(t *testing.T) {
	intRaw := []byte(`{"Int64":42,"Valid":true}`)
	if got, ok := primitiveExpect("int64", intRaw); !ok || got != "42" {
		t.Errorf("int64 expect = %q ok=%v", got, ok)
	}
	boolRaw := []byte(`{"Int16":1,"Valid":true}`)
	if got, ok := primitiveExpect("bool", boolRaw); !ok || got != "true" {
		t.Errorf("bool expect = %q ok=%v", got, ok)
	}
	if got, ok := primitiveExpect("bool", []byte(`"true"`)); !ok || got != "true" {
		t.Errorf("bool string expect = %q ok=%v", got, ok)
	}
	if got := colExprFromRaw("sql.NullInt64", intRaw); got != "sql.NullInt64{Int64: 42, Valid: true}" {
		t.Errorf("NullInt64 literal = %q", got)
	}
	if got := goStringBody("a \"quoted\"\nline"); got != `a \"quoted\"\nline` {
		t.Errorf("escaped body = %q", got)
	}
}
