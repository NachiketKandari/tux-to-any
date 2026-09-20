package flow

import (
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// chainSrc is a braced if/elseif/else dispatch ladder with a distinct FML
// surface per arm — the fixture for the chain-aware fold's invariants: the
// default arm never leaks into an enumerated value's slice, and the
// `=default` slice's body is exactly the else arm.
const chainSrc = `void SVC_CHAIN(TPSVCINFO *rqst) {
	char c_flag;
	if (c_flag == 'H') {
		if (Fget32(ptr_fml_Ibuffer, FML_COMP_CD, 0, (char *)&a, 0) == -1) {
			Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
			tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
		}
		Fadd32(ptr_fml_Obuffer, FML_HIST, (char *)&a, 0);
	} else if (c_flag == 'F') {
		Fadd32(ptr_fml_Obuffer, FML_FREED, (char *)&b, 0);
	} else {
		Fadd32(ptr_fml_Obuffer, FML_LIST, (char *)&c, 0);
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func chainAxis(t *testing.T) (*Tree, *DispatchAxis) {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(chainSrc), "chain.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(chainSrc), facts, "SVC_CHAIN", nil)
	axis := tree.DispatchAxisFor([]byte(chainSrc))
	if axis == nil {
		t.Fatal("no dispatch axis detected for the chain fixture")
	}
	return tree, axis
}

func TestChainAxisHasDefault(t *testing.T) {
	_, axis := chainAxis(t)
	if !axis.HasDefault {
		t.Error("a dispatch chain ending in else must set HasDefault")
	}
	if key := axis.DefaultKey(); key != "default" {
		t.Errorf("DefaultKey = %q, want default", key)
	}
	if got := axis.String(); !strings.Contains(got, "+default") {
		t.Errorf("axis String = %q, want the +default suffix", got)
	}
}

func TestDefaultKeyCollidesNever(t *testing.T) {
	axis := &DispatchAxis{Ref: "trn_cd", RefName: "trn_cd", Domain: []string{"A", "default"}}
	if key := axis.DefaultKey(); key != "default_" {
		t.Errorf("DefaultKey = %q, want default_ (the domain claims %q)", key, "default")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestScenarioDefaultArmSlices(t *testing.T) {
	tree, axis := chainAxis(t)
	scens := Scenarios(tree, axis)
	if len(scens) != len(axis.Domain)+1 {
		t.Fatalf("scenarios = %d, want %d (one per domain value + the default arm)", len(scens), len(axis.Domain)+1)
	}
	var def *Scenario
	for _, sc := range scens {
		if sc.Value == axis.DefaultKey() {
			def = sc
		}
	}
	if def == nil {
		t.Fatalf("no %s=%s slice among the scenarios", axis.Key(), axis.DefaultKey())
	}
	// The default slice's body is exactly the else arm: its census names
	// the else arm's response write and none of the enumerated arms'.
	if !contains(def.Adds, "FML_LIST") || contains(def.Adds, "FML_HIST") || contains(def.Adds, "FML_FREED") {
		t.Errorf("default slice adds = %v, want only the else arm's FML_LIST", def.Adds)
	}
	// The enumerated slices never carry the else arm's surface (the leak
	// the pre-chain-aware fold shipped: cur_mf_nav_list rode every slice).
	for _, sc := range scens {
		if sc.Value == axis.DefaultKey() {
			continue
		}
		if contains(sc.Adds, "FML_LIST") {
			t.Errorf("scenario %s leaks the else arm's FML_LIST: %v", sc.Key, sc.Adds)
		}
	}
}

func TestScenarioArmPartition(t *testing.T) {
	tree, axis := chainAxis(t)
	scens := Scenarios(tree, axis)
	// Every chain arm's header line is kept by exactly one scenario —
	// the arms partition the dispatch ladder, no arm is absorbed twice
	// and none vanishes.
	var arms []*Node
	for i := 0; i < len(tree.Root); {
		end := chainExtent(tree.Root, i)
		if tree.Root[i].Kind == KindBranch && tree.Root[i].Sub == "if" {
			arms = append(arms, tree.Root[i:end]...)
		}
		i = end
	}
	if len(arms) != 3 {
		t.Fatalf("chain arms = %d, want 3", len(arms))
	}
	for _, arm := range arms {
		// The arm's first body line carries the check: a chain header can
		// share its line with the previous arm's closing brace (`} else if
		// …`), so header-line membership spans two arms by construction.
		body := arm.Line + 1
		if body > arm.EndLine {
			body = arm.EndLine
		}
		keepers := 0
		for _, sc := range scens {
			if KeptLines(sc, tree)[body] {
				keepers++
			}
		}
		if keepers != 1 {
			t.Errorf("arm at line %d kept by %d scenarios, want exactly 1", arm.Line, keepers)
		}
	}
}

