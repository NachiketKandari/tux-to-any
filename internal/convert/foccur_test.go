package convert

import (
	"strings"
	"testing"

	"tux-to-any/internal/flow"
	"tux-to-any/internal/pred"
)

// TestFoccurGuardEquivalence pins the logical-part gate: the legacy
// existence check and the Go presence form are the same predicate —
// any buffer spelling, either polarity, request-qualified or bare.
func TestFoccurGuardEquivalence(t *testing.T) {
	bind := newGuardBindings()
	bind.claim("FmlThing", "FML_FML_THING")
	bind.claimIdent("FML_FML_THING")
	cases := []struct {
		legacy string
		goCond string
		want   bool
	}{
		{"Foccur32(fml_ibuffer, FML_FML_THING) > 0", `request.FmlThing != ""`, true},
		{"Foccur32(other_buf, FML_FML_THING) > 0", `FmlThing != ""`, true},
		{"Foccur32(fml_ibuffer, FML_FML_THING) == 0", `request.FmlThing == ""`, true},
		{"foccur32(fml_ibuffer, FML_FML_THING) > 0", `request.FmlThing != ""`, true},
		{"Foccur32(fml_ibuffer, FML_FML_THING) > 0", `request.FmlThing == ""`, false},
		{"Foccur32(fml_ibuffer, FML_FML_THING) > 0", `request.Other != ""`, false},
		{"Foccur32(fml_ibuffer, FML_FML_THING) > 0", `cnt > 0`, false},
	}
	for _, tc := range cases {
		a, b := pred.ParseCode(tc.legacy), pred.ParseCode(tc.goCond)
		if got := pred.Equivalent(&a, &b, bind.same); got != tc.want {
			t.Errorf("Equivalent(%q, %q) = %v, want %v", tc.legacy, tc.goCond, got, tc.want)
		}
	}
}

// TestFoccurGuardGateRetention pins the end-to-end guard: a MixedGuard
// carrying the legacy foccur predicate accepts the Go presence body and
// rejects a dropped guard.
func TestFoccurGuardGateRetention(t *testing.T) {
	sc := &flow.Scenario{
		Key: "presence",
		Body: []*flow.SliceNode{
			{Kind: flow.KindBranch, Line: 10, Fold: flow.FoldMixed,
				Cond: "Foccur32(fml_ibuffer, FML_FML_THING) > 0",
				FoldedCond: "Foccur32(fml_ibuffer, FML_FML_THING) > 0",
				Children: []*flow.SliceNode{{Kind: flow.KindStmt, Line: 11}}},
		},
	}
	guards := flow.MixedGuards(sc)
	if len(guards) != 1 {
		t.Fatalf("guards = %+v, want the foccur mixed guard", guards)
	}
	bind := guardBindingsFor(guards, "", nil, nil)
	keep := "if request.FmlThing != \"\" {\n\t_ = request\n}\nreturn data, err"
	if errs := scenarioGuardErrs(guards, bind, keep); len(errs) != 0 {
		t.Errorf("presence body rejected: %v", errs)
	}
	drop := "return data, err"
	if errs := scenarioGuardErrs(guards, bind, drop); len(errs) == 0 {
		t.Error("dropped presence guard accepted — want a reject")
	}
	// Inverted polarity must not pass.
	flip := "if request.FmlThing == \"\" {\n\t_ = request\n}\nreturn data, err"
	if errs := scenarioGuardErrs(guards, bind, flip); len(errs) == 0 {
		t.Error("inverted presence accepted — want a reject")
	}
}

// TestFoccurConditionPresence pins the census gate: the legacy foccur
// census entry is satisfied by the Go presence if with its effect.
func TestFoccurConditionPresence(t *testing.T) {
	census := []flow.CensusCond{
		{Line: 10, Cond: "Foccur32(fml_ibuffer, FML_FML_THING) > 0",
			Skeleton: `# != ""`, Idents: []string{"Foccur32", "fml_ibuffer", "FML_FML_THING"},
			Effects: []string{"c_flag"}},
	}
	body := "cFlag := \"N\"\nif request.FmlThing != \"\" {\n\tcFlag = \"Y\"\n}\nreturn data, err"
	if errs := conditionPresenceErrs(census, body); len(errs) != 0 {
		t.Errorf("presence body rejected: %v", errs)
	}
	joined := strings.Join(conditionPresenceErrs(census, "return data, err"), "; ")
	if !strings.Contains(joined, "line 10") || !strings.Contains(joined, "lost — implement the branch") {
		t.Errorf("dropped presence must fail with the line note, got %q", joined)
	}
}
