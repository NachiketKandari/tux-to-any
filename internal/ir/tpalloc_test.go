package ir

import "testing"

// TestAssignedBufferVarRecognisesOnlyRealAssignments pins the shape
// recogniser that lets `buf = tpalloc(...)` count as a buffer reset. It is
// the one place in the S/R machinery that reads raw source text instead of
// scanner facts, so it is pinned narrowly on purpose: the Tuxedo allocation
// idiom must be recognised, and every near-miss must be declined. A false
// positive here silently DROPS a field the caller really does send, which is
// the one failure direction that is not recoverable downstream.
func TestAssignedBufferVarRecognisesOnlyRealAssignments(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"plain", `sbuffer = tpalloc("FML32",NULL,1024);`, "sbuffer"},
		{"cast rhs", `sbuffer = (char *)tpalloc("FML32",NULL,BUF_LEN);`, "sbuffer"},
		{"cast lhs", `(char *)sbuffer = tpalloc("FML32",NULL,1024);`, "sbuffer"},
		{"ampersand lhs", `&sbuffer = tpalloc("FML32",NULL,1024);`, "sbuffer"},
		{"indented", `        sbuffer = (char *)tpalloc("FML32",NULL,BUF_LEN);`, "sbuffer"},
		{"guarded", `if((sbuffer = tpalloc("FML32",NULL,1024)) == NULL)`, "sbuffer"},
		// Declined shapes. Each of these would reset a buffer that was
		// not reset, dropping fields the caller does send.
		{"equality is a test", `if(sbuffer == tpalloc("FML32",NULL,1024))`, ""},
		{"inequality is a test", `if(sbuffer != tpalloc("FML32",NULL,1024))`, ""},
		{"member assignment is not a variable", `s.arena = tpalloc("FML32",NULL,1024);`, ""},
		{"arrow field is not a variable", `s->arena = tpalloc("FML32",NULL,1024);`, ""},
		{"no assignment at all", `    tpalloc("FML32",NULL,1024);`, ""},
		{"argument, not assignment", `    userlog(tpalloc("FML32",NULL,1024));`, ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := assignedBufferVar(tc.line, "tpalloc"); got != tc.want {
				t.Errorf("assignedBufferVar(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

// TestSendFieldsTpallocReassignResetsBuffer is the behaviour the recogniser
// exists for. One variable, allocated twice, with NO tpfree between — the
// leaked-reallocation shape. The second call's buffer is a different
// allocation, so the first call's field must not be reported as crossing the
// second call. Before this, the enclosing-block window carried FML_T_A
// forward and a callee would have been bound to a value the caller never
// sent on that pass.
func TestSendFieldsTpallocReassignResetsBuffer(t *testing.T) {
	f := extractSource(t, `void SVC_T_REALLOC(TPSVCINFO* rqst)
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
	if got, want := fieldNames(second.SendFields), []string{"FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("second call send = %v, want %v (the reallocation starts a new buffer; "+
			"the first call's field must not carry over)", got, want)
	}
	if _, leaked := findField(second.SendFields, "FML_T_A"); leaked {
		t.Error("FML_T_A survived the reallocation — the second callee would be bound to a field the caller never sent")
	}
}

// TestSendFieldsAllocateOnceStillAccumulates pins the OTHER direction, and
// it is the more important one: allocate-once-and-reuse is the standard
// Tuxedo shape, and there the fields genuinely DO accumulate. FML has no
// implicit clear — a buffer keeps its fields across reuses unless the caller
// deletes them — so treating every tpalloc as a reset, or treating the
// reuse as a fresh buffer, would be wrong. This test fails if the tpalloc
// rule is ever widened past a reassignment.
func TestSendFieldsAllocateOnceStillAccumulates(t *testing.T) {
	f := extractSource(t, `void SVC_T_REUSE1(TPSVCINFO* rqst)
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
	// No second tpalloc, so no reset: the second call sees both fields,
	// because that is what FML actually does.
	if got, want := fieldNames(f.TPCalls[1].SendFields), []string{"FML_T_A", "FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("second call send = %v, want %v (one allocation, no delete — FML keeps the field)", got, want)
	}
}

// TestSendFieldsAllocatedInsideGuardStillResets pins that the reset is
// recognised when the allocation is inside an `if` — the shape the recogniser
// gets for free, since it reads the call's own line and takes the rightmost
// `=` before it.
func TestSendFieldsAllocatedInsideGuardStillResets(t *testing.T) {
	f := extractSource(t, `void SVC_T_GUARD(TPSVCINFO* rqst)
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
    if((sbuffer = (char *)tpalloc("FML32",NULL,1024)) == NULL)
    {
        userlog("alloc failed");
    }
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
	if got, want := fieldNames(f.TPCalls[1].SendFields), []string{"FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("second call send = %v, want %v (a guarded reallocation is still a reallocation)", got, want)
	}
}

// TestAssignedBufferVarDeclinesWithoutSource pins the nil-source path. The
// recogniser is the only consumer of the raw lines, so when they are absent
// it must decline rather than guess — and declining must leave the tpfree
// and memset resets fully working, since those come from call facts alone.
func TestAssignedBufferVarDeclinesWithoutSource(t *testing.T) {
	if got := assignedBufferVar(lineAt(nil, 1), "tpalloc"); got != "" {
		t.Errorf("assignedBufferVar with no source = %q, want %q", got, "")
	}
	if got := assignedBufferVar(lineAt([]string{"a", "b"}, 99), "tpalloc"); got != "" {
		t.Errorf("assignedBufferVar past end of source = %q, want %q", got, "")
	}
	// tpfree still resets when the source is unavailable, proving the
	// fact-based resets do not depend on the line-based one.
	f := extractSource(t, `void SVC_T_NOSRC(TPSVCINFO* rqst)
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
	if got, want := fieldNames(f.TPCalls[1].SendFields), []string{"FML_T_B"}; !equalStrings(got, want) {
		t.Errorf("second call send = %v, want %v (tpfree resets on call facts alone)", got, want)
	}
}
