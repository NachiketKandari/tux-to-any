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
// lines never close a unit, and a trailing real block close is kept.
func TestSplitStatements(t *testing.T) {
	units := splitStatements(chunkView)
	joined := strings.Join(units, "\n")
	want := chunkView
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

// TestTrailingRealBlockCloseKept pins the Phase 3 contract: a view whose
// last statement is a braced block keeps its closing `}` — scenario views
// never carry the entry's own brace lines, so trailing brace-only lines are
// real code (pre-fix dropTrailingMethodBrace stripped any of them, including
// legitimate arm closes).
func TestTrailingRealBlockCloseKept(t *testing.T) {
	view := "if (a) {\n  s.store.One(c);\n}"
	units := splitStatements(view)
	if got := strings.Join(units, "\n"); got != view {
		t.Errorf("trailing real block close dropped:\n got %q\nwant %q", got, view)
	}
	view = "/*L10*/  s.store.One(c);\n/*L11*/}"
	units = splitStatements(view)
	if got := strings.Join(units, "\n"); got != view {
		t.Errorf("provenance-marked trailing block close dropped:\n got %q\nwant %q", got, view)
	}
}

// TestSplitStatementsBlankLineChain pins the chain-glue lookahead: blank
// lines between a block close and its `else` continuation must not break
// the unit — the next unit starting mid-chain makes the model emit a
// dangling `else` (audit 2026-09-16).
func TestSplitStatementsBlankLineChain(t *testing.T) {
	view := "if (a) {\n  s.store.One(c);\n}\n\nelse {\n  s.store.Two(c);\n}\ns.store.Three(c);\n}"
	units := splitStatements(view)
	if got, want := strings.Join(units, "\n"), view; got != want {
		t.Errorf("split lost bytes:\n got %q\nwant %q", got, want)
	}
	for _, u := range units {
		if startsChainContinuation(u) {
			t.Errorf("unit starts mid-chain despite blank-line glue: %q", firstLine(strings.TrimSpace(u)))
		}
	}
	var sawElse bool
	for _, u := range units {
		if strings.Contains(u, "s.store.Two(c)") {
			sawElse = true
			if !strings.Contains(u, "s.store.One(c)") {
				t.Errorf("else arm split from its chain head:\n%s", u)
			}
		}
	}
	if !sawElse {
		t.Errorf("else arm missing from units: %v", units)
	}
}

// TestGlueChainUnits pins chain-atomic merging: continuation units join
// their head, lone block closes and fresh statements stand alone, and the
// merge preserves byte-for-byte reassembly.
func TestGlueChainUnits(t *testing.T) {
	if startsChainContinuation("else {\n  s.store.Two(c);\n}") != true {
		t.Errorf("else-led unit not a continuation")
	}
	if startsChainContinuation("/*L24*/  else\n/*L25*/  {") != true {
		t.Errorf("provenance-marked else not a continuation")
	}
	if startsChainContinuation("} else {\n  s.store.Two(c);") != true {
		t.Errorf("} else-led unit not a continuation")
	}
	if startsChainContinuation("}\n}") {
		t.Errorf("lone block close misread as continuation")
	}
	if startsChainContinuation("s.store.One(c);") {
		t.Errorf("plain statement misread as continuation")
	}
	if startsChainContinuation("") {
		t.Errorf("empty unit misread as continuation")
	}

	units := []string{
		"if (a) {\n  s.store.One(c);",
		"else {\n  s.store.Two(c);",
		"}",
		"s.store.Three(c);",
	}
	glued := glueChainUnits(units)
	if len(glued) != 3 {
		t.Fatalf("glued %d units, want 3 (head+else, close, fresh): %q", len(glued), glued)
	}
	if !strings.Contains(glued[0], "s.store.One(c)") || !strings.Contains(glued[0], "s.store.Two(c)") {
		t.Errorf("chain head missing its else arm: %q", glued[0])
	}
	if glued[2] != "s.store.Three(c);" {
		t.Errorf("fresh statement merged: %q", glued[2])
	}
	if got, want := strings.Join(glued, "\n"), strings.Join(units, "\n"); got != want {
		t.Errorf("glue lost bytes:\n got %q\nwant %q", got, want)
	}
}

// TestGroupFragmentsChainAtomic pins the packing invariant: even under a
// budget that forces many chunks, no chunk starts mid-chain.
func TestGroupFragmentsChainAtomic(t *testing.T) {
	units := glueChainUnits(splitStatements(chunkView))
	chunks := groupFragments(units, 220, func(string) int { return 0 })
	if len(chunks) < 2 {
		t.Fatalf("budget 220 produced %d chunks, want several", len(chunks))
	}
	for _, c := range chunks {
		first := ""
		for _, line := range strings.Split(c, "\n") {
			if s := stripLeadingComments(line); strings.TrimSpace(s) != "" {
				first = s
				break
			}
		}
		trimmed := strings.TrimSpace(first)
		if strings.HasPrefix(trimmed, "else") {
			t.Errorf("chunk starts mid-chain: %q", firstLine(c))
		}
	}
	var re string
	for i, c := range chunks {
		if i > 0 {
			re += "\n"
		}
		re += c
	}
	if want := chunkView; re != want {
		t.Errorf("chunks do not reassemble the view")
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
	if want := chunkView; re != want {
		t.Errorf("chunks do not reassemble the view")
	}
	// A unit larger than the budget rides alone in its own chunk.
	one := "s.store.GetDetail(c);\ns.store.GetDetail2(c);\ns.store.GetDetail3(c);"
	outs := groupFragments([]string{one}, 10, func(string) int { return 0 })
	if len(outs) != 1 || outs[0] != one {
		t.Errorf("oversized unit split: %v", outs)
	}
}

// TestBalancedFragmentsEqualParts pins the equal-mass split: uniform units
// under a budget that forces 2 chunks divide evenly (not packed front-heavy)
// and still reassemble byte-for-byte.
func TestBalancedFragmentsEqualParts(t *testing.T) {
	units := []string{"a;", "b;", "c;", "d;"}
	chunks := groupFragments(units, 6, func(string) int { return 0 })
	if len(chunks) != 2 {
		t.Fatalf("budget 6 produced %d chunks, want 2 balanced parts", len(chunks))
	}
	if chunks[0] != "a;\nb;" || chunks[1] != "c;\nd;" {
		t.Errorf("balanced split = %q, want [a b] [c d]", chunks)
	}
	if got := strings.Join(chunks, "\n"); got != "a;\nb;\nc;\nd;" {
		t.Errorf("balanced chunks do not reassemble: %q", got)
	}
}

// TestComposerStitches pins the third-model pass: a chunked run writes one
// #composer audit exchange and the unit still lands appended.
func TestComposerStitches(t *testing.T) {
	opts, _, auditRoot := bigFixture(t, 3000, 4000)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("chunked run failed endpoints: %v", res.Failed)
	}
	matches, _ := filepath.Glob(filepath.Join(auditRoot, "bigrun", "controller_method-BigBranch#composer-attempt0.json"))
	if len(matches) != 1 {
		t.Errorf("composer audit exchanges = %d, want 1", len(matches))
	}
	u := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	if e := opts.Ledger.Get(u.ID, string(u.Kind), u.Name); e.Status != ledger.StatusAppended {
		t.Errorf("BigBranch ledger status = %s, want appended", e.Status)
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

// TestWrapTxBody pins the deterministic tx-wrap repair: a combined body
// whose tx calls lack the wrapper gains the ExecTransaction shape the gate
// names — tx threaded through call sites, method returns mapped into
// closure returns — and passes every combined gate afterwards.
func TestWrapTxBody(t *testing.T) {
	calls := map[string]budget.DBCall{
		"q9": {Receiver: "s.store", Name: "UpdateRiskProfileDeviation", CtxName: "c", Tx: "tx", Args: []string{"request.MatchAccnt"}},
		"q1": {Receiver: "s.store", Name: "FetchUserAccount", CtxName: "c"},
	}
	view := "s.store.FetchUserAccount(c, request.MatchAccnt)\n" +
		"s.store.UpdateRiskProfileDeviation(c, request.MatchAccnt)\n"
	body := "_, err := s.store.FetchUserAccount(c, request.MatchAccnt)\n" +
		"if err != nil {\nreturn nil, err\n}\n" +
		"if err := s.store.UpdateRiskProfileDeviation(c, request.MatchAccnt); err != nil {\nreturn nil, fmt.Errorf(\"S31020: boom\")\n}\n" +
		"data = append(data, &models.R{PointType: \"Y\"})\n" +
		"return data, err\n"
	if errs := txGateErrs(body, calls); len(errs) == 0 {
		t.Fatalf("unwrapped body passes the tx gate — repair has nothing to do")
	}
	fixed, ok := wrapTxBody(body, calls, "s.store.")
	if !ok {
		t.Fatalf("repair bailed on a plain tx body")
	}
	for _, want := range []string{
		"if err := utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {",
		"s.store.UpdateRiskProfileDeviation(c, tx, request.MatchAccnt)",
		"s.store.FetchUserAccount(c, request.MatchAccnt)",
		"return data, nil",
		"S31020",
	} {
		if !strings.Contains(fixed, want) {
			t.Errorf("repaired body missing %q:\n%s", want, fixed)
		}
	}
	// Method-level two-value returns inside the closure collapse to the
	// error; exactly one `return nil, err` remains — the wrapper check.
	if n := strings.Count(fixed, "return nil, err"); n != 1 {
		t.Errorf("repaired body has %d `return nil, err`, want 1 (wrapper check):\n%s", n, fixed)
	}
	if !strings.HasSuffix(strings.TrimSpace(fixed), "return data, nil") {
		t.Errorf("repaired body does not end `return data, nil`:\n%s", fixed)
	}
	if errs := validateBody(Options{}, fixed); len(errs) != 0 {
		t.Errorf("repaired body fails parse: %v\n%s", errs, fixed)
	}
	if errs := requiredCallErrs(view, fixed, "s.store."); len(errs) != 0 {
		t.Errorf("repaired body drops required calls: %v", errs)
	}
	if errs := controllerTuxedoErrs(fixed); len(errs) != 0 {
		t.Errorf("repaired body trips tuxedo gate: %v", errs)
	}
	if errs := txGateErrs(fixed, calls); len(errs) != 0 {
		t.Errorf("repaired body still fails tx gate: %v\n%s", errs, fixed)
	}
}

// TestWrapTxBodyTail pins the non-returning tail: a body that falls off
// the end gains `return nil` in the closure plus the data tail outside.
func TestWrapTxBodyTail(t *testing.T) {
	calls := map[string]budget.DBCall{
		"q9": {Receiver: "s.store", Name: "InsertDev", CtxName: "c", Tx: "tx"},
	}
	body := "a := 1\nif err := s.store.InsertDev(c, a); err != nil {\nreturn nil, err\n}\n" +
		"data = append(data, &models.R{})\n"
	fixed, ok := wrapTxBody(body, calls, "s.store.")
	if !ok {
		t.Fatalf("repair bailed on a tail-less body")
	}
	if !strings.Contains(fixed, "\nreturn nil\n}") {
		t.Errorf("closure missing `return nil` tail:\n%s", fixed)
	}
	if !strings.HasSuffix(strings.TrimSpace(fixed), "return data, nil") {
		t.Errorf("missing data tail:\n%s", fixed)
	}
	if errs := validateBody(Options{}, fixed); len(errs) != 0 {
		t.Errorf("repaired body fails parse: %v\n%s", errs, fixed)
	}
	if errs := txGateErrs(fixed, calls); len(errs) != 0 {
		t.Errorf("repaired body still fails tx gate: %v", errs)
	}
}

// TestWrapTxBodyBailouts pins the repair's refusal cases: a partial
// wrapper is never second-guessed, a tx-free unit has nothing to repair,
// and a body declaring its own tx local would be shadowed by the closure
// parameter.
func TestWrapTxBodyBailouts(t *testing.T) {
	txCalls := map[string]budget.DBCall{
		"q9": {Receiver: "s.store", Name: "InsertDev", CtxName: "c", Tx: "tx"},
	}
	plain := map[string]budget.DBCall{
		"q1": {Receiver: "s.store", Name: "GetCount", CtxName: "c"},
	}
	if _, ok := wrapTxBody("err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {\nreturn nil\n})", txCalls, "s.store."); ok {
		t.Errorf("repair rewrote a body that already wraps")
	}
	if _, ok := wrapTxBody("s.store.GetCount(c)\nreturn data, err\n", plain, "s.store."); ok {
		t.Errorf("repair ran on a tx-free unit")
	}
	if _, ok := wrapTxBody("tx, err := s.store.Raw(c)\nif err != nil {\nreturn nil, err\n}\ns.store.InsertDev(c, a)\nreturn data, err\n", txCalls, "s.store."); ok {
		t.Errorf("repair wrapped over a body-local tx (shadow risk)")
	}
	if _, ok := wrapTxBody("var tx *sqlx.Tx\ns.store.InsertDev(c, a)\nreturn data, err\n", txCalls, "s.store."); ok {
		t.Errorf("repair wrapped over a var-declared tx")
	}
	// Use-without-declare is fine: the closure parameter provides it.
	already := "s.store.InsertDev(c, tx, a)\nreturn data, err\n"
	fixed, ok := wrapTxBody(already, txCalls, "s.store.")
	if !ok {
		t.Fatalf("repair bailed on an already-threaded body")
	}
	if strings.Contains(fixed, "c, tx, tx,") {
		t.Errorf("double-threaded tx handle:\n%s", fixed)
	}
	if errs := txGateErrs(fixed, txCalls); len(errs) != 0 {
		t.Errorf("threaded body still fails tx gate: %v", errs)
	}
}

// TestRewriteClosureReturn pins the return mapping, including error values
// carrying their own commas inside call parens.
func TestRewriteClosureReturn(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"return data, err", "return err"},
		{"\treturn nil, err", "\treturn err"},
		{"  return nil, fmt.Errorf(\"S%d: boom\", x)", "  return fmt.Errorf(\"S%d: boom\", x)"},
		{"return", "return err"},
		{"s.store.GetCount(c)", "s.store.GetCount(c)"},
		{"returnX := 1", "returnX := 1"},
		{"return x", "return x"},
	} {
		if got := rewriteClosureReturn(tc.in); got != tc.want {
			t.Errorf("rewriteClosureReturn(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHasTxLocal pins the shadow guard: declared tx handles bail the
// repair, mere uses and lookalike idents do not.
func TestHasTxLocal(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{"tx, err := s.store.Raw(c)", true},
		{"\tvar tx *sqlx.Tx", true},
		{"s.store.InsertDev(c, tx, a)", false},
		{"ctx := c", false},
		{"context := x", false},
		{"s.store.GetCount(c)", false},
	} {
		if got := hasTxLocal(tc.body); got != tc.want {
			t.Errorf("hasTxLocal(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// TestPromptHardeningBudget pins the no-bloat hardening rule: the three
// system prompts must carry the failure-mode clauses (store-only calls,
// C-literal mapping, terseness) while the combined instruction mass stays
// within budget — hardening merges and trims, never just appends.
func TestPromptHardeningBudget(t *testing.T) {
	prompts := map[string]string{
		"system":   systemPrompt,
		"fragment": systemPromptFragment,
		"composer": systemPromptComposer,
	}
	total := 0
	for name, p := range prompts {
		total += len(p)
		for _, want := range []string{"s.store.*", "\\0", "terse"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt missing hardening clause %q", name, want)
			}
		}
		if strings.Contains(p, "template-shaped gap") {
			t.Errorf("%s prompt still carries the verbose template boilerplate", name)
		}
	}
	if total > 5000 {
		t.Errorf("combined system prompts = %d chars, want <= 5000 (harden by merging, not appending)", total)
	}
}
