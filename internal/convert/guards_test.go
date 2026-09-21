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
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/ledger"
	"tux-to-any/internal/llm"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/validate"
)

// mergedGuardScenario is the S7 shape: the F||I re-fold keeps both arm
// guards as FoldMixed nodes with surviving statements.
func mergedGuardScenario() *flow.Scenario {
	return &flow.Scenario{
		Key: "c_flag in {F,I}", Var: "c_flag", Value: "F_or_I",
		Filter: "c_flag == 'F' || c_flag == 'I'",
		Body: []*flow.SliceNode{
			{Kind: flow.KindBranch, Line: 10, Fold: flow.FoldKept, Cond: "x > 0", Children: []*flow.SliceNode{{Kind: flow.KindStmt, Line: 11}}},
			{Kind: flow.KindBranch, Line: 182, Fold: flow.FoldMixed, Cond: "c_flag == 'F'", FoldedCond: "c_flag == 'F'", Children: []*flow.SliceNode{{Kind: flow.KindStmt, Line: 183}}},
			{Kind: flow.KindBranch, Line: 420, Fold: flow.FoldMixed, Cond: "c_flag == 'I'", FoldedCond: "c_flag == 'I'", Children: []*flow.SliceNode{{Kind: flow.KindStmt, Line: 421}}},
			{Kind: flow.KindBranch, Line: 500, Fold: flow.FoldContradicted, Cond: "c_flag == 'H'"},
			{Kind: flow.KindBranch, Line: 600, Fold: flow.FoldMixed, Cond: "c_flag == 'X'", FoldedCond: "c_flag == 'X'"}, // folded-empty arm: no behavior to gate
			{Kind: flow.KindBranch, Line: 700, Fold: flow.FoldMixed, Cond: "c_flag == 'Y'", FoldedCond: "c_flag == 'Y'", Children: []*flow.SliceNode{
				{Kind: flow.KindBranch, Line: 701, Fold: flow.FoldSatisfied, Cond: "x > 0"},
			}}, // only empty branch descendants: no behavior either
		},
	}
}

// guardTestBind is the base binding set the tests use: guard identifiers
// claimed by spelling key (the real path adds the request-field spellings).
func guardTestBind(guards []flow.MixedGuard) *guardBindings {
	return guardBindingsFor(guards, "", nil, nil)
}

// TestMixedGuardsOf pins the extraction contract: only FoldMixed nodes with
// surviving behavior carry a dispatch guard; conditions dedup.
func TestMixedGuardsOf(t *testing.T) {
	guards := flow.MixedGuards(mergedGuardScenario())
	if len(guards) != 2 {
		t.Fatalf("guards = %+v, want the F and I mixed guards", guards)
	}
	if guards[0].Line != 182 || guards[0].Cond != "c_flag == 'F'" || guards[0].Alt != "" {
		t.Errorf("guard[0] = %+v, want L182 c_flag == 'F' with no alt", guards[0])
	}
	if guards[1].Line != 420 || guards[1].Cond != "c_flag == 'I'" {
		t.Errorf("guard[1] = %+v, want L420 c_flag == 'I'", guards[1])
	}
	if got := flow.MixedGuards(nil); got != nil {
		t.Errorf("nil scenario guards = %v, want nil", got)
	}
}

// TestScenarioGuardErrsDroppedF is the S7 regression in unit form: the real
// run's accepted body kept the I guard and ran the F arm unconditionally.
func TestScenarioGuardErrsDroppedF(t *testing.T) {
	guards := flow.MixedGuards(mergedGuardScenario())
	s7 := "cFlag := request.MfGrowthFlg\nif cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err"
	errs := scenarioGuardErrs(guards, guardTestBind(guards), s7)
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly the lost F guard", errs)
	}
	got := errs[0]
	for _, want := range []string{"line 182", "c_flag == 'F'", "lost — runtime dispatch guard"} {
		if !strings.Contains(got, want) {
			t.Errorf("guard error missing %q: %s", want, got)
		}
	}
	if !isConditionGap(got) {
		t.Errorf("lost-guard error must surface as a run-level condition gap: %s", got)
	}
}

