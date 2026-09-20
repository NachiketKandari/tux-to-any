package main

import (
	"strings"
	"testing"

	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// previewSrc is the discover-preview fixture: a primary c_flag ladder whose
// F arm nests a demo_flg chain — the union and the intersection examples the
// draft comments must derive from the registry.
const previewSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char c_flag;
	char demo_flg = 'N';
	if (c_flag == 'F') {
		demo_flg = demo_active();
		if (demo_flg == 'Y') {
			work_fy();
		} else if (demo_flg == 'N') {
			work_fn();
		}
	} else if (c_flag == 'H') {
		work_h();
	} else {
		work_other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func previewTree(t *testing.T) *flow.Tree {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(previewSrc), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	return flow.Build([]byte(previewSrc), facts, "SVC_DEMO", &ir.File{})
}

// TestFilterExamplesRegistryDerived pins the draft examples (plan §5): the
// union over the primary's first two domain values plus the first reachable
// primary×secondary intersection, both fold-verified (an unwitnessed
// candidate is never commented).
func TestFilterExamplesRegistryDerived(t *testing.T) {
	tree := previewTree(t)
	axes := tree.AxesFor([]byte(previewSrc))
	if len(axes) != 2 || axes[1].Kind != flow.AxisSecondary {
		t.Fatalf("registry = %+v, want a primary + secondary fixture", axes)
	}
	exs := flow.FilterExamples(tree, axes)
	if len(exs) != 2 {
		t.Fatalf("examples = %+v, want the union + intersection pair", exs)
	}
	if exs[0].Expr != "c_flag == 'F' || c_flag == 'H'" {
		t.Errorf("union example = %q, want the first two primary domain values", exs[0].Expr)
	}
	if exs[0].Key != "c_flag in {F,H}" {
		t.Errorf("union key = %q, want c_flag in {F,H}", exs[0].Key)
	}
	if !strings.Contains(exs[1].Expr, "&&") || !strings.Contains(exs[1].Expr, "demo_flg") {
		t.Errorf("intersection example = %q, want primary && secondary", exs[1].Expr)
	}
	for _, ex := range exs {
		if len(ex.Blocks) == 0 || ex.Lines == 0 {
			t.Errorf("example %q carries no kept-block evidence: %+v", ex.Expr, ex)
		}
	}
}

// TestRenderAxesList pins the -list-axes console table: rank, key, kind,
// domain with the default mark, sites, and guard lines; no-axis entries say
// so instead of printing an empty table.
func TestRenderAxesList(t *testing.T) {
	axes := []*flow.DispatchAxis{
		{Ref: "c_flag", RefName: "c_flag", Domain: []string{"F", "H"}, Sites: 3,
			Kind: flow.AxisPrimary, GuardLines: []int{4, 6}, HasDefault: true},
		{Ref: "demo_flg", RefName: "demo_flg", Domain: []string{"N", "Y"}, Sites: 2,
			Kind: flow.AxisSecondary, GuardLines: []int{8}},
	}
	out := renderAxesList("demo.pc", axes)
	for _, want := range []string{
		"- demo.pc: 2 dispatch axis(es)",
		"rank 0  c_flag",
		"primary",
		"domain F,H +default(default)",
		"sites 3",
		"guards 4, 6",
		"rank 1  demo_flg",
		"secondary",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("axes list missing %q:\n%s", want, out)
		}
	}
	if got := renderAxesList("fn.pc", nil); !strings.Contains(got, "no dispatch axis detected") {
		t.Errorf("empty registry must say so, got %q", got)
	}
}

// TestRenderFilterPreview pins the -filter console preview: merged key,
// matched assignments, honest kept blocks (never BodyExtent's span), fold
// counts, and queries; a rejecting expression comes back as the plan's
// positioned error.
func TestRenderFilterPreview(t *testing.T) {
	tree := previewTree(t)
	axes := tree.AxesFor([]byte(previewSrc))
	filter, err := flow.ParseScenarioFilter("c_flag == 'F' || c_flag == 'H'")
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderFilterPreview("demo.pc", tree, axes, filter)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- demo.pc",
		"scenarioFilter: c_flag == 'F' || c_flag == 'H'",
		"merged key: c_flag in {F,H}",
		"matched assignments: c_flag=F, c_flag=H",
		"kept blocks: ",
		"fold: kept ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("filter preview missing %q:\n%s", want, out)
		}
	}
	// A witnessed conjunction folds; an unknown axis is the loud,
	// registry-suggesting reject.
	bad, err := flow.ParseScenarioFilter("c_flag == 'F' && demo_flg == 'Y'")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderFilterPreview("demo.pc", tree, axes, bad); err != nil {
		t.Errorf("reachable F&&Y rejected: %v", err)
	}
	unknown, err := flow.ParseScenarioFilter("bogus == 'F'")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderFilterPreview("demo.pc", tree, axes, unknown); err == nil || !strings.Contains(err.Error(), "unknown axis") {
		t.Errorf("unknown-axis preview error = %v, want the registry suggestion", err)
	}
}

// TestRenderScenarioDraftFilterExamples pins the draft seam: the examples
// land commented (never active endpoints) with their matched-values preview,
// and the enumerated scenarioRef entries keep the draft loadable.
func TestRenderScenarioDraftFilterExamples(t *testing.T) {
	tree := previewTree(t)
	axes := tree.AxesFor([]byte(previewSrc))
	axis := tree.DispatchAxisFor([]byte(previewSrc))
	scens := flow.Scenarios(tree, axis)
	f := &ir.File{Path: "demo.pc"}
	draft := renderScenarioDraft(f, axis, scens, flow.DiffScenarios("SVC_DEMO", scens), nil, false, tree, axes)
	if !strings.Contains(draft, "# scenarioFilter examples") {
		t.Fatalf("draft carries no filter examples:\n%s", draft)
	}
	if strings.Contains(draft, "\n  - scenarioFilter:") {
		t.Errorf("filter example must stay commented:\n%s", draft)
	}
	for _, want := range []string{
		`# - scenarioFilter: "c_flag == 'F' || c_flag == 'H'"`,
		"#     merges to c_flag in {F,H} | matched: c_flag=F, c_flag=H",
		"#     kept ",
	} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft examples missing %q:\n%s", want, draft)
		}
	}
}

// TestReorderArgsFilterValue pins the flag-table contract that broke once:
// `discover --filter <expr>` must split the expression as the flag's value,
// not as the positional target.
func TestReorderArgsFilterValue(t *testing.T) {
	flagArgs, positional := reorderArgs([]string{"--filter", "c_flag == 'F' || c_flag == 'I'", "--stdout", "svc.pc"})
	if len(flagArgs) != 3 || flagArgs[0] != "--filter" || flagArgs[1] != "c_flag == 'F' || c_flag == 'I'" {
		t.Errorf("flagArgs = %v, want --filter + its expression + --stdout", flagArgs)
	}
	if len(positional) != 1 || positional[0] != "svc.pc" {
		t.Errorf("positional = %v, want the entry file only", positional)
	}
}
