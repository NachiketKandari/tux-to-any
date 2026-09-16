package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// txScenarioSrc mirrors the SCEN-D8 fixture: a dispatch axis, a helper
// begin/commit pair around DML, an abort in the error path, and the final
// tpreturn.
const txScenarioSrc = `void SVC_X(TPSVCINFO *rqst) {
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
	if(fn_equ_committran(c_ServiceName, c_usr_id, i_h, c_err_msg) == -1)
	{
		err = errors.New("S31800");
		tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

// TestTxTemplateFlowIntegration pins the flow→convert contract: the
// scenario slice keeps the begin/commit lines, and rewriteTxTemplate turns
// them into the ExecTransaction template with no legacy helper spellings
// or handle bookkeeping left.
func TestTxTemplateFlowIntegration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SVC_X.pc")
	if err := os.WriteFile(path, []byte(txScenarioSrc), 0o644); err != nil {
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
	tree := flow.Build(src, facts, f.Entry, f)
	axis := flow.DispatchAxisFor(src, facts, f.Entry, nil)
	if axis == nil {
		t.Fatal("no dispatch axis detected")
	}
	sc := flow.ScenarioFor(tree, axis, "A")
	if sc == nil {
		t.Fatal("no scenario")
	}
	view, _ := flow.ScenarioSource(sc, tree, src)
	if !strings.Contains(view, "fn_equ_begintran") {
		t.Fatalf("scenario slice lost the begin anchor:\n%s", view)
	}
	got, n := rewriteTxTemplate(view)
	if n != 2 {
		t.Fatalf("replaced = %d, want 2 (begin+commit):\n%s", n, got)
	}
	if !strings.Contains(got, txOpenLine) || !strings.Contains(got, "return nil\n})") {
		t.Errorf("template missing:\n%s", got)
	}
	for _, gone := range []string{"fn_equ_begintran", "fn_equ_committran", "fn_equ_aborttran", "i_h", "tpreturn(TPFAIL"} {
		if strings.Contains(got, gone) {
			t.Errorf("legacy residue %q survived:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "tpreturn(TPSUCCESS") {
		t.Errorf("live tail dropped:\n%s", got)
	}
}

// TestRewriteTxTemplate pins the helper-trio replacement: begin opens the
// ExecTransaction wrapper, the commit guard closes it (its error leg is
// subsumed by the wrapper's error return), aborts drop, and the handle
// bookkeeping (declaration, init, dead guard) drops with them.
func TestRewriteTxTemplate(t *testing.T) {
	view := "int    i_trnsctn;\n" +
		"i_trnsctn          = 0;\n" +
		"i_trnsctn = fn_equ_begintran(c_ServiceName, c_user_id, l_sssn_id, c_errmsg);\n" +
		"if (i_trnsctn == -1)\n" +
		"{\n" +
		"  err = errors.New(\"S31780\");\n" +
		"  tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0, 0);\n" +
		"}\n" +
		"s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, sqlRpdPrdtId)\n" +
		"if(fn_equ_committran(i_trnsctn) == -1)\n" +
		"{\n" +
		"  err = errors.New(\"S31800\");\n" +
		"  tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0, 0);\n" +
		"}\n" +
		"data = append(data, &models.X{PointType: \"X\"})\n"
	got, n := rewriteTxTemplate(view)
	if n != 2 {
		t.Fatalf("replaced = %d, want 2 (begin+commit):\n%s", n, got)
	}
	for _, want := range []string{
		"err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {",
		"s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, sqlRpdPrdtId)",
		"return nil\n})",
		"if err != nil {",
		"return nil, err",
		`data = append(data, &models.X{PointType: "X"})`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"fn_equ_begintran", "fn_equ_committran", "i_trnsctn", "tpreturn", "S31780", "S31800"} {
		if strings.Contains(got, gone) {
			t.Errorf("tx residue %q survived:\n%s", gone, got)
		}
	}
}

// TestRewriteTxTemplateStandaloneCommit pins the statement-form commit
// (assignment to a throwaway handle) and the abort drop.
func TestRewriteTxTemplateStandaloneCommit(t *testing.T) {
	view := "i_h2 = fn_equ_committran(c_ServiceName, c_usr_id, i_h, c_errmsg);\n" +
		"fn_equ_aborttran(c_ServiceName, i_h2, c_err_msg);\n" +
		"s.store.GetDetail(c)\n"
	got, n := rewriteTxTemplate(view)
	if n != 2 {
		t.Fatalf("replaced = %d, want 2 (commit+abort):\n%s", n, got)
	}
	if strings.Contains(got, "fn_equ_") {
		t.Errorf("legacy helper call survived:\n%s", got)
	}
	if !strings.Contains(got, "s.store.GetDetail(c)") {
		t.Errorf("live line dropped:\n%s", got)
	}
}

// TestRewriteTxTemplateATMI pins the tp* spellings (the same wrapper).
func TestRewriteTxTemplateATMI(t *testing.T) {
	view := "i_ch_val = tpbegin(TRAN_TIMEOUT, 0);\n" +
		"EXEC SQL UPDATE A SET C = 1;\n" +
		"tpcommit(0);\n"
	got, n := rewriteTxTemplate(view)
	if n != 2 {
		t.Fatalf("replaced = %d, want 2 (tpbegin+tpcommit):\n%s", n, got)
	}
	if !strings.Contains(got, txOpenLine) || !strings.Contains(got, "return nil\n})") {
		t.Errorf("template missing:\n%s", got)
	}
	if strings.Contains(got, "tpbegin") || strings.Contains(got, "tpcommit") {
		t.Errorf("ATMI spelling survived:\n%s", got)
	}
}

// TestRewriteTxTemplateUntouched pins the no-op path and the conservative
// guards: non-tx views and shape-mismatched lines stay byte-identical.
func TestRewriteTxTemplateUntouched(t *testing.T) {
	for _, view := range []string{
		"s.store.GetDetail(c)\ndata = append(data, &models.X{})\n",
		"if (fn_equ_committran(c, u, i_h, m) != OK) {\n}\n",
		"i_h = fn_equ_begintran(c, u, m); log_it();\n",
	} {
		if got, n := rewriteTxTemplate(view); n != 0 || got != view {
			t.Errorf("shape-mismatched view altered (n=%d):\n in: %q\nout: %q", n, view, got)
		}
	}
}
