package analyzer

// Transitive-closure, cycle-safety, and FN-file roll-up cases for the
// TP→SVC dependency feature. Each tree is self-contained in a temp dir.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTpFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("writing fixture %s failed: %v", name, err)
		}
	}
}

// TestTpDepTransitiveRollUp pins the DFS the user asked for: C1 calls C2,
// C2 calls C3 — C1's dep score folds the whole chain, with depths.
func TestTpDepTransitiveRollUp(t *testing.T) {
	dir := t.TempDir()
	writeTpFiles(t, dir, map[string]string{
		"SVC_TP_C1.pc": `void SVC_TP_C1(TPSVCINFO* rqst)
{
	char *sbuffer;
	char **rbuffer;
	long llen;
	long r;
	r = tpcall("SVC_TP_C2",sbuffer,0,&rbuffer,&llen,TPNOTRANS);
}
`,
		"SVC_TP_C2.pc": `void SVC_TP_C2(TPSVCINFO* rqst)
{
	char *sbuffer;
	char **rbuffer;
	long llen;
	long r;
	r = tpcall("SVC_TP_C3",sbuffer,0,&rbuffer,&llen,TPNOTRANS);
}
`,
		"SVC_TP_C3.pc": `void SVC_TP_C3(TPSVCINFO* rqst)
{
	long c;
	EXEC SQL SELECT 1 INTO :c FROM DUAL;
}
`,
	})
	reports, err := AnalyzeDir(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeDir failed: %v", err)
	}
	c1 := tpReportByFile(t, reports, "SVC_TP_C1.pc")
	c2 := tpReportByFile(t, reports, "SVC_TP_C2.pc")
	c3 := tpReportByFile(t, reports, "SVC_TP_C3.pc")

	// Own scores: C1 = 1 tpcall (20); C2 = 1 tpcall (20); C3 = 1 query (1).
	if c1.ComplexityScore != 20 || c2.ComplexityScore != 20 || c3.ComplexityScore != 1 {
		t.Fatalf("own scores = %d/%d/%d, want 20/20/1", c1.ComplexityScore, c2.ComplexityScore, c3.ComplexityScore)
	}
	if len(c1.TpSvcDeps) != 2 {
		t.Fatalf("C1 must see C2 + transitive C3, got %v", c1.TpSvcDeps)
	}
	d2 := tpDepByService(c1, "SVC_TP_C2")
	d3 := tpDepByService(c1, "SVC_TP_C3")
	if d2 == nil || !d2.Resolved || d2.Depth != 1 || d2.Score != 20 {
		t.Errorf("C2 must be a resolved depth-1 dep worth 20, got %+v", d2)
	}
	if d3 == nil || !d3.Resolved || d3.Depth != 2 || d3.Score != 1 {
		t.Errorf("C3 must be a resolved depth-2 dep worth 1, got %+v", d3)
	}
	if c1.TpDepScore != 21 {
		t.Errorf("C1 dep score must fold the chain (20+1), got %d", c1.TpDepScore)
	}
	if c1.TpTotalScore != 41 {
		t.Errorf("C1 total must be 20+21, got %d", c1.TpTotalScore)
	}
	if !strings.Contains(c1.Reasons, "(1 transitive)") {
		t.Errorf("reasons must flag the transitive hop: %s", c1.Reasons)
	}
	// Mid-chain report is unaffected in its own right.
	if c2.TpDepScore != 1 || c2.TpTotalScore != 21 {
		t.Errorf("C2 must roll only C3: dep %d total %d", c2.TpDepScore, c2.TpTotalScore)
	}
}

