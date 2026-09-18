package gen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/goast"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
)

// genNavFixture builds the plan (the user mapping pins reference-quality
// names) and the derived Service. The fn files are returned so variants can
// rebuild a Service (e.g. WithGorm) without re-extracting.
func genNavFixture(t *testing.T) (*Service, *plan.Plan, []*ir.File) {
	t.Helper()
	files, err := ir.ExtractDir("../../testdata/nav")
	if err != nil {
		t.Fatal(err)
	}
	var main *ir.File
	var fns []*ir.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "SVC_DEMO_LIST.pc") {
			main = f
		} else {
			fns = append(fns, f)
		}
	}
	if main == nil {
		t.Fatal("nav IR missing")
	}
	m := &plan.Mapping{
		Service:    "nav",
		Module:     "mutual-fund-be/pkg/services/nav",
		ReadDBs:    []string{"EBATEST", "MF"},
		RouteGroup: "/nav",
		Endpoints: []plan.Endpoint{
			{Condition: 1, Name: "NavHistory", Route: "/mfnavhistory"},
			{Condition: 2, Name: "SipFreedem", Route: "/mf_sipfreedem_schemes"},
			{Condition: 3, Name: "SipInsurance", Route: "/mf_sipinsurance_schemes"},
			{Condition: 4, Name: "NavList", Route: "/mfnavschemelist"},
		},
		DBMethods: map[string]plan.MethodPin{
			"q1":                   {Name: "GetDateDetails", Row: "DateInfo"},
			"cur_demo_hist":        {Name: "GetNavHistory", Row: "NavHistoryDetail", Params: []string{"compCd:string", "schCd:string", "fromDate:time.Time", "toDate:time.Time"}},
			"q3":                   {Name: "GetCount", Params: []string{"matchAccount:string"}},
			"cur_demo_featured":    {Name: "GetSipFreedem", Row: "SipFreedemDetail"},
			"cur_demo_insured":     {Name: "GetSipInsurance", Row: "SipInsuranceDetail"},
			"cur_demo_list":        {Name: "GetNavDetails", Params: []string{"compCd:string"}},
			"fn_is_demo_active:q1": {Name: "IsDemoActive", Row: "DemoActive"},
		},
	}
	src, err := os.ReadFile(main.Path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.Build(plan.Options{Main: main, Source: string(src), FnFiles: fns, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(Options{Plan: p, Main: main, FnFiles: fns})
	if err != nil {
		t.Fatal(err)
	}
	return s, p, fns
}

// TestModelFileRowNameCollision pins the models-file guard: a row name
// pinned for two different shapes is a loud error (the file would declare
// the type twice); identical shapes share one declaration.
func TestModelFileRowNameCollision(t *testing.T) {
	s, p, _ := genNavFixture(t)
	pin := s.Mapping.DBMethods["cur_demo_featured"]
	pin.Row = "NavHistoryDetail" // cur_demo_hist's shape differs
	s.Mapping.DBMethods["cur_demo_featured"] = pin
	if _, err := s.ModelFile(p); err == nil || !strings.Contains(err.Error(), "row struct name") {
		t.Fatalf("ModelFile = %v, want row-name collision error", err)
	}
	pin.Row = "SipFreedemDetail"
	s.Mapping.DBMethods["cur_demo_featured"] = pin

	// Identical shapes share one struct declaration.
	hist, list := s.Query("cur_demo_hist"), s.Query("cur_demo_list")
	list.RowShape = append([]string(nil), hist.RowShape...)
	// Computed-column aliases derive from the SELECT text and the canonical
	// query ID as well as the row shape — copy all three so the shapes are
	// truly identical under the alias-aware fingerprint.
	list.SQL = hist.SQL
	list.DuplicateOf = hist.ID
	listPin := s.Mapping.DBMethods["cur_demo_list"]
	listPin.Row = "NavHistoryDetail"
	s.Mapping.DBMethods["cur_demo_list"] = listPin
	out, err := s.ModelFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "type NavHistoryDetail struct"); got != 1 {
		t.Errorf("identical shapes must share one declaration, got %d", got)
	}
}

func TestGenModels(t *testing.T) {
	s, p, _ := genNavFixture(t)
	models, err := s.ModelFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type NavHistoryRequest struct",
		"CompCd string `json:\"FML_COMP_CD\" binding:\"required\"`",
		"type NavListResponse struct",
		"PriceDate", "json:\"FML_PRICE_DATE,omitempty\"",
		"type NavDetails struct",
		// gofmt column-aligns struct tags, so name and tag are asserted apart.
		"DemoCompCd", "DemoSchemeDesc", "db:\"DEMO_COMP_CD\"", "db:\"DEMO_SCHEME_DESC\"",
		"type NavHistoryDetail struct",
		"CFromDate", "CToDate", "db:\"TUXC_NAV_Q1_1\"", "db:\"TUXC_NAV_Q1_2\"",
		"type DemoActive struct",
		"CActiveFlag", "db:\"TUXC_",
		"import \"database/sql\"",
	} {
		if !strings.Contains(models, want) {
			t.Errorf("models file missing %q\n%s", want, models)
		}
	}
	again, err := s.ModelFile(p)
	if err != nil || models != again {
		t.Error("models derivation is not deterministic")
	}
}

