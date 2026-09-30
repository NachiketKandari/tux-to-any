package ir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cross-call join's own tests.
//
// svccall_corpus_test.go pins the FIXTURE — that a resolvable callee and a
// two-sided contract exist to be joined. This file pins the JOIN: the
// bindings it concludes, the gaps it reports, the notes it attaches, and the
// conditions under which it declines to run at all. The distinction is
// deliberate. A fixture test recomputing the conclusion would agree with a
// broken join for the same reason it agreed with a correct one, so the
// conclusion is asserted here against the IR's own output.

// writeCorpus materialises a .pc corpus in a temp dir and extracts it the way
// directory mode does, so the join runs exactly as it would on a real
// client codebase. Synthetic rather than tracked fixtures: these cases are
// shapes the corpus has no example of, and each one asserts a REFUSAL, so
// they belong next to the rule rather than in the golden corpus.
func writeCorpus(t *testing.T, files map[string]string) []*File {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := ExtractDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// joinOf returns the single tpcall of the single caller in a corpus, failing
// the test if the shape is not what the caller expected.
func joinOf(t *testing.T, files []*File, callerBase string) *TPCall {
	t.Helper()
	for _, f := range files {
		if filepath.Base(f.Path) != callerBase {
			continue
		}
		if len(f.TPCalls) != 1 {
			t.Fatalf("%s has %d tpcalls, want 1", callerBase, len(f.TPCalls))
		}
		return &f.TPCalls[0]
	}
	t.Fatalf("no %s in corpus", callerBase)
	return nil
}

// bindingFor returns the binding for a field, or nil.
func bindingFor(c *TPCallee, field string) *TPBinding {
	for i := range c.Bindings {
		if c.Bindings[i].Field == field {
			return &c.Bindings[i]
		}
	}
	return nil
}

// issueFor returns the first issue with a code for a field, or nil.
func issueFor(c *TPCallee, code TPIssueCode, field string) *TPIssue {
	for i := range c.Issues {
		if c.Issues[i].Code == code && c.Issues[i].Field == field {
			return &c.Issues[i]
		}
	}
	return nil
}

const joinCaller = `void SVC_J_CALLER(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char c_uid[33];
    long i_amt;
    char *sbuffer;
    char **rbuffer;
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_USER_ID,(char *)c_uid,0);
    Fadd32((FBFR32*)sbuffer,FML_AMT,(char *)&i_amt,0);
    if(tpcall("SVC_J_CALLEE",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("failed");
    }
    Fget32((FBFR32*)rbuffer,FML_ACC_ID,0,(char *)&i_amt,0);
}
`

// joinCallee reads the same two FIELDS as the caller above but into variables
// with entirely unrelated names. That is the whole point of joining on the
// field name: nothing about the two sides lines up except the FML identifier.
const joinCallee = `void SVC_J_CALLEE(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    FBFR32 *ptr_fml_Obuffer;
    char svc_acct_ref[33];
    char svc_amount_txt[19];
    long svc_row_id;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    ptr_fml_Obuffer = (FBFR32*)rqst->data;

    Fget32(ptr_fml_Ibuffer,FML_USER_ID,0,(char *)svc_acct_ref,0);
    Fget32(ptr_fml_Ibuffer,FML_AMT,0,(char *)svc_amount_txt,0);
    Fadd32(ptr_fml_Obuffer,FML_ACC_ID,(char *)&svc_row_id,0);
    tpreturn(TPSUCCESS,0L,(char *)ptr_fml_Obuffer,0L,0);
}
`

func joinedCorpus(t *testing.T) *TPCallee {
	t.Helper()
	tc := joinOf(t, writeCorpus(t, map[string]string{
		"SVC_J_CALLER.pc": joinCaller,
		"SVC_J_CALLEE.pc": joinCallee,
	}), "SVC_J_CALLER.pc")
	if tc.Callee == nil {
		t.Fatalf("no callee joined (refusal %q) — the fixture's callee is in the corpus", tc.JoinRefusal)
	}
	return tc.Callee
}

// TestJoinBindsAcrossUnrelatedHostVariables is the headline case: the same
// FML field, held in `c_uid` on the calling side and `svc_acct_ref` on the
// called side. Nothing else about the two statements is alike, and a join
// keyed on the host variable — the obvious thing to reach for, and the thing
// a reader would assume this code does — would produce nothing at all.
func TestJoinBindsAcrossUnrelatedHostVariables(t *testing.T) {
	c := joinedCorpus(t)

	uid := bindingFor(c, "FML_USER_ID")
	if uid == nil {
		t.Fatal("FML_USER_ID did not bind, though both sides touch it")
	}
	if uid.CallerVar != "c_uid" {
		t.Errorf("FML_USER_ID CallerVar = %q, want c_uid", uid.CallerVar)
	}
	if uid.CalleeVar != "svc_acct_ref" {
		t.Errorf("FML_USER_ID CalleeVar = %q, want svc_acct_ref", uid.CalleeVar)
	}
	if uid.Direction != "in" {
		t.Errorf("FML_USER_ID Direction = %q, want in", uid.Direction)
	}

	amt := bindingFor(c, "FML_AMT")
	if amt == nil {
		t.Fatal("FML_AMT did not bind, though both sides touch it")
	}
	if amt.CallerVar != "i_amt" || amt.CalleeVar != "svc_amount_txt" {
		t.Errorf("FML_AMT bound %q -> %q, want i_amt -> svc_amount_txt", amt.CallerVar, amt.CalleeVar)
	}

	acc := bindingFor(c, "FML_ACC_ID")
	if acc == nil {
		t.Fatal("FML_ACC_ID did not bind, though the callee writes it and the caller reads it")
	}
	if acc.Direction != "out" {
		t.Errorf("FML_ACC_ID Direction = %q, want out", acc.Direction)
	}
	if acc.CalleeVar != "svc_row_id" || acc.CallerVar != "i_amt" {
		t.Errorf("FML_ACC_ID bound %q -> %q, want svc_row_id -> i_amt", acc.CalleeVar, acc.CallerVar)
	}
}

// TestJoinOrdersBindingsAndIssuesDeterministically pins that the output is a
// function of the corpus and not of map iteration. Everything the join emits
// is gathered from maps keyed on field name; without an explicit sort two runs
// over identical input can differ, which would make every golden of it
// unreproducible.
func TestJoinOrdersBindingsAndIssuesDeterministically(t *testing.T) {
	first := joinedCorpus(t)
	// Compare the join's CONCLUSIONS, not the whole TPCallee: Callee.Path is
	// a filesystem path and each run gets its own temp dir, so including it
	// would make this test fail for a reason that has nothing to do with
	// determinism. The fields below are the ones the join derives.
	summarise := func(c *TPCallee) string {
		type view struct {
			Expects  []TPField   `json:"expects"`
			Produces []TPField   `json:"produces"`
			Bindings []TPBinding `json:"bindings"`
			Issues   []TPIssue   `json:"issues"`
		}
		b, _ := json.Marshal(view{c.Expects, c.Produces, c.Bindings, c.Issues})
		return string(b)
	}
	firstJSON := summarise(first)
	for run := 0; run < 5; run++ {
		again := joinedCorpus(t)
		if got := summarise(again); got != firstJSON {
			t.Fatalf("run %d differs from the first:\n first: %s\n  again: %s", run, firstJSON, got)
		}
	}
	// Sorted by field name, not merely deterministic.
	names := make([]string, 0, len(first.Bindings))
	for _, b := range first.Bindings {
		names = append(names, b.Field)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("bindings are not sorted by field: %v", names)
			break
		}
	}
}

