package flow

import (
	"testing"

	"tux-to-any/internal/ir"
)

// shadowedResponseSrc is the P3B regression shape at its smallest: ONE FML
// field written twice in a single scenario, to two different buffers and two
// different targets.
//
// The preamble (before the dispatch spine) writes FML_POINT_TYPE to the
// *input* buffer from a host variable — a default/guard write. The `c_flag ==
// 'A'` arm writes the same field to the *output* buffer from a different
// variable — the actual response value.
//
// Keying the scenario-condition dedup on kind+field alone made the preamble
// write claim FML_POINT_TYPE and silently discard the arm's write, so the
// response value was unrecoverable downstream and every endpoint that
// happened to share that spine emitted the field as a zero value.
const shadowedResponseSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char c_flag = 'F';
	char sql_default_val = 'Y';
	sql_answer_id = ' ';
	if(Fadd32(ptr_fml_Ibuffer, FML_POINT_TYPE, (char *)&sql_default_val, 0) == -1) {
		return;
	}
	if (c_flag == 'A') {
		i_err[4] = Fadd32(ptr_fml_Obuffer, FML_POINT_TYPE, (char *)&sql_answer_id, 0);
	} else if (c_flag == 'B') {
		other();
	} else {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// scenarioResponseOps returns the condition's FML ops for one axis value.
func scenarioResponseOps(t *testing.T, src, value string) *ir.Condition {
	t.Helper()
	tree := axesTree(t, src, nil)
	axis := tree.DispatchAxisFor([]byte(src))
	if axis == nil {
		t.Fatalf("no dispatch axis detected in fixture")
	}
	sc := ScenarioFor(tree, axis, value)
	if sc == nil {
		t.Fatalf("no scenario for value %q", value)
	}
	c := ScenarioCondition(sc, tree)
	if c == nil {
		t.Fatalf("ScenarioCondition returned nil for value %q", value)
	}
	return c
}

// TestScenarioConditionKeepsEveryWriteOfOneField is the P3B pin: a field
// written more than once in a scenario keeps every distinct write. The old
// kind+field key collapsed them to whichever came first in source order,
// which is the preamble's default rather than the branch's value.
func TestScenarioConditionKeepsEveryWriteOfOneField(t *testing.T) {
	c := scenarioResponseOps(t, shadowedResponseSrc, "A")

	var pointType []ir.FmlOp
	for _, op := range c.FmlOps {
		if op.Kind == ir.FmlAdd && op.Field == "FML_POINT_TYPE" {
			pointType = append(pointType, op)
		}
	}
	if len(pointType) != 2 {
		t.Fatalf("FML_POINT_TYPE writes kept = %d, want 2 (preamble default + arm value); got %+v",
			len(pointType), pointType)
	}

	// Source order: the preamble write precedes the arm's.
	if got, want := pointType[0].Target, "sql_default_val"; got != want {
		t.Errorf("first write target = %q, want %q (the preamble default)", got, want)
	}
	if got, want := pointType[1].Target, "sql_answer_id"; got != want {
		t.Errorf("second write target = %q, want %q (the arm's response value)", got, want)
	}
	// The buffer distinguishes them, and it is what makes the arm's write
	// identifiable as the response source.
	if got := pointType[1].Buffer; got != "ptr_fml_Obuffer" {
		t.Errorf("arm write buffer = %q, want ptr_fml_Obuffer", got)
	}
}

// TestScenarioConditionKeepsSameFieldAtDifferentTargets is the narrower
// property the shadowing case generalizes: two writes of one field to the
// same buffer but different targets are both real, and neither may be
// discarded as a duplicate.
//
// It reaches both arms in ONE scenario by re-folding with a || filter, rather
// than by picking a single arm — a single arm holds only one write, so it
// would not exercise the collapse at all. This is the pure form of the bug:
// same kind, same field, same buffer, different target.
func TestScenarioConditionKeepsSameFieldAtDifferentTargets(t *testing.T) {
	src := `void SVC_DEMO(TPSVCINFO *rqst) {
	char c_flag = 'F';
	if (c_flag == 'A') {
		i_err[4] = Fadd32(ptr_fml_Obuffer, FML_POINT_TYPE, (char *)&sql_arm_a, 0);
	} else if (c_flag == 'B') {
		i_err[4] = Fadd32(ptr_fml_Obuffer, FML_POINT_TYPE, (char *)&sql_arm_b, 0);
	} else {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	tree := axesTree(t, src, nil)
	registry := tree.AxesFor([]byte(src))
	if len(registry) == 0 {
		t.Fatalf("no dispatch axis detected in fixture")
	}
	sc, err := ScenarioForFilter(tree, registry, mustFilter(t, "c_flag == 'A' || c_flag == 'B'"))
	if err != nil {
		t.Fatalf("ScenarioForFilter: %v", err)
	}
	c := ScenarioCondition(sc, tree)

	targets := map[string]bool{}
	for _, op := range c.FmlOps {
		if op.Kind == ir.FmlAdd && op.Field == "FML_POINT_TYPE" {
			targets[op.Target] = true
		}
	}
	if !targets["sql_arm_a"] || !targets["sql_arm_b"] {
		t.Errorf("FML_POINT_TYPE targets = %v, want both arms kept (sql_arm_a and sql_arm_b)", targets)
	}
}

// TestScenarioConditionDeduplicatesSameWriteAcrossNodes is the guard on the
// other side: annotate attaches each op to every node whose span contains it,
// so one write is seen many times during the walk. Widening the key must not
// turn that benign re-visit into duplicate entries.
func TestScenarioConditionDeduplicatesSameWriteAcrossNodes(t *testing.T) {
	c := scenarioResponseOps(t, shadowedResponseSrc, "A")

	counts := map[string]int{}
	for _, op := range c.FmlOps {
		if op.Kind == ir.FmlAdd {
			counts[op.Field+"→"+op.Target+"@"+op.Buffer]++
		}
	}
	for key, n := range counts {
		if n != 1 {
			t.Errorf("%s appears %d times — one write must yield one op", key, n)
		}
	}
}
