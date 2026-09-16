package convert

import (
	"strings"
	"testing"
)

// TestRewriteSeamLine pins the three deterministic legacy-seam rewrites
// against the exact risk.pc shapes the measured run showed the model
// echoing: the Fget32 unpack block, the FML_ERR_MSG error legs, and the
// chk_sssn session check — plus the lines that must survive untouched
// (unmapped gets, response-shaping adds, condition-context calls).
func TestRewriteSeamLine(t *testing.T) {
	reqMap := map[string]string{
		"FML_USR_ID":      "UsrId",
		"FML_RQST_TYP":    "RqstTyp",
		"FML_MATCH_ACCNT": "MatchAccnt",
		"FML_SSSN_ID":     "SssnId",
		"FML_ADDRSS_LN1":  "AddrssLn1",
	}
	for _, tc := range []struct {
		name string
		in   string
		want string
		kind seamKind
	}{
		{
			"unpack assignment with cast",
			"        i_err [0] = Fget32(ptr_fml_Ibuffer, FML_USR_ID, 0, (char*)c_user_id, 0);",
			"        c_user_id := request.UsrId;",
			seamGet,
		},
		{
			"unpack with address-of",
			"i_err[1] = Fget32(ptr_fml_Ibuffer, FML_SSSN_ID, 0,(char *)&l_sssn_id, 0 );",
			"l_sssn_id := request.SssnId;",
			seamGet,
		},
		{
			"unpack varchar .arr target",
			"i_err[0]  = Fget32(ptr_fml_Ibuffer,FML_ADDRSS_LN1, 0, (char*)sql_rpd_prdt_id.arr,0);",
			"sql_rpd_prdt_id := request.AddrssLn1;",
			seamGet,
		},
		{
			"provenance markers kept, assignment dropped",
			"/*L225*/  i_err[2] = Fget32(ptr_fml_Ibuffer, FML_RQST_TYP, 0, (char *)&c_rqst_typ, 0);",
			"/*L225*/  c_rqst_typ := request.RqstTyp;",
			seamGet,
		},
		{
			"err leg with variable",
			"      Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);",
			"      err = fmt.Errorf(\"%s\", c_errmsg);",
			seamErrAdd,
		},
		{
			"err leg with literal",
			"    Fadd32( ptr_fml_Ibuffer, FML_ERR_MSG, \"This facility is not avalabile to you.\", 0 );",
			"    err = fmt.Errorf(\"%s\", \"This facility is not avalabile to you.\");",
			seamErrAdd,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, kind := rewriteSeamLine(tc.in, reqMap)
			if kind != tc.kind {
				t.Fatalf("kind = %v, want %v", kind, tc.kind)
			}
			if got != tc.want {
				t.Errorf("rewrite:\n got %q\nwant %q", got, tc.want)
			}
		})
	}

	kept := []struct {
		name string
		in   string
	}{
		{"unmapped get stays", "i_err[4] = Fget32(ptr_fml_Ibuffer, FML_UNKNOWN_F, 0, (char*)&x, 0);"},
		{"condition-context get stays", "if (Fget32(ibuf, FML_USR_ID, 0, (char*)&c_user_id, 0) == -1) {"},
		{"response-shaping add stays", "i_err[9]  = Fadd32(ptr_fml_Obuffer, FML_ANSWR_FLAG,(char *)&c_risk_prof_set_flg,0);"},
		{"point-type guard add stays", "if(Fadd32(ptr_fml_Ibuffer,FML_POINT_TYPE,(char *)&sql_urf_mm_opt_stts_2, 0) == -1) {"},
		{"bare chk_sssn call stays", "chk_sssn(c_ServiceName, c_user_id, l_sssn_id, c_errmsg);"},
	}
	for _, tc := range kept {
		t.Run(tc.name, func(t *testing.T) {
			got, kind := rewriteSeamLine(tc.in, reqMap)
			if kind != seamNone {
				t.Errorf("kind = %v, want seamNone:\n got %q", kind, got)
			}
			if got != tc.in {
				t.Errorf("line altered:\n got %q\nwant %q", got, tc.in)
			}
		})
	}
}

