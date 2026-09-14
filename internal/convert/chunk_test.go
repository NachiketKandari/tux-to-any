package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/audit"
	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/ledger"
	"tux-to-any/internal/llm"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/validate"
)

const chunkView = `/* preamble (shared init) */
/*L10*/  int i_cnt;
/*L12*/  EXEC SQL INCLUDE "table/x.h";
/*L14*/  if(Fget32(buf,FIELD,0,(char*)&c_flag,0) == -1)
/*L15*/  {
/*L16*/    errlog("svc", "B1", FMLMSG);
/*L17*/    Fadd32(out,FIELD,(char*)&c_flag,0);
/*L18*/    tpreturn(TPFAIL, 0L, (char *)buf, 0L, 0);
/*L19*/  }
/*L20*/  else if(c_flag == 'H')
/*L21*/  {
/*L22*/    s.store.GetDetail(c);
/*L23*/  }
/*L24*/  else
/*L25*/  {
/*L26*/    userlog("Folio # is {%s}", msg);
/*L27*/  }
/*L28*/  if(c_bad)
/*L29*/    s.store.DropIt(c);
/*L30*/  while(i < 3)
/*L31*/    i_cnt = i_cnt + 1;
/*L32*/}`

func firstLine(s string) string {
	return strings.SplitN(s, "\n", 2)[0]
}

// bigFixture builds a convert run whose single endpoint's branch view is
// far larger than the budget ceiling — the deterministic trigger for the
// chunked controller path. promptCeiling drives the input trigger; outputCeiling
// the output-estimate trigger (2026-09-14). Returns the options, the fake
// server, and the audit root (exchanges land under <root>/bigrun/).
func bigFixture(t *testing.T, promptCeiling, outputCeiling int) (Options, *llm.FakeServer, string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("void SVC_BIG(TPSVCINFO *rqst)\n{\n    int i;\n    char c_flag;\n    if(Fget32(rqst,FML_COMP_CD,0,(char*)&c_flag,0) == -1)\n    {\n")
	sb.WriteString("        EXEC SQL SELECT COUNT(*) INTO :i_cnt FROM DUAL;\n")
	for i := 0; i < 180; i++ {
		fmt.Fprintf(&sb, "        int junk_%d = %d + 1;\n        if(junk_%d > 50)\n        {\n            c_flag = 'Y';\n        }\n        else\n        {\n            c_flag = 'N';\n        }\n", i, i, i)
	}
	sb.WriteString("        tpreturn(TPSUCCESS, 0L, (char*)rqst, 0L, 0);\n    }\n    else\n    {\n        tpreturn(TPFAIL, 1L, (char*)rqst, 0L, 0);\n    }\n}\n")
	src := sb.String()

	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_BIG.pc")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := ir.ExtractFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{
		Service: "big",
		Module:  "mutual-fund-be/pkg/services/big",
		Endpoints: []plan.Endpoint{
			{Condition: 1, Name: "BigBranch", Route: "/big"},
		},
		DBMethods: map[string]plan.MethodPin{
			"q1": {Name: "GetBigCount"},
		},
	}
	p, err := plan.Build(plan.Options{Main: main, Source: src, Mapping: m, Budget: budget.New(promptCeiling, outputCeiling, 4)})
	if err != nil {
		t.Fatal(err)
	}
	fake := llm.NewFakeServer(llm.FakeResponse{Content: fakeBody})
	t.Cleanup(fake.Close)
	base := t.TempDir()
	led, err := ledger.Load(t.TempDir(), "big")
	if err != nil {
		t.Fatal(err)
	}
	auditRoot := t.TempDir()
	rec, err := audit.New(auditRoot, "bigrun")
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Plan: p, Main: main, Source: src,
		Client: contractClient{llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: fake.URL, Temperature: 0.1})},
		Budget: budget.New(promptCeiling, outputCeiling, 4), BaseDir: base,
		Ledger: led, Validator: validate.New(validate.Options{}), MaxRetries: 2, Audit: rec,
	}, fake, auditRoot
}