// TestScenarioGuardErrsRetention pins the accept shapes: both guards with
// spelling variants (snake/camel/Pascal, request-field selectors, locals
// derived from a bound spelling) and commutative && reordering.
func TestScenarioGuardErrsRetention(t *testing.T) {
	guards := flow.MixedGuards(mergedGuardScenario())
	bind := guardTestBind(guards)
	bind.claim("MfGrowthFlg", "c_flag")
	cases := []struct {
		name string
		body string
	}{
		{"both plain", "cFlag := request.MfGrowthFlg\nif cFlag == \"F\" {\n\t_ = cFlag\n}\nif cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err"},
		{"pascal spelling", "if CFlag == 'F' {\n\t_ = CFlag\n}\nif CFlag == 'I' {\n\t_ = CFlag\n}\nreturn data, err"},
		{"request field inline", "if request.MfGrowthFlg == \"F\" {\n\t_ = request\n}\nif request.MfGrowthFlg == \"I\" {\n\t_ = request\n}\nreturn data, err"},
		{"local derived from request", "growth := request.MfGrowthFlg\nif growth == \"F\" {\n\t_ = growth\n}\nif growth == \"I\" {\n\t_ = growth\n}\nreturn data, err"},
	}
	for _, tc := range cases {
		if errs := scenarioGuardErrs(guards, bind, tc.body); len(errs) != 0 {
			t.Errorf("%s: rejected a retaining body: %v", tc.name, errs)
		}
	}
}

// TestScenarioGuardErrsSubsetResidual pins the single-value slice shape:
// the FoldMixed residual is the axis-stripped remainder, so either the
// residual alone or the full legacy predicate (Alt) is accepted — including
// commutative reordering.
func TestScenarioGuardErrsSubsetResidual(t *testing.T) {
	sc := &flow.Scenario{
		Key: "c_flag=F",
		Body: []*flow.SliceNode{
			{Kind: flow.KindBranch, Line: 9, Fold: flow.FoldMixed, Cond: "c_flag == 'F' && cnt_d2u > 0", FoldedCond: "cnt_d2u > 0", Children: []*flow.SliceNode{{Kind: flow.KindStmt, Line: 10}}},
		},
	}
	guards := flow.MixedGuards(sc)
	if len(guards) != 1 || guards[0].Cond != "cnt_d2u > 0" || guards[0].Alt != "c_flag == 'F' && cnt_d2u > 0" {
		t.Fatalf("guards = %+v, want residual + alt", guards)
	}
	bind := guardTestBind(guards)
	if errs := scenarioGuardErrs(guards, bind, "if cntD2u > 0 {\n\t_ = cntD2u\n}\nreturn data, err"); len(errs) != 0 {
		t.Errorf("residual alone rejected: %v", errs)
	}
	if errs := scenarioGuardErrs(guards, bind, "if cFlag == \"F\" && cntD2u > 0 {\n\t_ = cFlag\n}\nreturn data, err"); len(errs) != 0 {
		t.Errorf("full legacy predicate rejected: %v", errs)
	}
	if errs := scenarioGuardErrs(guards, bind, "if cntD2u > 0 && cFlag == \"F\" {\n\t_ = cFlag\n}\nreturn data, err"); len(errs) != 0 {
		t.Errorf("reordered full predicate rejected: %v", errs)
	}
	if errs := scenarioGuardErrs(guards, bind, "if cntD2u >= 0 {\n\t_ = cntD2u\n}\nreturn data, err"); len(errs) == 0 {
		t.Error("mutated comparison accepted — want a reject")
	}
	if errs := scenarioGuardErrs(guards, bind, "if cntD2u > 0 || cFlag == \"F\" {\n\t_ = cntD2u\n}\nreturn data, err"); len(errs) == 0 {
		t.Error("weakened || predicate accepted — want a reject")
	}
}

// TestScenarioGuardErrsIdentity pins the variable-binding rule: an
// unrelated variable with the same literal, an inverted comparison, and a
// widened or narrowed predicate all fail the gate.
func TestScenarioGuardErrsIdentity(t *testing.T) {
	guards := flow.MixedGuards(mergedGuardScenario())
	bind := guardTestBind(guards)
	cases := []struct {
		name string
		body string
	}{
		{"unrelated variable", "if other == \"F\" {\n\t_ = other\n}\nif cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err"},
		{"inverted both", "if !(cFlag == \"F\") {\n\t_ = cFlag\n}\nif !(cFlag == \"I\") {\n\t_ = cFlag\n}\nreturn data, err"},
		{"weakened or", "if cFlag == \"F\" || cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err"},
		{"narrowed and", "if cFlag == \"F\" && cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err"},
	}
	for _, tc := range cases {
		if errs := scenarioGuardErrs(guards, bind, tc.body); len(errs) == 0 {
			t.Errorf("%s: accepted — want a reject", tc.name)
		}
	}
}

// TestScenarioGuardErrsDegrades pins the never-fatal shapes: an empty guard
// list and an unparseable body (owned by the parse gate) pass silently, and
// duplicate guards report once.
func TestScenarioGuardErrsDegrades(t *testing.T) {
	if errs := scenarioGuardErrs(nil, nil, "not go {"); errs != nil {
		t.Errorf("no guards must be a no-op, got %v", errs)
	}
	guards := flow.MixedGuards(mergedGuardScenario())
	if errs := scenarioGuardErrs(guards, guardTestBind(guards), "not go {"); errs != nil {
		t.Errorf("unparseable body must defer to the parse gate, got %v", errs)
	}
	dup := []flow.MixedGuard{guards[0], guards[0]}
	if errs := scenarioGuardErrs(dup, guardTestBind(dup), "return data, err"); len(errs) != 1 {
		t.Errorf("duplicate guards = %v, want one deduped error", errs)
	}
}

