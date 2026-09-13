package convert

import (
	"context"
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

// fnFakeMethod is the fake seam output for the fn-library fixture: a
// complete Go method with the fixed receiver/name, the store call the view
// requires, and the legacy status-return contract.
const fnFakeMethod = `func (s *fn_demo_libController) FnIsDemoActive(c context.Context, cServiceName string, cMtchAccnt string, cIsActiveFlg *string) int {
	row, err := s.store.GetDemoClientMap(c, cMtchAccnt)
	if err != nil {
		return -1
	}
	*cIsActiveFlg = row.CActiveFlag.String
	return 1
}`

func fnLibRunFixture(t *testing.T, skipLLM bool) (Options, *llm.FakeServer, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fn_demo_lib.pc")
	src := fnSrc()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.BuildFnLib(plan.Options{Main: main, Source: src, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	fake := llm.NewFakeServer(llm.FakeResponse{Content: fnFakeMethod})
	t.Cleanup(fake.Close)

	base := t.TempDir()
	led, err := ledger.Load(t.TempDir(), p.Service)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := audit.New(t.TempDir(), "test-run")
	if err != nil {
		t.Fatal(err)
	}
	client := (*llm.Client)(nil)
	if !skipLLM {
		c := llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: fake.URL, Temperature: 0.1})
		client = &c
	}
	// The cmd layer roots the output at the service subtree when the
	// module equals the service (runConvertFnLib) — mirror that here.
	base = filepath.Join(base, p.Service)
	opts := Options{
		Plan: p, Main: main, Source: src,
		Client: clientValue(client), Budget: budget.New(12000, 4000, 4), BaseDir: base,
		Ledger: led, Validator: validate.New(validate.Options{}), MaxRetries: 2, Audit: rec,
		SkipLLM: skipLLM,
	}
	return opts, fake, base
}

func fnSrc() string {
	return `int fn_is_demo_active(char* c_ServiceName, char* c_mtch_accnt, char* c_is_active_flg, char* c_err_msg)
{
    char c_active_flag;

    EXEC SQL
    SELECT DECODE(COUNT(*), 0, 'N', 'Y')
    INTO   :c_active_flag
    FROM   DEMO_CLIENT_MAP, DEMO_USER_ACCOUNTS
    WHERE  DEMO_MATCH_ACC = :c_mtch_accnt
    AND    DEMO_USER_ID = DEMO_ACCT_USER_ID
    AND    DEMO_PLAN_ACTIVE_FLAG = 'A'
    AND    ROWNUM = 1;

    if(SQLCODE != 0)
    {
        errlog(c_ServiceName,"S31240",SQLMSG,(char *)DEF_USR,DEF_SSSN,c_err_msg);
        return -1;
    }

    *c_is_active_flg = c_active_flag;

    return 1;
}
`
}

// clientValue adapts the optional client for the Options field.
func clientValue(c *llm.Client) llm.Client {
	if c == nil {
		return nil
	}
	return *c
}

// TestFnLibConvertEndToEnd pins the fn-library pipeline: the db surface is
// deterministic, the helper body rides the LLM seam into controller/fns.go
// with the struct scaffold, imports stay use-only, and a resume run makes
// zero LLM calls.
func TestFnLibConvertEndToEnd(t *testing.T) {
	opts, fake, base := fnLibRunFixture(t, false)
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 1 {
		t.Fatalf("llm calls = %d, want 1 (the fn helper); failed=%v skipped=%v files=%d", res.LLMCalls, res.Failed, res.Skipped, len(res.Files))
	}
	fnsPath := filepath.Join(base, "controller", "fns.go")
	data, err := os.ReadFile(fnsPath)
	if err != nil {
		t.Fatalf("fns.go missing: %v", err)
	}
	fns := string(data)
	for _, want := range []string{
		"type fn_demo_libController struct {",
		"func NewFn_demo_libController(",
		"func (s *fn_demo_libController) FnIsDemoActive(c context.Context,",
		"s.store.GetDemoClientMap(c, cMtchAccnt)",
	} {
		if !strings.Contains(fns, want) {
			t.Errorf("fns.go missing %q\n---\n%s", want, fns)
		}
	}
	if strings.Contains(fns, `"fn_demo_lib/models"`) {
		t.Error("fns.go must not import models it never references (Tier B unused-import failure)")
	}
	if !strings.Contains(fns, `"context"`) || !strings.Contains(fns, `"fn_demo_lib/db"`) {
		t.Errorf("fns.go imports missing context/db:\n%s", fns)
	}
	if len(res.SQLDeviations) != 0 {
		t.Errorf("sql deviations = %v, want none (the db file itself is exempt)", res.SQLDeviations)
	}

	// Resume: a second run appends nothing and calls nobody.
	before := fake.RequestCount()
	led2 := opts.Ledger
	_ = led2
	res2, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res2.LLMCalls != 0 || fake.RequestCount() != before {
		t.Errorf("resume made LLM calls (%d, fake %d→%d) — the ledger must short-circuit appended units", res2.LLMCalls, before, fake.RequestCount())
	}
}

// TestFnLibConvertSkipLLM pins the deterministic-only degrade: db methods
// land, helper bodies stay skipped (visible for an LLM-enabled resume), no
// fns.go is created.
func TestFnLibConvertSkipLLM(t *testing.T) {
	opts, _, base := fnLibRunFixture(t, true)
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "FnIsDemoActive" {
		t.Errorf("skipped = %v, want [FnIsDemoActive]", res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(base, "controller", "fns.go")); !os.IsNotExist(err) {
		t.Error("fns.go must not exist in deterministic-only mode")
	}
	dbPath := filepath.Join(base, "db", "fn_demo_lib.go")
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db methods must land without the LLM: %v", err)
	}
}
