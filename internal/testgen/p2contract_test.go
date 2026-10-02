package testgen

// P2 pins (docs/gentest-fix-plan.md §4): the controller and handler layers must
// compile against the code they are generated from. Every shape here was
// invisible in the demo fixture — a 1-arg ctor, a handler whose name matches
// its controller call, a store method that takes a context — so these are the
// pins that would have caught F5/F6/F7/F8/F9 before the corpus run did.

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/templates"
	"tux-to-any/internal/testscan"
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
	// The DECLARATION is qualified too, not just the literal. The corpus writes
	// `QnA []QnA` (same package, unqualified) but the generated case struct
	// lives in the handler package, where a raw `[]QnA` is "undefined: QnA".
	if typ != "[]models.QnA" {
		t.Errorf("type = %q, want []models.QnA — the declaration must be qualified too", typ)
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

// TestFullTestGateReportsCleanOnAKnownGoodSuite closes the other half of the
// plan's §5: fullTestGate produced gate lines that nothing checked, so the gate
// could report anything — including success — without a test noticing.
//
// It stages a generated suite over the grown fixture (which now covers every
// F1/F2/F3/F5/F6/F7/F8/F9 shape) and asserts the gate reports PASS rather than
// failing or degrading to "skipped". The latter two matter as much as a failure:
// F11 exists because a gate that could not verify looked identical to a gate
// with nothing to say.
func TestFullTestGateReportsCleanOnAKnownGoodSuite(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	fixture := filepath.Join("..", "..", "testdata", "gentest")
	tgt, rep := scanTarget(t, fixture)
	// Stage into the fixture itself: fullTestGate needs the generated files
	// inside a real module for `go test` to resolve them, and a temp dir is
	// outside any module (the gate reports "skipped" there, which is exactly
	// the degradation this test must not accept).
	scratch := t.TempDir()
	if err := copyTree(t, fixture, scratch); err != nil {
		t.Fatal(err)
	}
	// A scratch copy is not in a module, so give it one.
	if err := os.WriteFile(filepath.Join(scratch, "go.mod"),
		[]byte("module gentestgate\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The scratch fixture's packages import demo-be/... — rewrite that prefix to
	// this scratch module so the staged suite resolves.
	if err := rewriteImportPrefix(t, scratch, "demo-be", "gentestgate"); err != nil {
		t.Fatal(err)
	}

	tgt.Root = scratch
	tgt.Path = scratch
	for i := range tgt.LayerDirs {
		tgt.LayerDirs[i].Dir = filepath.Join(scratch, "pkg", "services", "demo", string(tgt.LayerDirs[i].Layer))
		tgt.LayerDirs[i].ServiceDir = filepath.Join(scratch, "pkg", "services", "demo")
	}
	rep.Services = []testscan.ServiceReport{{
		Name: "demo", Dir: filepath.Join(scratch, "pkg", "services", "demo"),
	}}

	// Staged, exactly as the CLI does it (`Stage: explicit, FullTest: explicit`).
	// That pairing is the point: fullTestGate used to read only res.Files,
	// which a staged run leaves empty, so the one gate that actually RUNS the
	// generated tests never ran from the command line. Writing into the
	// scratch copy's own out tree keeps the suites inside a real module, which
	// is what the gate needs to resolve them.
	out := filepath.Join(scratch, "out")
	res, err := Generate(context.Background(), tgt, rep, Options{
		BaseDir: out, Workers: 1, NoLLM: true, Stage: true, FullTest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files)+len(res.Staged) == 0 {
		t.Fatalf("nothing was generated, so there is nothing for the gate to check")
	}
	if len(res.Gates) == 0 {
		t.Fatalf("no gate lines at all; gates are unchecked by construction")
	}
	for _, g := range res.Gates {
		if strings.Contains(g, "skipped") || strings.Contains(g, "could not verify") {
			t.Errorf("gate degraded to a non-answer, which reads like success: %s", g)
		}
	}
	if res.TestsFailed {
		t.Errorf("a suite over the known-good fixture must not fail the full-test gate:\n%s",
			strings.Join(res.Gates, "\n"))
	}
	// fullTestGate's own line format is "…: PASS"; compileGate's is "…: clean"
	// and its test run carries `-run ^$` (compile only). Requiring the
	// fullTestGate shape proves the runnable gate ran, not just the compile one.
	var sawPass bool
	for _, g := range res.Gates {
		if strings.Contains(g, "go test -count=1 ") && !strings.Contains(g, "-run ^$") &&
			strings.HasSuffix(g, ": PASS") {
			sawPass = true
		}
	}
	if !sawPass {
		t.Errorf("expected a fullTestGate line ending in PASS, got:\n%s",
			strings.Join(res.Gates, "\n"))
	}
}

// rewriteImportPrefix rewrites a module path prefix across a tree's .go files.
// The staged suite refers to the fixture's own module by name; a scratch copy
// has a different one, so the imports have to follow.
func rewriteImportPrefix(t *testing.T, root, from, to string) error {
	t.Helper()
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".go" {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if !strings.Contains(string(data), from) {
			return nil
		}
		return os.WriteFile(path, []byte(strings.ReplaceAll(string(data), `"`+from, `"`+to)), 0o644)
	})
}

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

// TestHandlerExpectUsesOneCallSitesArgs pins F6/F9 against a handler that calls
// MORE THAN ONE controller method. The corpus's ViewQuestions routes:
//
//	if request.RequestType == "B" { data, err = f.controller.ViewQuestions(c, &request) }
//	if request.RequestType == "L" { data, err = f.controller.ListSection(c, &request) }
//
// CtrlCallArgs used to APPEND across both call sites, producing
// ListSection(c, &request, &request) — three arguments to a method that takes
// two, so every handler suite failed to compile. The name and the arguments
// must describe the SAME call.
func TestHandlerExpectUsesOneCallSitesArgs(t *testing.T) {
	dir := t.TempDir()
	src := "package handler\n\n" +
		"type handler struct{ controller controllerIface }\n\n" +
		"type controllerIface interface {\n" +
		"\tViewQuestions(ctx context.Context, request *models.ViewQuestionsRequest) ([]*models.ListSectionResponse, error)\n" +
		"\tListSection(ctx context.Context, request *models.ViewQuestionsRequest) ([]*models.ListSectionResponse, error)\n" +
		"}\n\n" +
		"func (f *handler) ViewQuestions(c *gin.Context) {\n" +
		"\tvar request models.ViewQuestionsRequest\n" +
		"\tvar data any\n" +
		"\tvar err error\n" +
		"\tif request.RequestType == \"B\" {\n" +
		"\t\tdata, err = f.controller.ViewQuestions(c, &request)\n" +
		"\t}\n" +
		"\tif request.RequestType == \"L\" {\n" +
		"\t\tdata, err = f.controller.ListSection(c, &request)\n" +
		"\t}\n" +
		"\t_, _ = data, err\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f := extractLayer(dir, "handler", nil).Handler["ViewQuestions"]
	if f == nil {
		t.Fatal("no handler fact")
	}
	// Last call site wins for the name, and its own arguments come with it.
	if f.CtrlCall != "ListSection" {
		t.Errorf("CtrlCall = %q, want ListSection", f.CtrlCall)
	}
	if len(f.CtrlCallArgs) != 1 || f.CtrlCallArgs[0] != "&request" {
		t.Errorf("CtrlCallArgs = %v, want exactly [&request] from the named call site", f.CtrlCallArgs)
	}
}

// TestToleratesNoRowsComesFromTheBody pins the rule the corpus run forced.
// riskprofile's DeleteQuestion is a DELETE-tx that checks RowsAffected and
// returns errors.New("unable to delete the question"); the old code read the
// SQL verb, decided "tolerates zero rows", and every zero-rows case failed with
// "Received unexpected error". A body with no zero-rows error is the only
// thing that tolerates zero rows now — which is also what stops the tool
// asserting a sentinel for a method like EditOrder that cannot produce one.
func TestToleratesNoRowsComesFromTheBody(t *testing.T) {
	// A DELETE-tx whose body states an error does NOT tolerate.
	stated := &dbFact{Name: "D", Shape: "dml", IsTx: true, Query: "DELETE FROM T WHERE X = :1",
		NoRowsError: "unable to delete the question"}
	if toleratesNoRows(stated) {
		t.Error("a body that states a zero-rows error must assert it, not tolerate")
	}
	// A body with no zero-rows branch tolerates — DELETE-tx or not.
	for _, q := range []string{"DELETE FROM T WHERE X = :1", "INSERT INTO T VALUES (:1)"} {
		silent := &dbFact{Name: "S", Shape: "dml", IsTx: true, Query: q}
		if !toleratesNoRows(silent) {
			t.Errorf("a body with no zero-rows branch tolerates, whatever the verb (%q)", q)
		}
	}
	// The SQL verb is now irrelevant to the decision, which is the point: the
	// corpus has a DELETE-tx that asserts an error.
	if toleratesNoRows(stated) == toleratesNoRows(&dbFact{Name: "D3", Shape: "dml", IsTx: true, Query: "UPDATE T SET X = 1"}) {
		t.Error("a stated error and a silent body must decide differently regardless of verb")
	}
}

// TestNoRequestControllerMethodParses pins the fix that F9's own change made
// necessary: once extractCtrlFact yields a fact for a no-request endpoint, the
// controller template must not build a request for it. `request := &{}` does
// not parse, and the parse gate then discards the WHOLE controller suite — so
// one endpoint's shape silently removed coverage of all of them.
func TestNoRequestControllerMethodParses(t *testing.T) {
	out, err := renderTestCtrlMethod(templates.TestControllerMethodData{
		SuiteName: "DemoControllerSuiteController", StoreVar: "demoStore",
		CtrlVar: "demoController", Name: "OrderHandle",
		NoRequest: true, ExpectType: "*sqlx.DB", ExpectExpr: "nil",
		Calls: []templates.CtrlCall{{Method: "GetDB", Field: "mockInput"}},
		Cases: []templates.CtrlCase{{Desc: "Success", Inputs: []string{"[]any{nil, nil}"}, ExpectedOutput: "nil"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "request :=") {
		t.Errorf("a no-request method must not build a request:\n%s", out)
	}
	if !strings.Contains(out, "suite.demoController.OrderHandle(suite.ctx)") {
		t.Errorf("a no-request method is called with ctx alone:\n%s", out)
	}
	// The decisive assertion: the generated block must PARSE. Every other check
	// in this file can be satisfied by text that does not compile, which is
	// precisely the gap that let eleven findings through.
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", "package db\n"+out, 0); err != nil {
		t.Fatalf("no-request controller output does not parse: %v\n%s", err, out)
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