// TestScenarioGuardsPromptFacts pins the prompt contract: the view and the
// composer/repair wordings carry the runtime dispatch guards, and a slice
// with no mixed nodes grows no section.
func TestScenarioGuardsPromptFacts(t *testing.T) {
	sp := scenPromptOf(mergedGuardScenario(), nil)
	if len(sp.Guards) != 2 || !strings.Contains(strings.Join(sp.Guards, "; "), "L182: c_flag == 'F'") {
		t.Fatalf("scenPrompt guards = %v, want the two mixed guards with lines", sp.Guards)
	}
	for name, w := range map[string]factsWording{"view": viewWording, "composer": composerWording} {
		var sb strings.Builder
		writePromptFacts(&sb, w, sp, true, "src", "s.store.", "db", "contract", nil, nil, nil, nil)
		if !strings.Contains(sb.String(), "Runtime dispatch guards — keep every one live as an if condition in the body: L182: c_flag == 'F'; L420: c_flag == 'I'") {
			t.Errorf("%s wording missing the guard contract:\n%s", name, sb.String())
		}
	}
	plain := &flow.Scenario{Key: "c_flag=F"}
	var sb strings.Builder
	writePromptFacts(&sb, viewWording, scenPromptOf(plain, nil), true, "src", "s.store.", "db", "contract", nil, nil, nil, nil)
	if strings.Contains(sb.String(), "Runtime dispatch guards") || strings.Contains(sb.String(), "Scenario filter") {
		t.Errorf("plain scenarioRef facts grew a guards/filter section:\n%s", sb.String())
	}
}

// guardSrc is the convert-side F||I dispatch fixture: both arms carry a
// query, the axis is c_flag, and the merged filter keeps both arm guards.
const guardSrc = `void SVC_G(TPSVCINFO *rqst) {
	char c_flag;
	if (Fget32(rqst, FML_MF_GROWTH_FLG, 0, (char *)&c_flag, 0) == -1) {
		Fadd32(rqst, FML_ERR_MSG, msg, 0);
		tpreturn(TPFAIL, 0L, rqst, 0L, 0);
	}
	if (c_flag == 'F') {
		EXEC SQL SELECT COUNT(*) INTO :cnt_f FROM TF;
		cnt_f = cnt_f + 1;
	} else if (c_flag == 'I') {
		EXEC SQL SELECT COUNT(*) INTO :cnt_i FROM TI;
		cnt_i = cnt_i + 1;
	}
	tpreturn(TPSUCCESS, 0L, rqst, 0L, 0);
}
`

// guardFixture builds a full convert run over guardSrc with the merged F||I
// endpoint and one scripted fake body.
func guardFixture(t *testing.T, response string) (Options, *llm.FakeServer) {
	t.Helper()
	return guardFixtureSrc(t, guardSrc, 12000, 4000, response)
}

// guardFixtureSrc is the ceiling/source-parameterized fixture builder.
func guardFixtureSrc(t *testing.T, src string, promptCeiling, outputCeiling int, response string) (Options, *llm.FakeServer) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_G.pc")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := ir.ExtractFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{
		Service: "guardsvc",
		Module:  "mutual-fund-be/pkg/services/guardsvc",
		Endpoints: []plan.Endpoint{
			{ScenarioFilter: "c_flag == 'F' || c_flag == 'I'", Name: "Guarded", Route: "/guarded"},
		},
		DBMethods: map[string]plan.MethodPin{
			"q1": {Name: "GetFCount"},
			"q2": {Name: "GetICount"},
		},
	}
	p, err := plan.Build(plan.Options{Main: main, Source: src, Mapping: m, Budget: budget.New(promptCeiling, outputCeiling, 4)})
	if err != nil {
		t.Fatal(err)
	}
	fake := llm.NewFakeServer(llm.FakeResponse{Content: response})
	t.Cleanup(fake.Close)
	led, err := ledger.Load(t.TempDir(), "guardsvc")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := audit.New(t.TempDir(), "guardrun")
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Plan: p, Main: main, Source: src,
		Client: contractClient{llm.New(llm.Endpoint{ProfileName: "fake", Model: "fake", APIBase: fake.URL, Temperature: 0.1})},
		Budget: budget.New(promptCeiling, outputCeiling, 4), BaseDir: t.TempDir(),
		Ledger: led, Validator: validate.New(validate.Options{}), MaxRetries: 1, Audit: rec,
	}, fake
}

