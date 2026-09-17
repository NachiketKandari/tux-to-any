package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// dispatchSrc: two business arms, each with its own success return inside
// the arm (the risk.pc shape in miniature).
const dispatchSrc = `void SVC_DISP(TPSVCINFO *rqst) {
	char c_flag;
	if (Fget32(ptr_fml_Ibuffer, FML_FLAG, 0, (char *)&c_flag, 0) == -1) {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	if (c_flag == 'H') {
		Fget32(ptr_fml_Ibuffer, FML_COMP_CD, 0, (char *)&sql_comp, 0);
		Fadd32(ptr_fml_Obuffer, FML_A, (char *)&a, 0);
		tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
	} else if (c_flag == 'F') {
		Fget32(ptr_fml_Ibuffer, FML_SCH_CD, 0, (char *)&sql_sch, 0);
		Fadd32(ptr_fml_Obuffer, FML_B, (char *)&b, 0);
		tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
	} else {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
}
`

func TestSuccessReturnsDispatch(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(dispatchSrc), "disp.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(dispatchSrc), facts, "SVC_DISP", nil)
	got := DiscoverByReturn(tree, nil, facts, "SVC_DISP")
	if len(got) != 2 {
		t.Fatalf("outcomes = %d, want 2 (one per TPSUCCESS): %+v", len(got), got)
	}
	for _, oc := range got {
		if !oc.InsideBranch {
			t.Errorf("line %d: want InsideBranch=true, got %+v", oc.ReturnLine, oc)
		}
		if oc.Convergent {
			t.Errorf("line %d: want Convergent=false, got %+v", oc.ReturnLine, oc)
		}
		if len(oc.GuardChain) == 0 {
			t.Errorf("line %d: empty guard chain", oc.ReturnLine)
		}
		for _, g := range oc.GuardChain {
			if IsNoiseCond(g) {
				t.Errorf("line %d: noise guard leaked into chain: %q", oc.ReturnLine, g)
			}
		}
		if oc.Buffer != "ptr_fml_Obuffer" {
			t.Errorf("line %d: buffer = %q, want ptr_fml_Obuffer", oc.ReturnLine, oc.Buffer)
		}
	}
	// The two outcomes must carry distinct response shapes.
	if strings.Join(got[0].Adds, ",") == strings.Join(got[1].Adds, ",") {
		t.Errorf("both outcomes share adds %v — return split must separate shapes", got[0].Adds)
	}
}

func TestSuccessReturnsConvergent(t *testing.T) {
	// demoSrc (flow_test.go): content arm + empty arms converge to one
	// tail TPSUCCESS. Return-anchored must agree with Discover's 1
	// candidate: exactly the content arm feeds the outcome.
	facts, err := scanner.ScanBytes([]byte(demoSrc), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(demoSrc), facts, "SVC_DEMO", nil)
	got := DiscoverByReturn(tree, nil, facts, "SVC_DEMO")
	if len(got) != 1 {
		t.Fatalf("outcomes = %+v, want exactly 1 tail return", got)
	}
	oc := got[0]
	if oc.InsideBranch || !oc.Convergent {
		t.Errorf("tail return: want InsideBranch=false Convergent=true, got %+v", oc)
	}
	if len(oc.Arms) != 1 || oc.Arms[0].Cond != "c_flag == 'H'" {
		t.Errorf("convergent arms = %+v, want the single content arm c_flag=='H'", oc.Arms)
	}

	// mainTux shape: two content arms converge to one tail return — the
	// outcome under-splits and the arms carry the 2-way split.
	const convergentSrc = `void SVC_CONV(TPSVCINFO *rqst) {
	char c_flag;
	if (c_flag == 'H') {
		Fget32(ptr_fml_Ibuffer, FML_COMP_CD, 0, (char *)&sql_comp, 0);
		Fadd32(ptr_fml_Obuffer, FML_A, (char *)&a, 0);
	} else {
		Fget32(ptr_fml_Ibuffer, FML_SCH_CD, 0, (char *)&sql_sch, 0);
		Fadd32(ptr_fml_Obuffer, FML_B, (char *)&b, 0);
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	facts2, err := scanner.ScanBytes([]byte(convergentSrc), "conv.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree2 := Build([]byte(convergentSrc), facts2, "SVC_CONV", nil)
	got2 := DiscoverByReturn(tree2, nil, facts2, "SVC_CONV")
	if len(got2) != 1 || !got2[0].Convergent {
		t.Fatalf("want 1 convergent outcome, got %+v", got2)
	}
	if len(got2[0].Arms) != 2 {
		t.Errorf("want 2 arms (the condition split inside the outcome), got %+v", got2[0].Arms)
	}
}

func TestNoiseCondFilter(t *testing.T) {
	for _, c := range []string{
		"SQLCODE != 0", "Ferror32(x) == FNOTPRES",
		"Fget32(ptr_fml_Ibuffer, FML_X, 0, (char *)&v, 0) == -1",
		"DEBUG_MSG_LVL_3", "tpalloc(\"FML32\", 0, 1024) == NULL",
	} {
		if !IsNoiseCond(c) {
			t.Errorf("want noise: %q", c)
		}
	}
	for _, c := range []string{"c_flag == 'H'", "c_rqst_typ == MANAGE_RISK_PROFILE_VIEW", "trn_cd == 'A'"} {
		if IsNoiseCond(c) {
			t.Errorf("want business: %q", c)
		}
	}
}

func TestTpacallTreatedAsTpcall(t *testing.T) {
	src := `void SVC_A(TPSVCINFO *rqst) {
	tpcall("SVC_SYNC", (char *)sbuf, 0, (char **)&rbuf, 0L, 0);
	tpacall("SVC_ASYNC", (char *)abuf, 0, 0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	facts, err := scanner.ScanBytes([]byte(src), "a.pc")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range facts.Calls {
		if c.IsTpCall {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("IsTpCall sites = %d, want 2 (tpcall + tpacall): %+v", n, facts.Calls)
	}
	if facts.TpCallCount != 2 {
		t.Fatalf("TpCallCount = %d, want 2", facts.TpCallCount)
	}
}

func TestTpacallIRContract(t *testing.T) {
	src := `void SVC_A(TPSVCINFO *rqst) {
	Fadd32(abuf, FML_COMP_CD, (char *)&c, 0);
	tpacall("SVC_ASYNC", (char *)abuf, 0, 0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	dir := t.TempDir()
	p := filepath.Join(dir, "SVC_A.pc")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.TPCalls) != 1 {
		t.Fatalf("tpcalls = %+v, want 1", f.TPCalls)
	}
	tc := f.TPCalls[0]
	if !tc.Async {
		t.Errorf("want async tpacall marker, got %+v", tc)
	}
	if tc.Service != "SVC_ASYNC" || tc.SendBuffer != "abuf" {
		t.Errorf("service/send = %q/%q, want SVC_ASYNC/abuf", tc.Service, tc.SendBuffer)
	}
	// tpacall args[3] is flags (here "0") — must never become a buffer.
	if tc.RecvBuffer != "" {
		t.Errorf("tpacall RecvBuffer = %q, want empty (args[3] is flags)", tc.RecvBuffer)
	}
	if len(tc.SendFML) != 1 || tc.SendFML[0].Field != "FML_COMP_CD" {
		t.Errorf("send contract = %+v, want the FML_COMP_CD add", tc.SendFML)
	}
}
