package inline

import (
	"strings"
	"testing"

	"tux-to-any/internal/ir"
)

// expandKitchen runs the pass over the stripped kitchen-sink fixture — the
// service calls fn_min_check (SQL-bearing) from fn_min_lib.pc, plus
// chk_session, which the pass must refuse.
func expandKitchen(t *testing.T, opts Options) (*Result, *ir.File) {
	t.Helper()
	c, svc := loadCorpus(t)
	res, err := Expand(svc, c, opts)
	if err != nil {
		t.Fatal(err)
	}
	return res, svc
}

func siteFor(t *testing.T, res *Result, name string) Site {
	t.Helper()
	for _, s := range res.Sites {
		if s.Fn == name {
			return s
		}
	}
	t.Fatalf("no site for %s in %+v", name, res.Sites)
	return Site{}
}

func skipFor(t *testing.T, res *Result, name string) Skip {
	t.Helper()
	for _, s := range res.Skips {
		if s.Fn == name {
			return s
		}
	}
	t.Fatalf("no skip for %s in %+v", name, res.Skips)
	return Skip{}
}

// TestExpandMaterializesCalledHelper is the headline claim: a cross-file
// helper becomes a locally-defined one whose SQL is owned by it.
func TestExpandMaterializesCalledHelper(t *testing.T) {
	res, svc := expandKitchen(t, Options{})

	if len(res.Sites) != 1 {
		t.Fatalf("sites = %+v, want exactly fn_min_check", res.Sites)
	}
	s := res.Sites[0]
	if s.Fn != "fn_min_check" {
		t.Fatalf("materialized %s, want fn_min_check", s.Fn)
	}
	if !strings.HasSuffix(s.DefinedIn, libPath) {
		t.Fatalf("defined in %s, want %s", s.DefinedIn, libPath)
	}
	if s.Depth != 1 || s.Via != "" || !s.HasSQL {
		t.Fatalf("site = %+v, want depth 1, no via, HasSQL", s)
	}

	f := res.File
	// The helper is now a local function, not an external one.
	if got := strings.Join(f.Functions, ","); got != "SVC_MIN_KITCHEN,fn_min_check" {
		t.Fatalf("functions = %s", got)
	}
	if f.Entry != svc.Entry {
		t.Fatalf("entry = %q, want %q — the append must not disturb the entry", f.Entry, svc.Entry)
	}
	for _, e := range f.ExternalFns {
		if e.Name == "fn_min_check" {
			t.Fatal("fn_min_check is still external after being materialized")
		}
	}
	// Its SELECT is now a query owned by the helper, which is what gives
	// the plan layer a db unit for it.
	var owned *ir.Query
	for _, q := range f.Queries {
		if q.OwningFunction == "fn_min_check" {
			owned = q
		}
	}
	if owned == nil {
		t.Fatalf("no query owned by fn_min_check; queries = %+v", f.Queries)
	}
	if !strings.Contains(owned.SQL, "MIN_CLIENT_MAP") {
		t.Fatalf("owned query is not the helper's SELECT: %q", owned.SQL)
	}
}

// TestExpandRefusesSessionPlumbing pins that chk_session is declined and
// stays visible as a refusal, not quietly dropped.
func TestExpandRefusesSessionPlumbing(t *testing.T) {
	res, _ := expandKitchen(t, Options{})
	sk := skipFor(t, res, "chk_session")
	if sk.Code != SkipSessionPlumbing {
		t.Fatalf("code = %q, want %q", sk.Code, SkipSessionPlumbing)
	}
	// It is still an external fn, so the plan layer keeps dropping it.
	var stillExternal bool
	for _, e := range res.File.ExternalFns {
		if e.Name == "chk_session" {
			stillExternal = true
		}
	}
	if !stillExternal {
		t.Fatal("chk_session vanished — a refusal must leave the existing handling intact")
	}
}

