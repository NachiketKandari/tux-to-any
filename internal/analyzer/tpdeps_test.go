package analyzer

// Own testcases for the TP→SVC dependency feature: per-file tpcall/tpacall
// targets resolve to defining files in the scanned tree, and each target's
// own complexity rolls into the caller's dep score (own score untouched).

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	scanner "tux-to-any/internal/tsscan"
)

const tpCallerSrc = `#include <atmi.h>
void SVC_TP_CALLER(TPSVCINFO* rqst)
{
	char *sbuffer;
	char **rbuffer;
	long llen;
	char *svcname;
	if(tpcall("SVC_TP_ALPHA",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
	{
		userlog("first failed");
	}
	if(tpacall("SVC_TP_BETA",sbuffer,0,TPNOTRANS) == -1)
	{
		userlog("async failed");
	}
	if(tpcall("SVC_TP_ALPHA",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
	{
		userlog("retry failed");
	}
	if(tpcall(svcname,sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
	{
		userlog("dynamic failed");
	}
	if(tpcall("SVC_TP_GONE",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
	{
		userlog("gone failed");
	}
}
`

const tpAlphaSrc = `void SVC_TP_ALPHA(TPSVCINFO* rqst)
{
	long c;
	EXEC SQL SELECT COUNT(*) INTO :c FROM DUAL;
	EXEC SQL UPDATE T SET A = 1 WHERE B = 2;
	if(c > 0)
	{
		EXEC SQL DELETE FROM T WHERE B = 3;
	}
}
`

const tpBetaSrc = `void SVC_TP_BETA(TPSVCINFO* rqst)
{
	long c;
	EXEC SQL SELECT 1 INTO :c FROM DUAL;
}
`

// writeTpTree lays out the caller + two resolvable targets; SVC_TP_GONE is
// deliberately absent and svcname is dynamic.
func writeTpTree(t *testing.T) (dir, caller string) {
	t.Helper()
	dir = t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("writing fixture %s failed: %v", name, err)
		}
	}
	write("SVC_TP_CALLER.pc", tpCallerSrc)
	write("SVC_TP_ALPHA.pc", tpAlphaSrc)
	write("SVC_TP_BETA.pc", tpBetaSrc)
	return dir, filepath.Join(dir, "SVC_TP_CALLER.pc")
}

func tpReportByFile(t *testing.T, reports []*Report, base string) *Report {
	t.Helper()
	for _, r := range reports {
		if filepath.Base(r.File) == base {
			return r
		}
	}
	t.Fatalf("report for %s not found in %d reports", base, len(reports))
	return nil
}

func tpDepByService(rep *Report, svc string) *TpSvcDep {
	for i := range rep.TpSvcDeps {
		if rep.TpSvcDeps[i].Service == svc {
			return &rep.TpSvcDeps[i]
		}
	}
	return nil
}