// TestJoinReportsEveryOneSidedField walks the svccall fixture, which was
// built to straddle: one field sent and never read, one read back and never
// written, one written and never read. All three must be reported — a join
// that only checks the direction that breaks things would catch only one.
func TestJoinReportsEveryOneSidedField(t *testing.T) {
	files, err := ExtractDir(svccallFixture)
	if err != nil {
		t.Fatal(err)
	}
	var caller *File
	for _, f := range files {
		if filepath.Base(f.Path) == "SVC_SVC_CALLER.pc" {
			caller = f
		}
	}
	if caller == nil {
		t.Fatal("svccall fixture missing its caller")
	}
	tp := &caller.TPCalls[0]
	if tp.Callee == nil {
		t.Fatalf("svccall's callee is in the corpus but did not join (refusal %q)", tp.JoinRefusal)
	}
	c := tp.Callee

	// Sent and never read: dead traffic, no default-value semantics.
	if iss := issueFor(c, IssueSentUnread, "FML_MODE_FLG"); iss == nil {
		t.Error("FML_MODE_FLG is sent and never read, but no sent_unread was reported")
	} else if strings.Contains(iss.Note, "FNOTPRES") {
		t.Error("a sent_unread finding must not carry the FNOTPRES explanation — " +
			"nothing fails when a field is simply never read")
	}
	// Read back and never written: the direction that can produce wrong
	// behaviour, and the one the FNOTPRES note is about.
	lst := issueFor(c, IssueCallerReadsUnwritten, "FML_LST_UPD")
	if lst == nil {
		t.Fatal("FML_LST_UPD is read back and never written, but no caller_reads_unwritten was reported")
	}
	if !strings.Contains(lst.Note, "FNOTPRES") {
		t.Error("caller_reads_unwritten carries no FNOTPRES explanation")
	}
	if !strings.Contains(lst.Note, "i_min_acc") {
		t.Errorf("caller_reads_unwritten does not name the variable it affects: %q", lst.Note)
	}
	// Written and never read: the callee's error field, which is a DROPPED
	// op for the callee's own contract yet genuinely crosses the call.
	if iss := issueFor(c, IssueWrittenUnread, "FML_ERR_MSG"); iss == nil {
		t.Error("FML_ERR_MSG is written and never read back, but no written_unread was reported")
	}
	// The fields that do line up must NOT be reported as gaps.
	for _, code := range []TPIssueCode{IssueCalleeExpectsUnsent, IssueCallerReadsUnwritten,
		IssueSentUnread, IssueWrittenUnread} {
		for _, iss := range c.Issues {
			if iss.Code != code {
				continue
			}
			switch iss.Field {
			case "FML_AMT", "FML_USER_ID", "FML_ACC_ID":
				t.Errorf("%s reported against %s, which both sides do touch", code, iss.Field)
			}
		}
	}
}

