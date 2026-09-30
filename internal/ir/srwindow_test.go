package ir

import (
	"encoding/json"
	"testing"
)

// These tests pin the S/R buffer STATE at a tpcall line, not the op window
// around it. The distinction is the whole point: an FML buffer is a map, so
// a deleted field is gone, a re-added field carries the later value, and a
// freed buffer is empty — none of which "the ops before the call" can tell
// you. A service-call inliner binds callee parameters against this view, so
// if it is wrong the generated Go reads a field the caller never sent.

func extractSource(t *testing.T, src string) *File {
	t.Helper()
	f, err := ExtractSourceOpts([]byte(src), "synthetic.pc", DefaultOptions().CorpusMode())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return f
}

func onlyTPCall(t *testing.T, f *File) TPCall {
	t.Helper()
	if len(f.TPCalls) != 1 {
		t.Fatalf("want exactly 1 tpcall, got %d", len(f.TPCalls))
	}
	return f.TPCalls[0]
}

// fieldNames is the ordered field list of a TPField slice, for terse
// comparison.
func fieldNames(fs []TPField) []string {
	out := make([]string, 0, len(fs))
	for _, x := range fs {
		out = append(out, x.Field)
	}
	return out
}

func findField(fs []TPField, name string) (TPField, bool) {
	for _, x := range fs {
		if x.Field == name {
			return x, true
		}
	}
	return TPField{}, false
}

func equalStrings(a, b []string) bool {
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

// TestSendFieldsIsBufferStateNotOpLog is the headline: the folded send
// state must be the fields the buffer HOLDS, so a field deleted before the
// call is absent, and a field re-added after deletion comes back with the
// later line and target. The op log above it keeps all three ops — the log
// is the audit trail, the fields are the answer, and they are allowed to
// disagree.
func TestSendFieldsIsBufferStateNotOpLog(t *testing.T) {
	f := extractSource(t, `void SVC_T_DEL(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_a[9];
    char c_b[9];
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_a,0);
    Fadd32((FBFR32*)sbuffer,FML_T_B,c_b,0);
    Fdel32((FBFR32*)sbuffer,FML_T_B);
    Fadd32((FBFR32*)sbuffer,FML_T_C,c_a,0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`)
	tc := onlyTPCall(t, f)

	// The log keeps every op: add A, add B, del B, add C.
	if len(tc.SendFML) != 4 {
		t.Fatalf("op log = %d ops, want 4 (the log must stay factual)", len(tc.SendFML))
	}
	// The state drops B, because B was deleted.
	if got, want := fieldNames(tc.SendFields), []string{"FML_T_A", "FML_T_C"}; !equalStrings(got, want) {
		t.Fatalf("send fields = %v, want %v (the deleted field must be absent)", got, want)
	}
	a, ok := findField(tc.SendFields, "FML_T_A")
	if !ok {
		t.Fatal("FML_T_A missing from the send state")
	}
	if a.Target != "c_a" || a.Line == 0 {
		t.Errorf("FML_T_A bound to %q@%d, want c_a at its add line", a.Target, a.Line)
	}
}

// TestSendFieldsLastWriteWins pins FML's per-field overwrite: two adds of
// the same field leave ONE field, bound to the later value. Without the
// fold the consumer would see the field twice and could bind the callee to
// the stale value.
func TestSendFieldsLastWriteWins(t *testing.T) {
	f := extractSource(t, `void SVC_T_OVER(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_first[9];
    char c_second[9];
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_first,0);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_second,0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`)
	tc := onlyTPCall(t, f)
	if len(tc.SendFML) != 2 {
		t.Fatalf("op log = %d ops, want 2", len(tc.SendFML))
	}
	if got, want := fieldNames(tc.SendFields), []string{"FML_T_A"}; !equalStrings(got, want) {
		t.Fatalf("send fields = %v, want %v (one field, not two)", got, want)
	}
	fld, _ := findField(tc.SendFields, "FML_T_A")
	if fld.Target != "c_second" {
		t.Errorf("FML_T_A bound to %q, want c_second (the later write wins)", fld.Target)
	}
}

// TestSendFieldsTpfreeResetsBuffer pins buffer reuse. Two calls on one
// send buffer is the standard Tuxedo pattern; a tpfree between them leaves
// the buffer EMPTY, so the second call's contract must not inherit the
// first call's fields. Without the reset bound the second call would report
// fields the caller never put in it on that pass.
func TestSendFieldsTpfreeResetsBuffer(t *testing.T) {
	f := extractSource(t, `void SVC_T_REUSE(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_a[9];
    char c_b[9];
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_a,0);
    if(tpcall("SVC_T_ONE",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail one");
    }
    tpfree((char *)sbuffer);
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_B,c_b,0);
    if(tpcall("SVC_T_TWO",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail two");
    }
}
`)
	if len(f.TPCalls) != 2 {
		t.Fatalf("want 2 tpcalls, got %d", len(f.TPCalls))
	}
	first, second := f.TPCalls[0], f.TPCalls[1]
	if got, want := fieldNames(first.SendFields), []string{"FML_T_A"}; !equalStrings(got, want) {
		t.Errorf("first call send = %v, want %v", got, want)
	}
	// The whole point: FML_T_A was added before the tpfree and is gone.
	if got, want := fieldNames(second.SendFields), []string{"FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("second call send = %v, want %v (tpfree empties the buffer; "+
			"the first call's field must not leak across)", got, want)
	}
	if _, leaked := findField(second.SendFields, "FML_T_A"); leaked {
		t.Error("FML_T_A survived tpfree — the second call would bind a field it never sent")
	}
}

// TestMemsetZeroResetsButNonZeroDoesNot pins the deliberate asymmetry. A
// zeroing memset empties the buffer, so it is a reset. A non-zero memset
// scribbles a byte pattern whose field semantics we cannot describe, so
// treating it as a reset would be a confident guess — it is left as "no
// reset", which can only make the field list staler, never wrong.
func TestMemsetZeroResetsButNonZeroDoesNot(t *testing.T) {
	zeroed := extractSource(t, `void SVC_T_Z(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_a[9];
    char c_b[9];
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_a,0);
    memset(sbuffer,0,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_B,c_b,0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`)
	if got, want := fieldNames(onlyTPCall(t, zeroed).SendFields), []string{"FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("memset(b,0,n) send = %v, want %v (a zeroing memset empties the buffer)", got, want)
	}

	scribbled := extractSource(t, `void SVC_T_NZ(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_a[9];
    char c_b[9];
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_a,0);
    memset(sbuffer,'x',1024);
    Fadd32((FBFR32*)sbuffer,FML_T_B,c_b,0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`)
	if got, want := fieldNames(onlyTPCall(t, scribbled).SendFields), []string{"FML_T_A", "FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("memset(b,'x',n) send = %v, want %v (a non-zero memset is not a reset)",
			got, want)
	}
}