// TestKeptBlocksNeverBridgeDroppedArms pins the honest-block form: the H
// slice's blocks stop at every dropped gap (BodyExtent's min-max would
// claim the whole 1..N span), and the block union is exactly KeptLines.
func TestKeptBlocksNeverBridgeDroppedArms(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	primary := tree.AxesFor([]byte(nestedAxisSrc))[0]
	sc := ScenarioFor(tree, primary, "H")
	blocks := KeptBlocks(sc, tree)
	if len(blocks) < 2 {
		t.Fatalf("blocks = %v, want at least two (the dropped F/I arms create gaps)", blocks)
	}
	kept := KeptLines(sc, tree)
	covered := 0
	for _, b := range blocks {
		for l := b[0]; l <= b[1]; l++ {
			if !kept[l] {
				t.Errorf("block %v covers dropped line %d", b, l)
			}
			covered++
		}
	}
	if covered != len(kept) {
		t.Errorf("blocks cover %d lines, KeptLines has %d", covered, len(kept))
	}
	if KeptBlockLines(blocks) != len(kept) {
		t.Errorf("KeptBlockLines = %d, want %d", KeptBlockLines(blocks), len(kept))
	}
	if ext := sc.BodyExtent(); ext[1]-ext[0]+1 == covered {
		t.Errorf("fixture has no gaps — BodyExtent(%v) equals the honest %d lines", ext, covered)
	}
}

// unbracedSrc pins the default-arm detection on unbraced chains: the
// condition inventory never sees unbraced arms, but the tree keeps them —
// the else arm must still surface as a sliceable default.
const unbracedSrc = `void SVC_UB(TPSVCINFO *rqst) {
	char c_flag;
	if (c_flag == 'H')
		work_h();
	else if (c_flag == 'F')
		work_f();
	else
		work_d();
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisDefaultUnbraced(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(unbracedSrc), "ub.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(unbracedSrc), facts, "SVC_UB", nil)
	axis := tree.DispatchAxisFor([]byte(unbracedSrc))
	if axis == nil {
		t.Fatal("no axis for the unbraced ladder")
	}
	if !axis.HasDefault {
		t.Error("an unbraced chain ending in else must set HasDefault (the inventory never sees unbraced arms — the tree is the truth)")
	}
}

// TestConditionIndexOfSharedLineElseIf pins the header-line match: `} else
// if` sharing the previous arm's closing-brace line sits inside both
// spans, and a smallest-span tie-break alone steals the header for the
// previous arm whenever that arm is the smaller one.
func TestConditionIndexOfSharedLineElseIf(t *testing.T) {
	elseifNode := &Node{Kind: KindBranch, Sub: "elseif", Line: 4, EndLine: 9}
	conds := []ir.Condition{
		{Index: 1, Kind: "if", StartLine: 2, EndLine: 4},
		{Index: 2, Kind: "elseif", StartLine: 4, EndLine: 9},
	}
	if got := conditionIndexOf(elseifNode, conds); got != 2 {
		t.Errorf("conditionIndexOf(elseif) = %d, want 2 (stole the previous arm's condition)", got)
	}
	if got := conditionIndexOf(&Node{Kind: KindBranch, Sub: "if", Line: 2, EndLine: 4}, conds); got != 1 {
		t.Errorf("conditionIndexOf(if) = %d, want 1", got)
	}
}

// TestDiscoverNestedKeyInvariants pins the candidate-key contract: keys
// are unique across the candidate set, and every emitted ref resolves —
// through the exact derivation plan/gen use — to the block the draft
// entry described. The pre-fix walkChildren renumbered nested candidates
// per invocation, colliding with later siblings and silently drifting
// ConditionFor's DFS replay to different blocks.
func TestDiscoverNestedKeyInvariants(t *testing.T) {
	q := func(get, add string) []ir.FmlOp {
		return []ir.FmlOp{{Kind: ir.FmlGet, Field: get}, {Kind: ir.FmlAdd, Field: add}}
	}
	a1 := &Node{Kind: KindBranch, Line: 4, EndLine: 5, FmlOps: q("FML_A1", "OUT_A1")}
	a := &Node{
		Kind: KindBranch, Line: 3, EndLine: 6,
		FmlOps:   []ir.FmlOp{{Kind: ir.FmlAdd, Field: "FML_ERR_MSG", Error: true}},
		Children: []*Node{a1},
	}
	b := &Node{Kind: KindBranch, Line: 7, EndLine: 8, FmlOps: q("FML_B", "OUT_B")}
	c := &Node{Kind: KindBranch, Line: 9, EndLine: 10, FmlOps: q("FML_C", "OUT_C")}
	root := &Node{Kind: KindBranch, Line: 2, EndLine: 11, Children: []*Node{a, b, c}}
	tree := &Tree{Root: []*Node{root}}
	conds := []ir.Condition{{Index: 1, StartLine: 2, EndLine: 11}}

	got := Discover(tree, conds)
	seen := map[string][]int{}
	for _, cand := range got {
		seen[cand.Key] = append(seen[cand.Key], cand.StartLine)
	}
	for k, lines := range seen {
		if len(lines) > 1 {
			t.Errorf("duplicate candidate key %q held by candidates at lines %v", k, lines)
		}
	}
	for _, cand := range got {
		cond, err := ConditionFor(tree, conds, cand.Key)
		if err != nil {
			t.Errorf("ConditionFor(%q): %v", cand.Key, err)
			continue
		}
		if cond.StartLine != cand.StartLine {
			t.Errorf("ref %q resolves to a block at line %d but its draft entry sits at line %d (silent wrong-block conversion)", cand.Key, cond.StartLine, cand.StartLine)
		}
	}
}
