package csgen

import (
	"context"
	"os"
	"strings"
	"testing"

	"tux-to-any/internal/cschk"
	"tux-to-any/internal/csplan"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/llm"
)

// The seam tests run the full deterministic pipeline over the synthetic
// CUSE-shaped fixture (IR → csplan → csgen with the LLM seam) against the
// scripted FakeServer: happy path, gate-rejection retry, exhaustion
// degradation, and ledger resume.
func loadCustPlan(t *testing.T) (*csplan.Plan, string) {
	t.Helper()
	const fixture = "../../testdata/fixtures/cs/SVC_CUST_GET_DTL.pc"
	m, err := csplan.LoadMapping("../../testdata/fixtures/cs/cust.mapping.yaml")
	if err != nil {
		t.Fatal(err)
	}
	irf, err := ir.ExtractFileOpts(fixture, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	p, err := csplan.Build(csplan.Options{Main: irf, Source: string(src), Mapping: m})
	if err != nil {
		t.Fatal(err)
	}
	return p, string(src)
}

// validResidual is a gate-clean residual block for the fixture's CUSE arm:
// the RM-validity derivation the deterministic prologue leaves open.
const validResidual = `// NOTE: FRE-98-style scalar logic — derive the RM validity flag
var numeric = true;
foreach (char ch in response.CST_EMP_NO)
{
    if (!char.IsDigit(ch)) { numeric = false; }
}
var rmValid = "N";
if (numeric && response.CST_EMP_NO.Length == 6 &&
    response.CST_EMP_NO != "111111" && response.CST_EMP_NO != "222222" && response.CST_EMP_NO != "333333")
{
    rmValid = "Y";
}
_logger.LogInformation("RM validity derived: {RMValid}", rmValid);`

func assertServiceClean(t *testing.T, p *csplan.Plan, res Result) {
	t.Helper()
	typeNames := map[string]string{
		"Controller/" + p.Controller + ".cs":   p.Controller,
		"DTO/" + p.DTOCls + ".cs":              p.DTOCls,
		"NamedQueries/" + p.QueriesCls + ".cs": p.QueriesCls,
		"Repository/I" + p.Repo + ".cs":        "I" + p.Repo,
		"Repository/" + p.Repo + ".cs":         p.Repo,
		"Service/I" + p.Service + ".cs":        "I" + p.Service,
		"Service/" + p.Service + ".cs":         p.Service,
	}
	for _, rel := range res.Order {
		for _, is := range cschk.Check(rel, res.Files[rel], typeNames[rel]) {
			t.Errorf("gate: %s", is.Error())
		}
	}
}

func TestConvertcsSeamHappyPath(t *testing.T) {
	p, src := loadCustPlan(t)
	srv := llm.NewFakeServer(llm.FakeResponse{Content: "```csharp\n" + validResidual + "\n```"})
	defer srv.Close()

	res, err := Generate(context.Background(), Options{
		Plan: p, Source: src,
		Client: llm.New(llm.Endpoint{APIBase: srv.URL, Model: "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 1 {
		t.Errorf("LLMCalls = %d, want 1", res.LLMCalls)
	}
	if len(res.Filled) != 1 || res.Filled[0] != "CustomEvent" {
		t.Errorf("Filled = %v, want [CustomEvent]", res.Filled)
	}
	svc := res.Files["Service/"+p.Service+".cs"]
	if strings.Contains(svc, "tuxgo:TODO") {
		t.Error("filled body still carries the tuxgo:TODO marker")
	}
	for _, want := range []string{"numeric = false;", "RM validity derived"} {
		if !strings.Contains(svc, want) {
			t.Errorf("filled body missing %q", want)
		}
	}
	assertServiceClean(t, p, res)
}

func TestConvertcsSeamGateRetry(t *testing.T) {
	p, src := loadCustPlan(t)
	srv := llm.NewFakeServer(
		llm.FakeResponse{Content: "```csharp\nvar x = 1;\nEXEC SQL SELECT 1 FROM DUAL;\n```"},
		llm.FakeResponse{Content: "```csharp\n" + validResidual + "\n```"},
	)
	defer srv.Close()

	res, err := Generate(context.Background(), Options{
		Plan: p, Source: src, MaxRetries: 1,
		Client: llm.New(llm.Endpoint{APIBase: srv.URL, Model: "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 2 {
		t.Errorf("LLMCalls = %d, want 2 (gate rejection fed back, retry accepted)", res.LLMCalls)
	}
	if len(res.Filled) != 1 {
		t.Errorf("Filled = %v, want the retry accepted", res.Filled)
	}
	assertServiceClean(t, p, res)
}

func TestConvertcsSeamExhaustionDegrades(t *testing.T) {
	p, src := loadCustPlan(t)
	srv := llm.NewFakeServer(
		llm.FakeResponse{Content: "```csharp\nvar x = EXEC SQL junk;\n```"},
		llm.FakeResponse{Content: "```csharp\nvar y = EXEC SQL junk;\n```"},
	)
	defer srv.Close()

	res, err := Generate(context.Background(), Options{
		Plan: p, Source: src, MaxRetries: 1,
		Client: llm.New(llm.Endpoint{APIBase: srv.URL, Model: "fake"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 2 {
		t.Errorf("LLMCalls = %d, want 2", res.LLMCalls)
	}
	if len(res.Filled) != 0 {
		t.Errorf("Filled = %v, want none (exhaustion degrades)", res.Filled)
	}
	svc := res.Files["Service/"+p.Service+".cs"]
	if !strings.Contains(svc, "tuxgo:TODO residual arm logic") {
		t.Error("exhausted seam must keep the TODO placeholder")
	}
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n, "llm fill failed for CustomEvent") {
			found = true
		}
	}
	if !found {
		t.Errorf("exhaustion must note the degradation, notes = %v", res.Notes)
	}
	assertServiceClean(t, p, res)
}

func TestConvertcsSeamResume(t *testing.T) {
	p, src := loadCustPlan(t)

	// A ledger-restored body is re-gated and reused — never re-generated.
	res, err := Generate(context.Background(), Options{
		Plan: p, Source: src,
		Resumed: map[string]string{"CustomEvent": "    " + validResidual},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 0 {
		t.Errorf("LLMCalls = %d, want 0 (resumed bodies are never re-generated)", res.LLMCalls)
	}
	if len(res.Filled) != 1 {
		t.Errorf("Filled = %v, want the resumed body reused", res.Filled)
	}
	assertServiceClean(t, p, res)

	// A stale restored body fails the gates loudly and keeps the TODO.
	res, err = Generate(context.Background(), Options{
		Plan: p, Source: src,
		Resumed: map[string]string{"CustomEvent": "var z = EXEC SQL junk;"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Filled) != 0 {
		t.Errorf("Filled = %v, want none (stale body rejected)", res.Filled)
	}
	if !strings.Contains(res.Files["Service/"+p.Service+".cs"], "tuxgo:TODO residual arm logic") {
		t.Error("rejected resume must keep the TODO placeholder")
	}
	assertServiceClean(t, p, res)
}

func TestNormalizeBody(t *testing.T) {
	in := "    var a = 1;\n    if (a > 0)\n    {\n        a--;\n    }\n"
	want := strings.Repeat(" ", bodyIndent) + "var a = 1;\n" +
		strings.Repeat(" ", bodyIndent) + "if (a > 0)\n" +
		strings.Repeat(" ", bodyIndent) + "{\n" +
		strings.Repeat(" ", bodyIndent+4) + "a--;\n" +
		strings.Repeat(" ", bodyIndent) + "}\n"
	if got := normalizeBody(in, bodyIndent); got != want {
		t.Errorf("normalizeBody:\n%q\nwant:\n%q", got, want)
	}
}