// TestSplitStatements pins the chunker's boundary rules: `} else` chains
// stay glued to their if, unbraced headers glue their single body
// statement, braces inside string literals never shift the depth, comment
// lines never close a unit, and the entry's own closing brace is dropped.
func TestSplitStatements(t *testing.T) {
	units := splitStatements(chunkView)
	joined := strings.Join(units, "\n")
	want := chunkView[:strings.LastIndex(chunkView, "\n")] // minus the trailing method-brace line
	if joined != want {
		t.Errorf("split lost bytes:\n got %q\nwant %q", joined, want)
	}
	for _, u := range units {
		lt := strings.TrimSpace(u)
		if strings.HasPrefix(lt, "}") || strings.HasPrefix(lt, "else") {
			t.Errorf("unit starts mid-chain: %q", firstLine(lt))
		}
	}
	var glued bool
	for _, u := range units {
		if strings.Contains(u, "if(c_bad)") && strings.Contains(u, "s.store.DropIt(c)") {
			glued = true
		}
	}
	if !glued {
		t.Errorf("unbraced header split from its body:\n%v", units)
	}
	// No unit opens with a bare else-chain continuation; brace pads cover
	// fragments cut mid-block.
	var sawOpen, sawClose bool
	for _, u := range units {
		if strings.Contains(u, "if(Fget32") {
			sawOpen = true
		}
		if strings.Contains(u, "Folio # is {%s}") {
			sawClose = true
		}
	}
	if !sawOpen || !sawClose {
		t.Errorf("chain statements missing from units")
	}
	if p, s := bracePads("s.store.GetDetail(c);\n}"); p != 1 || s != 0 {
		t.Errorf("bracePads(closing fragment) = %d, %d; want 1, 0", p, s)
	}
	if p, s := bracePads("if (x) {\n  stmt();"); p != 0 || s != 1 {
		t.Errorf("bracePads(open fragment) = %d, %d; want 0, 1", p, s)
	}
	if p, s := bracePads("if (x) {\n  stmt();\n}"); p != 0 || s != 0 {
		t.Errorf("bracePads(balanced fragment) = %d, %d; want 0, 0", p, s)
	}
}

func TestGroupFragmentsBudget(t *testing.T) {
	units := splitStatements(chunkView)
	chunks := groupFragments(units, 220, func(string) int { return 0 })
	if len(chunks) < 2 {
		t.Fatalf("budget 220 produced %d chunks, want several", len(chunks))
	}
	var re string
	for i, c := range chunks {
		if i > 0 {
			re += "\n"
		}
		re += c
	}
	if want := chunkView[:strings.LastIndex(chunkView, "\n")]; re != want {
		t.Errorf("chunks do not reassemble the view")
	}
	// A unit larger than the budget rides alone in its own chunk.
	one := "s.store.GetDetail(c);\ns.store.GetDetail2(c);\ns.store.GetDetail3(c);"
	outs := groupFragments([]string{one}, 10, func(string) int { return 0 })
	if len(outs) != 1 || outs[0] != one {
		t.Errorf("oversized unit split: %v", outs)
	}
}

func TestFragmentLocals(t *testing.T) {
	body := "\tx := FetchX(c)\n\ty, z := 1, 2\n\tfor i := range rows {\n\t}\n\tvar w int\n\t_ = w"
	if got := strings.Join(fragmentLocals(body), ","); got != "x,y,z,i,w" {
		t.Errorf("locals = %q, want x,y,z,i,w", got)
	}
}

// TestChunkedConvertEndToEnd pins the chunked path end-to-end: the oversized
// branch splits into fragments, one bounded seam call per fragment, the
// fragments combine into a parse-clean body, the ledger records the
// per-chunk attempts, and the audit trail keeps one exchange per chunk.
func TestChunkedConvertEndToEnd(t *testing.T) {
	opts, fake, auditRoot := bigFixture(t, 3000, 4000)
	base := opts.BaseDir

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("chunked run failed endpoints: %v", res.Failed)
	}
	if fake.RequestCount() < 4 {
		t.Fatalf("chunked run made %d llm calls, want ≥ 4 fragments", fake.RequestCount())
	}
	var fragmentPrompts int
	for _, req := range fake.Requests {
		prompt := promptOf(t, req)
		if strings.Contains(prompt, "Fragment 1 of") || strings.Contains(prompt, "Legacy fragment") {
			fragmentPrompts++
		}
		if strings.Contains(prompt, "FROM DUAL") {
			t.Errorf("raw SQL leaked into a fragment prompt")
		}
	}
	if fragmentPrompts == 0 {
		t.Errorf("no fragment prompts seen — the chunked path never engaged")
	}
	// The ledger recorded per-chunk attempts on the controller unit.
	cu := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	e := opts.Ledger.Get(cu.ID, string(cu.Kind), cu.Name)
	if e.Status != ledger.StatusAppended {
		t.Fatalf("BigBranch ledger status = %s, want appended", e.Status)
	}
	if e.Attempts < 4 {
		t.Errorf("BigBranch attempts = %d, want ≥ 4 chunks", e.Attempts)
	}
	// One audit exchange per chunk attempt, named per chunk.
	matches, _ := filepath.Glob(filepath.Join(auditRoot, "bigrun", "controller_method-BigBranch#chunk*-attempt0.json"))
	if len(matches) < 4 {
		t.Errorf("chunk audit exchanges = %d, want ≥ 4", len(matches))
	}
	if _, err := os.Stat(filepath.Join(base, "pkg/services/big/controller/big.go")); err != nil {
		t.Errorf("controller file missing: %v", err)
	}
}

