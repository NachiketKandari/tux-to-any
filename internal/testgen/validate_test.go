package testgen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// A handler binds its request with c.BindJSON, which enforces the model's
// validator tags. Before this, every synthesized request was a placeholder, so
// BindJSON refused it and the handler answered 400 without ever calling the
// controller — which is why the generated handler suite failed almost every
// case, Success included.
//
// These pins use the corpus's real tags.

// TestValidatedLitSatisfiesCorpusTags pins each rule against a tag taken
// verbatim from the corpus models.
func TestValidatedLitSatisfiesCorpusTags(t *testing.T) {
	cases := []struct {
		tag  string
		typ  string
		fall string
		want string
		why  string
	}{
		{"oneof=B L", "string", `"requesttype"`, `"B"`,
			"RequestType failed on the 'oneof' tag"},
		{"oneof=W X Y", "string", `"customertype"`, `"W"`,
			"CustomerType failed on the 'oneof' tag"},
		{"positivenum", "string", `"questionno"`, `"1"`,
			"QuestionNo failed on the 'positivenum' tag"},
		{"positivenum", "int", "0", "1",
			"a zero is not a positive number"},
		{"matchaccount", "string", `"matchaccount"`, `"1"`,
			"MatchAccount failed on the 'matchaccount' tag"},
		{"required", "string", `"userid"`, `"userid"`,
			"a non-empty placeholder already passes"},
		// The corpus's real spelling. `required` must NOT consume the value,
		// or the oneof behind it is never reached and the request is still
		// refused with 400.
		{"required,oneof=W R", "string", `"fmltypofusr"`, `"W"`,
			"required in front of oneof must not shadow it"},
		{"required,positivenum", "string", `"fmlurqrqstnmbr"`, `"1"`,
			"required in front of positivenum must not shadow it"},
		{"required,matchaccount", "string", `"fmlmatchaccnt"`, `"1"`,
			"required in front of matchaccount must not shadow it"},
		{"", "string", `"userid"`, `"userid"`,
			"an untagged field keeps its placeholder"},
		{"email", "string", `"emailid"`, `"test@example.com"`,
			"email must look like an address"},
		{"min=5", "string", `"ab"`, `"abaaa"`,
			"min pads to the bound"},
		{"len=3", "string", `"questionno"`, `"que"`,
			"len trims to the exact bound"},
		{"gte=2", "int", "0", "2",
			"gte moves a zero up to the bound"},
		{"unknownrule", "string", `"userid"`, `"userid"`,
			"an unmodelled rule leaves the value alone"},
	}
	for _, c := range cases {
		got := validatedLit(c.tag, c.typ, c.fall, false)
		if got != c.want {
			t.Errorf("validatedLit(%q, %q, %s) = %s, want %s  (%s)",
				c.tag, c.typ, c.fall, got, c.want, c.why)
		}
	}
}

// TestValidatedLitReadsSliceElements pins that a tag on a slice field reaches
// the element type, since that is what the validator checks.
func TestValidatedLitReadsSliceElements(t *testing.T) {
	got := validatedLit("oneof=A B", "[]string", "", true)
	if got != `"A"` {
		t.Errorf("slice element literal = %s, want %q", got, "A")
	}
}

// TestFieldValuesSatisfyValidatorTags runs the whole path: models source with
// the corpus's tags in, request field values out. This is the pin that would
// have caught the 400.
func TestFieldValuesSatisfyValidatorTags(t *testing.T) {
	src := `package models

type AddQuestionRequest struct {
	QuestionNo      string ` + "`json:\"questionNo\" validate:\"positivenum\"`" + `
	CustomerType    string ` + "`json:\"customerType\" validate:\"oneof=W X Y\"`" + `
	QuestionSection string ` + "`json:\"questionSection\"`" + `
	UserID          string ` + "`json:\"userId\" validate:\"required\"`" + `
	Marks           int    ` + "`json:\"marks\" validate:\"positivenum\"`" + `
}
`
	mi := modelsFromSource(t, src)
	fx := &AssumedFixtureSource{Models: mi}
	got := map[string]string{}
	for _, fv := range fx.FieldValues("AddQuestionRequest") {
		got[fv[0]] = fv[1]
	}
	want := map[string]string{
		"QuestionNo":      "1",
		"CustomerType":    "W",
		"QuestionSection": "questionsection", // untagged: placeholder kept
		"UserID":          "userid",
		"Marks":           "1",
	}
	for field, w := range want {
		if got[field] != w {
			t.Errorf("%s = %q, want %q", field, got[field], w)
		}
	}
}

// TestFieldValuesReadNestedValidatorTags pins the nested case: the corpus
// rejects `QnA[0].QuestionID` on its own tag, so a synthesized element has to
// satisfy it too or the whole request is refused.
func TestFieldValuesReadNestedValidatorTags(t *testing.T) {
	src := `package models

type QnA struct {
	QuestionID string ` + "`json:\"questionId\" validate:\"positivenum\"`" + `
	AnswerID   string ` + "`json:\"answerId\" validate:\"oneof=a b c\"`" + `
}

type AssessQnARequest struct {
	QnA []QnA ` + "`json:\"qnA\"`" + `
}
`
	mi := modelsFromSource(t, src)
	fx := &AssumedFixtureSource{Models: mi}
	lit, ok := fx.elemLiteral("QnA", 0)
	if !ok {
		t.Fatal("elemLiteral produced no literal for QnA")
	}
	for _, want := range []string{`QuestionID: "1"`, `AnswerID: "a"`} {
		if !contains(lit, want) {
			t.Errorf("QnA literal %q does not contain %s", lit, want)
		}
	}
}