// TestRewriteSeamLineChkSssn pins the session-check neutralization: the
// assignment keeps its LHS, the call becomes 0, trailing semicolon stays.
func TestRewriteSeamLineChkSssn(t *testing.T) {
	in := "    l_sssn_id_chk = chk_sssn(c_ServiceName,c_user_id,l_sssn_id ,c_errmsg);"
	got, kind := rewriteSeamLine(in, map[string]string{})
	if kind != seamSsn {
		t.Fatalf("kind = %v, want seamSsn", kind)
	}
	if want := "    l_sssn_id_chk = 0;"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// A comparison context never rewrites.
	cmp := "if (x == chk_sssn(a, b)) {"
	if got, kind := rewriteSeamLine(cmp, nil); kind != seamNone || got != cmp {
		t.Errorf("comparison rewritten: %q", got)
	}
}

// TestRewriteLegacySeamsFullView pins the whole-view pass on a realistic
// prologue slice: unpack → request reads, error legs → Errorf, session
// check → 0, and every intent line (store calls, branches, response adds)
// byte-identical.
func TestRewriteLegacySeamsFullView(t *testing.T) {
	reqMap := map[string]string{
		"FML_USR_ID":      "UsrId",
		"FML_RQST_TYP":    "RqstTyp",
		"FML_MATCH_ACCNT": "MatchAccnt",
		"FML_SSSN_ID":     "SssnId",
	}
	view := "/*L220*/\n" +
		"i_err[0] = Fget32(ptr_fml_Ibuffer, FML_USR_ID, 0, (char*)c_user_id, 0);\n" +
		"i_err[1] = Fget32(ptr_fml_Ibuffer, FML_SSSN_ID, 0,(char *)&l_sssn_id, 0 );\n" +
		"i_err[2] = Fget32(ptr_fml_Ibuffer, FML_RQST_TYP, 0, (char *)&c_rqst_typ, 0);\n" +
		"i_err[3] = Fget32(ptr_fml_Ibuffer, FML_MATCH_ACCNT,0,(char *)c_match_accnt,0);\n" +
		"l_sssn_id_chk = chk_sssn(c_ServiceName,c_user_id,l_sssn_id ,c_errmsg);\n" +
		"Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);\n" +
		"if(c_user_id[0] == 'B' && c_rqst_typ == \"O\") {\n" +
		"if(Fadd32(ptr_fml_Ibuffer,FML_POINT_TYPE,(char *)&sql_urf_mm_opt_stts_2, 0) == -1) {\n" +
		"s.store.GetDetail(c, tx, request.PrtfloId)\n" +
		"}\n" +
		"i_err[0] = Fadd32(ptr_fml_Obuffer, FML_ANSWR_FLAG,(char *)&c_risk_prof_set_flg,0);\n" +
		"i_err[5] = Fget32(ptr_fml_Ibuffer, FML_UNMAPPED_X, 0, (char*)&x, 0);\n" +
		"}\n"
	got, c, changed := rewriteLegacySeams(view, reqMap)
	if !changed {
		t.Fatal("no change reported")
	}
	if c.Gets != 4 || c.ErrAdds != 1 || c.Ssn != 1 {
		t.Fatalf("counts = %+v, want gets=4 erradds=1 ssn=1", c)
	}
	for _, want := range []string{
		"c_user_id := request.UsrId;",
		"l_sssn_id := request.SssnId;",
		"c_rqst_typ := request.RqstTyp;",
		"c_match_accnt := request.MatchAccnt;",
		"l_sssn_id_chk = 0;",
		"err = fmt.Errorf(\"%s\", c_errmsg);",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"FML_ERR_MSG", "chk_sssn"} {
		if strings.Contains(got, gone) {
			t.Errorf("legacy spelling %q survived:\n%s", gone, got)
		}
	}
	// The only surviving Fget is the unmapped read.
	if n := strings.Count(got, "Fget32"); n != 1 {
		t.Errorf("%d Fget32 spellings survived, want 1 (the unmapped read):\n%s", n, got)
	}
	for _, kept := range []string{
		"if(c_user_id[0] == 'B' && c_rqst_typ == \"O\") {",
		"s.store.GetDetail(c, tx, request.PrtfloId)",
		"Fadd32(ptr_fml_Obuffer, FML_ANSWR_FLAG",
		"Fget32(ptr_fml_Ibuffer, FML_UNMAPPED_X",
		"if(Fadd32(ptr_fml_Ibuffer,FML_POINT_TYPE",
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("intent line altered:\n%q\nin:\n%s", kept, got)
		}
	}
}
