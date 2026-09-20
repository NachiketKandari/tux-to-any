package flow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustFilter(t *testing.T, text string) *ScenarioFilter {
	t.Helper()
	f, err := ParseScenarioFilter(text)
	if err != nil {
		t.Fatalf("ParseScenarioFilter(%q): %v", text, err)
	}
	return f
}

// TestScenarioForFilterSingleValueEqualsScenarioFor is the equivalence
// property: for a plain single-value filter the re-fold is exactly the
// ScenarioFor slice (same body, preamble, counts, default mark) — the
// filter path can never drift from the slicer it generalizes.
func TestScenarioForFilterSingleValueEqualsScenarioFor(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	src := []byte(nestedAxisSrc)
	registry := tree.AxesFor(src)
	primary := registry[0]
	for _, value := range []string{"F", "H", "I", primary.DefaultKey()} {
		text := "c_flag == '" + value + "'"
		got, err := ScenarioForFilter(tree, registry, mustFilter(t, text))
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		want := ScenarioFor(tree, primary, value)
		gotEvidence := *got
		gotEvidence.Filter, gotEvidence.FilterMatched, gotEvidence.FilterPruned = "", nil, nil
		if !reflect.DeepEqual(&gotEvidence, want) {
			gotJSON, _ := jsonOf(&gotEvidence)
			wantJSON, _ := jsonOf(want)
			t.Errorf("%s: filter scenario != ScenarioFor:\n%s\nvs\n%s", text, gotJSON, wantJSON)
		}
		if got.Filter != text {
			t.Errorf("%s: scenario filter evidence = %q, want the expression verbatim", text, got.Filter)
		}
	}
}