// TestModelFileDigitLeadingNames pins the leading-digit escape end to end:
// FML fields, row host vars and mapping row pins that start with a digit
// (FML_1ST_AMT, sql_1qty, "1stRow") render as X-prefixed exported names and
// pass the goast.Emit parse gate instead of failing the whole run.
func TestModelFileDigitLeadingNames(t *testing.T) {
	s, p, _ := genNavFixture(t)
	if got := fieldFromFML("FML_1ST_AMT"); got != "X1stAmt" {
		t.Errorf("fieldFromFML = %q, want X1stAmt", got)
	}
	if c := s.conditionOf(s.Mapping.Endpoints[0]); c != nil {
		c.FmlOps = append(c.FmlOps, ir.FmlOp{Kind: ir.FmlGet, Field: "FML_1ST_AMT"})
	} else {
		t.Fatal("fixture endpoint has no condition")
	}
	pin := s.Mapping.DBMethods["cur_demo_hist"]
	pin.Row = "1stRow"
	s.Mapping.DBMethods["cur_demo_hist"] = pin
	if got := s.RowName("cur_demo_hist", "GetNavHistory"); got != "X1stRow" {
		t.Errorf("RowName pin = %q, want X1stRow", got)
	}
	q := s.Query("cur_demo_hist")
	q.RowShape[0] = "sql_1qty"
	fields, err := s.rowFields("cur_demo_hist", q)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].Name != "X1qty" || fields[0].DBTag != "1QTY" {
		t.Errorf("rowFields = %q/%q, want X1qty/1QTY", fields[0].Name, fields[0].DBTag)
	}
	out, err := s.ModelFile(p)
	if err != nil {
		t.Fatalf("ModelFile: %v", err)
	}
	for _, want := range []string{"type X1stRow struct", "X1qty", "db:\"1QTY\"", "X1stAmt", "json:\"FML_1ST_AMT\""} {
		if !strings.Contains(out, want) {
			t.Errorf("models missing %q\n%s", want, out)
		}
	}
	if _, ferr := goast.Emit("test: digit names", out); ferr != nil {
		t.Fatalf("rendered models fail the parse gate: %v\n%s", ferr, out)
	}
}

func TestGenDBMethodsAndInterface(t *testing.T) {
	s, p, fns := genNavFixture(t)
	file, err := s.DBMethodsFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func (g *store) GetNavDetails(c context.Context, compCd string) ([]*models.NavDetails, error)",
		"SelectContext(c, &navDetails, query, compCd)",
		"func (g *store) GetNavHistory(c context.Context, compCd string, schCd string, fromDate time.Time, toDate time.Time) ([]*models.NavHistoryDetail, error)",
		"func (g *store) GetCount(c context.Context, matchAccount string) (int64, error)",
		"GetContext(c, &count, query, matchAccount)",
		"func (g *store) IsDemoActive(c context.Context, cMtchAccnt string) (*models.DemoActive, error)",
		"\"database/sql\"",
		"\"time\"",
		"mutual-fund-be/pkg/logger",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("db methods file missing %q", want)
		}
	}
	if strings.Count(file, "func (g *store)") != 7 {
		t.Errorf("db methods = %d, want 7", strings.Count(file, "func (g *store)"))
	}

	iface, err := s.DBInterface(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type NavStore interface",
		"GetNavDetails(c context.Context, compCd string) ([]*models.NavDetails, error)",
		"GetCount(c context.Context, matchAccount string) (int64, error)",
		"func NewNavStore(db *sqlx.DB) NavStore",
	} {
		if !strings.Contains(iface, want) {
			t.Errorf("db interface missing %q\n%s", want, iface)
		}
	}
	// Default shape is sqlx-only — the gorm handle is the opt-in variant.
	if strings.Contains(iface, "gorm") {
		t.Errorf("default db interface must be sqlx-only:\n%s", iface)
	}

	// The db.withGorm variant carries the legacy handle (nav-example shape).
	g, err := NewService(Options{Plan: p, Main: s.Main, FnFiles: fns, WithGorm: true})
	if err != nil {
		t.Fatal(err)
	}
	gormIface, err := g.DBInterface(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func NewNavStore(oracle *gorm.DB, db *sqlx.DB) NavStore",
		"oracle *gorm.DB",
	} {
		if !strings.Contains(gormIface, want) {
			t.Errorf("gorm variant missing %q\n%s", want, gormIface)
		}
	}
}

