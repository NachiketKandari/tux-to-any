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
// chunked controller path. Returns the options, the fake server, and the
// audit root (exchanges land under <root>/bigrun/).
func bigFixture(t *testing.T, ceiling int) (Options, *llm.FakeServer, string) {
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
	p, err := plan.Build(plan.Options{Main: main, Source: src, Mapping: m, Budget: budget.New(3000, 4000, 4)})
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
		Budget: budget.New(3000, 4000, 4), BaseDir: base,
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
	opts, fake, auditRoot := bigFixture(t, 3000)
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
	opts, fake, _ := bigFixture(t, 3000)
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
