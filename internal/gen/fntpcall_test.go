package gen

import (
	"os"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
)

// tpSrc is a C service with two helpers: one that makes a tpcall and also
// runs SQL (so it renders a partial body), and one with no tpcall at all
// (so it must keep the legacy success value).
const tpSrc = `
int svc_t(void)
{
  EXEC SQL
    SELECT TBL_A
    INTO   :c_ura_user_id
    FROM   UAC_USR_ACCNTS
    WHERE  UAC_CLM_MTCH_ACCNT = :c_match_accnt;

  i_ret = fn_with_tpcall("SVC_T", c_match_accnt, d_out);
  i_ret = fn_no_tpcall("SVC_T");

  return 0;
}

int fn_with_tpcall(char *c_ServiceName, char *c_match_accnt, double *d_out)
{
  EXEC SQL
    SELECT TBL_A
    INTO   :c_ura_user_id
    FROM   UAC_USR_ACCNTS
    WHERE  UAC_CLM_MTCH_ACCNT = :c_match_accnt;

  i_ch_val = tpcall("SVC_OTHER", ptr_fml_Sbuf, 0, &ptr_fml_Rbuf, &l_buf_len, TPNOTRAN);
  if (i_ch_val == -1)
  {
    return (-1);
  }
  return(1);
}

int fn_no_tpcall(char *c_ServiceName)
{
  i_cnt = 1;
  return(1);
}
`

