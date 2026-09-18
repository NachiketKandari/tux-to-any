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

// sameFileFixtureSrc is a minimal service whose entry calls a fn_* helper
// the same file defines and which owns a query of its own.
const sameFileFixtureSrc = `#include <stdio.h>

int fn_helper(char *c_ServiceName,
              char *c_match_accnt,
              long l_sssn_id,
              char *c_err_msg)
{
    EXEC SQL
      SELECT UAC_USR_ID
      INTO   :sql_usr_id
      FROM   DEMO_ACCNTS
      WHERE  UAC_CLM_MTCH_ACCNT = :c_match_accnt;

    if(SQLCODE != 0)
    {
        errlog(c_ServiceName, "S31000", SQLMSG, DEF_USR, DEF_SSSN, c_err_msg);
        return (-1);
    }
    return 1;
}

void SVC_DEMO(TPSVCINFO *rqst)
{
    if(c_rqst_typ == 'A')
    {
        i_ret = fn_helper(c_ServiceName, c_match_accnt, l_sssn_id, c_err_msg);
        if(i_ret != 1)
        {
            tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0L, 0);
        }
        tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
    }
    else
    {
        EXEC SQL
          SELECT X
          INTO :sql_x
          FROM T1;
    }
}
`

// sameFileClient adapts the canned seam output to each prompt: the helper
// seam gets a method with the prescribed signature (the caller seam's
// contract), controller seams get a terse body, and every REQUIRED CALL the
// prompt lists is appended with an error check so the gates pass.
type sameFileClient struct{ inner llm.Client }

func (c sameFileClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.Response, error) {
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
	if strings.Contains(prompt, "Legacy helper function") {
		body := "func (s *demoController) FnHelper(c context.Context, c_match_accnt string) int {\n"
		for _, call := range requiredCalls(prompt, "s.store.") {
			body += "\tif _, cerr := " + call + "(c, c_match_accnt); cerr != nil {\n\t\treturn -1\n\t}\n"
		}
		body += "\treturn 1\n}"
		resp.Content = body
		return resp, nil
	}
	resp.Content = fakeBody
	for _, call := range requiredCalls(prompt, "s.store.") {
		if strings.Contains(resp.Content, call+"(") {
			continue
		}
		if strings.HasPrefix(call, "s.store.") {
			resp.Content += "\n\tif _, cerr := " + call + "(c); cerr != nil {\n\t\treturn nil, cerr\n\t}"
			continue
		}
		// Same-file helper call: the generated method returns the legacy
		// status, not an error.
		resp.Content += "\n\tif hret := " + call + "(c); hret != 1 {\n\t\treturn nil, err\n\t}"
	}
	return resp, nil
}

func (c sameFileClient) Stream(ctx context.Context, req llm.ChatRequest, onDelta func(string) error) (llm.Response, error) {
	return c.inner.Stream(ctx, req, onDelta)
}

// TestSameFileHelperConvertEndToEnd pins the user directive (2026-09-17):
// the entry's call to a same-file fn becomes a call to the generated
// controller method, the helper converts into controller/fns.go with the
// prescribed signature, and the service-mode scaffold is not redeclared
// (the struct + constructor live in controller/interface.go).
func TestSameFileHelperConvertEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc_demo.pc")
	if err := os.WriteFile(path, []byte(sameFileFixtureSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := ir.ExtractFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{
		Service:   "demo",
		Endpoints: []plan.Endpoint{{Condition: 1, Name: "Demo", Route: "/demo"}},
	}
	p, err := plan.Build(plan.Options{Main: main, Source: sameFileFixtureSrc, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.FnHelpers) != 1 || !p.FnHelpers[0].Fixed {
		t.Fatalf("plan helpers = %+v, want one fixed FnHelper", p.FnHelpers)
	}
	if len(p.FnHelpers[0].Params) != 1 || p.FnHelpers[0].Params[0].Name != "c_match_accnt" {
		t.Fatalf("helper params = %+v, want [c_match_accnt]", p.FnHelpers[0].Params)
	}

	fake := llm.NewFakeServer(llm.FakeResponse{Content: fakeBody})
	t.Cleanup(fake.Close)
	var prompts []string
	client := sameFileClient{inner: llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: fake.URL, Temperature: 0.1})}
	observed := &observingClient{inner: client, prompts: &prompts}
	base := t.TempDir()
	led, err := ledger.Load(t.TempDir(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := audit.New(t.TempDir(), "test-run")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Plan: p, Main: main, Source: sameFileFixtureSrc,
		Client: observed, Budget: budget.New(12000, 4000, 4), BaseDir: base,
		Ledger: led, Validator: validate.New(validate.Options{}), MaxRetries: 2, Audit: rec,
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed units = %v (llm calls %d)", res.Failed, res.LLMCalls)
	}

	// The controller method carries the generated helper call, never the
	// legacy spelling.
	ctrl, _ := os.ReadFile(filepath.Join(base, "controller", "demo.go"))
	if !strings.Contains(string(ctrl), "s.FnHelper(") || strings.Contains(string(ctrl), "fn_helper(") {
		t.Errorf("controller method must call s.FnHelper, not fn_helper:\n%s", ctrl)
	}

	// The helper lands in fns.go with the prescribed signature and no
	// duplicate controller scaffold (interface.go owns the struct).
	fns, err := os.ReadFile(filepath.Join(base, "controller", "fns.go"))
	if err != nil {
		t.Fatalf("fns.go missing: %v", err)
	}
	if !strings.Contains(string(fns), "func (s *demoController) FnHelper(c context.Context, c_match_accnt string) int {") {
		t.Errorf("fns.go missing the prescribed signature:\n%s", fns)
	}
	if strings.Contains(string(fns), "type demoController struct") || strings.Contains(string(fns), "func NewDemoController(") {
		t.Errorf("service-mode fns.go must not redeclare the controller scaffold:\n%s", fns)
	}
	if strings.Contains(string(fns), `"demo/db"`) {
		t.Errorf("fns.go must not import db it never references:\n%s", fns)
	}

	// The prompt seam told the controller model to call the helper, and the
	// helper prompt carried the fixed signature.
	var ctrlPrompt, helperPrompt string
	for _, pr := range prompts {
		if strings.Contains(pr, "Legacy helper function") {
			helperPrompt = pr
		} else if strings.Contains(pr, "Endpoint: Demo") {
			ctrlPrompt = pr
		}
	}
	if !strings.Contains(ctrlPrompt, "s.FnHelper(") {
		t.Errorf("controller prompt missing the generated helper call mapping:\n%s", ctrlPrompt)
	}
	if !strings.Contains(helperPrompt, "Signature (fixed, verbatim): func (s *demoController) FnHelper(c context.Context, c_match_accnt string) int") {
		t.Errorf("helper prompt missing the prescribed signature:\n%s", helperPrompt)
	}
	if strings.Contains(helperPrompt, "fn_helper(") {
		t.Errorf("helper prompt still shows the legacy fn spelling:\n%s", helperPrompt)
	}
}

// observingClient records the user prompts the seam saw (assertions only).
type observingClient struct {
	inner   llm.Client
	prompts *[]string
}

func (c *observingClient) Chat(ctx context.Context, req llm.ChatRequest) (llm.Response, error) {
	for _, m := range req.Messages {
		if m.Role == "user" {
			*c.prompts = append(*c.prompts, m.Content)
		}
	}
	return c.inner.Chat(ctx, req)
}

func (c *observingClient) Stream(ctx context.Context, req llm.ChatRequest, onDelta func(string) error) (llm.Response, error) {
	return c.inner.Stream(ctx, req, onDelta)
}
