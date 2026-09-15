package tsscan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const fixtureRoot = "../../testdata/fixtures"

func TestScanWeirdDirectives(t *testing.T) {
	src := []byte("#include <fake.h>\n" +
		"#define FAKE_MACRO(x) ((x) * 2 + \"string in macro\")\n" +
		"#define UNBALANCED_PAREN( (x\n\n" +
		"EXEC SQL INCLUDE \"table/none.h\" ;\n\n" +
		"void SVC_ADV_WEIRD(TPSVCINFO* rqst)\n{\n    tpreturn(TPSUCCESS,0L,0L,0L,0);\n}\n")
	facts, err := ScanBytes(src, "weird")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Functions) != 1 || facts.Functions[0].Name != "SVC_ADV_WEIRD" {
		t.Fatalf("function lost to broken define: %+v", facts.Functions)
	}
	if len(facts.ParseErrors) != 0 {
		t.Fatalf("unexpected parse errors: %+v", facts.ParseErrors)
	}
	var broken, macro bool
	for _, d := range facts.Directives {
		if d.Kind != "define" {
			continue
		}
		if d.Arg == "UNBALANCED_PAREN( (x" {
			broken = true
		}
		if d.Arg == "FAKE_MACRO(x) ((x) * 2 + \"string in macro\")" {
			macro = true
		}
	}
	if !broken || !macro {
		t.Fatalf("define facts wrong: broken=%v macro=%v (%+v)", broken, macro, facts.Directives)
	}
}

func TestScanBannerCommentDebris(t *testing.T) {
	// fn_*/chk_* inside a banner ends the comment early per C lexical
	// rules; the debris after must not derail the parse (self-healing).
	src := []byte("/***********************************************************************\n" +
		" * Name : demo\n" +
		" * Ver 1.0 added here: patterns with fn_*/chk_* calls.\n" +
		" * Ver 1.0 comment ends\n" +
		" *******************************************************************************/\n\n" +
		"#define BUF_LEN 1024\n\n" +
		"char c_ServiceName[33];\n\n" +
		"void SVC_DEMO(TPSVCINFO* rqst)\n{\n    tpreturn(TPSUCCESS,0L,0L,0L,0);\n}\n")
	facts, err := ScanBytes(src, "debris")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Functions) != 1 || facts.Functions[0].Name != "SVC_DEMO" {
		t.Fatalf("function lost to comment debris: %+v", facts.Functions)
	}
	found := false
	for _, d := range facts.Directives {
		if d.Kind == "define" && d.Arg == "BUF_LEN 1024" {
			found = true
		}
	}
	if !found {
		t.Fatalf("define lost: %+v", facts.Directives)
	}
}

func TestScanLenientExecClose(t *testing.T) {
	// A missing ';' leniently closes the region at the next EXEC keyword:
	// the two statements stay two regions, and the close is recorded as an
	// unbalanced defect fact (never a silent merge).
	src := []byte("void SVC_DEMO(TPSVCINFO* rqst)\n{\n" +
		"    EXEC SQL SELECT A FROM T\n" +
		"    EXEC SQL COMMIT;\n" +
		"}\n")
	facts, err := ScanBytes(src, "lenient")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.AllSQL) != 2 {
		t.Fatalf("allSQL = %d want 2 (statements must not merge): %+v", len(facts.AllSQL), facts.AllSQL)
	}
	if facts.AllSQL[0].Kind != SQLSelect || facts.AllSQL[1].Kind != SQLCommit {
		t.Fatalf("kinds = %s, %s want select, commit", facts.AllSQL[0].Kind, facts.AllSQL[1].Kind)
	}
	found := false
	for _, u := range facts.Unbalanced {
		if u.Kind == "exec_sql_lenient" && u.StartLine == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("lenient close not recorded: %+v", facts.Unbalanced)
	}
}

