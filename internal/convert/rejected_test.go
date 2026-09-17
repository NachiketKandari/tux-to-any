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

// TestRejectedOutputKeptAndReplaced pins the visible-fallback posture: when
// every attempt fails validation, the last rejected output stays commented
// inside a panicking placeholder (never a missing method), and a later
// accepted attempt replaces the placeholder instead of stacking a duplicate.
func TestRejectedOutputKeptAndReplaced(t *testing.T) {
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
		Budget: budget.New(12000, 4000, 4), BaseDir: base,
		Ledger: led, Validator: validate.New(validate.Options{}), MaxRetries: 2, Audit: rec,
	}

	// Run 1: every seam attempt is invalid — the run fails the units but
	// stages the rejected outputs visibly.
	bad := llm.NewFakeServer(llm.FakeResponse{Content: "if x {\n"})
	t.Cleanup(bad.Close)
	opts.Client = llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: bad.URL, Temperature: 0.1})
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) == 0 {
		t.Fatal("bad run must fail units")
	}

	ctrlPath := filepath.Join(base, "controller", "demo.go")
	ctrl, err := os.ReadFile(ctrlPath)
	if err != nil {
		t.Fatalf("controller file missing after failure: %v", err)
	}
	for _, want := range []string{
		"// tuxgo:REJECTED — Demo:",
		"panic(\"tuxgo: Demo rejected",
		"// tuxgo:REJECTED-BEGIN Demo",
		"// tuxgo:REJECTED-END Demo",
	} {
		if !strings.Contains(string(ctrl), want) {
			t.Errorf("controller missing %q:\n%s", want, ctrl)
		}
	}
	if !hasLiveMethod(string(ctrl), "Demo") {
		t.Errorf("controller must carry the panicking placeholder method:\n%s", ctrl)
	}
	fnsPath := filepath.Join(base, "controller", "fns.go")
	fns, err := os.ReadFile(fnsPath)
	if err != nil {
		t.Fatalf("fns.go missing after helper failure: %v", err)
	}
	for _, want := range []string{
		"// tuxgo:REJECTED — FnHelper:",
		"func (s *demoController) FnHelper(c context.Context, c_match_accnt string) int",
	} {
		if !strings.Contains(string(fns), want) {
			t.Errorf("fns.go missing %q:\n%s", want, fns)
		}
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "rejected output is kept commented") {
		t.Errorf("warnings must surface the kept rejected output: %v", res.Warnings)
	}

	// Run 2: a good seam replaces the placeholders — one live method each,
	// no rejected blocks left.
	good := llm.NewFakeServer(llm.FakeResponse{Content: fakeBody})
	t.Cleanup(good.Close)
	opts.Client = sameFileClient{inner: llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: good.URL, Temperature: 0.1})}
	res2, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Failed) != 0 {
		t.Fatalf("good run failed units: %v (%s)", res2.Failed, strings.Join(res2.Warnings, "; "))
	}
	ctrl2, _ := os.ReadFile(ctrlPath)
	if got := strings.Count(string(ctrl2), "func (s *demoController) Demo("); got != 1 {
		t.Errorf("live Demo methods = %d, want 1:\n%s", got, ctrl2)
	}
	if strings.Contains(string(ctrl2), "tuxgo:REJECTED-BEGIN Demo") || strings.Contains(string(ctrl2), "panic(\"tuxgo: Demo rejected") {
		t.Errorf("accepted run must replace the rejected placeholder:\n%s", ctrl2)
	}
	if !strings.Contains(string(ctrl2), "s.FnHelper(") {
		t.Errorf("accepted run must call the generated helper:\n%s", ctrl2)
	}
	fns2, _ := os.ReadFile(fnsPath)
	if strings.Contains(string(fns2), "tuxgo:REJECTED-BEGIN FnHelper") || strings.Contains(string(fns2), "panic(\"tuxgo: FnHelper rejected") {
		t.Errorf("accepted helper must replace its rejected placeholder:\n%s", fns2)
	}
	if got := strings.Count(string(fns2), "func (s *demoController) FnHelper("); got != 1 {
		t.Errorf("live FnHelper methods = %d, want 1:\n%s", got, fns2)
	}
}

// TestRejectedBlockStrip pins the marker surgery: the block (placeholder
// included) is removed, and only real method declarations count as live.
func TestRejectedBlockStrip(t *testing.T) {
	src := "package controller\n\nfunc (s *x) A() {}\n\n" +
		rejectedBlock("A", "func (s *x) A(c context.Context) int {\n\tpanic(\"tuxgo: A rejected\")\n}\n")
	out, ok := stripRejectedBlock(src, "A")
	if !ok {
		t.Fatal("block not found")
	}
	if strings.Contains(out, "panic(") || strings.Contains(out, "REJECTED") {
		t.Errorf("block survived:\n%s", out)
	}
	if !strings.Contains(out, "func (s *x) A() {}") {
		t.Errorf("live method removed:\n%s", out)
	}
	if _, ok := stripRejectedBlock(src, "B"); ok {
		t.Error("strip must not touch another method's block")
	}
	if !hasLiveMethod(src, "A") {
		t.Error("live method not detected")
	}
	if hasLiveMethod("// func (s *x) A(c context.Context) int {\n", "A") {
		t.Error("commented signature must not count as live")
	}
	if hasLiveMethod("func (s *x) AB() {}", "A") {
		t.Error("longer method name must not match")
	}
}