// TestScenarioFilterGuardGateEndToEnd is the deterministic S7 regression:
// the merged-filter run whose body drops the F guard fails loudly (ledger +
// condition gaps), and the prompt carries the guard contract.
func TestScenarioFilterGuardGateEndToEnd(t *testing.T) {
	opts, fake := guardFixture(t, "cFlag := request.MfGrowthFlg\nif cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err")

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) == 0 {
		t.Fatal("guard-dropping body landed — the S7 gate did not fire")
	}
	u := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	e := opts.Ledger.Get(u.ID, string(u.Kind), u.Name)
	if e.Status != ledger.StatusFailed || !strings.Contains(e.Error, "runtime dispatch guard") {
		t.Errorf("unit %s status=%s error=%q, want failed with the guard note", u.Name, e.Status, e.Error)
	}
	joined := strings.Join(res.ConditionGaps, "; ")
	if !strings.Contains(joined, "runtime dispatch guard") || !strings.Contains(joined, "c_flag == 'F'") {
		t.Errorf("condition gaps = %v, want the lost F guard surfaced", res.ConditionGaps)
	}
	// The prompt carried the guard contract before the model ever answered.
	var prompted bool
	for _, req := range fake.Requests {
		if strings.Contains(promptOf(t, req), "Runtime dispatch guards — keep every one live") {
			prompted = true
		}
	}
	if !prompted {
		t.Error("controller prompt never carried the runtime dispatch guards")
	}
}

// guardChunkSrc inflates the F arm so the endpoint routes to the chunked
// path — the S7 shape: the F and I arms land in separate fragments.
func guardChunkSrc() string {
	var sb strings.Builder
	sb.WriteString("void SVC_G(TPSVCINFO *rqst) {\n\tchar c_flag;\n\tif (Fget32(rqst, FML_MF_GROWTH_FLG, 0, (char *)&c_flag, 0) == -1) {\n\t\ttpreturn(TPFAIL, 0L, rqst, 0L, 0);\n\t}\n\tif (c_flag == 'F') {\n\t\tEXEC SQL SELECT COUNT(*) INTO :cnt_f FROM TF;\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "\t\tcnt_f = cnt_f + %d;\n", i)
	}
	sb.WriteString("\t} else if (c_flag == 'I') {\n\t\tEXEC SQL SELECT COUNT(*) INTO :cnt_i FROM TI;\n\t\tcnt_i = cnt_i + 1;\n\t}\n\ttpreturn(TPSUCCESS, 0L, rqst, 0L, 0);\n}\n")
	return sb.String()
}

// TestScenarioFilterGuardGateChunked is the S7 repair-path regression: the
// chunked run stitches fragments that dropped the F guard, the combined
// gate fires, and the repair seam cannot sneak the guardless body through.
func TestScenarioFilterGuardGateChunked(t *testing.T) {
	decl := "cFlag := request.MfGrowthFlg"
	iGuard := "if cFlag == \"I\" {\n\t_ = cFlag\n}"
	repairBody := decl + "\n" + iGuard
	opts, fake := guardFixtureSrc(t, guardChunkSrc(), 3000, 4000, "")
	fake.Reset(
		llm.FakeResponse{Content: decl},
		llm.FakeResponse{Content: iGuard},
		llm.FakeResponse{Content: repairBody},
	)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) == 0 {
		t.Fatal("chunked guard-dropping body landed — the S7 combined gate did not fire")
	}
	u := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	e := opts.Ledger.Get(u.ID, string(u.Kind), u.Name)
	if e.Status != ledger.StatusFailed || !strings.Contains(e.Error, "runtime dispatch guard") {
		t.Errorf("unit %s status=%s error=%q, want failed with the guard note", u.Name, e.Status, e.Error)
	}
	var chunked bool
	for _, req := range fake.Requests {
		if strings.Contains(promptOf(t, req), "Fragment 1 of") {
			chunked = true
		}
	}
	if !chunked {
		t.Error("the chunked path never engaged — fixture outgrew its premise")
	}
}

// TestScenarioFilterGuardGateEndToEndRetained is the positive control: the
// same fixture with both guards live lands appended.
func TestScenarioFilterGuardGateEndToEndRetained(t *testing.T) {
	body := "cFlag := request.MfGrowthFlg\nif cFlag == \"F\" {\n\t_ = cFlag\n}\nif cFlag == \"I\" {\n\t_ = cFlag\n}\nreturn data, err"
	opts, _ := guardFixture(t, body)

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("retaining body failed endpoints: %v", res.Failed)
	}
	u := unitsOf(opts.Plan, plan.KindControllerMethod)[0]
	if e := opts.Ledger.Get(u.ID, string(u.Kind), u.Name); e.Status != ledger.StatusAppended {
		t.Fatalf("unit %s status=%s error=%q, want appended", u.Name, e.Status, e.Error)
	}
}