// TestBothFnotpresFindingsCarryTheSameExplanation is the standing answer to
// "what happens when the two sides do not line up", and it has to be ONE
// answer. These are not errors: an absent field yields FNOTPRES and an
// untouched destination, not garbage. Writing the explanation once and
// attaching it to both codes is what keeps a later edit from softening one and
// leaving the other alarmist.
func TestBothFnotpresFindingsCarryTheSameExplanation(t *testing.T) {
	calleeSide := fnotpresIssue(IssueCalleeExpectsUnsent, "FML_X", "c_x", "callee", "sends")
	callerSide := fnotpresIssue(IssueCallerReadsUnwritten, "FML_Y", "c_y", "caller", "writes")

	for _, iss := range []TPIssue{calleeSide, callerSide} {
		if !strings.Contains(iss.Note, fnotpresNote) {
			t.Errorf("%s does not carry the shared explanation:\n%s", iss.Code, iss.Note)
		}
		// Case-insensitive: the note deliberately capitalises UNTOUCHED for
		// emphasis, and an assertion that pinned the lowercase spelling would
		// fail on a wording change that lost no meaning.
		lower := strings.ToLower(iss.Note)
		for _, want := range []string{"fnotpres", "untouched", "zero value", "indeterminate"} {
			if !strings.Contains(lower, want) {
				t.Errorf("%s omits %q — the mechanism is not fully stated", iss.Code, want)
			}
		}
		if !strings.Contains(iss.Note, iss.CalleeVar) {
			t.Errorf("%s does not name the variable it affects", iss.Code)
		}
	}
	// The two must not drift apart: same mechanism, so the shared sentence
	// has to be literally the same sentence.
	if !strings.HasSuffix(calleeSide.Note, fnotpresNote) ||
		!strings.HasSuffix(callerSide.Note, fnotpresNote) {
		t.Error("the FNOTPRES explanation is being personalised rather than shared, " +
			"so the two findings can drift apart")
	}
}