// TestChunkedFragmentFailure pins the loud failure: a model that cannot
// produce a fragment fails the unit with the chunk index named — never a
// silent drop, never a partial body appended.
func TestChunkedFragmentFailure(t *testing.T) {
	opts, fake, _ := bigFixture(t, 3000, 4000)
	// The first response is fine (chunk 1); every later call is a transport
	// failure — the second chunk exhausts its retries and the unit fails.
	fake.Reset(llm.FakeResponse{Content: fakeBody}, llm.FakeResponse{Status: 500})
	opts.MaxRetries = 1

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) == 0 {
		t.Fatalf("broken fragments did not fail any endpoint")
	}
	u := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	e := opts.Ledger.Get(u.ID, string(u.Kind), u.Name)
	if e.Status != ledger.StatusFailed || !strings.Contains(e.Error, "fragment 2/") {
		t.Errorf("unit %s status=%s error=%q, want failed with fragment 2 named", u.Name, e.Status, e.Error)
	}
}

// TestOutputDrivenChunking pins the output-estimate trigger: the assembled
// prompt fits its ceiling, but the view's projected Go translation exceeds
// the output ceiling — the unit chunks instead of betting on one call that
// would truncate (finish_reason=length, gates reject, retries burn).
func TestOutputDrivenChunking(t *testing.T) {
	// Prompt ceiling 12000 (the ~28k-char view's ~7k-token prompt fits);
	// output ceiling 1500 (the ~7k-token view × 130% ≈ 9k estimate breaks it).
	opts, fake, auditRoot := bigFixture(t, 12000, 1500)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("output-chunked run failed endpoints: %v", res.Failed)
	}
	if fake.RequestCount() < 2 {
		t.Fatalf("output-driven run made %d llm calls, want ≥ 2 fragments", fake.RequestCount())
	}
	var fragmentPrompts int
	for _, req := range fake.Requests {
		p := promptOf(t, req)
		if strings.Contains(p, "Fragment 1 of") || strings.Contains(p, "Legacy fragment") {
			fragmentPrompts++
		}
		if strings.Contains(p, "FROM DUAL") {
			t.Errorf("raw SQL leaked into a fragment prompt")
		}
	}
	if fragmentPrompts == 0 {
		t.Errorf("no fragment prompts seen — the output-driven chunk path never engaged")
	}
	u := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	e := opts.Ledger.Get(u.ID, string(u.Kind), u.Name)
	if e.Status != ledger.StatusAppended {
		t.Fatalf("BigBranch ledger status = %s, want appended", e.Status)
	}
	matches, _ := filepath.Glob(filepath.Join(auditRoot, "bigrun", "controller_method-BigBranch#chunk*-attempt0.json"))
	if len(matches) < 2 {
		t.Errorf("chunk audit exchanges = %d, want ≥ 2", len(matches))
	}
}

// TestSingleCallWhenOutputFits is the negative control: same fixture, an
// output ceiling the estimate fits — the single-call path stays (exactly
// one llm call, no chunk exchanges).
func TestSingleCallWhenOutputFits(t *testing.T) {
	opts, fake, auditRoot := bigFixture(t, 12000, 30000)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("single-call run failed endpoints: %v", res.Failed)
	}
	if fake.RequestCount() != 1 {
		t.Fatalf("single-call run made %d llm calls, want 1", fake.RequestCount())
	}
	matches, _ := filepath.Glob(filepath.Join(auditRoot, "bigrun", "controller_method-BigBranch#chunk*-attempt0.json"))
	if len(matches) != 0 {
		t.Errorf("single-call run wrote %d chunk audit exchanges, want 0", len(matches))
	}
}

