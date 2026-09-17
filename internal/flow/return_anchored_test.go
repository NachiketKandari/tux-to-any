package flow

import (
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// dispatchSrc: two business arms, each with its own success return inside
// the arm (the multi-outcome dispatch shape in miniature).
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

	// Convergent shape: two content arms converge to one tail return — the
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

// condsFromRoots fabricates the condition inventory from the tree's
// non-debug roots (the ir extraction shape for synthetic sources).
func condsFromRoots(tree *Tree) []ir.Condition {
	var conds []ir.Condition
	for _, n := range tree.Root {
		if n.Kind != KindBranch || isDebugCond(n) {
			continue
		}
		conds = append(conds, ir.Condition{
			Index: len(conds) + 1, Kind: n.Sub, Expr: n.Cond,
			StartLine: n.Line, EndLine: n.EndLine,
			FmlOps: n.FmlOps, QueryIDs: n.QueryIDs,
		})
	}
	return conds
}

// Terminal-anchored arms read from the shared preamble: no in-arm Gets,
// but a TPSUCCESS they own. The wired rubric must qualify them (the old
// gets&&adds rule skipped them), with a loadable key round-tripping
// through ConditionFor; the TPFAIL error arm stays excluded.
const terminalSrc = `void SVC_TERM(TPSVCINFO *rqst) {
	char c_flag;
	Fget32(ptr_fml_Ibuffer, FML_FLAG, 0, (char *)&c_flag, 0);
	if (c_flag == 'H') {
		Fadd32(ptr_fml_Obuffer, FML_A, (char *)&a, 0);
		tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
	} else {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
}
`

func TestDiscoverTerminalAnchoredArm(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(terminalSrc), "term.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(terminalSrc), facts, "SVC_TERM", nil)
	conds := condsFromRoots(tree)
	if len(conds) != 2 {
		t.Fatalf("inventory = %d, want 2", len(conds))
	}
	got := Discover(tree, conds)
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want exactly the terminal-anchored arm", got)
	}
	c := got[0]
	if c.Key != "c1" {
		t.Errorf("key = %s, want c1", c.Key)
	}
	if len(c.Gets) != 0 {
		t.Errorf("gets = %v, want empty (reads live in preamble)", c.Gets)
	}
	if len(c.Adds) != 1 || c.Adds[0] != "FML_A" {
		t.Errorf("adds = %v, want [FML_A]", c.Adds)
	}
	if c.TerminalLine == 0 {
		t.Errorf("terminal line not recorded: %+v", c)
	}
	// The key must resolve back to the same block.
	rc, err := ConditionFor(tree, conds, c.Key)
	if err != nil {
		t.Fatalf("ConditionFor(%s): %v", c.Key, err)
	}
	if rc.StartLine != c.StartLine || rc.EndLine != c.EndLine {
		t.Errorf("round-trip span %d-%d, want %d-%d",
			rc.StartLine, rc.EndLine, c.StartLine, c.EndLine)
	}
}