// TestGenMergeDBMethod pins the MERGE conversion path end-to-end on the
// merge fixture: the MERGE unit renders through db_method_merge (DML
// contract, error-only signature) with deterministic fallback naming.
func TestGenMergeDBMethod(t *testing.T) {
	files, err := ir.ExtractDir("../../testdata/merge")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("merge fixture extraction = %d files, want 1", len(files))
	}
	main := files[0]

	m := &plan.Mapping{
		Service:    "demomerge",
		Module:     "mutual-fund-be/pkg/services/demomerge",
		ReadDBs:    []string{"MF"},
		RouteGroup: "/demomerge",
		// The MERGE lives in the default branch (condition 2); no method
		// pin — the deterministic fallback naming applies.
		Endpoints: []plan.Endpoint{
			{Condition: 2, Name: "MergeAccount", Route: "/demo_merge"},
		},
		DBMethods: map[string]plan.MethodPin{},
	}
	p, err := plan.Build(plan.Options{Main: main, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(Options{Plan: p, Main: main})
	if err != nil {
		t.Fatal(err)
	}

	var mergeUnit *plan.Unit
	for i := range p.Units {
		u := &p.Units[i]
		if u.Kind == plan.KindDBMethod && len(u.QueryIDs) > 0 {
			mergeUnit = u
		}
	}
	if mergeUnit == nil {
		t.Fatalf("no db method unit in the merge plan:\n%+v", p.Units)
	}
	if mergeUnit.TemplateID != "db_method_merge" {
		t.Errorf("unit template = %s, want db_method_merge", mergeUnit.TemplateID)
	}
	if mergeUnit.Name != "MergeDemoAccounts" {
		t.Errorf("deterministic merge method name = %s, want MergeDemoAccounts", mergeUnit.Name)
	}
	// A2.6: the fallback row name strips the Merge verb too — before the
	// fix, RowName kept the verb and the struct would have been
	// "MergeDemoAccounts" (a method-shaped name in models).
	if got := s.RowName(mergeUnit.QueryIDs[0], mergeUnit.Name); got != "DemoAccounts" {
		t.Errorf("merge row name = %s, want DemoAccounts (verb stripped)", got)
	}

	body, sig, needsSQL, err := s.DBMethod(*mergeUnit)
	if err != nil {
		t.Fatalf("DBMethod(merge) failed: %v", err)
	}
	if needsSQL {
		t.Error("merge must not need the database/sql scan import (DML contract)")
	}
	if sig != "MergeDemoAccounts(c context.Context, accountId string, balance string) error" {
		t.Errorf("merge signature = %q", sig)
	}
	for _, want := range []string{
		"func (g *store) MergeDemoAccounts(c context.Context, accountId string, balance string) error {",
		"g.db.ExecContext(c, query, accountId, balance)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("merge body missing %q\n---\n%s", want, body)
		}
	}
}

func TestGenAccumulateDBInterface(t *testing.T) {
	s, p, _ := genNavFixture(t)
	path := filepath.Join(t.TempDir(), "db", "interface.go")
	n := 0
	for _, u := range p.Units {
		if u.Kind != plan.KindDBMethod {
			continue
		}
		_, sig, _, err := s.DBMethod(u)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AccumulateDBInterface(path, sig); err != nil {
			t.Fatal(err)
		}
		n++
	}
	// Re-accumulate — idempotent, converged.
	for _, u := range p.Units {
		if u.Kind != plan.KindDBMethod {
			continue
		}
		_, sig, _, _ := s.DBMethod(u)
		if err := s.AccumulateDBInterface(path, sig); err != nil {
			t.Fatal(err)
		}
	}
	sigs, err := goastInspect(path, "NavStore")
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != n {
		t.Errorf("accumulated %d signatures, want %d", len(sigs), n)
	}
}

