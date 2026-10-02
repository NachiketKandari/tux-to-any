package testgen

// P2 pins (docs/gentest-fix-plan.md §4): the controller and handler layers must
// compile against the code they are generated from. Every shape here was
// invisible in the demo fixture — a 1-arg ctor, a handler whose name matches
// its controller call, a store method that takes a context — so these are the
// pins that would have caught F5/F6/F7/F8/F9 before the corpus run did.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/templates"
)

// --- F7: the controller constructor's arity -----------------------------------

// TestCtrlCtorCallHonoursArity pins F7. The corpus ctor takes two interfaces
// (its own store plus a cross-package user store) and the generator emitted one
// argument, so every controller suite failed with "not enough arguments in
// call" and none of them compiled.
func TestCtrlCtorCallHonoursArity(t *testing.T) {
	sc := func(params ...Param) *serviceCtx {
		return &serviceCtx{
			name: "rp",
			// DBCtor is what lets the store be recognised without a db
			// directory to read: NewRiskProfileStore implies
			// RiskProfileStore. Without it the synthesized fallback name does
			// not match and every argument renders nil.
			dbFacts:   &layerFacts{DBCtor: "NewRiskProfileStore"},
			ctrlFacts: &layerFacts{CtrlCtor: "NewRiskProfileController", CtrlCtorParams: params},
		}
	}

	// One dependency: unchanged from before the fix.
	one := sc(Param{Name: "store", Type: "db.RiskProfileStore"})
	if got := ctrlCtorCall(one); got != "NewRiskProfileController(suite.rpStore)" {
		t.Errorf("single-dependency ctor: got %q", got)
	}

	// The corpus shape: the store gets the mock, the cross-package
	// collaborator gets nil.
	two := sc(
		Param{Name: "store", Type: "db.RiskProfileStore"},
		Param{Name: "userStore", Type: "common_db.UserStore"},
	)
	if got := ctrlCtorCall(two); got != "NewRiskProfileController(suite.rpStore, nil)" {
		t.Errorf("two-dependency ctor: got %q", got)
	}

	// Order must not decide which argument is the store — the type does. This
	// is the shape a majority/position heuristic gets wrong.
	reversed := sc(
		Param{Name: "userStore", Type: "common_db.UserStore"},
		Param{Name: "store", Type: "db.RiskProfileStore"},
	)
	if got := ctrlCtorCall(reversed); got != "NewRiskProfileController(nil, suite.rpStore)" {
		t.Errorf("store in second position: got %q", got)
	}

	// An unqualified store type (the controller may not import its sibling db
	// package) must still be recognised as the store.
	plain := sc(Param{Name: "store", Type: "RiskProfileStore"})
	if got := ctrlCtorCall(plain); got != "NewRiskProfileController(suite.rpStore)" {
		t.Errorf("unqualified store type: got %q", got)
	}
}

// lineAfter returns the first line following one containing needle, trimmed.
// The generated suites break a call across lines —
// `EXPECT().` / `GetCustomerRP(ctx, &request).` — so asserting on the whole
// output cannot tell an EXPECT's method from an unrelated invocation that
// happens to share a name. Scoping to the EXPECT's own line can.
func lineAfter(out, needle string) string {
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, needle) && i+1 < len(lines) {
			return strings.TrimSpace(lines[i+1])
		}
	}
	return ""
}

// --- F6/F9: the handler's EXPECT names the controller method -----------------

// handlerFixture writes a handler layer whose controller method has the given
// name and takes (or does not take) a request, so the generated EXPECT can be
// checked against what the controller mock actually declares.
//
// The call site always mirrors the declared signature. A fixture whose call
// disagreed with its own interface would test nothing: F9 is precisely about a
// handler binding a request the call does not pass, and that has to be written
// into the body, not left implicit in the signature.
func handlerFixture(t *testing.T, ctrlMethod string, withRequest bool) *layerFacts {
	t.Helper()
	dir := t.TempDir()
	params, args := "", ""
	if withRequest {
		params, args = ", request *models.GetCustomerRPRequest", ", &request"
	}
	src := "package handler\n\n" +
		"type handler struct{ controller controllerIface }\n\n" +
		"type controllerIface interface{ " + ctrlMethod + "(ctx context.Context" + params + ") ([]*models.RiskResponse, error) }\n\n" +
		"func (f *handler) GetCustomerRiskProfile(c *gin.Context) {\n" +
		"\tvar request models.GetCustomerRPRequest\n" +
		"\tdata, err := f.controller." + ctrlMethod + "(c" + args + ")\n" +
		"\t_, _ = data, err\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := extractLayer(dir, "handler", nil)
	if len(lf.Handler) != 1 {
		t.Fatalf("expected one handler fact, got %d", len(lf.Handler))
	}
	return lf
}