// TestGoldenFixtures runs the scanner over the vendored synthetic fixtures
// and pins the vocabulary's structural numbers (testdata/goldens).
func TestGoldenFixtures(t *testing.T) {
	cases := []struct {
		file            string
		fns             int
		branches        int
		loops           int
		allSQL          int
		queries         int
		calls           int
		decls           int
		comments        int
		unbalancedKinds []string
	}{
		{"nav/SVC_DEMO_LIST.pc", 1, 85, 8, 33, 7, 272, 20, 83, nil},
		{"merge/SVC_DEMO_MERGE.pc", 1, 4, 0, 3, 1, 5, 2, 1, nil},
		{"pf/SVC_TP_DEMO.pc", 1, 4, 0, 1, 1, 11, 5, 1, nil},
		{"pf/comment_traps.pc", 1, 2, 0, 1, 1, 1, 2, 9, nil},
		{"pf/unbalanced.pc", 1, 0, 0, 0, 0, 0, 1, 1, []string{"exec_sql", "braces"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			facts, err := ScanFile(filepath.Join(fixtureRoot, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if len(facts.Functions) != tc.fns {
				t.Errorf("functions = %d want %d", len(facts.Functions), tc.fns)
			}
			if len(facts.Branches) != tc.branches {
				t.Errorf("branches = %d want %d", len(facts.Branches), tc.branches)
			}
			if len(facts.Loops) != tc.loops {
				t.Errorf("loops = %d want %d", len(facts.Loops), tc.loops)
			}
			if len(facts.AllSQL) != tc.allSQL {
				t.Errorf("allSQL = %d want %d", len(facts.AllSQL), tc.allSQL)
			}
			if len(facts.Queries) != tc.queries {
				t.Errorf("queries = %d want %d", len(facts.Queries), tc.queries)
			}
			if len(facts.Calls) != tc.calls {
				t.Errorf("calls = %d want %d", len(facts.Calls), tc.calls)
			}
			if len(facts.VarDecls) != tc.decls {
				t.Errorf("decls = %d want %d", len(facts.VarDecls), tc.decls)
			}
			if len(facts.Comments) != tc.comments {
				t.Errorf("comments = %d want %d", len(facts.Comments), tc.comments)
			}
			if tc.unbalancedKinds != nil {
				if len(facts.Unbalanced) != len(tc.unbalancedKinds) {
					t.Fatalf("unbalanced = %d want %d (%+v)", len(facts.Unbalanced), len(tc.unbalancedKinds), facts.Unbalanced)
				}
				for i, kind := range tc.unbalancedKinds {
					if facts.Unbalanced[i].Kind != kind {
						t.Errorf("unbalanced[%d].Kind = %q want %q", i, facts.Unbalanced[i].Kind, kind)
					}
				}
			}
		})
	}
}

func TestNavBranchingFactor(t *testing.T) {
	facts, err := ScanFile(filepath.Join(fixtureRoot, "nav/SVC_DEMO_LIST.pc"))
	if err != nil {
		t.Fatal(err)
	}
	headers, factor := 0, 0
	for _, b := range facts.Branches {
		if b.Kind == BranchElse {
			continue
		}
		headers++
		factor += 1 << b.NestDepth
	}
	if headers != 75 || factor != 142 {
		t.Fatalf("nav branching = %d/%d want 75/142", headers, factor)
	}
}

func TestFragmentScan(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(fixtureRoot, "pf/fragment_nav_slice.txt"))
	if err != nil {
		t.Fatal(err)
	}
	facts, err := ScanFragment(src, "fragment_nav_slice.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Fragment {
		t.Fatal("Fragment not set")
	}
	if len(facts.Functions) != 1 || facts.Functions[0].Name != "__fragment" {
		t.Fatalf("functions = %+v", facts.Functions)
	}
	if len(facts.Branches) != 3 || len(facts.AllSQL) != 4 || len(facts.Queries) != 1 {
		t.Fatalf("fragment counts: br=%d sql=%d q=%d", len(facts.Branches), len(facts.AllSQL), len(facts.Queries))
	}
	// rebase: the first if sits on the fragment's own line 8
	if facts.Branches[0].StartLine != 8 || facts.Branches[0].BlockStart != 9 || facts.Branches[0].BlockEnd != 28 {
		t.Fatalf("rebased branch: %+v", facts.Branches[0])
	}
	if facts.Queries[0].StartLine != 15 || facts.Queries[0].CursorName != "CUR_DEMO" {
		t.Fatalf("rebased query: %+v", facts.Queries[0])
	}
	if len(facts.VarDecls) != 4 || facts.VarDecls[2].Type != "varchar" || !facts.VarDecls[2].Array {
		t.Fatalf("decls: %+v", facts.VarDecls)
	}
}

func TestInComment(t *testing.T) {
	facts, err := ScanFile(filepath.Join(fixtureRoot, "pf/comment_traps.pc"))
	if err != nil {
		t.Fatal(err)
	}
	if !facts.InComment(25, 13) {
		t.Error("(25,13) should be inside the dead banner block")
	}
	if facts.InComment(16, 9) || facts.InComment(13, 5) || facts.InComment(27, 9) {
		t.Error("live code positions must not be inside comments")
	}
}

func TestDeterminism(t *testing.T) {
	path := filepath.Join(fixtureRoot, "nav/SVC_DEMO_LIST.pc")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ScanBytes(src, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ScanBytes(src, path)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(ab) != string(bb) {
		t.Fatal("ScanBytes is not deterministic")
	}
}
