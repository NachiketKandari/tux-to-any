package convert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/audit"
	"tux-to-any/internal/budget"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/gen"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/ledger"
	"tux-to-any/internal/llm"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/validate"
)

const fakeBody = "\tlogger.Log(c).Debug(\"converted\")\n\treturn nil, err\n"

// contractClient wraps the scripted fake so canned bodies satisfy the
// required-call contract (BP hardening parity with batchpy): any store call
// the prompt's branch view shows but the canned body omits is appended as a
// checked no-op assignment — Tier A only parses, so the shape stays valid.
type contractClient struct{ inner llm.Client }

func (c contractClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.Response, error) {
	resp, err := c.inner.Chat(ctx, req)
	if err != nil {
		return resp, err
	}
	prompt := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			prompt += "\n" + m.Content
		}
	}
	extra := ""
	for _, call := range requiredCalls(prompt, "s.store.") {
		if !strings.Contains(resp.Content, call+"(") {
			extra += "\n\tif _, cerr := " + call + "(c); cerr != nil {\n\t\treturn nil, cerr\n\t}"
		}
	}
	resp.Content += extra
	return resp, nil
}

func (c contractClient) Stream(ctx context.Context, req llm.ChatRequest, onDelta func(string) error) (llm.Response, error) {
	return c.inner.Stream(ctx, req, onDelta)
}

func convertFixture(t *testing.T) (Options, *llm.FakeServer) {
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

	fake := llm.NewFakeServer(llm.FakeResponse{Content: fakeBody})
	t.Cleanup(fake.Close)

	base := t.TempDir()
	ledgerDir := t.TempDir()
	led, err := ledger.Load(ledgerDir, "nav")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := audit.New(t.TempDir(), "test-run")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Plan: p, Main: main, Source: string(src), FnFiles: fns,
		Client: contractClient{llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: fake.URL, Temperature: 0.1})},
		Budget: budget.New(12000, 4000, 4), BaseDir: base,
		Ledger: led, Validator: validate.New(validate.Options{}), MaxRetries: 2, Audit: rec,
	}
	return opts, fake
}