// TestRecvFieldsFlagsUncheckedReads pins the R-side hazard. A bare
// `Fget32(rbuffer,...)` statement leaves the destination at whatever it
// held when the field is absent, and the caller usually has no idea. An
// inliner must not treat that destination as holding a value, so the flag
// has to be on the field itself.
func TestRecvFieldsFlagsUncheckedReads(t *testing.T) {
	unchecked := extractSource(t, `void SVC_T_UNCK(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    long li_id;
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
    Fget32((FBFR32*)rbuffer,FML_T_ID,0,(char*)&li_id,0);
}
`)
	fld, ok := findField(onlyTPCall(t, unchecked).RecvFields, "FML_T_ID")
	if !ok {
		t.Fatal("FML_T_ID missing from the recv state")
	}
	if !fld.Unchecked {
		t.Error("a bare Fget32 statement must be reported Unchecked — the destination may never have been written")
	}
	if fld.Target != "li_id" {
		t.Errorf("recv FML_T_ID bound to %q, want li_id", fld.Target)
	}

	checked := extractSource(t, `void SVC_T_CK(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    long li_id;
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
    if(Fget32((FBFR32*)rbuffer,FML_T_ID,0,(char*)&li_id,0) == -1)
    {
        userlog("absent");
    }
}
`)
	fld, ok = findField(onlyTPCall(t, checked).RecvFields, "FML_T_ID")
	if !ok {
		t.Fatal("FML_T_ID missing from the checked recv state")
	}
	if fld.Unchecked {
		t.Error("a guarded Fget32 must NOT be reported Unchecked — its error is tested")
	}
}

// TestCompositeFlagMarksPartialBinding pins that a non-bare expression is
// flagged. `c_buf[i]` reduces to the leading identifier `c_buf` under the
// existing Target convention, so without Composite a consumer would bind a
// callee parameter to the array rather than to the element.
func TestCompositeFlagMarksPartialBinding(t *testing.T) {
	f := extractSource(t, `void SVC_T_COMP(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_buf[9][9];
    long li_i;
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_V,c_buf[li_i],0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`)
	fld, ok := findField(onlyTPCall(t, f).SendFields, "FML_T_V")
	if !ok {
		t.Fatal("FML_T_V missing from the send state")
	}
	if !fld.Composite {
		t.Error("c_buf[li_i] must be marked Composite — Target holds only the leading identifier")
	}

	// A string literal is NOT composite: its value is known outright, so
	// there is nothing partial about the binding.
	lit := extractSource(t, `void SVC_T_LIT(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_V,"ok",0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`)
	fld, ok = findField(onlyTPCall(t, lit).SendFields, "FML_T_V")
	if !ok {
		t.Fatal("FML_T_V missing from the literal send state")
	}
	if fld.Composite {
		t.Error("a string literal must NOT be marked Composite — its value is fully known")
	}
}