// TestExpandPreservesCallerLineNumbers is the invariant the whole approach
// rests on: the caller's own text is untouched, so every line number its IR
// already carried is still correct.
func TestExpandPreservesCallerLineNumbers(t *testing.T) {
	res, svc := expandKitchen(t, Options{})

	origLines := strings.Split(readSource(t, svc.Path), "\n")
	newLines := strings.Split(res.Source, "\n")
	if len(newLines) < len(origLines) {
		t.Fatalf("expanded source is shorter than the original (%d < %d)", len(newLines), len(origLines))
	}
	for i, want := range origLines {
		if got := newLines[i]; got != want {
			t.Fatalf("caller line %d changed:\n got %q\nwant %q", i+1, got, want)
		}
	}
	if res.OriginalLines != len(origLines)-1 && res.OriginalLines != len(origLines) {
		t.Fatalf("OriginalLines = %d, split length %d", res.OriginalLines, len(origLines))
	}
	// And the IR agrees: the entry's facts keep their original positions.
	for _, oq := range svc.Queries {
		var found bool
		for _, nq := range res.File.Queries {
			if nq.ID == oq.ID {
				found = true
				if nq.StartLine != oq.StartLine || nq.EndLine != oq.EndLine {
					t.Fatalf("query %s moved: [%d,%d] -> [%d,%d]", oq.ID, oq.StartLine, oq.EndLine, nq.StartLine, nq.EndLine)
				}
			}
		}
		if !found {
			t.Fatalf("query %s lost in the expanded fold", oq.ID)
		}
	}
	// The caller's own conditions keep their spans too.
	if len(res.File.Conditions) != len(svc.Conditions) {
		t.Fatalf("conditions %d -> %d: the helper's branches must not become endpoint arms", len(svc.Conditions), len(res.File.Conditions))
	}
	for i := range svc.Conditions {
		if res.File.Conditions[i].StartLine != svc.Conditions[i].StartLine {
			t.Fatalf("condition %d moved", i+1)
		}
	}
}

// TestExpandIsDeterministic is the property the user asked for outright:
// identical inputs, byte-identical output — repeatedly, and across a
// freshly-built corpus (so nothing is carried over in hidden state).
func TestExpandIsDeterministic(t *testing.T) {
	first, _ := expandKitchen(t, Options{})
	for i := 0; i < 5; i++ {
		again, _ := expandKitchen(t, Options{})
		if again.Source != first.Source {
			t.Fatalf("run %d: expanded source differs", i)
		}
		if len(again.Sites) != len(first.Sites) {
			t.Fatalf("run %d: site count differs", i)
		}
		for j := range first.Sites {
			if again.Sites[j] != first.Sites[j] {
				t.Fatalf("run %d: site %d differs: %+v vs %+v", i, j, again.Sites[j], first.Sites[j])
			}
		}
		for j := range first.Skips {
			if again.Skips[j] != first.Skips[j] {
				t.Fatalf("run %d: skip %d differs: %+v vs %+v", i, j, again.Skips[j], first.Skips[j])
			}
		}
	}
	// Order is a function of the seed order, not of map iteration.
	want := []string{"fn_min_check"}
	var got []string
	for _, s := range first.Sites {
		got = append(got, s.Fn)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("site order = %v, want %v", got, want)
	}
}

// TestExpandRecordsProvenance pins that every materialized definition carries
// a banner naming its origin span — a reader of the expanded text must be
// able to get back to the real file.
func TestExpandRecordsProvenance(t *testing.T) {
	res, _ := expandKitchen(t, Options{})
	lines := strings.Split(res.Source, "\n")
	s := siteFor(t, res, "fn_min_check")

	banner := lines[s.AppendedAtLine-1]
	if !strings.Contains(banner, "tuxgo:inlined fn_min_check") {
		t.Fatalf("line %d is not the provenance banner: %q", s.AppendedAtLine, banner)
	}
	if !strings.Contains(banner, libPath) || !strings.Contains(banner, ":15-34") {
		t.Fatalf("banner does not name the origin span: %q", banner)
	}
	// AppendedToLine lands on the definition's closing brace.
	if got := strings.TrimSpace(lines[s.AppendedToLine-1]); got != "}" {
		t.Fatalf("AppendedToLine %d = %q, want the closing brace", s.AppendedToLine, got)
	}
	// Every appended line past the caller's own text is accounted for.
	if s.AppendedAtLine <= res.OriginalLines {
		t.Fatalf("appended at line %d, not past the caller's %d lines", s.AppendedAtLine, res.OriginalLines)
	}
}