// TestConvertGateEndToEnd is the Phase 5 orchestration gate: the nav
// fixtures convert end-to-end against the fake LLM — deterministic files
// land, controller prompts never contain raw SQL, blocking is visible, the
// ledger resumes (a second run makes zero LLM calls).
func TestConvertGateEndToEnd(t *testing.T) {
	opts, fake := convertFixture(t)
	base := opts.BaseDir

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	// Deterministic artifacts exist and parse.
	for _, rel := range []string{
		"pkg/services/nav/models/nav.go",
		"pkg/services/nav/db/nav.go",
		"pkg/services/nav/db/interface.go",
		"pkg/services/nav/controller/interface.go",
		"pkg/services/nav/controller/fnstubs.go",
		"pkg/services/nav/controller/nav.go",
		"pkg/services/nav/handler/interface.go",
		"pkg/services/nav/handler/nav.go",
		"pkg/services/nav/handler/router_snippet.txt",
	} {
		if _, err := os.Stat(filepath.Join(base, rel)); err != nil {
			t.Errorf("missing artifact %s", rel)
		}
	}
	// The db interface accumulated every method (7 units).
	iface, _ := os.ReadFile(filepath.Join(base, "pkg/services/nav/db/interface.go"))
	if got := strings.Count(string(iface), "\tGet") + strings.Count(string(iface), "\tIs"); got != 7 {
		t.Errorf("db interface accumulated %d methods, want 7\n%s", got, iface)
	}

	// LLM: all four controller bodies (fn_long_to_int rides the stub), and
	// no raw SQL ever reached the prompts — plus the one best-effort stub
	// synthesis attempt, which the canned body fails (panic stub kept).
	if fake.RequestCount() != 5 {
		t.Errorf("llm calls = %d, want 5 (4 controllers + 1 stub synthesis)", fake.RequestCount())
	}
	for i, req := range fake.Requests {
		prompt := promptOf(t, req)
		if strings.Contains(prompt, "Unresolved legacy helper") {
			continue // stub synthesis seam — own contract, checked below
		}
		// The branch view legitimately keeps non-query EXEC constructs
		// (COMMIT/ROLLBACK tx markers) and dead SQL inside C comments
		// (the demo commented block targets :i_cnt_demos — never extracted); the
		// contract is that no LIVE query SQL leaks. Fragments are bind-
		// specific raw lines from the extracted regions.
		for _, frag := range []string{
			"INTO   :cnt_demo",                 // q3/q5 site
			"FROM   DEMO_ACCOUNT_MAP",          // q3/q5 site
			"DECLARE cur_demo_hist CURSOR",     // cursor q2
			"FROM   DEMO_PRICE_HIST",           // cursor q2
			"into :c_from_date",                // q1 dual select
			"DECLARE cur_demo_list CURSOR",     // cursor q7
			"DECLARE cur_demo_featured CURSOR", // cursor q4
			"DECLARE cur_demo_insured CURSOR",  // cursor q6
		} {
			if strings.Contains(prompt, frag) {
				t.Errorf("prompt %d leaked raw SQL (%q)", i, frag)
			}
		}
		if !strings.Contains(prompt, "s.store.Get") {
			t.Errorf("prompt %d missing the store contract", i)
		}
		// Resolved legacy fns in the view must be mapped to their store
		// methods — the model never guesses a substitute symbol.
		if strings.Contains(prompt, "fn_is_demo_active(") && !strings.Contains(prompt, "fn_is_demo_active(...) → s.store.IsDemoActive(") {
			t.Errorf("prompt %d missing the legacy-helper mapping for fn_is_demo_active\n---\n%s", i, prompt)
		}
	}

	// Ledger: everything appended — no blocked units under the stub
	// policy, the unresolved fn carries a panicking stub instead.
	appended, failed, blocked, _, placeholders, deviated := opts.Ledger.Counts()
	if appended < 10 || failed != 0 || blocked != 0 || placeholders != 0 || deviated != 0 {
		t.Errorf("ledger = appended %d, failed %d, blocked %d, placeholders %d, deviated %d", appended, failed, blocked, placeholders, deviated)
	}
	if len(res.Stubs) != 1 || !strings.HasPrefix(res.Stubs[0], "fn_long_to_int") || !strings.Contains(res.Stubs[0], "NavList") {
		t.Errorf("stubs = %v, want fn_long_to_int → NavList", res.Stubs)
	}

	// The controller file holds the four converted methods, and the stub
	// file pins the panicking placeholder for the unresolved fn.
	ctrl, _ := os.ReadFile(filepath.Join(base, "pkg/services/nav/controller/nav.go"))
	for _, m := range []string{"NavHistory", "SipFreedem", "SipInsurance", "NavList"} {
		if !strings.Contains(string(ctrl), "func (s *navController) "+m+"(") {
			t.Errorf("controller file missing method %s", m)
		}
	}
	stubSrc, _ := os.ReadFile(filepath.Join(base, "pkg/services/nav/controller/fnstubs.go"))
	for _, want := range []string{"func fnLongToInt(args ...any) int", `panic("tuxgo: fn_long_to_int is undefined in the source corpus`} {
		if !strings.Contains(string(stubSrc), want) {
			t.Errorf("fnstubs.go missing %q\n---\n%s", want, stubSrc)
		}
	}
	// The canned stub body fails the synthesis gate, so the summary marks
	// the fallback visibly while the file keeps the panicking stub.
	if !strings.Contains(res.Stubs[0], "stubbed:") {
		t.Errorf("stubs = %v, want the synthesis-fallback mark", res.Stubs)
	}
	// The NavList prompt must tell the model about the stub it can call.
	var navListPrompt string
	for _, req := range fake.Requests {
		if p := promptOf(t, req); strings.Contains(p, "Endpoint: NavList") {
			navListPrompt = p
			break
		}
	}
	if navListPrompt == "" {
		t.Fatal("no prompt requested the NavList body")
	}
	if !strings.Contains(navListPrompt, "Stubbed helpers") || !strings.Contains(navListPrompt, "fnLongToInt(args ...any) int") {
		t.Errorf("NavList prompt missing the stubbed-helper contract\n---\n%s", navListPrompt)
	}
	// The stub call's view carries no session args and no strcpy prologue:
	// the model copies the call, so middleware-owned C names must not be
	// there to copy (the measured c_ServiceName/c_errmsg reject class).
	for _, absent := range []string{"c_ServiceName", "c_errmsg", "c_err_msg", "l_sssn_id", "strcpy("} {
		if strings.Contains(navListPrompt, absent) {
			t.Errorf("NavList prompt leaks middleware-owned identifier %q\n---\n%s", absent, navListPrompt)
		}
	}

	// Resume: a second run converts nothing new — zero LLM calls.
	res2, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res2.LLMCalls != 0 {
		t.Errorf("resume made %d llm calls, want 0", res2.LLMCalls)
	}

	// Tier B degrade: no target module → recorded reason, no failure.
	if res.TierB == nil || res.TierB.DegradeReason == "" {
		t.Errorf("tier B should record its degrade reason: %+v", res.TierB)
	}
}