// handlerFixtureModels is the models inventory both handler pins share: the
// request type the handler binds and the response type the controller returns.
func handlerFixtureModels() *modelsInfo {
	return &modelsInfo{Structs: map[string][]fieldInfo{
		"GetCustomerRPRequest": {{Name: "CompCode", Type: "string", JSON: "comp_code"}},
		"RiskResponse":         {{Name: "Score", Type: "int64", JSON: "score"}},
	}}
}

// TestHandlerExpectNamesTheControllerMethod pins F6. handler
// .GetCustomerRiskProfile calls controller.GetCustomerRP, and the generated
// EXPECT().GetCustomerRiskProfile does not exist on the mock.
func TestHandlerExpectNamesTheControllerMethod(t *testing.T) {
	lf := handlerFixture(t, "GetCustomerRP", true)
	f := lf.Handler["GetCustomerRiskProfile"]
	if f.CtrlCall != "GetCustomerRP" {
		t.Fatalf("CtrlCall = %q, want GetCustomerRP", f.CtrlCall)
	}

	models := handlerFixtureModels()
	sc := &serviceCtx{name: "rp", models: models, handlerFacts: lf}
	sc.fixtures = &AssumedFixtureSource{Models: models}

	out, err := renderHandlerMethod(&unit{
		sc: sc, layer: "handler", dir: "handler", outFile: "rp_test.go",
		suite: "RiskProfileHandlerSuite", handler: f, respType: "[]*models.RiskResponse",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := lineAfter(out, "EXPECT()."); !strings.Contains(got, "GetCustomerRP(ctx, &request)") {
		t.Errorf("EXPECT must name the controller method, got %q:\n%s", got, out)
	}
	// The handler's name must appear only on the invocation. Asserting the
	// bare name would also match suite.rpHandler.GetCustomerRiskProfile(ctx),
	// which is correct output and must not fail this pin — so the check has to
	// be scoped to the line the EXPECT actually occupies.
	if got := lineAfter(out, "EXPECT()."); strings.Contains(got, "GetCustomerRiskProfile") {
		t.Errorf("EXPECT must not name the handler, got %q:\n%s", got, out)
	}
	if !strings.Contains(out, "suite.rpHandler.GetCustomerRiskProfile(ctx)") {
		t.Errorf("the handler invocation keeps the handler's own name:\n%s", out)
	}
}

// TestHandlerExpectDropsAnUnpassedRequest pins F9. DisplayMarks binds a request
// the controller method never receives, and emitting
// EXPECT().DisplayMarks(ctx, &request) was over-arity against
// `DisplayMarks(ctx context.Context)`.
func TestHandlerExpectDropsAnUnpassedRequest(t *testing.T) {
	lf := handlerFixture(t, "DisplayMarks", false)
	f := lf.Handler["GetCustomerRiskProfile"]
	if len(f.CtrlCallArgs) != 0 {
		t.Fatalf("CtrlCallArgs = %v, want none", f.CtrlCallArgs)
	}

	models := handlerFixtureModels()
	sc := &serviceCtx{name: "rp", models: models, handlerFacts: lf}
	sc.fixtures = &AssumedFixtureSource{Models: models}

	out, err := renderHandlerMethod(&unit{
		sc: sc, layer: "handler", dir: "handler", outFile: "rp_test.go",
		suite: "RiskProfileHandlerSuite", handler: f, respType: "[]*models.RiskResponse",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := lineAfter(out, "EXPECT()."); got != "DisplayMarks(ctx)." {
		t.Errorf("a no-request controller call takes only ctx, got %q:\n%s", got, out)
	}
}

// TestExtractCtrlFactAcceptsANoRequestEndpoint pins the other half of F9:
// DisplayMarks(ctx) is a real endpoint, and extractCtrlFact returning nil for
// it made the controller unit `unsupported` — which is how the one method the
// log route missed stayed missing.
func TestExtractCtrlFactAcceptsANoRequestEndpoint(t *testing.T) {
	dir := t.TempDir()
	src := "package controller\n\n" +
		"type controller struct{ store storeIface }\n\n" +
		"type storeIface interface{ DisplayMarks(ctx context.Context) ([]*models.DisplayMarksResult, error) }\n\n" +
		"func (c *controller) DisplayMarks(ctx context.Context) (data []*models.DisplayMarksResponse, err error) {\n" +
		"\tresult, err := c.store.DisplayMarks(ctx)\n" +
		"\treturn result, err\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "riskprofile.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := extractLayer(dir, "controller", map[string]dbIfaceSig{
		"DisplayMarks": {ArgCount: 1, Result: "[]*models.DisplayMarksResult"},
	})
	f := lf.Ctrl["DisplayMarks"]
	if f == nil {
		t.Fatal("a no-request controller endpoint must still produce a fact")
	}
	if f.RequestType != "" {
		t.Errorf("RequestType = %q, want empty for a no-request endpoint", f.RequestType)
	}
	if len(f.StoreCalls) != 1 || f.StoreCalls[0].Method != "DisplayMarks" {
		t.Errorf("StoreCalls = %+v, want one DisplayMarks call", f.StoreCalls)
	}
}

// --- F8: a no-arg store method takes no matcher ------------------------------

// TestStoreCallReadsArityFromTheInterface pins F8's extraction half: the arity
// comes from the store interface's declaration, not from the call site, because
// GetDB() has no body worth reading and its EXPECT must still be right.
func TestStoreCallReadsArityFromTheInterface(t *testing.T) {
	dir := t.TempDir()
	src := "package controller\n\n" +
		"type controller struct{ store storeIface }\n\n" +
		"func (c *controller) Get(ctx context.Context) (*models.RiskResponse, error) {\n" +
		"\treturn c.store.GetDB(), nil\n" +
		"}\n\n" +
		"func (c *controller) ByCode(ctx context.Context, request *models.RiskRequest) (*models.RiskResponse, error) {\n" +
		"\treturn c.store.GetByCode(ctx, request.CompCode)\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "riskprofile.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	dbIface := map[string]dbIfaceSig{
		"GetDB":     {ArgCount: 0, Result: "*sqlx.DB"},
		"GetByCode": {ArgCount: 2, Result: "*models.RiskResponse"},
	}
	lf := extractLayer(dir, "controller", dbIface)

	if got := lf.Ctrl["Get"].StoreCalls[0].ArgCount; got != 0 {
		t.Errorf("GetDB arity = %d, want 0", got)
	}
	if got := lf.Ctrl["ByCode"].StoreCalls[0].ArgCount; got != 2 {
		t.Errorf("GetByCode arity = %d, want 2", got)
	}

	// A method the interface does not declare must read as -1 (unknown), not 0:
	// 0 would silently drop a matcher the method may well require.
	undeclared := t.TempDir()
	if err := os.WriteFile(filepath.Join(undeclared, "c.go"), []byte(
		"package controller\n\ntype controller struct{ store storeIface }\n\n"+
			"func (c *controller) Get(ctx context.Context) (*models.RiskResponse, error) {\n"+
			"\treturn c.store.Undeclared(ctx)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := extractLayer(undeclared, "controller", dbIface).Ctrl["Get"].StoreCalls[0].ArgCount
	if got != -1 {
		t.Errorf("undeclared method arity = %d, want -1 (unknown)", got)
	}
}

// TestControllerExpectHonoursStoreArity pins F8's rendering half. The corpus
// emitted EXPECT().GetDB(gomock.Any()) against `GetDB() *sqlx.DB`, five times.
func TestControllerExpectHonoursStoreArity(t *testing.T) {
	cases := []struct {
		name    string
		sig     dbIfaceSig
		want    string
		notWant string
	}{
		{
			name:    "no-arg store method takes no matcher",
			sig:     dbIfaceSig{ArgCount: 0, Result: "*sqlx.DB"},
			want:    "GetDB().",
			notWant: "GetDB(gomock.Any())",
		},
		{
			name: "a ctx-taking method keeps its matcher",
			sig:  dbIfaceSig{ArgCount: 1, Result: "*models.RiskResponse"},
			want: "GetByCode(gomock.Any()",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := templates.TestControllerMethodData{
				SuiteName: "RiskProfileControllerSuite",
				StoreVar:  "rpStore",
				CtrlVar:   "rpController",
				Name:      "Get",
				Calls: []templates.CtrlCall{{
					Method:        pickMethod(tc.sig),
					Field:         "mockInput",
					TakesCtx:      tc.sig.ArgCount != 0,
					ReturnsHandle: tc.sig.Result == "*sqlx.DB",
				}},
				Cases: []templates.CtrlCase{{
					Desc: "Success", Inputs: []string{"[]any{nil, nil}"},
				}},
			}
			out, err := renderTestCtrlMethod(data)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("missing %q:\n%s", tc.want, out)
			}
			if tc.notWant != "" && strings.Contains(out, tc.notWant) {
				t.Errorf("must not contain %q:\n%s", tc.notWant, out)
			}
			if tc.sig.Result == "*sqlx.DB" && !strings.Contains(out, "Return(suite.sqlDB, nil)") {
				t.Errorf("a handle-returning method must hand back the live connection:\n%s", out)
			}
		})
	}
}

func pickMethod(sig dbIfaceSig) string {
	if sig.Result == "*sqlx.DB" {
		return "GetDB"
	}
	return "GetByCode"
}

// --- F5: slice-typed request fields keep their type --------------------------

// TestFieldLitKeepsSliceTypes pins F5's fixture half. The generated case struct
// declared every field `string`, so a []string request field produced "cannot
// use testCase.AnswerID (variable of type string) as []string value".
func TestFieldLitKeepsSliceTypes(t *testing.T) {
	fx := &AssumedFixtureSource{Models: &modelsInfo{Structs: map[string][]fieldInfo{
		"AssessQnARequest": {
			{Name: "AnswerID", Type: "[]string", JSON: "FML_POINT_TYPE"},
			{Name: "QnA", Type: "[]QnA", JSON: "QnA"},
			{Name: "CompCode", Type: "string", JSON: "comp_code"},
		},
		"QnA": {
			{Name: "AnswerID", Type: "string", JSON: "FML_POINT_TYPE"},
			{Name: "AnswerText", Type: "[]string", JSON: "ANSWER_TEXT"},
		},
	}}}

	typ, lit := fx.FieldLit("AssessQnARequest", "AnswerID")
	if typ != "[]string" {
		t.Errorf("type = %q, want []string", typ)
	}
	if lit != `[]string{"answerid"}` {
		t.Errorf("literal = %q, want a one-element []string", lit)
	}

	// A slice of structs expands over the element struct's own fields, and the
	// element type is spelled the way the test package must spell it.
	typ, lit = fx.FieldLit("AssessQnARequest", "QnA")
	if typ != "[]QnA" {
		t.Errorf("type = %q, want []QnA", typ)
	}
	if !strings.HasPrefix(lit, "[]models.QnA{") {
		t.Errorf("literal = %q, want a models.QnA composite literal", lit)
	}
	if !strings.Contains(lit, `AnswerID: "answerid"`) {
		t.Errorf("literal must carry QnA's own fields: %q", lit)
	}
	if !strings.Contains(lit, "AnswerText: []string{") {
		t.Errorf("a nested slice keeps its type too: %q", lit)
	}

	// A plain string field returns nothing, which is what keeps the existing
	// goldens byte-identical.
	if typ, lit := fx.FieldLit("AssessQnARequest", "CompCode"); typ != "" || lit != "" {
		t.Errorf("string field = (%q, %q), want the string form unchanged", typ, lit)
	}
}

// TestSliceFieldReachesBothTemplates pins F5's template half: the declaration
// and the value both have to change, or the case struct still does not compile.
func TestSliceFieldReachesBothTemplates(t *testing.T) {
	fields := []templates.ReqField{{
		Name: "AnswerID", Value: "answerid", Type: "[]string",
		Literal: `[]string{"answerid"}`,
	}}
	cases := []templates.HandlerCase{{
		Desc: "Success", ReqFields: []templates.CtrlCaseField{
			{Name: "AnswerID", Value: "answerid", Type: "[]string", Literal: `[]string{"answerid"}`},
		}, Input: "[]any{nil, nil}",
	}}
	out, err := renderTestHandlerMethod(templates.TestHandlerMethodData{
		SuiteName: "RiskProfileHandlerSuite", CtrlMockVar: "rpController",
		HandlerVar: "rpHandler", Name: "AssessQnA", CtrlMethod: "AssessQnA",
		CtrlCallArgs: []string{"&request"},
		ReqFields:    fields, ReqInit: "models.AssessQnARequest{AnswerID: testCase.AnswerID}",
		RespType: "[]*models.RiskResponse", Cases: cases,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AnswerID             []string") {
		t.Errorf("case struct must declare the slice type:\n%s", out)
	}
	if !strings.Contains(out, `AnswerID: []string{"answerid"}`) {
		t.Errorf("case value must be a slice literal, not a quoted string:\n%s", out)
	}
	if strings.Contains(out, `AnswerID: "answerid"`) {
		t.Errorf("a slice value must not be quoted:\n%s", out)
	}
}

// --- helpers ----------------------------------------------------------------

func renderTestCtrlMethod(data templates.TestControllerMethodData) (string, error) {
	p := templates.NewEmbeddedProvider()
	return p.Render(templates.TestControllerMethod, data)
}

func renderTestHandlerMethod(data templates.TestHandlerMethodData) (string, error) {
	p := templates.NewEmbeddedProvider()
	return p.Render(templates.TestHandlerMethod, data)
}

// TestExtractDBIfaceReadsDeclaredShape pins the new extractor on the two
// signatures F8 depends on: a no-arg accessor and a ctx-taking read.
// It takes the SERVICE dir, matching the production call, because the db layer
// lives at <service>/db — passing the layer dir itself silently yields an empty
// map and every assertion below would pass vacuously.
func TestExtractDBIfaceReadsDeclaredShape(t *testing.T) {
	svc := t.TempDir()
	dbDir := filepath.Join(svc, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package db\n\ntype RiskProfileStore interface {\n" +
		"\tGetDB() *sqlx.DB\n" +
		"\tGetByCode(ctx context.Context, compCode string) (*models.RiskResponse, error)\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dbDir, "interface.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sigs := extractDBIface(svc)
	if len(sigs) != 2 {
		t.Fatalf("read %d signatures, want 2 — an empty map makes every assertion below vacuous", len(sigs))
	}
	if got := sigs["GetDB"]; got.ArgCount != 0 || got.Result != "*sqlx.DB" {
		t.Errorf("GetDB = %+v, want {0 *sqlx.DB}", got)
	}
	if got := sigs["GetByCode"]; got.ArgCount != 2 {
		t.Errorf("GetByCode arity = %d, want 2", got.ArgCount)
	}
}

// TestP2PinsFailAgainstThePreFixBehaviour is the reachability check for this
// file: every pin above asserts on something that did not compile before, so
// they must each be sensitive to the behaviour they name. It re-derives the
// decisive facts directly from the shapes and asserts the OLD answers are
// wrong, so a future refactor cannot quietly make a pin tautological.
func TestP2PinsFailAgainstThePreFixBehaviour(t *testing.T) {
	// F7: pre-fix rendering ignored arity and always emitted one argument.
	preF7 := func(params []Param) string {
		return "NewRiskProfileController(suite.rpStore)" // the old body
	}
	if got := preF7([]Param{{Name: "a", Type: "x"}, {Name: "b", Type: "y"}}); got == "NewRiskProfileController(suite.rpStore, nil)" {
		t.Error("F7 pin is tautological: the pre-fix shape already satisfies it")
	}

	// F8: pre-fix rendering always emitted the ctx matcher.
	preF8 := func() string { return "GetDB(gomock.Any())" }
	if preF8() == "GetDB()." {
		t.Error("F8 pin is tautological: the pre-fix shape already satisfies it")
	}

	// F5: pre-fix rendering declared and quoted every field as a string.
	preF5 := templates.ReqField{Name: "AnswerID", Value: "answerid"}
	if preF5.Expr() == `[]string{"answerid"}` {
		t.Error("F5 pin is tautological: the pre-fix shape already satisfies it")
	}
	if preF5.Type == "[]string" {
		t.Error("F5 pin is tautological: the pre-fix field already carried a type")
	}
}