// TestValidateTagFallsBackToBinding pins the gin spelling: a model may carry
// the constraint under `binding` rather than `validate`.
func TestValidateTagFallsBackToBinding(t *testing.T) {
	src := `package models

type NavRequest struct {
	RequestType string ` + "`json:\"requestType\" binding:\"oneof=B L\"`" + `
}
`
	mi := modelsFromSource(t, src)
	fx := &AssumedFixtureSource{Models: mi}
	got := fx.FieldValues("NavRequest")
	if len(got) != 1 || got[0][1] != "B" {
		t.Fatalf("FieldValues = %v, want [[RequestType B]]", got)
	}
}

// modelsFromSource parses a models file into the inventory the fixtures read.
func modelsFromSource(t *testing.T, src string) *modelsInfo {
	t.Helper()
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "models.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	mi := &modelsInfo{Structs: map[string][]fieldInfo{}}
	for _, d := range af.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			var fields []fieldInfo
			for _, f := range st.Fields.List {
				info := fieldInfo{Type: renderExpr(f.Type, fset)}
				for _, n := range f.Names {
					info.Name = n.Name
					break
				}
				if f.Tag != nil {
					tag, _ := unquoteTag(f.Tag.Value)
					info.JSON = tagValue(tag, "json")
					info.DB = tagValue(tag, "db")
					info.Validate = tagValue(tag, "validate")
					if info.Validate == "" {
						info.Validate = tagValue(tag, "binding")
					}
				}
				if info.Name != "" {
					fields = append(fields, info)
				}
			}
			mi.Structs[ts.Name.Name] = fields
		}
	}
	return mi
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
func unquoteTag(v string) (string, error) { return strconv.Unquote(v) }

// TestHandlerBranchesCarryTheirRoutingGuard pins that a handler routing one
// request field to two controller methods records the guard that selects each
// one. Without it the generated EXPECT names a method the request may never
// reach, and the run fails with "Unexpected call to …ViewQuestions: there are
// no expected calls of the method".
func TestHandlerBranchesCarryTheirRoutingGuard(t *testing.T) {
	src := `package handler

func (f *handler) ViewQuestions(c *gin.Context) {
	var request models.ViewQuestionsRequest
	if err := c.BindJSON(&request); err != nil {
		gCtx.BadRequestJSON(err, request)
		return
	}

	var data any
	var err error

	if request.RequestType == "B" {
		data, err = f.controller.ViewQuestions(c, &request)
	}

	if request.RequestType == "L" {
		data, err = f.controller.ListSection(c, &request)
	}

	if err != nil {
		gCtx.FailureJSON(err)
		return
	}

	gCtx.SuccessJSON(data)
}
`
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "riskprofile.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		f := extractHandlerFact(fd, fset)
		if f == nil {
			t.Fatal("extractHandlerFact returned nil")
		}
		if len(f.CtrlBranches) != 2 {
			t.Fatalf("branches = %d, want 2 (the handler routes to two methods)", len(f.CtrlBranches))
		}
		want := []struct{ method, field, value string }{
			{"ViewQuestions", "RequestType", "B"},
			{"ListSection", "RequestType", "L"},
		}
		for i, w := range want {
			got := f.CtrlBranches[i]
			if got.Method != w.method || got.Field != w.field || got.Value != w.value || !got.Guarded {
				t.Errorf("branch %d = %+v, want method=%s field=%s value=%s guarded",
					i, got, w.method, w.field, w.value)
			}
		}
		// The envelope is read from the same body.
		for _, want := range []string{"BadRequestJSON", "FailureJSON", "SuccessJSON"} {
			found := false
			for _, e := range f.Envelope {
				if e.Method == want {
					found = true
				}
			}
			if !found {
				t.Errorf("envelope is missing %s", want)
			}
		}
	}
}

// TestHandlerWithoutRoutingHasNoGuard pins that an unguarded single call is not
// given a fabricated one, so the renderer leaves its fixture value alone.
func TestHandlerWithoutRoutingHasNoGuard(t *testing.T) {
	src := `package handler

func (f *handler) OrderList(c *gin.Context) {
	var request models.OrderRequest
	if err := c.BindJSON(&request); err != nil {
		gCtx.BadRequestJSON(err, request)
		return
	}
	data, err := f.controller.OrderList(c, &request)
	if err != nil {
		gCtx.FailureJSON(err)
		return
	}
	gCtx.SuccessJSON(data)
}
`
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "riskprofile.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		f := extractHandlerFact(fd, fset)
		if f == nil {
			t.Fatal("extractHandlerFact returned nil")
		}
		if len(f.CtrlBranches) != 1 {
			t.Fatalf("branches = %d, want 1", len(f.CtrlBranches))
		}
		if f.CtrlBranches[0].Guarded {
			t.Errorf("an unguarded call was given a guard: %+v", f.CtrlBranches[0])
		}
	}
}