// TestConvertRetryFeedsTrimmedErrors: a syntactically broken first response
// is rejected by Tier A and retried; the second attempt lands.
func TestConvertRetryFeedsTrimmedErrors(t *testing.T) {
	opts, fake := convertFixture(t)
	// Script: the stub synthesis attempt declines, the first controller
	// response is broken (unbalanced brace) and retried, then the good one
	// repeats (the fake repeats its last entry).
	fake.Reset(
		llm.FakeResponse{Content: "CANNOT_SYNTHESIZE: test decline"},
		llm.FakeResponse{Content: "\tfunc oops( {\n"},
		llm.FakeResponse{Content: fakeBody},
	)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.RequestCount() != 6 { // 1 stub decline + 4 endpoints, one needed a retry
		t.Errorf("llm calls = %d, want 6 (1 stub + 4 + 1 retry)", fake.RequestCount())
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed = %v, want none", res.Failed)
	}
}

// TestStubSynthesisLandsPureHelper: when the stub seam returns a gate-clean
// pure implementation, fnstubs.go carries the real body (no panic) and the
// summary marks it synthesized. Controllers still convert on the same run.
func TestStubSynthesisLandsPureHelper(t *testing.T) {
	opts, fake := convertFixture(t)
	const synthBody = "func fnLongToInt(lSizeof int64, iOut *int64, cErrmsg string) int {\n\tif lSizeof < 0 {\n\t\treturn -1\n\t}\n\t*iOut = lSizeof\n\t_ = cErrmsg\n\treturn 1\n}"
	fake.Reset(
		llm.FakeResponse{Content: synthBody},
		llm.FakeResponse{Content: fakeBody},
	)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.RequestCount() != 5 { // 1 stub synthesis + 4 controllers
		t.Errorf("llm calls = %d, want 5 (1 stub + 4 controllers)", fake.RequestCount())
	}
	stubSrc, _ := os.ReadFile(filepath.Join(opts.BaseDir, "pkg/services/nav/controller/fnstubs.go"))
	for _, want := range []string{"func fnLongToInt(lSizeof int64", ") int {", "*iOut = lSizeof"} {
		if !strings.Contains(string(stubSrc), want) {
			t.Errorf("fnstubs.go missing %q\n---\n%s", want, stubSrc)
		}
	}
	if strings.Contains(string(stubSrc), "panic(") {
		t.Errorf("fnstubs.go keeps a panic after synthesis\n---\n%s", stubSrc)
	}
	if len(res.Stubs) != 1 || !strings.Contains(res.Stubs[0], "synthesized") {
		t.Errorf("stubs = %v, want the synthesized mark", res.Stubs)
	}
	ctrl, _ := os.ReadFile(filepath.Join(opts.BaseDir, "pkg/services/nav/controller/nav.go"))
	for _, m := range []string{"NavHistory", "SipFreedem", "SipInsurance", "NavList"} {
		if !strings.Contains(string(ctrl), "func (s *navController) "+m+"(") {
			t.Errorf("controller file missing method %s", m)
		}
	}
}

// TestStubEvidenceInference pins the call-site evidence the synthesis seam
// shows the model for fn_long_to_int: raw lines, arg split, and the
// host-declaration-backed signature (long→int64, int→*int for &i_out,
// char[]→string).
func TestStubEvidenceInference(t *testing.T) {
	files, err := ir.ExtractDir("../../testdata/nav")
	if err != nil {
		t.Fatal(err)
	}
	var main *ir.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "SVC_DEMO_LIST.pc") {
			main = f
		}
	}
	src, err := os.ReadFile(main.Path)
	if err != nil {
		t.Fatal(err)
	}
	ev := collectStubEvidence(main, strings.Split(string(src), "\n"), hostTypeIndex(main), "fn_long_to_int")
	if len(ev.calls) != 1 {
		t.Fatalf("callsites = %d, want 1", len(ev.calls))
	}
	if got := ev.calls[0].text; !strings.Contains(got, "fn_long_to_int(l_sizeof,&i_out,c_errmsg)") {
		t.Errorf("callsite text = %q", got)
	}
	if len(ev.calls[0].args) != 3 {
		t.Fatalf("args = %v, want 3", ev.calls[0].args)
	}
	if !strings.Contains(ev.signature, "func fnLongToInt(") || !strings.HasSuffix(ev.signature, ") int") {
		t.Errorf("signature = %q", ev.signature)
	}
	for _, want := range []string{"int64", "*int", "string"} {
		if !strings.Contains(ev.signature, want) {
			t.Errorf("signature = %q, want %s", ev.signature, want)
		}
	}
	if !strings.Contains(ev.returnNote, "-1") {
		t.Errorf("return note = %q, want the -1 convention", ev.returnNote)
	}
}