func TestTpDepResolutionDirMode(t *testing.T) {
	dir, _ := writeTpTree(t)
	reports, err := AnalyzeDir(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeDir failed: %v", err)
	}
	caller := tpReportByFile(t, reports, "SVC_TP_CALLER.pc")
	alpha := tpReportByFile(t, reports, "SVC_TP_ALPHA.pc")
	beta := tpReportByFile(t, reports, "SVC_TP_BETA.pc")

	// 5 tp sites, all counted as before (sync + async alike).
	if caller.TpCallCount != 5 {
		t.Errorf("expected 5 tpcalls, got %d", caller.TpCallCount)
	}
	// Own score excludes dep complexity: 5 tpcalls × 20 + 5 top-level
	// ifs × 1, no queries, no external fns.
	if caller.ComplexityScore != 105 {
		t.Errorf("own score must stay 105 (deps roll up separately), got %d", caller.ComplexityScore)
	}

	if len(caller.TpSvcDeps) != 4 {
		t.Fatalf("expected 4 tp svc deps, got %v", caller.TpSvcDeps)
	}
	a := tpDepByService(caller, "SVC_TP_ALPHA")
	if a == nil {
		t.Fatalf("missing SVC_TP_ALPHA dep in %v", caller.TpSvcDeps)
	}
	if a.Calls != 2 || a.Kind != "tpcall" {
		t.Errorf("ALPHA want 2 sync calls, got %+v", a)
	}
	if !a.Resolved || a.File != alpha.File {
		t.Errorf("ALPHA must resolve to %s, got %+v", alpha.File, a)
	}
	if a.Score != alpha.ComplexityScore {
		t.Errorf("ALPHA dep score %d must equal target score %d", a.Score, alpha.ComplexityScore)
	}
	// 3 queries + 1 top-level if = 4.
	if alpha.ComplexityScore != 4 {
		t.Errorf("expected ALPHA score 4, got %d", alpha.ComplexityScore)
	}
	b := tpDepByService(caller, "SVC_TP_BETA")
	if b == nil || b.Calls != 1 || b.Kind != "tpacall" {
		t.Errorf("BETA want 1 async call, got %+v", b)
	}
	if !b.Resolved || b.File != beta.File || b.Score != beta.ComplexityScore {
		t.Errorf("BETA must resolve with target score, got %+v", b)
	}
	if g := tpDepByService(caller, "SVC_TP_GONE"); g == nil || g.Resolved {
		t.Errorf("GONE must be an unresolved dep, got %+v", g)
	}
	if d := tpDepByService(caller, tpDynamicService); d == nil || d.Resolved || d.Calls != 1 {
		t.Errorf("dynamic target must be an unresolved dep, got %+v", d)
	}

	if caller.TpUnresolved != 2 {
		t.Errorf("expected 2 unresolved deps, got %d", caller.TpUnresolved)
	}
	if want := alpha.ComplexityScore + beta.ComplexityScore; caller.TpDepScore != want {
		t.Errorf("expected dep score %d, got %d", want, caller.TpDepScore)
	}
	if caller.TpTotalScore != caller.ComplexityScore+caller.TpDepScore {
		t.Errorf("total must be own + dep, got %+v", caller)
	}
	if !strings.Contains(caller.Reasons, "tp svc dep score +") {
		t.Errorf("reasons must carry the dep score: %s", caller.Reasons)
	}
	if !strings.Contains(caller.Reasons, "tp svc unresolved: "+tpDynamicService+", SVC_TP_GONE") {
		t.Errorf("reasons must name the unresolved targets: %s", caller.Reasons)
	}

	// Targets themselves carry no outbound deps.
	if len(alpha.TpSvcDeps) != 0 || alpha.TpDepScore != 0 || alpha.TpTotalScore != alpha.ComplexityScore {
		t.Errorf("ALPHA must have empty dep roll-up, got %+v", alpha)
	}
}

func TestTpDepAmbiguousStaysUnresolved(t *testing.T) {
	dir := t.TempDir()
	dup := `void SVC_TP_DUP(TPSVCINFO* rqst)
{
	userlog("dup");
}
`
	for _, sub := range []string{"sub1", "sub2"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "SVC_TP_DUP.pc"), []byte(dup), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	caller := `#include <atmi.h>
void SVC_TP_DCALLER(TPSVCINFO* rqst)
{
	char *sbuffer;
	char **rbuffer;
	long llen;
	if(tpcall("SVC_TP_DUP",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
	{
		userlog("dup failed");
	}
}
`
	callerPath := filepath.Join(dir, "SVC_TP_DCALLER.pc")
	if err := os.WriteFile(callerPath, []byte(caller), 0o644); err != nil {
		t.Fatal(err)
	}
	reports, err := AnalyzeDir(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeDir failed: %v", err)
	}
	rep := tpReportByFile(t, reports, "SVC_TP_DCALLER.pc")
	d := tpDepByService(rep, "SVC_TP_DUP")
	if d == nil {
		t.Fatalf("missing SVC_TP_DUP dep in %v", rep.TpSvcDeps)
	}
	if d.Resolved || d.File != "" {
		t.Errorf("ambiguous target must stay unresolved, got %+v", d)
	}
	if rep.TpUnresolved != 1 || rep.TpDepScore != 0 {
		t.Errorf("ambiguous dep contributes nothing, got %+v", rep)
	}
}

func TestTpDepSingleFileStaysUnresolved(t *testing.T) {
	_, caller := writeTpTree(t)
	rep, err := AnalyzeFile(caller, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeFile failed: %v", err)
	}
	if len(rep.TpSvcDeps) != 4 {
		t.Fatalf("single-file mode must still list 4 deps, got %v", rep.TpSvcDeps)
	}
	for _, d := range rep.TpSvcDeps {
		if d.Resolved {
			t.Errorf("single-file mode must not resolve %s", d.Service)
		}
	}
	if rep.TpUnresolved != 4 || rep.TpDepScore != 0 {
		t.Errorf("single-file mode resolves nothing, got %+v", rep)
	}
	if rep.TpTotalScore != rep.ComplexityScore {
		t.Errorf("single-file total must equal own score, got %+v", rep)
	}
}