// TestOutputTokenEstimate pins the estimator arithmetic: chars→tokens at the
// configured ratio, then the translation expansion.
func TestOutputTokenEstimate(t *testing.T) {
	b := budget.New(0, 0, 4)
	if got := outputTokenEstimate(b, strings.Repeat("a", 4000)); got != 1300 {
		t.Errorf("outputTokenEstimate(4000 chars) = %d, want 1300", got)
	}
	if got := outputTokenEstimate(b, ""); got != 0 {
		t.Errorf("outputTokenEstimate(empty) = %d, want 0", got)
	}
}

// TestOutputChunkReason pins the margin trigger (2026-09-14 dense-C run): the
// 10.3k-char view's 3360-token estimate read below the 4000 ceiling but the
// provider truncated at exactly 4000 — the trigger fires at 80% of the
// ceiling, not 100%.
func TestOutputChunkReason(t *testing.T) {
	b := budget.New(0, 0, 4)
	view := strings.Repeat("a", 10333) // the dense-C F-branch view's char mass
	if r := outputChunkReason(b, view, 4000); r == "" {
		t.Errorf("outputChunkReason(view≈3360, ceiling 4000) = %q, want a trigger reason", r)
	}
	if r := outputChunkReason(b, strings.Repeat("a", 4000), 4000); r != "" {
		t.Errorf("outputChunkReason(view≈1300, ceiling 4000) = %q, want empty", r)
	}
	if r := outputChunkReason(b, view, 0); r != "" {
		t.Errorf("outputChunkReason with unset ceiling = %q, want empty", r)
	}
}

// TestDynamicBudgetSingleCall pins the yaml switch (run.tokenPolicy:
// dynamic): the same fixture that chunks under a small static output
// ceiling runs as ONE call when the model context is configured — the
// truncation the policy exists to prevent never engages, and the request
// carries the computed room.
func TestDynamicBudgetSingleCall(t *testing.T) {
	opts, fake, auditRoot := bigFixture(t, 12000, 1500)
	opts.Budget.ModelContextTokens = 40960
	opts.Budget.ModelMaxOutputTokens = 16384
	opts.Budget.OutputReserveTokens = 768

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("dynamic run failed endpoints: %v", res.Failed)
	}
	if fake.RequestCount() != 1 {
		t.Fatalf("dynamic run made %d llm calls, want 1 (no chunking under a roomy context)", fake.RequestCount())
	}
	maxTokens, ok := fake.Requests[0]["max_tokens"].(float64)
	if !ok || maxTokens != 16384 {
		t.Errorf("request max_tokens = %v, want the model completion cap 16384", fake.Requests[0]["max_tokens"])
	}
	matches, _ := filepath.Glob(filepath.Join(auditRoot, "bigrun", "controller_method-BigBranch#chunk*-attempt0.json"))
	if len(matches) != 0 {
		t.Errorf("dynamic run wrote %d chunk audit exchanges, want 0", len(matches))
	}
}

// TestTxGateErrs pins the transaction contract gate: a unit whose store
// calls take tx must wrap the flow in utils.ExecTransaction and call each
// tx-variant with the tx handle — string-level, deterministic notes.
func TestTxGateErrs(t *testing.T) {
	calls := map[string]budget.DBCall{
		"cur_x": {Receiver: "s.store", Name: "InsertOrder", CtxName: "c", Tx: "tx", Args: []string{"a"}},
		"q1":    {Receiver: "s.store", Name: "GetCount", CtxName: "c"},
	}
	// No tx calls on the unit → the gate is inert.
	if errs := txGateErrs("whatever", map[string]budget.DBCall{"q1": calls["q1"]}); errs != nil {
		t.Errorf("inert gate = %v, want nil", errs)
	}
	// Missing wrapper AND missing call form → both named.
	errs := txGateErrs("s.store.InsertOrder(c, a)", calls)
	if len(errs) != 2 {
		t.Fatalf("errs = %v, want 2 (no wrapper, call without tx)", errs)
	}
	if !strings.Contains(errs[0], "ExecTransaction") || !strings.Contains(errs[1], "InsertOrder") {
		t.Errorf("errs = %v, want the wrapper + call-form notes", errs)
	}
	// Wrapper + tx call form → clean.
	ok := "err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {\n" +
		"\ts.store.InsertOrder(c, tx, a)\n" +
		"\treturn nil\n})"
	if errs := txGateErrs(ok, calls); errs != nil {
		t.Errorf("valid tx body rejected: %v", errs)
	}
}