// TestScenarioForFilterUnionKeepsGuards pins the re-fold rule for ||:
// neither arm unwraps; both guards stay live, contradicted arms drop, and
// the default arm dies because every matching assignment is covered by an
// earlier arm.
func TestScenarioForFilterUnionKeepsGuards(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	sc, err := ScenarioForFilter(tree, tree.AxesFor([]byte(nestedAxisSrc)), mustFilter(t, "c_flag == 'F' || c_flag == 'I'"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Key != "c_flag in {F,I}" {
		t.Errorf("key = %q, want c_flag in {F,I}", sc.Key)
	}
	byLine := bodyByLine(sc.Body)
	f := byLine[4] // if (c_flag == 'F')
	h := byLine[6] // else if (c_flag == 'H')
	i := byLine[14]
	el := byLine[16] // else
	if f == nil || f.Fold != FoldMixed || !strings.Contains(f.FoldedCond, "c_flag == 'F'") {
		t.Errorf("F arm = %+v, want mixed with its guard kept", f)
	}
	if i == nil || i.Fold != FoldMixed || !strings.Contains(i.FoldedCond, "c_flag == 'I'") {
		t.Errorf("I arm = %+v, want mixed with its guard kept", i)
	}
	if h != nil {
		t.Errorf("H arm = %+v, want dropped (contradicted under both assignments)", h)
	}
	if el != nil {
		t.Errorf("default arm = %+v, want dropped (all assignments taken)", el)
	}
	if sc.Var != "c_flag" || sc.Value != "F_or_I" {
		t.Errorf("var/value = %q/%q, want c_flag/F_or_I", sc.Var, sc.Value)
	}
	if got := strings.Join(sc.FilterMatched, "; "); got != "c_flag=F; c_flag=I" {
		t.Errorf("filter matched = %q, want the two union assignments in key order", got)
	}
	if len(sc.FilterPruned) != 0 {
		t.Errorf("filter pruned = %v, want none (both arms are witnessed)", sc.FilterPruned)
	}
}

// TestScenarioForFilterIntersection pins && across a secondary axis: the H
// arm and its nested K arm unwrap (satisfied), J and both default arms
// drop, and the key becomes the combined assignment.
func TestScenarioForFilterIntersection(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	sc, err := ScenarioForFilter(tree, tree.AxesFor([]byte(nestedAxisSrc)), mustFilter(t, "c_flag == 'H' && new_flag == 'K'"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Key != "c_flag=H && new_flag=K" {
		t.Errorf("key = %q, want c_flag=H && new_flag=K", sc.Key)
	}
	if got := strings.Join(sc.FilterMatched, "; "); got != "c_flag=H && new_flag=K" {
		t.Errorf("filter matched = %q, want the one intersection assignment", got)
	}
	byLine := bodyByLine(sc.Body)
	h := byLine[6]
	k := byLine[7]
	j := byLine[9]
	innerElse := byLine[11]
	if h == nil || h.Fold != FoldSatisfied {
		t.Errorf("H arm = %+v, want satisfied", h)
	}
	if k == nil || k.Fold != FoldSatisfied {
		t.Errorf("K arm = %+v, want satisfied", k)
	}
	if j != nil {
		t.Errorf("J arm = %+v, want dropped", j)
	}
	if innerElse != nil {
		t.Errorf("nested default = %+v, want dropped", innerElse)
	}
	for _, line := range []int{4, 14, 16} { // F, I, outer else
		if n := byLine[line]; n != nil {
			t.Errorf("line %d = %+v, want dropped", line, n)
		}
	}
}

// TestScenarioForFilterNotEqualsKeepsDefault pins != semantics: the
// complement values keep their guards live and the default arm stays (it is
// one of the matching assignments).
func TestScenarioForFilterNotEqualsKeepsDefault(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	sc, err := ScenarioForFilter(tree, tree.AxesFor([]byte(nestedAxisSrc)), mustFilter(t, "c_flag != 'H'"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Key != "c_flag in {F,I,default}" {
		t.Errorf("key = %q, want c_flag in {F,I,default}", sc.Key)
	}
	if !sc.Default {
		t.Error("default = false, want true (the default arm is a surviving assignment)")
	}
	if got := strings.Join(sc.FilterMatched, "; "); got != "c_flag=F; c_flag=I; c_flag=default" {
		t.Errorf("filter matched = %q, want the complement plus the default arm", got)
	}
	byLine := bodyByLine(sc.Body)
	if n := byLine[6]; n != nil {
		t.Errorf("H arm = %+v, want dropped", n)
	}
	if n := byLine[16]; n == nil || n.Fold != FoldKept {
		t.Errorf("default arm = %+v, want kept below the live guards", n)
	}
}

// crossedAxisSrc nests the secondary chain inside the default arm only —
// the fixture for the reachability prune (H, and F, never co-occur with K).
const crossedAxisSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char c_flag = 'F';
	char new_flag = 'K';
	if (c_flag == 'F') {
		work_f();
	} else if (c_flag == 'H') {
		work_h();
	} else {
		if (new_flag == 'K') {
			work_k();
		} else if (new_flag == 'J') {
			work_j();
		} else {
			work_other();
		}
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// TestScenarioForFilterReachabilityPrune pins the correlated-axis rule: a
// conjunction whose values never co-occur (K lives under the default arm,
// not under H) is a hard error naming the unwitnessed value — never a
// silently narrowed endpoint. The valid counterpart (default && K) folds.
func TestScenarioForFilterReachabilityPrune(t *testing.T) {
	tree := axesTree(t, crossedAxisSrc, nil)
	registry := tree.AxesFor([]byte(crossedAxisSrc))
	if len(registry) != 2 {
		t.Fatalf("registry = %d axes, want 2", len(registry))
	}
	for _, text := range []string{
		"c_flag == 'H' && new_flag == 'K'",
		"c_flag == 'F' && new_flag == 'K'",
	} {
		_, err := ScenarioForFilter(tree, registry, mustFilter(t, text))
		if err == nil {
			t.Fatalf("%s: no error, want the unreachable-conjunction reject", text)
		}
		if !strings.Contains(err.Error(), "no reachable arm") || !strings.Contains(err.Error(), "new_flag") {
			t.Errorf("%s: error = %v, want it naming the unwitnessed new_flag", text, err)
		}
	}
	sc, err := ScenarioForFilter(tree, registry, mustFilter(t, "c_flag == 'default' && new_flag == 'K'"))
	if err != nil {
		t.Fatalf("default && K: %v", err)
	}
	if got := strings.Join(sc.FilterMatched, "; "); got != "c_flag=default && new_flag=K" {
		t.Errorf("filter matched = %q, want the default&&K assignment", got)
	}
	byLine := bodyByLine(sc.Body)
	if n := byLine[8]; n == nil || n.Fold != FoldKept {
		t.Errorf("default arm = %+v, want kept", n)
	}
	if n := byLine[9]; n == nil || n.Fold != FoldSatisfied {
		t.Errorf("K arm = %+v, want satisfied under the default assignment", n)
	}
	if n := byLine[11]; n != nil {
		t.Errorf("J arm = %+v, want dropped", n)
	}
}

// TestScenarioForFilterUnionWitnessNoCrossTalk pins the reach-gated witness
// rule: in a union, a guard walked because another assignment reaches its
// arm never witnesses an assignment that cannot — `(H && K) || (default &&
// K)` keeps default&&K and prunes H&&K (K never lives under H), instead of
// cross-witnessing K through the default arm's nested chain.
func TestScenarioForFilterUnionWitnessNoCrossTalk(t *testing.T) {
	tree := axesTree(t, crossedAxisSrc, nil)
	registry := tree.AxesFor([]byte(crossedAxisSrc))
	sc, err := ScenarioForFilter(tree, registry,
		mustFilter(t, "(c_flag == 'H' && new_flag == 'K') || (c_flag == 'default' && new_flag == 'K')"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sc.FilterMatched, "; "); got != "c_flag=default && new_flag=K" {
		t.Errorf("matched = %q, want only the reachable default&&K assignment", got)
	}
	if len(sc.FilterPruned) != 1 || !strings.Contains(sc.FilterPruned[0], "c_flag=H && new_flag=K") {
		t.Errorf("pruned = %v, want the H&&K assignment with its no-reachable reason", sc.FilterPruned)
	}
	byLine := bodyByLine(sc.Body)
	if n := byLine[6]; n != nil {
		t.Errorf("H arm = %+v, want dropped (K does not live under H)", n)
	}
	if n := byLine[9]; n == nil || n.Fold != FoldSatisfied {
		t.Errorf("K arm = %+v, want satisfied under the default assignment", n)
	}
}

// TestFilterExamplesFoldVerified pins the draft-example helper (plan §5):
// the union over the primary's first two values plus the first reachable
// primary×secondary conjunction — the unreachable F&&K candidate is skipped
// by the real fold, never commented.
func TestFilterExamplesFoldVerified(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	axes := tree.AxesFor([]byte(nestedAxisSrc))
	exs := FilterExamples(tree, axes)
	if len(exs) != 2 {
		t.Fatalf("examples = %+v, want the union + intersection pair", exs)
	}
	if exs[0].Expr != "c_flag == 'F' || c_flag == 'H'" || exs[0].Key != "c_flag in {F,H}" {
		t.Errorf("union example = %q → %q", exs[0].Expr, exs[0].Key)
	}
	if exs[1].Expr != "c_flag == 'H' && new_flag == 'J'" {
		t.Errorf("intersection example = %q, want the first reachable H×new_flag pair (F×K/F×J are never witnessed)", exs[1].Expr)
	}
	for _, ex := range exs {
		if ex.Lines == 0 || len(ex.Blocks) == 0 {
			t.Errorf("example %q lacks block evidence: %+v", ex.Expr, ex)
		}
	}
	if got := FilterExamples(tree, nil); got != nil {
		t.Errorf("no registry must yield no examples, got %v", got)
	}
}

// TestScenarioForFilterErrors pins the v1 language boundary: every reject
// names the atom and, for identifiers, the registry suggestion.
func TestScenarioForFilterErrors(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	registry := tree.AxesFor([]byte(nestedAxisSrc))
	cases := []struct {
		text string
		want string
	}{
		{"c_flag > 'F'", "unsupported atom"},
		{"c_flag == new_flag", "unsupported atom"},
		{"length(c_flag) == 1", "unsupported atom"},
		{"c_flag", "unsupported atom"},
		{"c_flg == 'F'", `did you mean "c_flag"`},
		{"c_flag == 'X'", "matches nothing (domain F,H,I,default)"},
		{"c_flag == 'H' && c_flag == 'K'", "matches nothing (domain F,H,I,default)"},
		{"c_flag == 'F' && c_flag != 'F'", "contradictory literals for c_flag"},
		{"c_flag != 'X'", "matches nothing"},
	}
	for _, tc := range cases {
		f, err := ParseScenarioFilter(tc.text)
		if err == nil {
			_, err = ScenarioForFilter(tree, registry, f)
		}
		if err == nil {
			t.Errorf("%s: no error, want %q", tc.text, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.text, err, tc.want)
		}
	}
	if _, err := ParseScenarioFilter("   "); err == nil {
		t.Error("empty filter: no error")
	}
	if _, err := ParseScenarioFilter("c_flag == 'F' ||"); err == nil {
		t.Error("dangling ||: no error")
	}
}

// TestParseScenarioFilterVars pins the identifier inventory order (first
// use, deduped) — the discover/filter preview reads it.
func TestParseScenarioFilterVars(t *testing.T) {
	f := mustFilter(t, "new_flag == 'K' && (c_flag == 'F' || c_flag != 'I')")
	if want := []string{"new_flag", "c_flag"}; !reflect.DeepEqual(f.Vars, want) {
		t.Errorf("vars = %v, want %v", f.Vars, want)
	}
}

// TestScenarioForFilterSecondaryOnly pins the nested-preamble split: a
// filter on a secondary axis keeps the outer dispatch chain live
// (non-axis atoms never drop code — they ride along as runtime guards) and
// folds the nested guards underneath it.
func TestScenarioForFilterSecondaryOnly(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	sc, err := ScenarioForFilter(tree, tree.AxesFor([]byte(nestedAxisSrc)), mustFilter(t, "new_flag == 'K'"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.Key != "new_flag=K" {
		t.Errorf("key = %q, want new_flag=K", sc.Key)
	}
	byLine := bodyByLine(sc.Body)
	for _, line := range []int{4, 6, 14, 16} { // F, H, I, outer default
		if n := byLine[line]; n == nil || n.Fold != FoldKept {
			t.Errorf("outer chain line %d = %+v, want kept verbatim", line, n)
		}
	}
	if n := byLine[7]; n == nil || n.Fold != FoldSatisfied {
		t.Errorf("K arm = %+v, want satisfied", n)
	}
	if n := byLine[9]; n != nil {
		t.Errorf("J arm = %+v, want dropped (contradicted)", n)
	}
	if n := byLine[11]; n != nil {
		t.Errorf("nested default = %+v, want dropped (K arm taken)", n)
	}
}

// bodyByLine indexes every body node (nested included) by source line.
func bodyByLine(nodes []*SliceNode) map[int]*SliceNode {
	out := map[int]*SliceNode{}
	var walk func(ns []*SliceNode)
	walk = func(ns []*SliceNode) {
		for _, n := range ns {
			out[n.Line] = n
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// jsonOf renders a value for diff-style failure output.
func jsonOf(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return string(data), err
}