// TestExpandNoExternalsIsANoOp pins the degenerate case: a caller with
// nothing to inline comes back byte-identical, so wiring the pass in cannot
// perturb files that do not need it.
func TestExpandNoExternalsIsANoOp(t *testing.T) {
	src := `#include <atmi.h>

void SVC_PLAIN(TPSVCINFO *rqst)
{
    char c_flag;
    c_flag = 'A';
    tpreturn(TPFAIL, 0L, NULL, 0L, 0);
}
`
	f, err := ir.ExtractSourceOpts([]byte(src), "plain.pc", ir.DefaultOptions().CorpusMode())
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{f}, map[string]string{"plain.pc": src})
	res, err := Expand(f, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Expanded() {
		t.Fatalf("expanded a file with no externals: %+v", res.Sites)
	}
	if res.Source != src {
		t.Fatalf("source changed:\n got %q\nwant %q", res.Source, src)
	}
	if res.Sites == nil || res.Skips == nil {
		t.Fatal("Sites/Skips must be non-nil so JSON output is stable")
	}
}

// TestExpandRefusesFmlTouchingHelper pins the hazard end to end: a helper
// that reads the caller's FML buffers is not lifted, and the refusal says
// which call gave it away.
func TestExpandRefusesFmlTouchingHelper(t *testing.T) {
	svcSrc := `#include <fml32.h>

void SVC_CALLER(TPSVCINFO *rqst)
{
    FBFR32 *ibuf;
    char c_out;
    ibuf = (FBFR32 *)rqst->data;
    if(fn_reads_buffers(ibuf, &c_out) == -1)
    {
        tpreturn(TPFAIL, 0L, NULL, 0L, 0);
    }
    tpreturn(TPSUCCESS, 0L, NULL, 0L, 0);
}
`
	libSrc := `#include <fml32.h>

int fn_reads_buffers(FBFR32 *pbuf, char *c_out)
{
    char c_flg;
    Fget32(pbuf, FML_X, 0, &c_flg, 0);
    *c_out = c_flg;
    return 1;
}
`
	opts := ir.DefaultOptions().CorpusMode()
	svcIR, err := ir.ExtractSourceOpts([]byte(svcSrc), "svc.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	libIR, err := ir.ExtractSourceOpts([]byte(libSrc), "lib.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{svcIR, libIR}, map[string]string{"svc.pc": svcSrc, "lib.pc": libSrc})
	res, err := Expand(svcIR, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Expanded() {
		t.Fatalf("lifted an FML-touching helper: %+v", res.Sites)
	}
	sk := skipFor(t, res, "fn_reads_buffers")
	if sk.Code != SkipFmlTraffic {
		t.Fatalf("code = %q, want %q", sk.Code, SkipFmlTraffic)
	}
	if !strings.Contains(sk.Detail, "Fget32") {
		t.Fatalf("detail does not name the call: %s", sk.Detail)
	}
}

// TestExpandFollowsHelperOfHelper pins the transitive case, including the
// depth bookkeeping and the cycle report. fn_outer calls fn_inner, which
// calls fn_outer again — the third hop must be refused as a cycle, not
// followed, and not merely depth-bounded.
func TestExpandFollowsHelperOfHelper(t *testing.T) {
	svcSrc := `void SVC_TOPLEVEL(TPSVCINFO *rqst)
{
    char c_out;
    if(fn_outer(c_out) == -1)
    {
        tpreturn(TPFAIL, 0L, NULL, 0L, 0);
    }
    tpreturn(TPSUCCESS, 0L, NULL, 0L, 0);
}
`
	libSrc := `int fn_outer(char *c_out)
{
    if(fn_inner(*c_out) == -1)
    {
        return -1;
    }
    return 1;
}

int fn_inner(char c_in)
{
    if(fn_outer(&c_in) == -1)
    {
        return -1;
    }
    return 1;
}
`
	opts := ir.DefaultOptions().CorpusMode()
	svcIR, err := ir.ExtractSourceOpts([]byte(svcSrc), "svc.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	libIR, err := ir.ExtractSourceOpts([]byte(libSrc), "lib.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{svcIR, libIR}, map[string]string{"svc.pc": svcSrc, "lib.pc": libSrc})
	res, err := Expand(svcIR, c, Options{})
	if err != nil {
		t.Fatal(err)
	}

	outer := siteFor(t, res, "fn_outer")
	if outer.Depth != 1 || outer.Via != "" {
		t.Fatalf("fn_outer = %+v, want depth 1 with no via", outer)
	}
	inner := siteFor(t, res, "fn_inner")
	if inner.Depth != 2 || inner.Via != "fn_outer" {
		t.Fatalf("fn_inner = %+v, want depth 2 pulled in by fn_outer", inner)
	}
	// fn_outer is already on fn_inner's chain, so the third hop is a cycle.
	var cycle Skip
	for _, s := range res.Skips {
		if s.Code == SkipCycle {
			cycle = s
		}
	}
	if cycle.Fn != "fn_outer" {
		t.Fatalf("no cycle reported; skips = %+v", res.Skips)
	}
	if !strings.Contains(cycle.Detail, "fn_outer") || !strings.Contains(cycle.Detail, "fn_inner") {
		t.Fatalf("cycle detail does not name the chain: %s", cycle.Detail)
	}
	// Both definitions are in the expanded text, exactly once each.
	if n := strings.Count(res.Source, "int fn_outer("); n != 1 {
		t.Fatalf("fn_outer appears %d times, want 1", n)
	}
	if n := strings.Count(res.Source, "int fn_inner("); n != 1 {
		t.Fatalf("fn_inner appears %d times, want 1", n)
	}
}

// TestExpandMaxDepthBounds pins the depth bound independently of cycles.
func TestExpandMaxDepthBounds(t *testing.T) {
	svcSrc := `void SVC_D(TPSVCINFO *rqst)
{
    char c_out;
    if(fn_a(c_out) == -1) { tpreturn(TPFAIL, 0L, NULL, 0L, 0); }
    tpreturn(TPSUCCESS, 0L, NULL, 0L, 0);
}
`
	libSrc := `int fn_a(char *p)
{
    if(fn_b(*p) == -1) { return -1; }
    return 1;
}

int fn_b(char p)
{
    if(fn_c(p) == -1) { return -1; }
    return 1;
}

int fn_c(char p)
{
    return p == 0 ? 0 : 1;
}
`
	opts := ir.DefaultOptions().CorpusMode()
	svcIR, err := ir.ExtractSourceOpts([]byte(svcSrc), "svc.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	libIR, err := ir.ExtractSourceOpts([]byte(libSrc), "lib.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{svcIR, libIR}, map[string]string{"svc.pc": svcSrc, "lib.pc": libSrc})

	res, err := Expand(svcIR, c, Options{MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	siteFor(t, res, "fn_a")
	siteFor(t, res, "fn_b")
	if res.Expanded() && strings.Contains(res.Source, "int fn_c(") {
		t.Fatal("fn_c is at depth 3 and MaxDepth is 2")
	}
	sk := skipFor(t, res, "fn_c")
	if sk.Code != SkipMaxDepth {
		t.Fatalf("code = %q, want %q", sk.Code, SkipMaxDepth)
	}
}

// TestExpandLiftsMissingGlobal pins that a helper's own file-scope
// declaration travels with it — otherwise the lifted body references a name
// the caller never declares.
func TestExpandLiftsMissingGlobal(t *testing.T) {
	svcSrc := `void SVC_G(TPSVCINFO *rqst)
{
    char c_out;
    if(fn_uses_global(&c_out) == -1) { tpreturn(TPFAIL, 0L, NULL, 0L, 0); }
    tpreturn(TPSUCCESS, 0L, NULL, 0L, 0);
}
`
	libSrc := `char c_threshold[10];

int fn_uses_global(char *c_out)
{
    if(c_threshold[0] == 'X')
    {
        return -1;
    }
    *c_out = c_threshold[0];
    return 1;
}
`
	opts := ir.DefaultOptions().CorpusMode()
	svcIR, err := ir.ExtractSourceOpts([]byte(svcSrc), "svc.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	libIR, err := ir.ExtractSourceOpts([]byte(libSrc), "lib.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{svcIR, libIR}, map[string]string{"svc.pc": svcSrc, "lib.pc": libSrc})
	res, err := Expand(svcIR, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Expanded() {
		t.Fatal("helper not lifted")
	}
	if len(res.AppendedGlobals) != 1 || res.AppendedGlobals[0] != "c_threshold" {
		t.Fatalf("appended globals = %v, want [c_threshold]", res.AppendedGlobals)
	}
	if !strings.Contains(res.Source, "char c_threshold[10];") {
		t.Fatal("the global declaration is missing from the expanded source")
	}
}

// TestExpandCallerGlobalWins pins the collision rule: when both files
// declare the same file-scope name, the caller's declaration stands and the
// callee's is not re-declared.
func TestExpandCallerGlobalWins(t *testing.T) {
	svcSrc := `char c_threshold[4];

void SVC_G2(TPSVCINFO *rqst)
{
    char c_out;
    if(fn_reads_threshold(&c_out) == -1) { tpreturn(TPFAIL, 0L, NULL, 0L, 0); }
    tpreturn(TPSUCCESS, 0L, NULL, 0L, 0);
}
`
	libSrc := `char c_threshold[99];

int fn_reads_threshold(char *c_out)
{
    *c_out = c_threshold[0];
    return 1;
}
`
	opts := ir.DefaultOptions().CorpusMode()
	svcIR, err := ir.ExtractSourceOpts([]byte(svcSrc), "svc.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	libIR, err := ir.ExtractSourceOpts([]byte(libSrc), "lib.pc", opts)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{svcIR, libIR}, map[string]string{"svc.pc": svcSrc, "lib.pc": libSrc})
	res, err := Expand(svcIR, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one DECLARATION survives: the caller's. (Counting the
	// subscripted uses inside the lifted body would count the wrong thing —
	// the collision rule is about declarations.)
	if n := strings.Count(res.Source, "char c_threshold["); n != 1 {
		t.Fatalf("c_threshold declared %d times, want 1 (the caller's own)", n)
	}
	if !strings.Contains(res.Source, "char c_threshold[4];") {
		t.Fatal("the caller's declaration should have been kept")
	}
	if len(res.AppendedGlobals) != 0 {
		t.Fatalf("appended globals = %v, want none", res.AppendedGlobals)
	}
}

// TestExpandUnresolvedHelperStaysExternal pins the never-guess rule: a helper
// no corpus file defines is reported and left exactly as the plan layer
// expects to find it.
func TestExpandUnresolvedHelperStaysExternal(t *testing.T) {
	res, _ := expandKitchen(t, Options{})
	for _, e := range res.File.ExternalFns {
		if e.Resolved && e.DefinedIn == "" {
			t.Fatalf("an unresolved helper came back resolved: %+v", e)
		}
	}
}