func tpService(t *testing.T) (*Service, *plan.Plan) {
	t.Helper()
	path := t.TempDir() + "/SVC_TP.pc"
	if err := os.WriteFile(path, []byte(tpSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{Service: "tp", Module: "app/tp"}
	p, err := plan.Build(plan.Options{Main: f, Source: tpSrc, Mapping: m,
		Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(Options{Plan: p, Main: f, Source: tpSrc})
	if err != nil {
		t.Fatal(err)
	}
	return svc, p
}

// TestTPCallsForFiltersByOwningFunction: the IR records which function each
// tpcall belongs to, so this is a filter over ground truth rather than a scan
// for call-shaped text. Getting the attribution wrong would attach a gap to
// the wrong helper — the same reasoning as the control-flow walk using the
// flow tree instead of grepping the source.
func TestTPCallsForFiltersByOwningFunction(t *testing.T) {
	svc, _ := tpService(t)
	if got := svc.tpcallsFor("fn_with_tpcall"); len(got) != 1 {
		t.Fatalf("fn_with_tpcall has %d tpcalls, want 1", len(got))
	} else if got[0].Service != "SVC_OTHER" {
		t.Errorf("service = %q, want SVC_OTHER", got[0].Service)
	}
	if got := svc.tpcallsFor("fn_no_tpcall"); len(got) != 0 {
		t.Errorf("fn_no_tpcall has %d tpcalls, want 0", len(got))
	}
	if got := svc.tpcallsFor("fn_absent"); len(got) != 0 {
		t.Errorf("an unknown helper has %d tpcalls, want 0", len(got))
	}
}

// TestTPCallTODOIsOneGapPerSiteAndNamesTheCall: the gap has to be
// attributable. The emitted statement list has no marker where the call
// would have gone, so the message is the only thing tying the gap back to the
// legacy source.
func TestTPCallTODOIsOneGapPerSiteAndNamesTheCall(t *testing.T) {
	svc, _ := tpService(t)
	tps := svc.tpcallsFor("fn_with_tpcall")
	todos := detFnTPCallTODOs("FnWithTpcall", tps)
	if len(todos) != len(tps) {
		t.Fatalf("%d TODOs for %d sites, want one each", len(todos), len(tps))
	}
	got := todos[0]
	for _, want := range []string{TPCallNotRendered, `"SVC_OTHER"`, "FnWithTpcall"} {
		if !strings.Contains(got, want) {
			t.Errorf("gap does not mention %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "legacy line") {
		t.Errorf("gap carries no legacy line to match against the source:\n%s", got)
	}
}

// TestTPCallTODONamesTheAsyncVariant: tpacall has no inline reply, so a gap
// that reads like a synchronous call would mislead about when the data
// arrives.
func TestTPCallTODONamesTheAsyncVariant(t *testing.T) {
	sync := detFnTPCallTODOs("FnX", []ir.TPCall{{Service: "SVC_Y", StartLine: 10}})
	async := detFnTPCallTODOs("FnX", []ir.TPCall{{Service: "SVC_Y", StartLine: 10, Async: true}})
	if !strings.Contains(sync[0], "tpcall calls") {
		t.Errorf("sync variant not worded as a call:\n%s", sync[0])
	}
	if !strings.Contains(async[0], "tpacall") || !strings.Contains(async[0], "tpgetrply") {
		t.Errorf("async variant does not say the reply is deferred:\n%s", async[0])
	}
	if sync[0] == async[0] {
		t.Error("sync and async produced an identical message; the distinction is the point")
	}
}

// TestTPCallTailIsTheFailureStatus is the behaviour that gives Option A its
// weight. A helper that skipped its inter-service call must not report the
// legacy success value, because the generated callers test `== -1` and would
// otherwise continue into a store write with data that was never fetched.
func TestTPCallTailIsTheFailureStatus(t *testing.T) {
	if got := detFnTPCallTail(&plan.FnHelper{Return: "int"}); got != "return -1" {
		t.Errorf("int helper tail = %q, want %q", got, "return -1")
	}
	// A void helper cannot return -1; it must not be given one.
	if got := detFnTPCallTail(&plan.FnHelper{Return: ""}); got != "return" {
		t.Errorf("void helper tail = %q, want a bare return", got)
	}
}

// TestHelperWithTPCallEndsOnTheFailureStatusNotTheLegacySuccess is the
// end-to-end statement of the decision, and it pins the two fixes together.
//
// The fixture's helper has BOTH a tpcall and a parenthesised `return(1);`.
// detFnSuccessRet now reads that 1 correctly, so without the tail override
// this helper would emit `return 1` — the legacy success value — and the
// caller's `== -1` check would pass straight over a call that never
// happened. The override has to win.
func TestHelperWithTPCallEndsOnTheFailureStatusNotTheLegacySuccess(t *testing.T) {
	svc, p := tpService(t)
	body, err := svc.DeterministicFnHelperBody("FnWithTpcall", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, TPCallNotRendered) {
		t.Errorf("helper carries no %s gap:\n%s", TPCallNotRendered, body)
	}
	if !strings.Contains(body, "return -1") {
		t.Errorf("helper does not return the failure status:\n%s", body)
	}
	if strings.Contains(body, "return 1") {
		t.Errorf("helper returns the legacy SUCCESS value despite the skipped "+
			"tpcall; the caller's ==-1 check would pass:\n%s", body)
	}
	// And it still renders the SQL it could: the tpcall is deferred, not the
	// whole helper.
	if !strings.Contains(body, "s.store.") {
		t.Errorf("helper dropped its SQL along with the tpcall:\n%s", body)
	}
}

// TestHelperWithoutTPCallKeepsTheLegacySuccessValue is the other half: the
// override must be scoped to helpers that actually have a tpcall, or every
// helper in the tree starts failing.
func TestHelperWithoutTPCallKeepsTheLegacySuccessValue(t *testing.T) {
	svc, p := tpService(t)
	body, err := svc.DeterministicFnHelperBody("FnNoTpcall", p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, TPCallNotRendered) {
		t.Errorf("helper with no tpcall carries a tpcall gap:\n%s", body)
	}
	if strings.Contains(body, "return -1") {
		t.Errorf("helper with no tpcall was forced to the failure status:\n%s", body)
	}
	if !strings.Contains(body, "return 1") {
		t.Errorf("helper with no tpcall lost its legacy success value:\n%s", body)
	}
}

// TestSuccessRetReadsBothReturnSpellings covers the parser fix on its own.
// C writes the literal bare or parenthesised and this corpus uses both;
// reading only the bare form silently produced 0, a status the legacy code
// never returns.
func TestSuccessRetReadsBothReturnSpellings(t *testing.T) {
	for _, src := range []string{
		"  return 1;\n",
		"  return(1);\n",
		"  return (1);\n",
		"  return(1) ;\n",
	} {
		if got := detFnSuccessRet(src); got != "1" {
			t.Errorf("detFnSuccessRet(%q) = %q, want \"1\"", src, got)
		}
	}
	// The -1 failure literal is still not a success value, either spelling.
	for _, src := range []string{"  return -1;\n", "  return(-1);\n"} {
		if got := detFnSuccessRet(src); got != "0" {
			t.Errorf("detFnSuccessRet(%q) = %q, want \"0\" (no success literal)", src, got)
		}
	}
	// A non-integer return is not a status value.
	if got := detFnSuccessRet("  return (x);\n"); got != "0" {
		t.Errorf("detFnSuccessRet of a non-integer return = %q, want \"0\"", got)
	}
	// Two distinct success values remain ambiguous, as before.
	if got := detFnSuccessRet("  return 1;\n  return 2;\n"); got != "0" {
		t.Errorf("detFnSuccessRet with two success values = %q, want \"0\"", got)
	}
}