// TestTpDepCycleTerminates pins cycle safety: Y1↔Y2 must terminate and
// price each side once, never rolling a file into itself.
func TestTpDepCycleTerminates(t *testing.T) {
	dir := t.TempDir()
	writeTpFiles(t, dir, map[string]string{
		"SVC_TP_Y1.pc": `void SVC_TP_Y1(TPSVCINFO* rqst)
{
	char *sbuffer;
	char **rbuffer;
	long llen;
	long r;
	r = tpcall("SVC_TP_Y2",sbuffer,0,&rbuffer,&llen,TPNOTRANS);
}
`,
		"SVC_TP_Y2.pc": `void SVC_TP_Y2(TPSVCINFO* rqst)
{
	char *sbuffer;
	char **rbuffer;
	long llen;
	long r;
	r = tpcall("SVC_TP_Y1",sbuffer,0,&rbuffer,&llen,TPNOTRANS);
}
`,
	})
	reports, err := AnalyzeDir(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeDir failed: %v", err)
	}
	y1 := tpReportByFile(t, reports, "SVC_TP_Y1.pc")
	if len(y1.TpSvcDeps) != 1 {
		t.Fatalf("Y1 must see exactly Y2 (self excluded), got %v", y1.TpSvcDeps)
	}
	d := tpDepByService(y1, "SVC_TP_Y2")
	if d == nil || !d.Resolved || d.Depth != 1 || d.Score != 20 {
		t.Errorf("Y2 must be a resolved depth-1 dep worth 20, got %+v", d)
	}
	if y1.TpDepScore != 20 || y1.TpTotalScore != 40 {
		t.Errorf("cycle must price the peer once: dep %d total %d", y1.TpDepScore, y1.TpTotalScore)
	}
}

// TestFnFileDepRollUp pins the FN ask: fn_ext_price/fn_ext_tax live in
// fn_ext_price.pc (a <function-name>.pc file); the caller prices both the
// tier weight and the defining file's real score — shared file counts once.
func TestFnFileDepRollUp(t *testing.T) {
	dir := t.TempDir()
	writeTpFiles(t, dir, map[string]string{
		"SVC_TP_FCALLER.pc": `void SVC_TP_FCALLER(TPSVCINFO* rqst)
{
	char buf[64];
	long p;
	long v;
	p = fn_ext_price(buf);
	v = fn_ext_tax(buf);
}
`,
		"fn_ext_price.pc": `long fn_ext_price(char *buf)
{
	long c;
	EXEC SQL SELECT COUNT(*) INTO :c FROM DUAL;
	return c;
}
long fn_ext_tax(char *buf)
{
	return 1;
}
`,
	})
	reports, err := AnalyzeDir(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeDir failed: %v", err)
	}
	caller := tpReportByFile(t, reports, "SVC_TP_FCALLER.pc")
	// Tier weights stay: SQL-bearing fn_ext_price complex +10, pure
	// fn_ext_tax simple +5 → own score 15.
	if caller.ComplexityScore != 15 {
		t.Fatalf("own score must stay 15 (tier weights), got %d", caller.ComplexityScore)
	}
	if len(caller.FnFileDeps) != 1 {
		t.Fatalf("one shared defining file must roll once, got %+v", caller.FnFileDeps)
	}
	fd := caller.FnFileDeps[0]
	if !strings.HasSuffix(fd.File, "fn_ext_price.pc") || fd.Score != 1 {
		t.Errorf("fn file dep must carry the real body score 1, got %+v", fd)
	}
	if len(fd.Fns) != 2 || fd.Fns[0] != "fn_ext_price" || fd.Fns[1] != "fn_ext_tax" {
		t.Errorf("fn file dep must name both callers, got %+v", fd)
	}
	if caller.FnDepScore != 1 {
		t.Errorf("fn dep score must count the shared file once, got %d", caller.FnDepScore)
	}
	if caller.TpTotalScore != 16 {
		t.Errorf("total must be 15 own + 1 fn file, got %d", caller.TpTotalScore)
	}
	if !strings.Contains(caller.Reasons, "fn file dep score +1") {
		t.Errorf("reasons must carry the fn file roll-up: %s", caller.Reasons)
	}
}
