package flow

import (
	"strings"
	"testing"

	scanner "tux-to-any/internal/tsscan"
)

// localFuncsSrc is the appendix fixture: a two-value axis where BOTH arms call
// fn_chk_foo (common), only the 'A' arm calls fn_chk_bar (not common), and
// both arms make a tpcall that has no definition in this file (external, so
// nothing to show). fn_chk_foo/fn_chk_bar are defined AFTER the entry, in the
// region the fold never touches.
const localFuncsSrc = `int SVC_DEMO(TPSVCINFO *rqst)
{
    char sql_trn_cd[2];
    int done = 0;

    if (strcmp(sql_trn_cd.arr, "A") == 0)
    {
        fn_chk_foo(rqst);
        fn_chk_bar(rqst);
        tpcall("SRV_X", 0, rqst, 0, 0);
        done = 1;
    }
    if (strcmp(sql_trn_cd.arr, "P") == 0)
    {
        fn_chk_foo(rqst);
        tpcall("SRV_Y", 0, rqst, 0, 0);
        done = 2;
    }

    tpfree((char *)ptr_fml_Obuffer);
    tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}

int fn_chk_foo(TPSVCINFO *rqst)
{
    int rc = 0;
    return rc;
}

int fn_chk_bar(TPSVCINFO *rqst)
{
    int rc = 1;
    return rc;
}
`

func localFuncScenarios(t *testing.T) (*Tree, []*Scenario, []byte) {
	t.Helper()
	src := []byte(localFuncsSrc)
	facts, err := scanner.ScanBytes(src, "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build(src, facts, "SVC_DEMO", nil)
	axis := tree.DispatchAxisFor(src)
	if axis == nil {
		t.Fatalf("fixture no longer detects an axis: %s", localFuncsSrc)
	}
	return tree, Scenarios(tree, axis), src
}

func TestMarkCommonLocalFuncsKeepsOnlySharedHelpers(t *testing.T) {
	tree, scens, _ := localFuncScenarios(t)
	if len(scens) != 2 {
		t.Fatalf("scenarios = %d, want 2", len(scens))
	}
	MarkCommonLocalFuncs(tree, scens)

	for _, sc := range scens {
		var names []string
		for _, lf := range sc.LocalFuncs {
			names = append(names, lf.Name)
		}
		if len(names) != 1 || names[0] != "fn_chk_foo" {
			t.Errorf("%s LocalFuncs = %v, want [fn_chk_foo] — fn_chk_bar is one-arm only and tpcall has no definition here",
				sc.Key, names)
		}
		for _, lf := range sc.LocalFuncs {
			if len(lf.Keys) != 2 {
				t.Errorf("%s: fn_chk_foo Keys = %v, want both scenarios", sc.Key, lf.Keys)
			}
			if lf.Start <= 0 || lf.End < lf.Start {
				t.Errorf("%s: fn_chk_foo span = %d..%d, want a real definition span", sc.Key, lf.Start, lf.End)
			}
		}
	}
}

func TestRenderScenarioAppendsCommonLocalFuncs(t *testing.T) {
	tree, scens, src := localFuncScenarios(t)
	MarkCommonLocalFuncs(tree, scens)
	out := RenderScenario(scens[0], tree, "SVC_DEMO", src, nil)
	if !strings.Contains(out, "referenced local functions") {
		t.Fatalf("no local-function appendix in:\n%s", out)
	}
	if !strings.Contains(out, "int fn_chk_foo(TPSVCINFO *rqst)") {
		t.Errorf("appendix missing the fn_chk_foo body:\n%s", out)
	}
	if !strings.Contains(out, "int rc = 0;") {
		t.Errorf("appendix missing fn_chk_foo's body text:\n%s", out)
	}
	if strings.Contains(out, "fn_chk_bar(TPSVCINFO") {
		t.Errorf("appendix must not carry the one-arm fn_chk_bar:\n%s", out)
	}
}

func TestRenderScenarioLabelsSharedTail(t *testing.T) {
	tree, scens, src := localFuncScenarios(t)
	MarkCommonLocalFuncs(tree, scens)
	for _, sc := range scens {
		if len(sc.Tail) == 0 {
			t.Fatalf("%s: no Tail spans — the fixture's epilogue is common", sc.Key)
		}
		out := RenderScenario(sc, tree, "SVC_DEMO", src, nil)
		if !strings.Contains(out, "tail (shared epilogue") {
			t.Errorf("%s: no tail banner in:\n%s", sc.Key, out)
		}
		// The tail's own lines must still be present exactly once.
		if n := strings.Count(out, "tpfree((char *)ptr_fml_Obuffer);"); n != 1 {
			t.Errorf("%s: tpfree emitted %d times, want 1", sc.Key, n)
		}
		if n := strings.Count(out, "tpreturn(TPSUCCESS"); n != 1 {
			t.Errorf("%s: tpreturn emitted %d times, want 1", sc.Key, n)
		}
	}
}

// The tail is a label, not a move: Body still owns those lines, so the
// coverage reconcile and DiffScenarios are unaffected.
func TestTailLinesStayInBody(t *testing.T) {
	_, scens, _ := localFuncScenarios(t)
	for _, sc := range scens {
		body := map[int]bool{}
		for _, l := range bodyLines(sc) {
			body[l] = true
		}
		for i := 0; i+1 < len(sc.Tail); i += 2 {
			for l := sc.Tail[i]; l <= sc.Tail[i+1]; l++ {
				if !body[l] {
					t.Errorf("%s: tail line %d missing from Body — the fold view must not change", sc.Key, l)
				}
			}
		}
	}
}

// A single scenario has no siblings, so nothing can be common.
func TestMarkCommonLocalFuncsSingleScenarioIsEmpty(t *testing.T) {
	tree, scens, _ := localFuncScenarios(t)
	MarkCommonLocalFuncs(tree, scens[:1])
	if len(scens[0].LocalFuncs) != 0 {
		t.Errorf("LocalFuncs = %v, want none (one scenario cannot share anything)", scens[0].LocalFuncs)
	}
}

// Re-entrant: a second call must not accumulate duplicates.
func TestMarkCommonLocalFuncsIsIdempotent(t *testing.T) {
	tree, scens, _ := localFuncScenarios(t)
	MarkCommonLocalFuncs(tree, scens)
	MarkCommonLocalFuncs(tree, scens)
	for _, sc := range scens {
		if len(sc.LocalFuncs) != 1 {
			t.Errorf("%s: LocalFuncs = %v after two calls, want exactly 1", sc.Key, sc.LocalFuncs)
		}
	}
}
