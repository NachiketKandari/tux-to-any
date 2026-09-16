package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	"tux-to-any/internal/pred"
	scanner "tux-to-any/internal/tsscan"
)

// normChainSrc pins the normalize-chain recognizer (monolith idiom): a
// strcmp guard chain writing a scalar alias, followed by predicates on the
// alias — ref sql_trn_cd.arr, alias trn_cd, domain P/R/A.
const normChainSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	if (Fget32(ptr_fml_Ibuffer, FML_TRANS_CD, 0, (char *)sql_trn_cd.arr, 0) == -1) {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "R")))
		trn_cd = 'R';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (trn_cd == 'A') {
		work_a();
	}
	if (trn_cd == 'P' || trn_cd == 'R') {
		work_pr();
	}
	if (trn_cd != 'P' && trn_cd != 'A') {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// directStrcmpSrc pins the direct-strcmp recognizer (direct-ref idiom): no
// alias — predicates strcmp the buffer ref itself, domain from the literals.
const directStrcmpSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	if (Fget32(ptr_fml_Ibuffer, FML_TRANS_CD, 0, (char *)sql_trn_cd.arr, 0) == -1) {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	if (strcmp(sql_trn_cd.arr, "I") == 0) {
		work_i();
	}
	if (strcmp(sql_trn_cd.arr, "P") == 0) {
		work_p();
	}
	if (strcmp(sql_trn_cd.arr, "W") == 0) {
		work_w();
	}
	if (strcmp(sql_trn_cd.arr, "R") != 0 && strcmp(sql_trn_cd.arr, "W") != 0) {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// charCompareSrc pins recognizer 3: a scalar compared against char
// literals with no strcmp anywhere.
const charCompareSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char mode;
	mode = 'X';
	if (mode == 'X') {
		work_x();
	}
	if (mode == 'Y') {
		work_y();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// noAxisSrc pins the honest fallback: no dispatch spine (a single flag
// branch) → nil.
const noAxisSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	if (c_flag == 'H') {
		work();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func dispatchAxisOf(t *testing.T, src string) *DispatchAxis {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(src), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	return DispatchAxisFor([]byte(src), facts, "SVC_DEMO", nil)
}

func TestDispatchAxisNormalizeChain(t *testing.T) {
	a := dispatchAxisOf(t, normChainSrc)
	if a == nil {
		t.Fatal("no axis detected for the normalize-chain idiom")
	}
	if a.Ref != "sql_trn_cd.arr" || a.Alias != "trn_cd" {
		t.Errorf("ref/alias = %q/%q, want sql_trn_cd.arr/trn_cd", a.Ref, a.Alias)
	}
	if !a.Normalized {
		t.Error("normalize chain not flagged")
	}
	if a.RefName != "trn_cd" {
		t.Errorf("key name = %q, want trn_cd (alias wins)", a.RefName)
	}
	if want := "A,P,R"; want != joinDomain(a.Domain) {
		t.Errorf("domain = %s, want %s", joinDomain(a.Domain), want)
	}
	if a.Sites == 0 {
		t.Error("no predicate sites counted")
	}
}

func TestDispatchAxisDirectStrcmp(t *testing.T) {
	a := dispatchAxisOf(t, directStrcmpSrc)
	if a == nil {
		t.Fatal("no axis detected for the direct-strcmp idiom")
	}
	if a.Alias != "" {
		t.Errorf("alias = %q, want none", a.Alias)
	}
	if a.Normalized {
		t.Error("direct-strcmp must not be flagged normalized")
	}
	if a.RefName != "sql_trn_cd" {
		t.Errorf("key name = %q, want sql_trn_cd (ref base)", a.RefName)
	}
	if want := "I,P,R,W"; want != joinDomain(a.Domain) {
		t.Errorf("domain = %s, want %s", joinDomain(a.Domain), want)
	}
}

func TestDispatchAxisCharCompareFallback(t *testing.T) {
	a := dispatchAxisOf(t, charCompareSrc)
	if a == nil {
		t.Fatal("no axis detected for the char-compare idiom")
	}
	if a.RefName != "mode" || len(a.Domain) != 2 {
		t.Errorf("axis = %s, want mode domain [X,Y]", a)
	}
}

func TestDispatchAxisNone(t *testing.T) {
	if a := dispatchAxisOf(t, noAxisSrc); a != nil {
		t.Errorf("axis = %s, want none", a)
	}
}

// membershipSrc pins the guard-only shape the real corpus carries (the
// CUSE service): every value tested inside ONE compound strcmp guard —
// flag derivation, not dispatch. The values never sit in the guards of
// mutually exclusive arms, so no axis may fire (the phantom axis used to
// slice every scenario to an empty body — all preamble, nothing kept).
const membershipSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char c_rm_valid_flag;
	if (strcmp(sql_emp_no.arr, "111111") != 0 &&
		strcmp(sql_emp_no.arr, "222222") != 0 &&
		strcmp(sql_emp_no.arr, "333333") != 0 &&
		strcmp(sql_emp_no.arr, "777777") != 0 &&
		strcmp(sql_emp_no.arr, "888888") != 0) {
		c_rm_valid_flag = 'Y';
	}
	else {
		c_rm_valid_flag = 'N';
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// The discriminator, not the value count: the same five values in the
// guards of five separate ifs are a dispatch (direct-strcmp idiom).
const membershipSeparateSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	if (strcmp(sql_emp_no.arr, "111111") == 0) {
		special();
	}
	if (strcmp(sql_emp_no.arr, "222222") == 0) {
		special();
	}
	if (strcmp(sql_emp_no.arr, "333333") == 0) {
		special();
	}
	if (strcmp(sql_emp_no.arr, "777777") == 0) {
		special();
	}
	if (strcmp(sql_emp_no.arr, "888888") == 0) {
		special();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisCompoundMembershipNotADispatch(t *testing.T) {
	if a := dispatchAxisOf(t, membershipSrc); a != nil {
		t.Errorf("axis = %s, want none — a compound membership strcmp is one guard, not a dispatch spine", a)
	}
	a := dispatchAxisOf(t, membershipSeparateSrc)
	if a == nil {
		t.Fatal("no axis for the same values in separate guards — the discriminator is guard structure")
	}
	if a.Ref != "sql_emp_no.arr" || len(a.Domain) != 5 {
		t.Errorf("axis = %s, want sql_emp_no domain of 5", a)
	}
}

func joinDomain(d []string) string {
	out := ""
	for i, v := range d {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

// foldSrc: one alias + a body exercising every fold outcome — satisfied
// (trn_cd=='A'), contradicted (trn_cd=='P'), mixed (trn_cd=='A' && extra),
// or-with-alias (P||A), kept (untouched flag), plus a commit for the tx fact.
const foldSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	int extra;
	extra = 0;
	if (Fget32(ptr_fml_Ibuffer, FML_TRANS_CD, 0, (char *)sql_trn_cd.arr, 0) == -1) {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (trn_cd == 'A') {
		if (extra == 1) {
			inner_a();
		}
		i_ch_val = tpbegin(TRAN_TIMEOUT, 0);
		EXEC SQL UPDATE T SET C = 1;
		tpcommit(0);
		i_err = Fadd32(ptr_fml_Obuffer, FML_OUT, (char *)&a, 0);
	}
	if (trn_cd == 'P') {
		work_p();
	}
	if (trn_cd == 'P' || trn_cd == 'A') {
		work_pa();
	}
	if (extra == 1) {
		work_extra();
	}
	if (trn_cd == 'A' && extra == 1) {
		work_mixed();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func foldScenario(t *testing.T, value string) *Scenario {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_DEMO.pc")
	if err := os.WriteFile(path, []byte(foldSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return scenarioFromFile(t, path, value)
}

// scenarioFromFile runs the full SCEN-1/2 pipeline over a real file: IR
// extraction, flow build, axis detection, and the per-value slice — the
// same shape every caller uses.
func scenarioFromFile(t *testing.T, path, value string) *Scenario {
	t.Helper()
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := scanner.ScanBytes(src, f.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree := Build(src, facts, f.Entry, f)
	axis := DispatchAxisFor(src, facts, f.Entry, nil)
	if axis == nil {
		t.Fatal("no axis detected")
	}
	return ScenarioFor(tree, axis, value)
}

func findFold(nodes []*SliceNode, fold FoldKind) *SliceNode {
	for _, n := range nodes {
		if n.Fold == fold {
			return n
		}
		if got := findFold(n.Children, fold); got != nil {
			return got
		}
	}
	return nil
}

func TestScenarioFoldSatisfied(t *testing.T) {
	sc := foldScenario(t, "A")
	// The normalization if (!=strcmp → 'A') is also satisfied; the body
	// branch is the first satisfied one WITH children (nesting preserved).
	var br *SliceNode
	var find func(nodes []*SliceNode) *SliceNode
	find = func(nodes []*SliceNode) *SliceNode {
		for _, n := range nodes {
			if n.Fold == FoldSatisfied && len(n.Children) > 0 {
				return n
			}
			if got := find(n.Children); got != nil {
				return got
			}
		}
		return nil
	}
	br = find(sc.Body)
	if br == nil {
		t.Fatal("no satisfied body fold found")
	}
	if br.FoldedCond != "" {
		t.Errorf("satisfied node must not carry a residual, got %q", br.FoldedCond)
	}
	if len(br.Children) == 0 {
		t.Error("satisfied branch must keep children")
	}
}

func TestScenarioFoldContradicted(t *testing.T) {
	sc := foldScenario(t, "A")
	if findFold(sc.Body, "contradicted") != nil {
		// contradicted nodes are dropped — never present.
		t.Error("contradicted node present in slice")
	}
	if sc.Counts.Dropped == 0 {
		t.Error("dropped count = 0, want the trn_cd=='P' branch")
	}
	if len(sc.Counts.DroppedLines) == 0 {
		t.Error("dropped lines not recorded")
	}
	for _, n := range sc.Body {
		if n.Cond == "trn_cd == 'P'" {
			t.Errorf("contradicted branch %d survived the slice", n.Line)
		}
	}
}

func TestScenarioFoldMixed(t *testing.T) {
	sc := foldScenario(t, "A")
	mixed := findFold(sc.Body, FoldMixed)
	if mixed == nil {
		t.Fatal("no mixed fold found")
	}
	if mixed.FoldedCond != "extra == 1" {
		t.Errorf("residual = %q, want %q", mixed.FoldedCond, "extra == 1")
	}
}

func TestScenarioOrWithAlias(t *testing.T) {
	sc := foldScenario(t, "P")
	// (P||A) under P: satisfied.
	br := findFold(sc.Body, FoldSatisfied)
	if br == nil {
		t.Fatal("or-branch did not fold satisfied")
	}
	// The mixed branch (A && extra) under P: contradicted (A is false).
	sc2 := foldScenario(t, "P")
	if findFold(sc2.Body, FoldMixed) != nil {
		t.Error("A&&extra must fold contradicted under P")
	}
}

func TestScenarioKeptAndCensus(t *testing.T) {
	sc := foldScenario(t, "A")
	kept := findFold(sc.Body, FoldKept)
	if kept == nil {
		t.Fatal("kept flag branch missing")
	}
	// The preamble's axis read (FML_TRANS_CD) is shared; the body adds the
	// response write (FML_OUT).
	has := func(list []string, want string) bool {
		for _, s := range list {
			if s == want {
				return true
			}
		}
		return false
	}
	if !has(sc.Gets, "FML_TRANS_CD") {
		t.Errorf("census gets=%v, want the shared axis read", sc.Gets)
	}
	if !has(sc.Adds, "FML_OUT") {
		t.Errorf("census adds=%v, want FML_OUT", sc.Adds)
	}
	var txQuery bool
	for _, q := range sc.Queries {
		if q.DML && q.Tx {
			txQuery = true
		}
	}
	if !txQuery {
		t.Errorf("UPDATE in a scenario with COMMIT must carry tx=true; queries=%v", sc.Queries)
	}
}

func TestScenarioResidueLoud(t *testing.T) {
	// Raw text the predicate grammar cannot parse and that mentions the
	// axis stays in the slice UNRECOGNIZED and is listed in Residue with
	// line provenance (SCEN-D4, loud — never silently mis-sliced).
	src := `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (trn_cd == 'A' ? work() : idle()) {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	facts, err := scanner.ScanBytes([]byte(src), "res.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(src), facts, "SVC_DEMO", nil)
	axis := DispatchAxisFor([]byte(src), facts, "SVC_DEMO", nil)
	if axis == nil {
		t.Fatal("no axis")
	}
	sc := ScenarioFor(tree, axis, "A")
	if len(sc.Residue) == 0 {
		t.Fatalf("residue empty — unparseable axis predicate must stay loud: %+v", sc.Counts)
	}
	if !strings.Contains(sc.Residue[0], "L") {
		t.Errorf("residue entry missing line provenance: %v", sc.Residue)
	}
}

// TestScenarioMixedSubstitution pins the mixed fold's residual rendering:
// `trn_cd == c_other` under A keeps the runtime comparison with the axis
// side substituted ('A' == c_other) — decided structure, not residue.
func TestScenarioMixedSubstitution(t *testing.T) {
	src := `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	char c_other;
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (trn_cd == c_other) {
		work();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	facts, err := scanner.ScanBytes([]byte(src), "res2.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(src), facts, "SVC_DEMO", nil)
	axis := DispatchAxisFor([]byte(src), facts, "SVC_DEMO", nil)
	sc := ScenarioFor(tree, axis, "A")
	mixed := findFold(sc.Body, FoldMixed)
	if mixed == nil {
		t.Fatalf("no mixed fold; residue=%v counts=%+v", sc.Residue, sc.Counts)
	}
	if mixed.FoldedCond != "'A' == c_other" {
		t.Errorf("residual = %q, want %q", mixed.FoldedCond, "'A' == c_other")
	}
}

// txPairsSrc pins SCEN-D8 span pairing: an entry-level tp pair enclosing
// all DML, a helper (fn_equ_*tran) pair in a non-dispatch block, an abort
// that must never pair, and a commit without a begin that must ride the
// enclosing span.
const txPairsSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	char c_euin;
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	i_h = fn_equ_begintran(c_ServiceName, c_usr_id, c_err_msg);
	EXEC SQL INSERT INTO MAP_T VALUES (:a);
	if (SQLCODE != 0) {
		fn_equ_aborttran(c_ServiceName, i_h, c_err_msg);
	}
	i_h2 = fn_equ_committran(c_ServiceName, c_usr_id, i_h, c_err_msg);
	i_ch_val = tpbegin(TRAN_TIMEOUT, 0);
	if (trn_cd == 'A') {
		EXEC SQL UPDATE A SET C = 1;
	}
	if (trn_cd == 'P') {
		EXEC SQL UPDATE B SET C = 2;
	}
	tpcommit(0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func txScenario(t *testing.T, value string) (*Scenario, *Tree) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_DEMO.pc")
	if err := os.WriteFile(path, []byte(txPairsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return scenarioFromFileTree(t, path, value)
}

// scenarioFromFileTree mirrors scenarioFromFile but returns the tree too —
// the ScenarioSource consumers (tx plumbing elision) walk it.
func scenarioFromFileTree(t *testing.T, path, value string) (*Scenario, *Tree) {
	t.Helper()
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := scanner.ScanBytes(src, f.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree := Build(src, facts, f.Entry, f)
	axis := DispatchAxisFor(src, facts, f.Entry, nil)
	if axis == nil {
		t.Fatal("no axis detected")
	}
	return ScenarioFor(tree, axis, value), tree
}

func TestScenarioTxSpanPairs(t *testing.T) {
	sc, _ := txScenario(t, "A")
	if len(sc.TxSpans) != 2 {
		t.Fatalf("tx spans = %v, want 2 (helper pair + tp pair)", sc.TxSpans)
	}
	// DML inside both spans is tx; both scenarios share the tp span, but
	// only the 'A' scenario keeps the A-update.
	var txQ, plainQ bool
	for _, q := range sc.Queries {
		if q.Tx {
			txQ = true
		} else if q.DML {
			plainQ = true
		}
	}
	if !txQ {
		t.Errorf("DML inside a begin→commit span must be tx: %v", sc.Queries)
	}
	if plainQ {
		t.Errorf("every DML here sits inside a span: %v", sc.Queries)
	}
}

func TestScenarioTxSpansScenarioScoped(t *testing.T) {
	// Under P the A-update's branch is contradicted; the B-update survives.
	// Both scenarios keep the entry-level spans (non-dispatch nodes).
	sc, _ := txScenario(t, "P")
	if len(sc.TxSpans) != 2 {
		t.Errorf("tx spans = %v, want the helper + tp pair in both scenarios", sc.TxSpans)
	}
	for _, q := range sc.Queries {
		if q.DML && !q.Tx {
			t.Errorf("surviving DML must be tx inside a span: %v", sc.Queries)
		}
	}
}

// TestScenarioCommitWithoutBeginRidesSpan pins the mid-branch commit: it
// pairs nothing (no begin inside the branch) and the enclosing entry-level
// span still tx-flags the branch's DML — a corpus mid-branch-commit shape.
func TestScenarioCommitWithoutBeginRidesSpan(t *testing.T) {
	src := `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	i_ch_val = tpbegin(TRAN_TIMEOUT, 0);
	if (trn_cd == 'A') {
		EXEC SQL UPDATE A SET C = 1;
		tpcommit(0);
		EXEC SQL UPDATE B SET C = 2;
	}
	tpcommit(0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	path := filepath.Join(t.TempDir(), "SVC_DEMO.pc")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sc := scenarioFromFile(t, path, "A")
	// The mid-branch commit pairs the entry tpbegin; the tail commit finds
	// no open begin and is ignored. UPDATE A sits inside the span (tx);
	// UPDATE B after the mid-commit sits outside it (the corpus's legacy
	// wart rendered honestly non-tx — SCEN-D8).
	if len(sc.TxSpans) != 1 {
		t.Fatalf("tx spans = %v, want 1 (begin→mid-branch commit)", sc.TxSpans)
	}
	txCount := 0
	for _, q := range sc.Queries {
		if q.Tx {
			txCount++
		}
	}
	if txCount != 1 {
		t.Errorf("tx queries = %d, want 1 (UPDATE B after the mid-commit is outside every span)", txCount)
	}
}

// respValuesSrc pins SCEN-D9 response resolution: Fget-flow values, SQL
// INTO values, and the ladder rule — the opt-in/opt-out rewrite sits in
// mutually exclusive branches, so each scenario resolves one stable value.
const respValuesSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	char v_doc_typ[10];
	char c_nom_flg;
	char c_rewritten;
	c_nom_flg = 'N';
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (Fget32(ptr_fml_Ibuffer, FML_ORDR_ID, 0, (char *)&l_id, 0) == -1) {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
	}
	if (nom_flg == 'Y') {
		strcpy(v_doc_typ.arr, "MFNIAP");
		i_err = Fadd32(ptr_fml_Obuffer, FML_LD_CAT, (char*)v_doc_typ.arr, 0);
	} else {
		strcpy(v_doc_typ.arr, "MFNOAP");
		i_err = Fadd32(ptr_fml_Obuffer, FML_LD_CAT, (char*)v_doc_typ.arr, 0);
	}
	EXEC SQL SELECT X INTO :sql_comp FROM T;
	i_err = Fadd32(ptr_fml_Obuffer, FML_OUT_ID, (char *)&l_id, 0);
	i_err = Fadd32(ptr_fml_Obuffer, FML_OUT_X, (char *)&sql_comp, 0);
	i_err = Fadd32(ptr_fml_Obuffer, FML_REWRITTEN, (char *)&c_rewritten, 0);
	c_rewritten = 'A';
	i_err = Fadd32(ptr_fml_Obuffer, FML_REWRITTEN, (char *)&c_rewritten, 0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func respScenario(t *testing.T) *Scenario {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_DEMO.pc")
	if err := os.WriteFile(path, []byte(respValuesSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := scanner.ScanBytes(src, f.Path)
	if err != nil {
		t.Fatal(err)
	}
	tree := Build(src, facts, f.Entry, f)
	axis := DispatchAxisFor(src, facts, f.Entry, nil)
	if axis == nil {
		t.Fatal("no axis")
	}
	// The fixture's adds are not dispatch-gated; slice on the first value.
	return ScenarioFor(tree, axis, axis.Domain[0])
}

func respOf(sc *Scenario, field string) *ScenarioResponse {
	for i := range sc.Responses {
		if sc.Responses[i].Field == field {
			return &sc.Responses[i]
		}
	}
	return nil
}

func TestScenarioResponseRequestFlow(t *testing.T) {
	sc := respScenario(t)
	r := respOf(sc, "FML_OUT_ID")
	if r == nil {
		t.Fatal("FML_OUT_ID response missing")
	}
	if r.Value != "request FML_ORDR_ID" {
		t.Errorf("value = %q, want the FML read flow", r.Value)
	}
	if !r.Stable {
		t.Error("stable field flagged unstable")
	}
}

func TestScenarioResponseLadderStable(t *testing.T) {
	sc := respScenario(t)
	count := 0
	for _, r := range sc.Responses {
		if r.Field == "FML_LD_CAT" {
			count++
			if !r.Stable {
				t.Errorf("ladder add@%d flagged unstable (mutually exclusive branches)", r.Line)
			}
			if r.Value != `"MFNIAP"` && r.Value != `"MFNOAP"` {
				t.Errorf("value = %q, want the ladder literal", r.Value)
			}
		}
	}
	if count == 0 {
		t.Fatal("no FML_LD_CAT responses")
	}
}

func TestScenarioResponseUnstableLoud(t *testing.T) {
	sc := respScenario(t)
	r := respOf(sc, "FML_REWRITTEN")
	if r == nil {
		t.Fatal("FML_REWRITTEN response missing")
	}
	if r.Stable {
		t.Error("genuine same-path rewrite must be unstable")
	}
	loud := false
	for _, res := range sc.Residue {
		if strings.Contains(res, "FML_REWRITTEN") && strings.Contains(res, "rewritten after add") {
			loud = true
		}
	}
	if !loud {
		t.Errorf("unstable field not in residue: %v", sc.Residue)
	}
}

// TestScenarioTxAbortsCensus pins the abort census: aborts never pair into
// spans but register as sites (SCEN-D8) — one helper abort in the txPairsSrc
// fixture.
func TestScenarioTxAbortsCensus(t *testing.T) {
	sc, _ := txScenario(t, "A")
	if len(sc.TxAborts) != 1 || sc.TxAborts[0] != 11 {
		t.Errorf("tx aborts = %v, want [11]", sc.TxAborts)
	}
}

// TestScenarioSourceElidesTxPlumbing pins the slice-level elision: standalone
// abort/rollback call lines drop from the scenario text (the wrapper owns
// rollback), while begin/commit lines stay — the convert view pass rewrites
// them into the utils.ExecTransaction template — and SQL regions still map.
func TestScenarioSourceElidesTxPlumbing(t *testing.T) {
	sc, tree := txScenario(t, "A")
	src, spans := ScenarioSource(sc, tree, []byte(txPairsSrc))
	for _, bad := range []string{"fn_equ_aborttran", "tpabort"} {
		if strings.Contains(src, bad) {
			t.Errorf("scenario source still contains %q\n%s", bad, src)
		}
	}
	for _, kept := range []string{"fn_equ_begintran", "fn_equ_committran", "tpbegin", "tpcommit"} {
		if !strings.Contains(src, kept) {
			t.Errorf("scenario source lost tx template anchor %q\n%s", kept, src)
		}
	}
	// Scenario A folds the P-branch away — the B-update's branch is
	// contradicted and its SQL drops with it.
	for _, good := range []string{"INSERT INTO MAP_T", "UPDATE A SET C = 1", "tpreturn(TPSUCCESS"} {
		if !strings.Contains(src, good) {
			t.Errorf("scenario source lost %q\n%s", good, src)
		}
	}
	if len(spans) == 0 {
		t.Fatal("no SQL regions captured")
	}
	last := len(strings.Split(src, "\n"))
	for id, sp := range spans {
		if sp[0] < 1 || sp[1] > last {
			t.Errorf("query %s span %v exceeds the slice text (%d lines)", id, sp, last)
		}
	}
}

// TestLineIsTxPlumbingCall pins the line classifier: assignment form elides;
// guard conditions, multi-statement lines, and non-tx calls stay.
func TestLineIsTxPlumbingCall(t *testing.T) {
	name := "fn_equ_begintran"
	for _, good := range []string{
		"i_h = fn_equ_begintran(c_ServiceName, c_usr_id, c_err_msg);",
		"fn_equ_aborttran(c_ServiceName, i_h, c_err_msg);",
		"tpcommit(0);",
	} {
		if !lineIsTxPlumbingCall(good, name) && strings.Contains(good, name) {
			t.Errorf("%q must classify as tx plumbing for %s", good, name)
		}
	}
	if lineIsTxPlumbingCall("", name) {
		t.Error("empty line must not classify")
	}
	for _, bad := range []string{
		"if (fn_equ_committran(c, u, i_h, m) != OK) {",
		"i_h = fn_equ_begintran(c, u, m); log_it();",
		"errlog(c_ServiceName, \"S1\", m);",
	} {
		if lineIsTxPlumbingCall(bad, name) {
			t.Errorf("%q must NOT classify as tx plumbing", bad)
		}
	}
}

// TestExprTouchesAxisRawWholeIdent pins the Raw-text axis test: whole
// identifier matches touch the axis, a match inside a longer identifier
// (trn_cd inside trn_cd_arr) is a different variable and never does.
func TestExprTouchesAxisRawWholeIdent(t *testing.T) {
	touch := func(text, ref, alias string) bool {
		return exprTouchesAxis(&pred.Expr{Kind: pred.KindRaw, Text: text}, ref, alias)
	}
	if !touch("trn_cd == c_x", "trn_cd", "") {
		t.Error("whole-ident mention must touch the axis")
	}
	if !touch("strcmp(sql_trn_cd.arr, \"P\") != 0", "sql_trn_cd.arr", "trn_cd") {
		t.Error("ref mention must touch the axis")
	}
	for _, bad := range []string{
		"trn_cd_arr != 0",
		"my_trn_cd == 1",
	} {
		if touch(bad, "trn_cd", "") {
			t.Errorf("%q must not touch axis trn_cd", bad)
		}
	}
}