func TestGenControllerHandlerRouter(t *testing.T) {
	s, p, _ := genNavFixture(t)

	ctrl, err := s.ControllerInterface(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type NavController interface",
		"NavHistory(ctx context.Context, request *models.NavHistoryRequest) (data []*models.NavHistoryResponse, err error)",
		"func NewNavController(store db.NavStore) NavController",
		"store db.NavStore",
	} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("controller interface missing %q\n%s", want, ctrl)
		}
	}

	hiface, err := s.HandlerInterface(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type NavHandler interface",
		"NavHistory(c *gin.Context)",
		"func NavController(repo repo.DataObject) controller.NavController",
		"db.NewNavStore(repo.Databases.ReadDatabase.EBATEST, repo.Databases.ReadDatabase.MF)",
	} {
		if !strings.Contains(hiface, want) {
			t.Errorf("handler interface missing %q\n%s", want, hiface)
		}
	}

	handlers, err := s.HandlerMethodsFile()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func (f *navHandler) NavHistory(c *gin.Context)",
		"var request models.NavHistoryRequest",
		"gCtx.BadRequestJSON(err, request)",
		"gCtx.SuccessJSON(data)",
	} {
		if !strings.Contains(handlers, want) {
			t.Errorf("handler methods missing %q", want)
		}
	}

	router, err := s.Router()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"nav := v1.Group(\"/nav\")",
		"nav.POST(\"/mfnavhistory\", obj.NavHistory)",
		"nav.POST(\"/mfnavschemelist\", obj.NavList)",
	} {
		if !strings.Contains(router, want) {
			t.Errorf("router snippet missing %q", want)
		}
	}
}

// TestGenGateGoldenFiles is the Phase 5 deterministic-generation gate: every
// artifact renders deterministically (byte-identical re-runs).
func TestGenGateGoldenFiles(t *testing.T) {
	s, p, _ := genNavFixture(t)
	type artifact struct {
		name string
		fn   func() (string, error)
	}
	arts := []artifact{
		{"models", func() (string, error) { return s.ModelFile(p) }},
		{"db-methods", func() (string, error) { return s.DBMethodsFile(p) }},
		{"db-interface", func() (string, error) { return s.DBInterface(p) }},
		{"controller-interface", func() (string, error) { return s.ControllerInterface(p) }},
		{"handler-interface", func() (string, error) { return s.HandlerInterface(p) }},
		{"handler-methods", func() (string, error) { return s.HandlerMethodsFile() }},
		{"router", func() (string, error) { return s.Router() }},
	}
	for _, a := range arts {
		first, err := a.fn()
		if err != nil {
			t.Fatalf("%s: %v", a.name, err)
		}
		second, err := a.fn()
		if err != nil {
			t.Fatalf("%s rerun: %v", a.name, err)
		}
		if first != second {
			t.Errorf("%s generation is not deterministic", a.name)
		}
		if strings.TrimSpace(first) == "" {
			t.Errorf("%s rendered empty", a.name)
		}
	}
}

func goastInspect(path, iface string) ([]string, error) {
	sigs, err := goast.InspectInterface(path, iface)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(sigs))
	for i, s := range sigs {
		out[i] = s.Text
	}
	return out, nil
}

// TestGenControllerPromptContext pins the fixed contract the controller
// prompt consumes: exact signature (named returns), compact request/
// response/row name lists, plus the response-shaping seam (the FETCH/Fadd
// loop the SQL replacement elides: store I/O, row→response map, arg
// provenance, data init).
func TestGenControllerPromptContext(t *testing.T) {
	s, p, _ := genNavFixture(t)
	ctx, err := s.ControllerPromptContext("SipFreedem", p, []string{"GetCount", "IsDemoActive", "GetSipFreedem"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func (s *navController) SipFreedem(c context.Context, request *models.SipFreedemRequest) (data []*models.SipFreedemResponse, err error)",
		"request SipFreedemRequest (all string):",
		"CompCd [FML_COMP_CD]",
		"response SipFreedemResponse (all string):",
		"row SipFreedemDetail (all sql.NullString, read row.X.String):",
		"row DemoActive (all sql.NullString, read row.X.String):",
		"Response shaping",
		"s.store.GetCount(c, matchAccount string) returns int64 scalar",
		"s.store.GetSipFreedem(c, sqlDemoCompCd string, cDemoEnableFlg string) returns []*models.SipFreedemDetail",
		"CompCd ← DemoCompCd.String",
		"Rating ← DemoRating.String",
		"Label ← StrDesc.String",
		"matchAccount ← request.Account",
		"data = make([]*models.SipFreedemResponse, 0)",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("prompt context missing %q:\n%s", want, ctx)
		}
	}
	// Compact means no struct dumps or tags: the body never emits them.
	for _, absent := range []string{"type SipFreedemDetail struct", "type SipFreedemRequest struct", "db:\"", "binding:\""} {
		if strings.Contains(ctx, absent) {
			t.Errorf("prompt context carries bloat %q:\n%s", absent, ctx)
		}
	}
	// No phantom placeholder names: the shaping block must not name a
	// loop target the view never declares (the model copies it verbatim
	// and the gate rejects `undefined identifier`).
	if strings.Contains(ctx, "range result") {
		t.Errorf("prompt context invents loop target `result`:\n%s", ctx)
	}
	// Only the endpoint's own rows: other units' structs stay out (the
	// shaping block names the endpoint's own store methods only).
	for _, absent := range []string{"row NavHistoryDetail", "row NavDetails", "NavListRequest", "GetNavHistory", "GetDateDetails"} {
		if strings.Contains(ctx, absent) {
			t.Errorf("prompt context leaks %q:\n%s", absent, ctx)
		}
	}
}

