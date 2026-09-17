package ir

import (
	"os"
	"path/filepath"
	"testing"
)

func writePC(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "SVC_A.pc")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTpacallIRContract(t *testing.T) {
	p := writePC(t, `void SVC_A(TPSVCINFO *rqst) {
	Fadd32(abuf, FML_COMP_CD, (char *)&c, 0);
	tpacall("SVC_ASYNC", (char *)abuf, 0, 0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`)
	f, err := ExtractFile(p)
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

// The async reply arrives via tpgetrply: its data buffer is the recv
// buffer and later reads of it are the recv contract.
func TestTpacallRecvViaTpgetrply(t *testing.T) {
	p := writePC(t, `void SVC_A(TPSVCINFO *rqst) {
	Fadd32(abuf, FML_COMP_CD, (char *)&c, 0);
	tpacall("SVC_ASYNC", (char *)abuf, 0, 0);
	tpgetrply(&cd, (char **)&rbuf, &rlen, 0);
	Fget32(rbuf, FML_NAV_DATE, 0, (char *)&sql_nav_date, 0);
	Fget32(rbuf, FML_NAV_NAV, 0, (char *)&lnav, 0);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`)
	f, err := ExtractFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.TPCalls) != 1 {
		t.Fatalf("tpcalls = %+v, want 1", f.TPCalls)
	}
	tc := f.TPCalls[0]
	if !tc.Async {
		t.Fatalf("want async, got %+v", tc)
	}
	if tc.RecvBuffer != "rbuf" {
		t.Errorf("recv buffer = %q, want rbuf (tpgetrply data arg)", tc.RecvBuffer)
	}
	if len(tc.RecvFML) != 2 || tc.RecvFML[0].Field != "FML_NAV_DATE" || tc.RecvFML[1].Field != "FML_NAV_NAV" {
		t.Errorf("recv contract = %+v, want the two post-tpgetrply gets", tc.RecvFML)
	}
}

// No tpgetrply after the call: recv side stays empty, never flags.
func TestTpacallWithoutTpgetrply(t *testing.T) {
	p := writePC(t, `void SVC_A(TPSVCINFO *rqst) {
	Fadd32(abuf, FML_COMP_CD, (char *)&c, 0);
	tpacall("SVC_ASYNC", (char *)abuf, 0, TPNOREPLY);
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`)
	f, err := ExtractFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.TPCalls) != 1 {
		t.Fatalf("tpcalls = %+v, want 1", f.TPCalls)
	}
	tc := f.TPCalls[0]
	if tc.RecvBuffer != "" || len(tc.RecvFML) != 0 {
		t.Errorf("fire-and-forget recv = %q/%+v, want empty", tc.RecvBuffer, tc.RecvFML)
	}
	if tc.Ambiguous {
		t.Errorf("must not flag ambiguous with no recv buffer: %+v", tc)
	}
}