// TestConvertConcurrentDBUnitsByteIdentical: workers>1 must produce the same
// bytes as workers=1 — the pool renders in parallel, the merge stays in unit
// order. Run under `go test -race` for the data-race check.
func TestConvertConcurrentDBUnitsByteIdentical(t *testing.T) {
	solo, _ := convertFixture(t)
	if _, err := Run(context.Background(), solo); err != nil {
		t.Fatal(err)
	}
	pool, _ := convertFixture(t)
	pool.Workers = 5
	if _, err := Run(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"pkg/services/nav/db/nav.go",
		"pkg/services/nav/db/interface.go",
		"pkg/services/nav/models/nav.go",
		"pkg/services/nav/controller/nav.go",
	} {
		a, err := os.ReadFile(filepath.Join(solo.BaseDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(pool.BaseDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs between workers=1 and workers=5", rel)
		}
	}
}

// TestConvertSkipLLM: the deterministic-only mode (run.llm: false) generates
// every deterministic artifact with zero LLM calls, marks pending controller
// units skipped — never failed — and a later LLM-enabled resume converts
// exactly those.
func TestConvertSkipLLM(t *testing.T) {
	opts, _ := convertFixture(t)
	opts.SkipLLM = true
	opts.Client = nil

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 0 {
		t.Errorf("skip-llm run made %d llm calls, want 0", res.LLMCalls)
	}
	for _, rel := range []string{
		"pkg/services/nav/models/nav.go",
		"pkg/services/nav/db/nav.go",
		"pkg/services/nav/db/interface.go",
		"pkg/services/nav/controller/interface.go",
		"pkg/services/nav/handler/router_snippet.txt",
	} {
		if _, err := os.Stat(filepath.Join(opts.BaseDir, rel)); err != nil {
			t.Errorf("missing artifact %s", rel)
		}
	}
	if len(res.Skipped) != 4 {
		t.Errorf("skipped = %v, want the 4 mapped endpoints", res.Skipped)
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed = %v, want none in skip-llm mode", res.Failed)
	}
	appended, failed, _, skipped, _, _ := opts.Ledger.Counts()
	if skipped != 4 || failed != 0 || appended < 10 {
		t.Errorf("ledger = appended %d, failed %d, skipped %d", appended, failed, skipped)
	}

	// Resume with the LLM enabled: the skipped units convert, nothing re-runs.
	opts2, _ := convertFixture(t)
	// Share the first run's ledger by pointing opts2 at the same one.
	opts2.Ledger = opts.Ledger
	opts2.BaseDir = opts.BaseDir
	res2, err := Run(context.Background(), opts2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.LLMCalls != 4 {
		t.Errorf("resume made %d llm calls, want 4 (only the skipped units)", res2.LLMCalls)
	}
	if len(res2.Skipped) != 0 || len(res2.Failed) != 0 {
		t.Errorf("resume skipped %v, failed %v, want none", res2.Skipped, res2.Failed)
	}
}

func promptOf(t *testing.T, req map[string]any) string {
	t.Helper()
	msgs, ok := req["messages"].([]any)
	if !ok {
		t.Fatal("request has no messages")
	}
	var sb strings.Builder
	for _, m := range msgs {
		mm := m.(map[string]any)
		sb.WriteString(mm["content"].(string))
		sb.WriteString("\n")
	}
	return sb.String()
}

// TestBuildPromptFlowDraft pins the FLW-D7 seam: the deterministic draft is
// an additive prompt section, present only when rendered — the legacy view,
// DB contract, and REQUIRED CALLS sections are unchanged either way.
func TestBuildPromptFlowDraft(t *testing.T) {
	view := budget.View{Source: "legacy C branch"}
	prompt := buildPrompt(view, "db contract", "struct contract", "NavHistory", "", nil, nil, nil, nil, nil)
	if strings.Contains(prompt, "Deterministic flow draft") {
		t.Error("empty draft must not add the flow section")
	}
	if !strings.Contains(prompt, "legacy C branch") {
		t.Error("legacy view missing from the prompt")
	}
	withDraft := buildPrompt(view, "db contract", "struct contract", "NavHistory", "\trows, err := s.store.GetNavHistory(c)", nil, nil, nil, nil, nil)
	if !strings.Contains(withDraft, "Deterministic flow draft") ||
		!strings.Contains(withDraft, "s.store.GetNavHistory(c)") {
		t.Errorf("draft section missing:\n%s", withDraft)
	}
	if !strings.Contains(withDraft, "keep the flow and every store call") {
		t.Error("draft instruction line missing")
	}
}

// TestBuildPromptLegacyFacts pins the defines-pass prompt sections
// (PRD-2026-09-10 DEF-4): constants and error codes are deterministic
// additive sections, absent when the span carries none.
func TestBuildPromptLegacyFacts(t *testing.T) {
	view := budget.View{Source: "legacy C branch"}
	prompt := buildPrompt(view, "db contract", "struct contract", "NavHistory", "", nil, nil, nil, nil, nil)
	if strings.Contains(prompt, "Legacy constants") || strings.Contains(prompt, "Legacy error codes") {
		t.Error("empty facts must not add the sections")
	}
	withFacts := buildPrompt(view, "db contract", "struct contract", "NavHistory", "", nil, nil,
		[]string{"BUF_LEN = 6144", "DEMO_OUT_FML = 6"}, []string{"S31005", "S31010"}, nil)
	if !strings.Contains(prompt, "legacy C branch") {
		t.Error("baseline prompt broken")
	}
	for _, want := range []string{
		"Legacy constants (use literal values directly)",
		"  - BUF_LEN = 6144",
		"  - DEMO_OUT_FML = 6",
		"Legacy error codes (retain in returned error text)",
		"S31005, S31010",
	} {
		if !strings.Contains(withFacts, want) {
			t.Errorf("prompt missing %q:\n%s", want, withFacts)
		}
	}
}

// TestPromptFactsShared pins the extraction: the chunk and whole-view
// builders must emit the identical fact sections (DB contract, constants,
// error codes, stubs, signature) — only the stance words differ.
func TestPromptFactsShared(t *testing.T) {
	view := budget.View{Source: "s.store.GetNavHistory(c)\n"}
	stubs := []plan.Stub{{Fn: "fn_long_to_int"}}
	helpers := []string{"fn_is_demo_active(...) → s.store.IsDemoActive(...)"}
	constants := []string{"BUF_LEN = 6144"}
	codes := []string{"S31005"}

	whole := buildPrompt(view, "db contract", "struct contract", "NavHistory", "", stubs, helpers, constants, codes, nil)
	chunk := buildChunkPrompt(chunkCtx{unit: plan.Unit{Name: "NavHistory"}}, 0, 1, view.Source,
		"db contract", "struct contract", helpers, constants, codes, stubs, nil)

	for _, block := range []string{
		"DB layer contract (call these; never write SQL):\ndb contract\n\n",
		"Legacy constants (use literal values directly):\n  - BUF_LEN = 6144\n\n",
		"Legacy error codes (retain in returned error text): S31005\n\n",
		"Stubbed helpers (generated package-level stubs, variadic args, int return): call the RIGHT stub per legacy fn, passing only declared identifiers (declare zero-value locals for C-only names; out-pointers become &local):\n  - fn_long_to_int(...) → fnLongToInt(args ...any) int\n\n",
		"Signature + structs (exact names; types noted once):\nstruct contract\n\n",
	} {
		if !strings.Contains(whole, block) {
			t.Errorf("whole-view prompt missing shared block %q:\n%s", block, whole)
		}
		if !strings.Contains(chunk, block) {
			t.Errorf("fragment prompt missing shared block %q:\n%s", block, chunk)
		}
	}
	if !strings.Contains(whole, "Legacy helpers in the view — never substitute") ||
		!strings.Contains(chunk, "Legacy helpers in the fragment — never substitute") {
		t.Error("fragment/view scoping wording lost")
	}
}

const scenarioViewSrc = `void SVC_SV(TPSVCINFO *rqst) {
	char trn_cd;
	if (Fget32(ibuf, FML_TRANS_CD, 0, (char *)sql_trn_cd.arr, 0) == -1) {
		Fadd32(ibuf, FML_ERR_MSG, msg, 0);
		tpreturn(TPFAIL, 0L, ibuf, 0L, 0);
	}
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	i_ch = tpbegin(60, 0);
	if (trn_cd == 'A') {
		EXEC SQL INSERT INTO T VALUES (:a);
	}
	tpcommit(0);
	if (trn_cd == 'P') {
		Fadd32(obuf, FML_P_OUT, (char *)&p, 0);
	}
	if (trn_cd == 'A') {
		Fadd32(obuf, FML_A_OUT, (char *)&a, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`

// TestScenarioViewReplacesSQLNoLeak pins the scenario prompt base (G-SCEN6):
// the flattened slice carries no raw SQL (the insert's region is replaced
// by its store call), dropped branches stay out, and the tx-flagged call
// carries the tx handle.
func TestScenarioViewReplacesSQLNoLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_SV.pc")
	if err := os.WriteFile(path, []byte(scenarioViewSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{
		Service: "svv", Module: "app/pkg/services/svv",
		Endpoints: []plan.Endpoint{{ScenarioRef: "trn_cd=A", Name: "TransA", Route: "/a"}},
	}
	p, err := plan.Build(plan.Options{Main: main, Source: scenarioViewSrc, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := gen.NewService(gen.Options{Plan: p, Main: main, Source: scenarioViewSrc})
	if err != nil {
		t.Fatal(err)
	}
	tree := flowTreeOf(scenarioViewSrc, main)
	axis := tree.DispatchAxisFor([]byte(scenarioViewSrc))
	if axis == nil {
		t.Fatal("no axis")
	}
	sc := svc.ScenarioOf("TransA")
	if sc == nil {
		t.Fatal("scenario not resolved")
	}
	_, calls, err := svc.BranchCalls(svc.ConditionOf("TransA"), p)
	if err != nil {
		t.Fatal(err)
	}
	view, err := scenarioView(Options{Source: scenarioViewSrc}, svc, sc, tree, calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(view.Source), "INSERT INTO") {
		t.Errorf("raw SQL leaked into the scenario view:\n%s", view.Source)
	}
	if !strings.Contains(view.Source, "s.store.") {
		t.Errorf("no store call in the view:\n%s", view.Source)
	}
	if !strings.Contains(view.Source, ", tx,") {
		t.Errorf("tx-flagged DML must carry the tx handle in the view:\n%s", view.Source)
	}
	if strings.Contains(view.Source, "trn_cd == 'P'") {
		t.Errorf("dropped P branch leaked into the A slice:\n%s", view.Source)
	}
	// The tx facts prompt section names the span and the wrap pattern.
	sp := scenPromptOf(sc, flow.DiffScenarios("SVC_SV", flow.Scenarios(tree, axis)))
	if len(sp.TxNotes) == 0 || !strings.Contains(strings.Join(sp.TxNotes, "\n"), "tpbegin") {
		t.Errorf("tx facts missing the span evidence: %v", sp.TxNotes)
	}
	if !strings.Contains(strings.Join(sp.TxNotes, "\n"), "utils.ExecTransaction") {
		t.Errorf("tx facts missing the wrap pattern: %v", sp.TxNotes)
	}
}

// TestDBSignaturesForPrefixedNames pins the fragment-path contract fix
// (audit 2026-09-16): the chunked path passes receiver-prefixed names
// (s.store.GetDateRange) while the single-call path passes bare names —
// both must resolve to the unit's signature, never an empty contract.
func TestDBSignaturesForPrefixedNames(t *testing.T) {
	p := &plan.Plan{Units: []plan.Unit{
		{ID: "u1", Kind: plan.KindDBMethod, Name: "GetDateRange", QueryIDs: []string{"q1"}},
	}}
	bodies := map[string]dbOut{"u1": {sig: "GetDateRange(c context.Context) (*models.DateRange, error)"}}
	for _, methods := range [][]string{
		{"GetDateRange"},
		{"s.store.GetDateRange"},
	} {
		got := dbSignaturesFor(p, bodies, methods)
		if !strings.Contains(got, "s.store.GetDateRange(c context.Context)") {
			t.Errorf("dbSignaturesFor(%v) = %q, want the store signature", methods, got)
		}
	}
	if got := bareStoreCall("s.store.FetchNavHistory"); got != "FetchNavHistory" {
		t.Errorf("bareStoreCall = %q, want FetchNavHistory", got)
	}
}

// TestStripDeadComments pins the dead-SQL elision (audit 2026-09-16): the
// ver-2.2 D2U SELECT rides a /* ... **/ block into views as live SQL —
// comment-only lines (including multi-line block regions) must go, code
// lines (even with trailing comments or /* inside strings) must stay.
func TestStripDeadComments(t *testing.T) {
	src := "int i;                           /* Loop counter */\n" +
		"/*Added in Ver 2.1*/\n" +
		"/* ver 2.2 **\n" +
		"EXEC SQL\n" +
		"SELECT COUNT(*) INTO :i_cnt FROM DUAL;\n" +
		"} **/\n" +
		"// line comment\n" +
		"EXEC SQL include \"table/mf_navs.h\";\n" +
		"s.store.GetDateRange(c)\n" +
		"userlog(\"x /* not a comment */\");\n"
	got := stripDeadComments(src)
	if strings.Contains(got, "SELECT COUNT") || strings.Contains(got, "ver 2.2") {
		t.Errorf("dead comment block survived:\n%s", got)
	}
	for _, want := range []string{"int i;", "EXEC SQL include", "s.store.GetDateRange(c)", "not a comment"} {
		if !strings.Contains(got, want) {
			t.Errorf("code line %q stripped:\n%s", want, got)
		}
	}
}

// TestStripLegacyScaffold pins the deterministic scaffold elision
// (micro-chunk step 1): pure Tuxedo/Pro*C scaffold lines never reach the
// model, while store calls, branches, and FML-mapping lines survive.
func TestStripLegacyScaffold(t *testing.T) {
	src := "FBFR32 *ptr_fml_Ibuffer;\n" +
		"EXEC SQL include \"table/mf_navs.h\";\n" +
		"MEMSET(sql_mf_nav_sch_cd);\n" +
		"SETNULL(sql_urf_usr_id);\n" +
		"SETLEN(sql_buf, 10);\n" +
		"/*L113*/ userlog(\"%s: debug\", c_ServiceName);\n" +
		"errlog(c_ServiceName, \"S31030\", TPMSG);\n" +
		"ptr = tpalloc(\"FML32\", nil, 3);\n" +
		"tpfree(ptr);\n" +
		"CLOSE cur_get_tblc_dtls;\n" +
		"INITDBGLVL(3);\n" +
		"s.store.GetDateRange(c)\n" +
		"if request.RqstTyp == \"S\" {\n" +
		"s.store.DeleteRpam(c, tx, request.PrtfloId)\n" +
		"}\n" +
		"Fadd32(ptr, FML_ERR_MSG, c_errmsg, 0);\n" +
		"Fget32(ptr, FML_USR_ID, c_user_id, 0);\n" +
		"tpreturn(TPSUCCESS, 0, ptr, 0, 0);\n" +
		"x = strcpy(dst, src);\n"
	got, dropped := stripLegacyScaffold(src)
	if dropped != 11 {
		t.Errorf("dropped = %d, want 11:\n%s", dropped, got)
	}
	for _, want := range []string{
		"s.store.GetDateRange(c)", "if request.RqstTyp", "s.store.DeleteRpam",
		"Fadd32(", "Fget32(", "tpreturn(", "strcpy(",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("intent line %q stripped:\n%s", want, got)
		}
	}
	for _, gone := range []string{
		"FBFR32", "EXEC SQL include", "MEMSET(", "SETNULL(", "SETLEN(",
		"userlog(", "errlog(", "tpalloc(", "tpfree(", "CLOSE cur_", "INITDBGLVL(",
	} {
		if strings.Contains(got, gone) {
			t.Errorf("scaffold %q survived:\n%s", gone, got)
		}
	}
}

// TestStripLegacyScaffoldDeclareSection pins the DECLARE-SECTION extension:
// host-var section markers are scaffold like INCLUDE and never reach the
// model, while live EXEC SQL (already store calls post-replacement) stays.
func TestStripLegacyScaffoldDeclareSection(t *testing.T) {
	src := "EXEC SQL BEGIN DECLARE SECTION;\n" +
		"varchar sql_usr_id[20];\n" +
		"EXEC SQL END DECLARE SECTION;\n" +
		"s.store.GetDetail(c)\n"
	got, dropped := stripLegacyScaffold(src)
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2 (both section markers):\n%s", dropped, got)
	}
	if !strings.Contains(got, "s.store.GetDetail(c)") || !strings.Contains(got, "varchar sql_usr_id") {
		t.Errorf("live lines stripped:\n%s", got)
	}
}

// TestStripPreludeDecls pins the prelude hoist: the leading run of
// context-only declarations drops, stripping stops at the first live line,
// initialized decls and calls survive, and store calls are never matched.
func TestStripPreludeDecls(t *testing.T) {
	src := "\n" +
		"/* preamble */\n" +
		"int i_cnt;\n" +
		"char c_user_id[19]; /* acc */\n" +
		"/*L10*/  long l_sssn_id;\n" +
		"if (a) {\n" +
		"  int inner;\n" +
		"  s.store.One(c);\n" +
		"}\n"
	got, dropped := stripPreludeDecls(src)
	if dropped != 5 {
		t.Errorf("dropped = %d, want 5 (blank, preamble, 3 decls):\n%s", dropped, got)
	}
	if !strings.HasPrefix(got, "if (a) {") {
		t.Errorf("strip did not stop at first live line:\n%s", got)
	}
	if !strings.Contains(got, "int inner;") || !strings.Contains(got, "s.store.One(c);") {
		t.Errorf("live body stripped:\n%s", got)
	}

	// Initialized declarations and call-shaped lines stop the strip.
	live := "int i_cnt = 0;\ns.store.One(c);\n"
	if got, dropped := stripPreludeDecls(live); dropped != 0 || got != live {
		t.Errorf("live-first view altered: dropped=%d got=%q", dropped, got)
	}
	// A view that is only prelude strips to empty without failing.
	if got, _ := stripPreludeDecls("int a;\nchar b[4];\n"); got != "" {
		t.Errorf("all-prelude view = %q, want empty", got)
	}
}

// TestControllerTuxedoGate pins the transliteration ban (audit 2026-09-16):
// the staged GetNavHistory-style body (s.tpalloc/s.errlog/s.Fadd32/
// s.tpreturn/EXEC SQL CLOSE/break) must fail, while a reference-style body
// (store calls + data shaping + return data, err) passes.
func TestControllerTuxedoGate(t *testing.T) {
	bad := "ptrFmlObuffer := s.tpalloc(\"FML32\", nil, 3)\n" +
		"s.errlog(c_ServiceName, \"S31030\", TPMSG, c_user_id, li_session_id, c_errmsg)\n" +
		"s.Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0)\n" +
		"s.tpreturn(TPFAIL, 0, (string)(ptr_fml_Ibuffer), 0, 0)\n" +
		"s.EXEC_SQL_CLOSE(cur_mf_nav_hist)\n" +
		"SETNULL(sql_mf_nav_sch_cd)\n" +
		"x := (string)(unsafe.Pointer(ptr))\n" +
		"if SQLCODE != 0 {\n\treturn nil, err\n}\n"
	errs := controllerTuxedoErrs(bad)
	if len(errs) == 0 {
		t.Errorf("transliterated body passed the tuxedo gate")
	}
	joined := strings.Join(errs, "; ")
	for _, want := range []string{"tpreturn", "Fadd32", "errlog"} {
		if !strings.Contains(strings.ToLower(joined), strings.ToLower(want)) {
			t.Errorf("gate notes %q miss %q", joined, want)
		}
	}
	good := "dateDetail, err := s.store.GetDateDetails(c)\n" +
		"if err != nil {\n\treturn nil, err\n}\n" +
		"for _, d := range result {\n\tdata = append(data, &models.NavHistoryResponse{CompCode: d.CompCd.String})\n}\n" +
		"return data, err\n"
	if errs := controllerTuxedoErrs(good); len(errs) != 0 {
		t.Errorf("reference-style body rejected: %v", errs)
	}
}

// TestCleanBodyNormalizesNullString pins the extraction fixup: two-level
// .String() (the model's NullString spelling) becomes the .String field,
// while single-level calls (e.g. time.Time.String()) pass through.
func TestCleanBodyNormalizesNullString(t *testing.T) {
	in := "data := make([]*models.R, 0)\nfor _, row := range rows {\n\tdata = append(data, &models.R{C: row.C.String(), D: d.Date.String()})\n}\nfrom := fromDate.String()\n"
	got := cleanBody(in)
	for _, want := range []string{"row.C.String,", "d.Date.String}", "fromDate.String()", "data = make([]*models.R, 0)"} {
		if !strings.Contains(got, want) {
			t.Errorf("cleanBody output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "data := make(") {
		t.Errorf("shadowed data init remains:\n%s", got)
	}
	if strings.Contains(got, ".String()") && !strings.Contains(got, "fromDate.String()") {
		t.Errorf("unexpected .String() remains:\n%s", got)
	}
}