// TestTPCallStubNamesTargetFile pins the tpcall-resolution surfacing
// (PRD-2026-09-10): the placeholder stub and the prompt signature both
// name the corpus file(s) behind the called service when the IR resolved
// them, and stay silent when unresolved.
func TestTPCallStubNamesTargetFile(t *testing.T) {
	s := &Service{}
	tp := &ir.TPCall{
		Service:     "SVC_TARGET",
		ServiceFile: "/corpus/SVC_TARGET.pc",
		SendFML:     []ir.FmlOp{{Kind: ir.FmlAdd, Field: "FML_A", Target: "a"}},
		RecvFML:     []ir.FmlOp{{Kind: "get", Field: "FML_B", Target: "b"}},
	}
	u := plan.Unit{ID: "u01", Kind: plan.KindTPCall, Name: "TPCallTarget", TP: tp}
	stub := s.tpcallStub(u)
	for _, want := range []string{
		"// tuxgo:TODO tp:SVC_TARGET",
		"// target: /corpus/SVC_TARGET.pc (the corpus file(s) behind service SVC_TARGET)",
		"send: FML_A",
	} {
		if !strings.Contains(stub, want) {
			t.Errorf("stub missing %q:\n%s", want, stub)
		}
	}

	// Unresolved service: no target line — honest emptiness.
	tp.ServiceFile = ""
	if strings.Contains(s.tpcallStub(u), "// target:") {
		t.Error("unresolved service must not render a target line")
	}

	// The prompt signature carries the target too.
	tp.ServiceFile = "/corpus/SVC_TARGET.pc"
	tp.StartLine, tp.EndLine = 10, 20
	c := &ir.Condition{Index: 1, StartLine: 5, EndLine: 30}
	sigs := s.PlaceholderSignatures(c, &plan.Plan{Units: []plan.Unit{u}})
	if len(sigs) != 1 || !strings.Contains(sigs[0], "target corpus file: /corpus/SVC_TARGET.pc") {
		t.Errorf("signature = %v, want the target file suffix", sigs)
	}
}

// TestRunMocksSkipsMissingSources pins the best-effort contract: targets
// whose interface file does not exist are skipped silently — no file
// created, no failure — without invoking any mock backend.
func TestRunMocksSkipsMissingSources(t *testing.T) {
	dir := t.TempDir()
	RunMocks(context.Background(), []MockTarget{
		{Source: filepath.Join(dir, "db", "interface.go"), Dest: filepath.Join(dir, "db", "mock_store.go"), Name: "NavStore"},
	})
	if _, err := os.Stat(filepath.Join(dir, "db", "mock_store.go")); !os.IsNotExist(err) {
		t.Errorf("mock written for a missing source")
	}
}

// TestMockgenCmd pins the one-command contract: the local binary and the
// pinned go-run fallback carry identical generation flags.
func TestMockgenCmd(t *testing.T) {
	tgt := MockTarget{Source: "db/interface.go", Dest: "db/mock_store.go", Name: "NavStore"}
	bin := mockgenCmd("/usr/local/bin/mockgen", false, tgt)
	if bin.Path != "/usr/local/bin/mockgen" {
		t.Errorf("binary path = %q", bin.Path)
	}
	fallback := mockgenCmd("", true, tgt)
	if fallback.Path != "go" && !strings.HasSuffix(fallback.Path, "/go") {
		t.Errorf("fallback runs %q, want the go toolchain", fallback.Path)
	}
	want := []string{"run", "go.uber.org/mock/mockgen@" + mockgenVersion,
		"-source", "db/interface.go", "-destination", "db/mock_store.go", "-package", "db", "NavStore"}
	got := fallback.Args[1:]
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("fallback args = %v, want %v", got, want)
	}
	if strings.Join(bin.Args[1:], "\x00") != strings.Join(want[2:], "\x00") {
		t.Errorf("binary args = %v, want %v", bin.Args[1:], want[2:])
	}
}