// A non-error write arm with only TPFAIL exits is logic/error handling,
// not an outcome — the terminal leg must not promote it.
const failOnlySrc = `void SVC_FAILONLY(TPSVCINFO *rqst) {
	char c_flag;
	if (c_flag == 'H') {
		Fadd32(ptr_fml_Obuffer, FML_A, (char *)&a, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	} else {
		other();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDiscoverIgnoresFailOnlyArm(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(failOnlySrc), "fail.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(failOnlySrc), facts, "SVC_FAILONLY", nil)
	conds := condsFromRoots(tree)
	got := Discover(tree, conds)
	for _, c := range got {
		if c.Cond == "c_flag == 'H'" {
			t.Errorf("TPFAIL-only arm must not qualify: %+v", c)
		}
	}
}

// Convergent promotion: content arms with preamble-only reads drain into
// a shared tail. None owns a terminal and none carries in-arm Gets, so
// every pre-existing rubric leg misses them — the tail-feeder pass must
// promote exactly the two business arms (the trap and DEBUG arms stay
// out), each marked FeedsTail with a loadable key.
const convergentPromoteSrc = `void SVC_PROMOTE(TPSVCINFO *rqst) {
	char c_flag;
	Fget32(ptr_fml_Ibuffer, FML_FLAG, 0, (char *)&c_flag, 0);
	if (c_flag == 'H') {
		Fadd32(ptr_fml_Obuffer, FML_A, (char *)&a, 0);
	} else if (c_flag == 'F') {
		Fadd32(ptr_fml_Obuffer, FML_B, (char *)&b, 0);
	} else {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	if (DEBUG_MSG_LVL_3) {
		Fadd32(ptr_fml_Obuffer, FML_DBG, (char *)&d, 0);
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDiscoverConvergentTailFeeders(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(convergentPromoteSrc), "promote.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(convergentPromoteSrc), facts, "SVC_PROMOTE", nil)
	conds := condsFromRoots(tree)
	if len(conds) != 3 {
		t.Fatalf("inventory = %d, want 3 (H/F/trap; DEBUG excluded)", len(conds))
	}
	sites := SuccessReturnSites(facts, "SVC_PROMOTE")
	if len(sites) != 1 {
		t.Fatalf("success sites = %+v, want the single tail", sites)
	}
	tail := sites[0].Line
	got := Discover(tree, conds)
	if len(got) != 2 {
		t.Fatalf("candidates = %+v, want exactly the two business arms", got)
	}
	for _, c := range got {
		if c.FeedsTail != tail {
			t.Errorf("%s: FeedsTail = %d, want tail %d", c.Key, c.FeedsTail, tail)
		}
		if len(c.Gets) != 0 {
			t.Errorf("%s: gets = %v, want empty (preamble reads)", c.Key, c.Gets)
		}
		if c.TerminalLine != 0 {
			t.Errorf("%s: TerminalLine set without an owned terminal", c.Key)
		}
		rc, err := ConditionFor(tree, conds, c.Key)
		if err != nil {
			t.Errorf("ConditionFor(%s): %v", c.Key, err)
			continue
		}
		if rc.StartLine != c.StartLine || rc.EndLine != c.EndLine {
			t.Errorf("%s: round-trip span %d-%d, want %d-%d",
				c.Key, rc.StartLine, rc.EndLine, c.StartLine, c.EndLine)
		}
	}
	if got[0].Key != "c1" || got[1].Key != "c2" {
		t.Errorf("keys = %s,%s, want c1,c2", got[0].Key, got[1].Key)
	}
	if len(got[0].Adds) != 1 || got[0].Adds[0] != "FML_A" {
		t.Errorf("c1 adds = %v, want [FML_A]", got[0].Adds)
	}
	if len(got[1].Adds) != 1 || got[1].Adds[0] != "FML_B" {
		t.Errorf("c2 adds = %v, want [FML_B]", got[1].Adds)
	}
}
// carrying the target, qualifying its arm when it shapes a reply.
const forwardSrc = `void SVC_FWD(TPSVCINFO *rqst) {
	char c_flag;
	Fget32(ptr_fml_Ibuffer, FML_FLAG, 0, (char *)&c_flag, 0);
	if (c_flag == 'H') {
		Fadd32(ptr_fml_Obuffer, FML_A, (char *)&a, 0);
		tpforward("SVC_OTHER", (char *)ptr_fml_Ibuffer, 0L, 0);
	} else {
		Fadd32(ptr_fml_Obuffer, FML_B, (char *)&b, 0);
		tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
	}
}
`

func TestForwardOutcome(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(forwardSrc), "fwd.pc")
	if err != nil {
		t.Fatal(err)
	}
	if len(ForwardSites(facts, "SVC_FWD")) != 1 {
		t.Fatalf("forward sites = %+v, want 1", facts.Calls)
	}
	tree := Build([]byte(forwardSrc), facts, "SVC_FWD", nil)
	outs := DiscoverByReturn(tree, nil, facts, "SVC_FWD")
	if len(outs) != 2 {
		t.Fatalf("outcomes = %+v, want forward + return", outs)
	}
	if outs[0].Kind != "forward" || outs[0].ForwardTo != "SVC_OTHER" {
		t.Errorf("first outcome = %+v, want forward→SVC_OTHER", outs[0])
	}
	conds := condsFromRoots(tree)
	got := Discover(tree, conds)
	if len(got) != 2 {
		t.Fatalf("candidates = %+v, want both arms", got)
	}
	if got[0].ForwardTo != "SVC_OTHER" || got[0].TerminalLine == 0 {
		t.Errorf("forward candidate missing terminal attribution: %+v", got[0])
	}
	if got[1].TerminalLine == 0 || got[1].ForwardTo != "" {
		t.Errorf("return candidate misattributed: %+v", got[1])
	}
}