func TestTpCallServiceParsing(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"plain", `"SVC_A",sbuffer,0,&rbuffer,&llen,TPNOTRANS`, "SVC_A"},
		{"spaced", `  "SVC_B" , sbuffer , 0`, "SVC_B"},
		{"async-shape", `"SVC_C",sbuffer,0,TPNOTRANS`, "SVC_C"},
		{"variable", `svcname,sbuffer,0,&rbuffer,&llen,TPNOTRANS`, ""},
		{"call-expr", `get_svc(),sbuffer,0`, ""},
		{"comma-in-literal", `"A,B",sbuffer,0`, "A,B"},
		{"empty", ``, ""},
		{"unbalanced", `"SVC_D",sbuffer`, "SVC_D"},
	}
	for _, c := range cases {
		if got := tpCallService(&scanner.FunctionCall{Name: "tpcall", Args: c.args}); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTpDepCSVColumns(t *testing.T) {
	dir, _ := writeTpTree(t)
	reports, err := AnalyzeDir(dir, DefaultOptions())
	if err != nil {
		t.Fatalf("AnalyzeDir failed: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, reports, DefaultMarks()); err != nil {
		t.Fatalf("WriteCSV failed: %v", err)
	}
	reader := csv.NewReader(strings.NewReader(buf.String()))
	reader.FieldsPerRecord = -1
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("reading CSV back failed: %v", err)
	}
	var header []string
	rows := map[string][]string{}
	for _, rec := range records {
		if len(rec) == 0 {
			continue
		}
		if rec[0] == "file" {
			header = rec
			continue
		}
		if header == nil {
			continue // marks cells row
		}
		rows[rec[0]] = rec
	}
	tail := header[len(header)-6:]
	for i, want := range []string{"tp_svc_deps", "tp_dep_score", "tp_unresolved", "fn_file_deps", "fn_dep_score", "tp_total_score"} {
		if tail[i] != want {
			t.Fatalf("trailing columns = %v, want [... tp_svc_deps tp_dep_score tp_unresolved fn_file_deps fn_dep_score tp_total_score]", tail)
		}
	}
	col := map[string]int{}
	for i, name := range header {
		col[name] = i
	}
	caller := tpReportByFile(t, reports, "SVC_TP_CALLER.pc")
	row := rows[caller.File]
	if row == nil {
		t.Fatalf("no CSV row for %s", caller.File)
	}
	cell := row[col["tp_svc_deps"]]
	if !strings.Contains(cell, "SVC_TP_ALPHA=>"+caller.File[:len(caller.File)-len("SVC_TP_CALLER.pc")]) {
		t.Errorf("tp_svc_deps must resolve ALPHA to its file: %q", cell)
	}
	if !strings.Contains(cell, "SVC_TP_GONE=>UNRESOLVED") || !strings.Contains(cell, tpDynamicService+"=>UNRESOLVED") {
		t.Errorf("tp_svc_deps must mark missing/dynamic UNRESOLVED: %q", cell)
	}
	if got, _ := strconv.Atoi(row[col["tp_dep_score"]]); got != caller.TpDepScore {
		t.Errorf("tp_dep_score cell %q must equal %d", row[col["tp_dep_score"]], caller.TpDepScore)
	}
	if got, _ := strconv.Atoi(row[col["tp_unresolved"]]); got != 2 {
		t.Errorf("tp_unresolved cell %q must equal 2", row[col["tp_unresolved"]])
	}
	total := row[col["tp_total_score"]]
	if !strings.HasPrefix(total, "=") || strings.Count(total, "+") != 2 {
		t.Errorf("tp_total_score must be a =own+tpdep+fndep formula, got %q", total)
	}

	// The extended schema must still feed the weights re-score flow.
	weightsPath := filepath.Join(t.TempDir(), "weights.csv")
	if err := os.WriteFile(weightsPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, err := LoadOptionsCSV(weightsPath)
	if err != nil {
		t.Fatalf("LoadOptionsCSV must accept the dep columns: %v", err)
	}
	if opts.Marks != DefaultMarks() {
		t.Errorf("marks must round-trip, got %+v", opts.Marks)
	}
}