// TestJoinIsAbsentWithoutAnInCorpusCallee is the no-regression proof, stated
// on the join's own output rather than on a diff. Every other tpcall in the
// tracked corpus names a callee that is never defined, so the join must leave
// BOTH its fields empty there — not a nil callee with a refusal attached,
// which would still change the emitted IR and the placeholder's inputs.
func TestJoinIsAbsentWithoutAnInCorpusCallee(t *testing.T) {
	for _, dir := range []string{"stripped", "adversarial", "pf"} {
		files, err := ExtractDir(filepath.Join("../../testdata", dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			for i := range f.TPCalls {
				tp := &f.TPCalls[i]
				if tp.Callee != nil {
					t.Errorf("%s/%s joined callee %q — the placeholder path is meant to be unchanged",
						dir, filepath.Base(f.Path), tp.Callee.Path)
				}
				if tp.JoinRefusal != "" {
					t.Errorf("%s/%s recorded refusal %q for a tpcall with no callee in the corpus; "+
						"an absent callee is the placeholder path, not a refusal",
						dir, filepath.Base(f.Path), tp.JoinRefusal)
				}
			}
		}
	}
}

// TestJoinRefusesAnAmbiguousCallee: two files claiming the same service name.
// Picking one would be a guess, and the failure mode of a wrong guess is a
// WRONG BINDING rather than a missing one, so this refuses.
func TestJoinRefusesAnAmbiguousCallee(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"SVC_J_CALLER.pc": joinCaller,
		"SVC_J_CALLEE.pc": joinCallee,
		// A differently named file that still claims the service: entry-name
		// matching is one of the two resolution rules, so this matches too,
		// and two matches is exactly what makes the callee ambiguous.
		"SVC_J_CALLEE_dup.pc": joinCallee,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := ExtractDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var caller *File
	for _, f := range out {
		if filepath.Base(f.Path) == "SVC_J_CALLER.pc" {
			caller = f
		}
	}
	if caller == nil {
		t.Fatal("caller missing")
	}
	tp := &caller.TPCalls[0]
	if !strings.Contains(tp.ServiceFile, ",") {
		t.Skipf("resolution matched a single file (%q); the fixture no longer produces the ambiguity it exists for", tp.ServiceFile)
	}
	if tp.Callee != nil {
		t.Error("an ambiguous callee produced a join — one of the candidate files was picked")
	}
	if tp.JoinRefusal != RefuseAmbiguousCallee {
		t.Errorf("JoinRefusal = %q, want %q", tp.JoinRefusal, RefuseAmbiguousCallee)
	}
}

// TestJoinRefusesACalleeWithNoEntry: the file is matched by base name but
// defines no SVC_ function, so its ops cannot be attributed to the service.
// Scoping them to "the whole file" would report a helper's reads as the
// service's contract and could name the wrong variable in a binding.
func TestJoinRefusesACalleeWithNoEntry(t *testing.T) {
	dir := t.TempDir()
	// The callee file's base name matches the service, and its only function
	// is a helper — no SVC_ entry.
	helperOnly := `void fn_j_helper(FBFR32 *ib)
{
    char c_x[9];
    Fget32(ib,FML_AMT,0,(char *)c_x,0);
}
`
	for name, body := range map[string]string{
		"SVC_J_CALLER.pc": joinCaller,
		"SVC_J_CALLEE.pc": helperOnly,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := ExtractDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	tc := joinOf(t, out, "SVC_J_CALLER.pc")
	if tc.Callee != nil {
		t.Errorf("a callee with no entry function still produced a join: %+v", tc.Callee)
	}
	if tc.JoinRefusal != RefuseCalleeNoEntry {
		t.Errorf("JoinRefusal = %q, want %q", tc.JoinRefusal, RefuseCalleeNoEntry)
	}
}

// TestJoinRefusesACalleeWithNoBufferRoles: with no resolved input or output
// buffer there is no telling a read from a write, so any contract projected
// from it would be inverted at random.
func TestJoinRefusesACalleeWithNoBufferRoles(t *testing.T) {
	dir := t.TempDir()
	// A service whose FML traffic does not go through a recognisable ibuffer
	// or obuffer, so no role resolves.
	noRoles := `void SVC_J_CALLEE(TPSVCINFO* rqst)
{
    FBFR32 *ptr_anon;
    char c_x[9];
    Fget32(ptr_anon,FML_USER_ID,0,(char *)c_x,0);
    tpreturn(TPSUCCESS,0L,0L,0L,0);
}
`
	for name, body := range map[string]string{
		"SVC_J_CALLER.pc": joinCaller,
		"SVC_J_CALLEE.pc": noRoles,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := ExtractDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	tc := joinOf(t, out, "SVC_J_CALLER.pc")
	if tc.Callee != nil {
		t.Errorf("a callee with no buffer roles still produced a join: %+v", tc.Callee)
	}
	if tc.JoinRefusal != RefuseCalleeNoBuffers {
		t.Errorf("JoinRefusal = %q, want %q", tc.JoinRefusal, RefuseCalleeNoBuffers)
	}
}

// TestJoinIgnoresHelperTrafficInTheCallee is what File.FunctionSpans exists
// for. FmlOp records no owning function, so without the span a helper's
// Fget32 is indistinguishable from the service's own — and the consequence is
// not a cosmetic extra: the caller would be told it omits a field the SERVICE
// never reads, and a binding could name the helper's variable.
func TestJoinIgnoresHelperTrafficInTheCallee(t *testing.T) {
	withHelper := `void SVC_J_CALLEE(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    FBFR32 *ptr_fml_Obuffer;
    char svc_acct_ref[33];
    char svc_amount_txt[19];
    long svc_row_id;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    ptr_fml_Obuffer = (FBFR32*)rqst->data;
    Fget32(ptr_fml_Ibuffer,FML_USER_ID,0,(char *)svc_acct_ref,0);
    Fget32(ptr_fml_Ibuffer,FML_AMT,0,(char *)svc_amount_txt,0);
    Fadd32(ptr_fml_Obuffer,FML_ACC_ID,(char *)&svc_row_id,0);
    tpreturn(TPSUCCESS,0L,(char *)ptr_fml_Obuffer,0L,0);
}

void fn_j_audit(FBFR32 *ib)
{
    char audit_scratch[9];
    Fget32(ib,FML_AUDIT_ONLY,0,(char *)audit_scratch,0);
}
`
	c := joinOf(t, writeCorpus(t, map[string]string{
		"SVC_J_CALLER.pc": joinCaller,
		"SVC_J_CALLEE.pc": withHelper,
	}), "SVC_J_CALLER.pc")
	if c.Callee == nil {
		t.Fatalf("no join (refusal %q)", c.JoinRefusal)
	}
	for _, f := range c.Callee.Expects {
		if f.Field == "FML_AUDIT_ONLY" {
			t.Fatal("a helper function's Fget32 was reported as something the SERVICE expects")
		}
	}
	if issueFor(c.Callee, IssueCalleeExpectsUnsent, "FML_AUDIT_ONLY") != nil {
		t.Error("the caller was reported as omitting a field only a helper reads")
	}
	// The exclusion is a count, not just an absence: the callee reads exactly
	// the two fields its own entry function reads, and the helper's third is
	// not among them.
	if c.Callee.Entry != "SVC_J_CALLEE" {
		t.Errorf("Entry = %q, want SVC_J_CALLEE", c.Callee.Entry)
	}
	if len(c.Callee.Expects) != 2 {
		t.Errorf("callee expects %d fields, want 2 — the helper's read must be excluded, "+
			"and the entry function's two must not be", len(c.Callee.Expects))
	}
}

// TestFuncSpanZeroEndIsOpen: a body the scanner never closed has BodyEnd 0.
// Treating that as a zero-width span would silently drop every real op in it,
// which is the wrong way to fail — the join would report a service as
// expecting nothing and tell the caller it omits everything.
func TestFuncSpanZeroEndIsOpen(t *testing.T) {
	open := FuncSpan{Name: "SVC_X", BodyStart: 10, BodyEnd: 0}
	if !open.contains(10) || !open.contains(10_000) {
		t.Error("an unclosed body span must stay open upward, not read as empty")
	}
	if open.contains(9) {
		t.Error("an unclosed body span must not include lines before the body")
	}
	closed := FuncSpan{Name: "SVC_X", BodyStart: 10, BodyEnd: 20}
	if !closed.contains(10) || !closed.contains(20) {
		t.Error("a closed span must include both endpoints")
	}
	if closed.contains(21) || closed.contains(9) {
		t.Error("a closed span included a line outside it")
	}
}