// TestFieldsAreDeterministicAndOrdered pins that the fold is a pure
// function of the source: same bytes in, same JSON out, across repeated
// runs, and the field list is name-ordered rather than write-ordered so the
// serialized IR is stable no matter how the writes were interleaved.
func TestFieldsAreDeterministicAndOrdered(t *testing.T) {
	src := `void SVC_T_DET(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char *sbuffer;
    char **rbuffer;
    char c_z[9];
    char c_m[9];
    char c_a[9];
    long llen;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    sbuffer = (char *)tpalloc("FML32",NULL,1024);
    Fadd32((FBFR32*)sbuffer,FML_T_Z,c_z,0);
    Fadd32((FBFR32*)sbuffer,FML_T_M,c_m,0);
    Fadd32((FBFR32*)sbuffer,FML_T_A,c_a,0);
    if(tpcall("SVC_T_TARGET",sbuffer,0,&rbuffer,&llen,TPNOTRANS) == -1)
    {
        userlog("fail");
    }
}
`
	first := onlyTPCall(t, extractSource(t, src))
	if got, want := fieldNames(first.SendFields), []string{"FML_T_A", "FML_T_M", "FML_T_Z"}; !equalStrings(got, want) {
		t.Fatalf("send fields = %v, want %v (name-ordered, not write-ordered)", got, want)
	}
	a, _ := json.Marshal(first.SendFields)
	for i := 0; i < 8; i++ {
		b, _ := json.Marshal(onlyTPCall(t, extractSource(t, src)).SendFields)
		if string(a) != string(b) {
			t.Fatalf("run %d differed:\n got %s\nwant %s", i, b, a)
		}
	}
}

// TestLiveFixtureSendRecvState pins the tracking against the tracked
// corpus, where the shapes are known by inspection: two fields go into the
// send buffer, one comes back, and that one is read without checking the
// FML error. The op log must survive the fold unchanged.
func TestLiveFixtureSendRecvState(t *testing.T) {
	f, err := ExtractFileOpts("../../testdata/stripped/SVC_MIN_KITCHEN.pc", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.TPCalls) != 1 {
		t.Fatalf("want 1 tpcall, got %d", len(f.TPCalls))
	}
	tc := f.TPCalls[0]
	if tc.Service != "SVC_MIN_DETAIL" {
		t.Fatalf("service = %q, want SVC_MIN_DETAIL", tc.Service)
	}
	if got, want := fieldNames(tc.SendFields), []string{"FML_AMT", "FML_USER_ID"}; !equalStrings(got, want) {
		t.Errorf("send fields = %v, want %v", got, want)
	}
	if amt, ok := findField(tc.SendFields, "FML_AMT"); !ok || amt.Target != "li_amt" {
		t.Errorf("FML_AMT bound to %+v, want li_amt", amt)
	}
	if uid, ok := findField(tc.SendFields, "FML_USER_ID"); !ok || uid.Target != "c_user_id" {
		t.Errorf("FML_USER_ID bound to %+v, want c_user_id", uid)
	}
	if got, want := fieldNames(tc.RecvFields), []string{"FML_ACC_ID"}; !equalStrings(got, want) {
		t.Errorf("recv fields = %v, want %v", got, want)
	}
	acc, _ := findField(tc.RecvFields, "FML_ACC_ID")
	if acc.Target != "li_id" {
		t.Errorf("FML_ACC_ID bound to %q, want li_id", acc.Target)
	}
	// The fixture reads the reply field as a bare statement, so li_id may
	// never have been written before the INSERT that consumes it.
	if !acc.Unchecked {
		t.Error("FML_ACC_ID is a bare Fget32 statement and must be reported Unchecked")
	}
	// The audit trail is untouched by the fold.
	if len(tc.SendFML) != 2 || len(tc.RecvFML) != 1 {
		t.Errorf("op log = %d send / %d recv, want 2 / 1 — the fold must not rewrite the log",
			len(tc.SendFML), len(tc.RecvFML))
	}
}
