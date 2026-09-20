package flow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// axesTree builds the entry tree the way every consumer does (scanner →
// Build) with an optional define table for the symbolic recognizer.
func axesTree(t *testing.T, src string, irf *ir.File) *Tree {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(src), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	if irf == nil {
		irf = &ir.File{}
	}
	return Build([]byte(src), facts, "SVC_DEMO", irf)
}

// TestAxesForRankZeroMatchesDispatchAxisFor is the zero-churn contract: for
// every fixture whose DispatchAxisFor detects a spine, the registry's rank 0
// is that axis, field for field — the filter feature can never drift from
// the slicer it builds on.
func TestAxesForRankZeroMatchesDispatchAxisFor(t *testing.T) {
	cases := []struct {
		name string
		src  string
		irf  *ir.File
	}{
		{name: "normalize-chain", src: normChainSrc},
		{name: "direct-strcmp", src: directStrcmpSrc},
		{name: "char-compare", src: charCompareSrc},
		{name: "symbolic", src: symbolicSrc, irf: &ir.File{Defines: []ir.Define{
			{Name: "MAKE_FEE", Value: "'A'"},
			{Name: "KILL_FEE", Value: "'B'"},
			{Name: "MIX_FEE", Value: "'C'"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := axesTree(t, tc.src, tc.irf)
			want := tree.DispatchAxisFor([]byte(tc.src))
			if want == nil {
				t.Fatalf("fixture no longer detects a dispatch axis")
			}
			axes := tree.AxesFor([]byte(tc.src))
			if len(axes) == 0 {
				t.Fatalf("AxesFor empty, want rank 0 %v", want)
			}
			got := axes[0]
			if got.Ref != want.Ref || got.RefName != want.RefName || got.Alias != want.Alias ||
				!reflect.DeepEqual(got.Domain, want.Domain) || got.Sites != want.Sites ||
				got.Normalized != want.Normalized || got.HasDefault != want.HasDefault {
				t.Errorf("rank 0 = %+v, want DispatchAxisFor %+v", got, want)
			}
			if got.Kind != AxisPrimary {
				t.Errorf("rank 0 kind = %q, want %q", got.Kind, AxisPrimary)
			}
			if len(got.GuardLines) < 2 {
				t.Errorf("rank 0 guard lines = %v, want the ≥2 rubric evidence", got.GuardLines)
			}
		})
	}
}

// nestedAxisSrc pins the secondary-axis shape: the primary dispatches on
// c_flag (F/H/I + else), and the H arm nests a new_flag chain (K/J + else).
const nestedAxisSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char c_flag = 'F';
	char new_flag = 'K';
	if (c_flag == 'F') {
		work_f();
	} else if (c_flag == 'H') {
		if (new_flag == 'K') {
			work_k();
		} else if (new_flag == 'J') {
			work_j();
		} else {
			work_other();
		}
	} else if (c_flag == 'I') {
		work_i();
	} else {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestAxesForSecondaryNestedAxis(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	axes := tree.AxesFor([]byte(nestedAxisSrc))
	if len(axes) != 2 {
		ids := make([]string, 0, len(axes))
		for _, a := range axes {
			ids = append(ids, a.Key())
		}
		t.Fatalf("axes = %v, want [c_flag new_flag]", ids)
	}
	primary, secondary := axes[0], axes[1]
	if primary.Key() != "c_flag" || !reflect.DeepEqual(primary.Domain, []string{"F", "H", "I"}) {
		t.Errorf("primary = %s domain %v, want c_flag [F H I]", primary.Key(), primary.Domain)
	}
	if !primary.HasDefault || primary.Kind != AxisPrimary {
		t.Errorf("primary default/kind = %v/%q, want true/primary", primary.HasDefault, primary.Kind)
	}
	if secondary.Key() != "new_flag" || !reflect.DeepEqual(secondary.Domain, []string{"J", "K"}) {
		t.Errorf("secondary = %s domain %v, want new_flag [J K]", secondary.Key(), secondary.Domain)
	}
	if !secondary.HasDefault {
		t.Error("secondary HasDefault = false, want true — its nested chain ends in else")
	}
	if secondary.Kind != AxisSecondary {
		t.Errorf("secondary kind = %q, want %q", secondary.Kind, AxisSecondary)
	}
	// The secondary's guards must sit inside the primary's H arm, i.e.
	// nested deeper than the primary's top-level guards.
	if len(secondary.GuardLines) < 2 {
		t.Fatalf("secondary guard lines = %v, want ≥2", secondary.GuardLines)
	}
	if guardLinesOverlap(primary.GuardLines, secondary.GuardLines) {
		t.Error("secondary guards overlap the primary's lines — the fixtures must nest, not alias")
	}
}

// TestAxesForAliasTieDeterministic pins the deterministic alias pick: one
// ref linked to two aliases with equal counts must always resolve to the
// lexicographically smaller alias (map iteration is not an order), and the
// other alias must not surface as a separate registry axis.
func TestAxesForAliasTieDeterministic(t *testing.T) {
	const src = `void SVC_DEMO(TPSVCINFO *rqst) {
	char a1, a2;
	if (!(strcmp(sql_x.arr, "A")))
		a1 = 'A';
	if (!(strcmp(sql_x.arr, "A")))
		a2 = 'A';
	if (!(strcmp(sql_x.arr, "B")))
		a1 = 'B';
	if (!(strcmp(sql_x.arr, "B")))
		a2 = 'B';
	if (a1 == 'A') {
		work_a();
	}
	if (a2 == 'A') {
		work_a2();
	}
	if (a2 == 'B') {
		work_b2();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	for i := 0; i < 20; i++ {
		tree := axesTree(t, src, nil)
		axes := tree.AxesFor([]byte(src))
		if len(axes) != 1 {
			t.Fatalf("run %d: axes = %d, want 1 (the alternates collapse)", i, len(axes))
		}
		if got := axes[0].Alias; got != "a1" {
			t.Fatalf("run %d: alias = %q, want lexicographic tie-break a1", i, got)
		}
		if !reflect.DeepEqual(axes[0].Domain, []string{"A", "B"}) {
			t.Fatalf("run %d: domain = %v, want [A B]", i, axes[0].Domain)
		}
	}
}

// TestAxesForRepeatRunByteIdentical is the registry's determinism promise:
// the JSON twin (the artifact artifact consumers diff) is stable run to run.
func TestAxesForRepeatRunByteIdentical(t *testing.T) {
	var first []byte
	for i := 0; i < 5; i++ {
		tree := axesTree(t, nestedAxisSrc, nil)
		data, err := json.Marshal(tree.AxesFor([]byte(nestedAxisSrc)))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = data
			continue
		}
		if string(data) != string(first) {
			t.Fatalf("run %d: registry JSON drifted:\n%s\nvs\n%s", i, data, first)
		}
	}
}

func TestAxesForNoAxis(t *testing.T) {
	tree := axesTree(t, noAxisSrc, nil)
	if axes := tree.AxesFor([]byte(noAxisSrc)); len(axes) != 0 {
		t.Errorf("axes = %v, want none for a file with no dispatch spine", axes)
	}
}

func TestAxesForNilFacts(t *testing.T) {
	tree := &Tree{Function: "SVC_DEMO"}
	if axes := tree.AxesFor([]byte("void SVC_DEMO() {}")); axes != nil {
		t.Errorf("axes = %v, want nil for a tree built without scanner facts", axes)
	}
}

// TestRenderAxesMDAndReport pins the artifact twins' content: the human
// table carries rank/kind/domain/guards/default, and the JSON report
// round-trips the registry unchanged.
func TestRenderAxesMDAndReport(t *testing.T) {
	tree := axesTree(t, nestedAxisSrc, nil)
	axes := tree.AxesFor([]byte(nestedAxisSrc))
	md := RenderAxesMD("mainTux.pc", axes)
	for _, want := range []string{
		"# Dispatch axes — mainTux.pc",
		"| 0 | c_flag | c_flag | c_flag | primary | F, H, I | 3 | 4, 6, 14 | yes (default) |",
		"| 1 | new_flag | new_flag | new_flag | secondary | J, K | 2 | 7, 9 | yes (default) |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("axes md missing %q:\n%s", want, md)
		}
	}
	data, err := json.Marshal(AxesReport{Entry: "mainTux.pc", Axes: axes})
	if err != nil {
		t.Fatal(err)
	}
	var back AxesReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Entry != "mainTux.pc" || len(back.Axes) != 2 {
		t.Fatalf("report round-trip = entry %q axes %d, want mainTux.pc/2", back.Entry, len(back.Axes))
	}
	if back.Axes[0].Kind != AxisPrimary || back.Axes[1].Kind != AxisSecondary {
		t.Errorf("kinds = %q/%q, want primary/secondary", back.Axes[0].Kind, back.Axes[1].Kind)
	}
	if len(back.Axes[0].GuardLines) < 2 {
		t.Errorf("guard lines lost in JSON: %v", back.Axes[0].GuardLines)
	}
}

// guardLinesOverlap reports whether two guard-line sets share any line.
func guardLinesOverlap(a, b []int) bool {
	set := map[int]bool{}
	for _, l := range a {
		set[l] = true
	}
	for _, l := range b {
		if set[l] {
			return true
		}
	}
	return false
}
