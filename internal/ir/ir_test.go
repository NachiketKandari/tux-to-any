package ir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/tsscan"
)

func navFile(t *testing.T) *File {
	t.Helper()
	f, err := ExtractFile(filepath.Join(fixtureRoot, "nav/SVC_DEMO_LIST.pc"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNavPins(t *testing.T) {
	f := navFile(t)
	if f.Entry != "SVC_DEMO_LIST" || len(f.Functions) != 1 {
		t.Fatalf("entry/functions: %q %v", f.Entry, f.Functions)
	}
	if f.BranchCount != 75 || f.BranchingFactor != 142 {
		t.Fatalf("branching %d/%d want 75/142", f.BranchCount, f.BranchingFactor)
	}
	if len(f.Conditions) != 4 {
		t.Fatalf("conditions = %d want 4", len(f.Conditions))
	}
	wantKinds := []string{"if", "elseif", "elseif", "else"}
	wantSpans := [][2]int{{180, 351}, {353, 578}, {582, 808}, {811, 974}}
	wantQids := [][]string{{"q1", "cur_demo_hist"}, {"q3", "cur_demo_featured"}, {"q5", "cur_demo_insured"}, {"cur_demo_list"}}
	for i, c := range f.Conditions {
		if c.Index != i+1 || c.Kind != wantKinds[i] {
			t.Fatalf("cond %d: %d/%q", i, c.Index, c.Kind)
		}
		if c.StartLine != wantSpans[i][0] || c.EndLine != wantSpans[i][1] {
			t.Fatalf("cond %d span [%d,%d] want %v", i, c.StartLine, c.EndLine, wantSpans[i])
		}
		if strings.Join(c.QueryIDs, ",") != strings.Join(wantQids[i], ",") {
			t.Fatalf("cond %d qids %v want %v", i, c.QueryIDs, wantQids[i])
		}
	}
	if !f.Conditions[3].IsDefault || f.Conditions[3].Predicate != nil {
		t.Fatalf("default condition wrong: %+v", f.Conditions[3])
	}
	if f.Conditions[0].Predicate == nil || f.Conditions[0].Predicate.Kind != "cmp" {
		t.Fatalf("c1 predicate: %+v", f.Conditions[0].Predicate)
	}
	if strings.Join(f.Conditions[0].FlagVars, ",") != "c_flag" {
		t.Fatalf("c1 flag vars: %v", f.Conditions[0].FlagVars)
	}
}

func TestNavQueries(t *testing.T) {
	f := navFile(t)
	wantIDs := []string{"q1", "cur_demo_hist", "q3", "cur_demo_featured", "q5", "cur_demo_insured", "cur_demo_list"}
	if len(f.Queries) != len(wantIDs) {
		t.Fatalf("queries = %d want %d", len(f.Queries), len(wantIDs))
	}
	for i, q := range f.Queries {
		if q.ID != wantIDs[i] {
			t.Fatalf("query %d id %q want %q", i, q.ID, wantIDs[i])
		}
	}
	// q1: dual, no binds (INTO targets are the row), single-row
	q1 := f.Queries[0]
	if q1.Type != QuerySelectSingle || len(q1.Tables) != 1 || q1.Tables[0] != "dual" {
		t.Fatalf("q1: %+v", q1)
	}
	if len(q1.Binds) != 0 || q1.BindArity != 0 || strings.Join(q1.RowShape, ",") != "c_from_date,c_to_date" {
		t.Fatalf("q1 binds/row: %v %v", q1.Binds, q1.RowShape)
	}
	// cur_demo_hist flattened: 4 binds, 6-col row, spans declare->close
	cur := f.Queries[1]
	if !cur.CursorFlattened || cur.Type != QuerySelectMulti || cur.BindArity != 4 || len(cur.RowShape) != 6 {
		t.Fatalf("cur_demo_hist: %+v", cur)
	}
	if cur.OrderBy != "DEMO_HIST_DATE desc" || cur.StartLine != 232 || cur.EndLine != 343 {
		t.Fatalf("cur span/order: %d-%d %q", cur.StartLine, cur.EndLine, cur.OrderBy)
	}
	// dedup: q5 duplicates q3; unique view = 6
	q5 := f.Queries[4]
	if q5.DuplicateOf != "q3" || q5.DedupKey != f.Queries[2].DedupKey {
		t.Fatalf("dedup: q5 dup=%q", q5.DuplicateOf)
	}
	if len(f.UniqueQueries()) != 6 {
		t.Fatalf("unique = %d want 6", len(f.UniqueQueries()))
	}
}

func TestNavFMLAndBuffers(t *testing.T) {
	f := navFile(t)
	// preamble: 7 ops; user id/session dropped, mode flag optional
	if len(f.FmlOps) != 7 {
		t.Fatalf("preamble ops = %d want 7", len(f.FmlOps))
	}
	if f.FmlOps[0].Field != "FML_USER_ID" || !f.FmlOps[0].Dropped {
		t.Fatalf("op0: %+v", f.FmlOps[0])
	}
	if f.FmlOps[5].Field != "FML_MODE_FLG" || !f.FmlOps[5].Optional || f.FmlOps[5].Target != "c_flag" {
		t.Fatalf("op5: %+v", f.FmlOps[5])
	}
	if f.FmlOps[1].Code != "S31005" {
		t.Fatalf("errlog code correlation: %+v", f.FmlOps[1])
	}
	// per-condition pins
	c1 := &f.Conditions[0]
	gets := 0
	for _, op := range c1.FmlOps {
		if op.Kind == FmlGet && op.Field == "FML_COMP_CD" {
			gets++
			if op.Optional {
				t.Fatal("c1 FML_COMP_CD get must not be optional")
			}
		}
	}
	if gets != 1 {
		t.Fatalf("c1 comp_cd gets = %d", gets)
	}
	c2 := &f.Conditions[1]
	compOpt := false
	for _, op := range c2.FmlOps {
		if op.Kind == FmlGet && op.Field == "FML_COMP_CD" && op.Optional {
			compOpt = true
		}
	}
	if !compOpt {
		t.Fatal("c2 FML_COMP_CD get must be optional (FNOTPRES)")
	}
	// buffers resolved through the last-'_' segment
	want := map[string]string{"ptr_fml_Ibuffer": "input", "ptr_fml_Obuffer": "output"}
	if len(f.Buffers) != len(want) {
		t.Fatalf("buffers: %+v", f.Buffers)
	}
	for _, b := range f.Buffers {
		if want[b.Name] != string(b.Role) {
			t.Fatalf("buffer %s role %s", b.Name, b.Role)
		}
	}
	if len(f.TPCalls) != 0 {
		t.Fatalf("tpcalls = %d want 0", len(f.TPCalls))
	}
}

func TestNavDefinesAndExternals(t *testing.T) {
	f := navFile(t)
	if len(f.Defines) != 3 {
		t.Fatalf("defines = %d want 3", len(f.Defines))
	}
	for _, d := range f.Defines {
		if d.Function != "" || d.Macro {
			t.Fatalf("file-scope scalar define expected: %+v", d)
		}
	}
	if v, ok := f.DefineAt("SVC_DEMO_LIST", 200, "BUF_LEN"); !ok || v.Value != "6144" {
		t.Fatalf("DefineAt BUF_LEN: %+v %v", v, ok)
	}
	if _, ok := f.DefineAt("SVC_DEMO_LIST", 60, "BUF_LEN"); ok {
		t.Fatal("define must not be visible before its line")
	}
	ext := map[string][]int{}
	for _, e := range f.ExternalFns {
		ext[e.Name] = e.Callsites
	}
	if !eqInts(ext["fn_is_demo_active"], []int{419, 654}) || !eqInts(ext["chk_session"], []int{147}) {
		t.Fatalf("externals: %+v", ext)
	}
	if len(f.Unbalanced) != 0 {
		t.Fatalf("unbalanced: %+v", f.Unbalanced)
	}
}

func TestMergeGolden(t *testing.T) {
	f, err := ExtractFile(filepath.Join(fixtureRoot, "merge/SVC_DEMO_MERGE.pc"))
	if err != nil {
		t.Fatal(err)
	}
	if f.BranchCount != 3 || f.BranchingFactor != 4 {
		t.Fatalf("branching %d/%d want 3/4", f.BranchCount, f.BranchingFactor)
	}
	if len(f.Queries) != 1 || f.Queries[0].Type != QueryMerge || f.Queries[0].TemplateID != TemplateMerge {
		t.Fatalf("queries: %+v", f.Queries)
	}
	q := f.Queries[0]
	if strings.Join(q.Tables, ",") != "DEMO_ACCOUNTS" || strings.Join(q.Binds, ",") != "account_id,balance" || q.BindArity != 2 {
		t.Fatalf("merge query: %+v", q)
	}
	if len(f.Conditions) != 2 || !f.Conditions[1].IsDefault || strings.Join(f.Conditions[1].QueryIDs, ",") != "q1" {
		t.Fatalf("conditions: %+v", f.Conditions)
	}
}

func TestTPCallCorrelation(t *testing.T) {
	f, err := ExtractFile(filepath.Join(fixtureRoot, "pf/SVC_TP_DEMO.pc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.TPCalls) != 1 {
		t.Fatalf("tpcalls = %d want 1", len(f.TPCalls))
	}
	tc := f.TPCalls[0]
	if tc.Service != "SVC_DEMO_DETAIL" || tc.SendBuffer != "sbuffer" || tc.RecvBuffer != "rbuffer" || tc.Ambiguous {
		t.Fatalf("tpcall: %+v", tc)
	}
	if tc.StartLine != 35 || tc.EndLine != 40 {
		t.Fatalf("extent %d-%d want 35-40", tc.StartLine, tc.EndLine)
	}
	if len(tc.SendFML) != 2 || tc.SendFML[0].Field != "FML_COMP_CD" || tc.SendFML[0].Code != "0001" ||
		tc.SendFML[1].Field != "FML_SCHEME_CD" || tc.SendFML[1].Code != "X1" {
		t.Fatalf("send: %+v", tc.SendFML)
	}
	if len(tc.RecvFML) != 2 || tc.RecvFML[0].Field != "FML_NAV_DATE" || tc.RecvFML[0].Target != "sql_nav_date" ||
		tc.RecvFML[1].Field != "FML_NAV_NAV" || tc.RecvFML[1].Target != "lnav" {
		t.Fatalf("recv: %+v", tc.RecvFML)
	}
}

func TestFragmentPins(t *testing.T) {
	f, err := ExtractFile(filepath.Join(fixtureRoot, "pf/fragment_nav_slice.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Fragment || f.Entry != "__fragment" || len(f.Functions) != 1 {
		t.Fatalf("fragment identity: %+v", f)
	}
	if len(f.Conditions) != 2 || f.Conditions[0].StartLine != 8 || f.Conditions[0].EndLine != 28 {
		t.Fatalf("conditions: %+v", f.Conditions)
	}
	if !f.Conditions[1].IsDefault || f.Conditions[1].StartLine != 29 {
		t.Fatalf("else member: %+v", f.Conditions[1])
	}
	if len(f.Queries) != 1 || !f.Queries[0].CursorFlattened || f.Queries[0].OwningFunction != "__fragment" {
		t.Fatalf("queries: %+v", f.Queries)
	}
	cur := f.Queries[0]
	if cur.CursorName != "cur_demo" || strings.Join(cur.Tables, ",") != "DEMO_PRICE_HIST" ||
		cur.BindArity != 1 || strings.Join(cur.RowShape, ",") != "sql_nav_date,sql_nav_nav" ||
		cur.OrderBy != "NAV_DATE DESC" {
		t.Fatalf("cursor unit: %+v", cur)
	}
}

func TestUnbalancedReachIR(t *testing.T) {
	f, err := ExtractFile(filepath.Join(fixtureRoot, "pf/unbalanced.pc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Unbalanced) != 2 || f.Unbalanced[0].Kind != "exec_sql" || f.Unbalanced[1].Kind != "braces" {
		t.Fatalf("unbalanced: %+v", f.Unbalanced)
	}
	if len(f.Queries) != 0 {
		t.Fatalf("broken SQL must not become a statement: %+v", f.Queries)
	}
	if len(f.Functions) != 1 || f.Functions[0] != "SVC_BROKEN" {
		t.Fatalf("functions: %+v", f.Functions)
	}
}

// TestAmbiguousTPCall pins F5: identified buffers with empty contracts.
func TestAmbiguousTPCall(t *testing.T) {
	f, err := ExtractFile(filepath.Join(fixtureRoot, "adversarial/ADV_TPCALL_NOFML.pc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.TPCalls) != 1 || !f.TPCalls[0].Ambiguous {
		t.Fatalf("ambiguous tpcall: %+v", f.TPCalls)
	}
}

// TestHelperFileNotFragment pins that helper files with function
// definitions keep full-file semantics.
func TestHelperFileNotFragment(t *testing.T) {
	f, err := ExtractFile(filepath.Join(fixtureRoot, "nav/fn_demo_lib.pc"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Fragment || f.Entry != "" {
		t.Fatalf("fragment flags: %+v", f)
	}
	if len(f.Queries) != 1 || f.Queries[0].OwningFunction != "fn_is_demo_active" {
		t.Fatalf("queries: %+v", f.Queries)
	}
}

// TestGuardOnlyInventory pins the guard-only fallback: a whole file whose
// top-level chains are all single-branch (one braced if carrying the whole
// body, its FML reads in the preamble) still gets a condition inventory —
// an empty one would leave the file unmappable. A file that also carries a
// qualifying chain keeps the two-branch bar (lone ifs stay out).
func TestGuardOnlyInventory(t *testing.T) {
	src := `#include <atmi.h>

void SVC_ONE_ARM(TPSVCINFO *rqst)
{
    char c_flag;
    if (chk_session(rqst) == -1)
    {
        tpreturn(TPFAIL, 0, (char *)rqst->data, 0L, 0);
    }
    if (strcmp(c_flag, "CUSE") == 0)
    {
        EXEC SQL SELECT MAR_FORM_NO INTO :sql_form_no FROM MAR_MBL_ACCOPN_RQST;
        Fadd32(ptr_fml_Obuffer, FML_FORM_NO, (char *)sql_form_no.arr, 0);
    }
    tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_ONE_ARM.pc")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ExtractFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Fragment {
		t.Fatal("guard-only service must stay a whole file")
	}
	if len(f.Conditions) != 2 {
		t.Fatalf("conditions = %d, want 2 (both lone chains)", len(f.Conditions))
	}
	arm := &f.Conditions[1]
	if arm.Expr != "strcmp(c_flag, \"CUSE\") == 0" || arm.StartLine >= arm.EndLine || len(arm.QueryIDs) != 1 {
		t.Fatalf("query-bearing arm: %+v", arm)
	}
}

// TestDirModeResolvesExternalFns pins the corpus-mode cross-file resolution.
func TestDirModeResolvesExternalFns(t *testing.T) {
	files, err := ExtractDir(filepath.Join(fixtureRoot, "nav"))
	if err != nil {
		t.Fatal(err)
	}
	var list, lib *File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "SVC_DEMO_LIST.pc") {
			list = f
		}
		if strings.HasSuffix(f.Path, "fn_demo_lib.pc") {
			lib = f
		}
	}
	if list == nil || lib == nil {
		t.Fatal("files missing")
	}
	for _, e := range list.ExternalFns {
		if e.Name != "fn_is_demo_active" {
			continue
		}
		if !e.Resolved || filepath.Base(e.DefinedIn) != "fn_demo_lib.pc" || !e.HasSQL ||
			strings.Join(e.QueryIDs, ",") != "q1" {
			t.Fatalf("fn_is_demo_active: %+v", e)
		}
	}
	// dir mode never applies the fragment rubric
	for _, f := range files {
		if f.Fragment {
			t.Fatalf("%s: dir mode must not fragment", f.Path)
		}
	}
}

func TestSameCursorAndConditionLookup(t *testing.T) {
	if !SameCursor("CUR_DEMO", "cur_demo") || SameCursor("CUR_DEMO", "cur_other") {
		t.Fatal("SameCursor semantics")
	}
	f := navFile(t)
	if c := f.Condition(2); c == nil || c.Kind != "elseif" {
		t.Fatalf("Condition(2): %+v", c)
	}
	if c := f.Condition(99); c != nil {
		t.Fatal("Condition(99) should be nil")
	}
	if !f.Condition(1).ContainsLine(213) || f.Condition(1).ContainsLine(352) {
		t.Fatal("ContainsLine ownership")
	}
}

// TestFmlOpOfExported pins the exported classifier flow consumes.
func TestFmlOpOfExported(t *testing.T) {
	src := []byte("void F(void)\n{\n    if(Fget32(buf,FML_X,0,(char*)&v,0) == -1)\n    {\n        if(Ferror32 == FNOTPRES)\n        {\n        }\n    }\n    Fadd32(ob,FML_Y,\"code-y\",0);\n}\n")
	facts, err := tsscan.ScanBytes(src, "x")
	if err != nil {
		t.Fatal(err)
	}
	for i := range facts.Calls {
		c := &facts.Calls[i]
		if c.Name != "Fget32" && c.Name != "Fadd32" {
			continue
		}
		op, ok := FmlOpOf(c, facts)
		if !ok {
			t.Fatalf("FmlOpOf rejected %s", c.Name)
		}
		switch c.Name {
		case "Fget32":
			if op.Kind != FmlGet || op.Field != "FML_X" || op.Target != "v" || op.Buffer != "buf" || !op.Optional {
				t.Fatalf("get op: %+v", op)
			}
		case "Fadd32":
			if op.Kind != FmlAdd || op.Field != "FML_Y" || op.Code != "code-y" {
				t.Fatalf("add op: %+v", op)
			}
		}
	}
}

// TestLiveFacts pins the comment-live rule as a queryable filter.
func TestLiveFacts(t *testing.T) {
	src := []byte("/* dead: Fadd32(buf,FML_X,1,0); EXEC SQL SELECT 1 INTO :x FROM DUAL; */\nvoid F(void)\n{\n    Fadd32(buf,FML_Y,2,0);\n}\n")
	facts, err := tsscan.ScanBytes(src, "x")
	if err != nil {
		t.Fatal(err)
	}
	live := LiveFacts(facts)
	if len(live.Calls) != 1 || live.Calls[0].Name != "Fadd32" {
		t.Fatalf("live calls: %+v", live.Calls)
	}
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
