package convert

import (
	"strings"
	"testing"
)

// TestStripFMLProbes pins the probe elision on the corpus prologues: dead
// INIT/i_ferr lines drop, the i_loop check collapses to its FNOTPRES
// fallback in Go presence form, and the unpack-error legs (middleware-owned)
// drop with the scaffolding.
func TestStripFMLProbes(t *testing.T) {
	prologue := "INIT(i_err, TOTAL_FML);\n" +
		"INIT(i_ferr,TOTAL_FML);        \n" +
		"c_user_id := request.UsrId;\n" +
		"i_ferr[0] = Ferror32;\n" +
		"l_sssn_id := request.SssnId;\n" +
		"i_ferr[1] = Ferror32;\n" +
		"c_rqst_typ := request.RqstTyp;\n" +
		"i_ferr[2] = Ferror32;\n" +
		"c_match_accnt := request.MatchAccnt;\n" +
		"i_ferr[3] = Ferror32;\n" +
		"for(i_loop = 0; i_loop < 4 ; i_loop++)\n" +
		"{\n" +
		"  if(i_err[i_loop] == -1)\n" +
		"  {\n" +
		"    if((i_loop == 3)  && (i_ferr[i_loop] == FNOTPRES))\n" +
		"    {\n" +
		"      strcpy(c_match_accnt,\"%\");\n" +
		"    }\n" +
		"    else\n" +
		"    {\n" +
		"      err = errors.New(\"S31010\");\n" +
		"      tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0, 0);\n" +
		"    }\n" +
		"  }\n" +
		"}\n" +
		"s.store.GetUserInfo(c)\n"
	got, elided := stripFMLProbes(prologue)
	if elided == 0 {
		t.Fatal("no elision reported")
	}
	for _, want := range []string{
		"c_user_id := request.UsrId;",
		"l_sssn_id := request.SssnId;",
		"c_rqst_typ := request.RqstTyp;",
		"c_match_accnt := request.MatchAccnt;",
		`s.store.GetUserInfo(c)`,
		`if request.MatchAccnt == "" {`,
		`strcpy(c_match_accnt,"%");`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"Ferror32", "FNOTPRES", "i_ferr", "i_err", "i_loop", "INIT(", "tpreturn", "S31010"} {
		if strings.Contains(got, gone) {
			t.Errorf("probe residue %q survived:\n%s", gone, got)
		}
	}
}

// TestStripFMLProbesMultiBranch pins the else-if fallback chain (the
// SaveRiskProfile-style second probe): every FNOTPRES arm becomes its own
// Go presence guard, index-aligned with the block's request reads.
func TestStripFMLProbesMultiBranch(t *testing.T) {
	block := "INIT(i_err, TOTAL_FML);\n" +
		"INIT(i_ferr, TOTAL_FML);\n" +
		"i_ferr[0] = Ferror32;\n" +
		"sql_rp_prof := request.UsrAddrss2Ln1;\n" +
		"i_ferr[1] = Ferror32;\n" +
		"sql_urf_updated_from := request.Advq1;\n" +
		"i_ferr[2] = Ferror32;\n" +
		"c_flg_using := request.PointType;\n" +
		"i_ferr[3] = Ferror32;\n" +
		"sql_urf_updated_by := request.Q1edittext;\n" +
		"i_ferr[4] = Ferror32;\n" +
		"sql_psd_prdct_typ := request.Advq1;\n" +
		"i_ferr[5] = Ferror32;\n" +
		"sql_url_prtflo_id := request.RstrctLstId;\n" +
		"i_ferr[6] = Ferror32;\n" +
		"for(i_loop = 0; i_loop < 4 ; i_loop++)\n" +
		"{\n" +
		"  if(i_err[i_loop] == -1)\n" +
		"  {\n" +
		"    if(i_loop == 3 && i_ferr[3] == FNOTPRES)\n" +
		"    {\n" +
		"      strcpy(sql_urf_updated_by.arr, c_user_id);\n" +
		"    }\n" +
		"    else if(i_loop == 4 && i_ferr[4] == FNOTPRES)\n" +
		"    {\n" +
		"      strcpy(sql_psd_prdct_typ.arr, \"NA\");\n" +
		"    }\n" +
		"    else if(i_loop == 5 && i_ferr[5] == FNOTPRES)\n" +
		"    {\n" +
		"      /* strcpy commented in ver 2.0 */\n" +
		"      strcpy(sql_url_prtflo_id.arr, \"NA\"); /* ver 2.0 */\n" +
		"    }\n" +
		"    else\n" +
		"    {\n" +
		"      err = errors.New(\"S31220\");\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	got, elided := stripFMLProbes(block)
	if elided == 0 {
		t.Fatal("no elision reported")
	}
	fields := []string{"UsrAddrss2Ln1", "Advq1", "PointType", "Q1edittext", "Advq1", "RstrctLstId", ""}
	for i, want := range []string{
		`if request.Q1edittext == "" {`,
		`if request.Advq1 == "" {`,
		`if request.RstrctLstId == "" {`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback %d missing %q:\n%s", i, want, got)
		}
	}
	_ = fields
	for _, gone := range []string{"Ferror32", "FNOTPRES", "i_loop", "S31220"} {
		if strings.Contains(got, gone) {
			t.Errorf("residue %q survived:\n%s", gone, got)
		}
	}
}

// TestStripFMLProbesNoFallback pins the pure unpack-error cycle: with no
// FNOTPRES arm the whole check drops (unpack failure is middleware-owned).
func TestStripFMLProbesNoFallback(t *testing.T) {
	block := "INIT(i_err, TOTAL_FML);\n" +
		"INIT(i_ferr, TOTAL_FML);\n" +
		"c_user_id := request.UsrId;\n" +
		"i_ferr[0] = Ferror32;\n" +
		"for(i_loop = 0; i_loop < 4 ; i_loop++)\n" +
		"{\n" +
		"  if(i_err[i_loop] == -1)\n" +
		"  {\n" +
		"    err = errors.New(\"S31010\");\n" +
		"    tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0, 0);\n" +
		"  }\n" +
		"}\n"
	got, elided := stripFMLProbes(block)
	if elided == 0 {
		t.Fatal("no elision reported")
	}
	if got != "c_user_id := request.UsrId;\n" {
		t.Errorf("no-fallback cycle survived:\n%s", got)
	}
}

// TestStripFMLProbesSimpleForm pins the single-field if form (no loop) with
// an absent-input fallback.
func TestStripFMLProbesSimpleForm(t *testing.T) {
	block := "INIT(i_err, TOTAL_FML);\n" +
		"INIT(i_ferr, TOTAL_FML);\n" +
		"sql_rpd_prdt_id := request.UsrAddrss2Ln1;\n" +
		"i_ferr[0] = Ferror32;\n" +
		"if(i_err[0] == -1)\n" +
		"{\n" +
		"  if(i_ferr[0] == FNOTPRES)\n" +
		"  {\n" +
		"    strcpy(sql_rpd_prdt_id.arr, \"*\");\n" +
		"  }\n" +
		"  else\n" +
		"  {\n" +
		"    err = errors.New(\"S31040\");\n" +
		"    tpreturn(TPFAIL, 0, (char *)ptr_fml_Ibuffer, 0, 0);\n" +
		"  }\n" +
		"}\n"
	got, elided := stripFMLProbes(block)
	if elided == 0 {
		t.Fatal("no elision reported")
	}
	if !strings.Contains(got, `if request.UsrAddrss2Ln1 == "" {`) || !strings.Contains(got, `strcpy(sql_rpd_prdt_id.arr, "*");`) {
		t.Errorf("simple-form fallback not rewritten:\n%s", got)
	}
	if strings.Contains(got, "S31040") || strings.Contains(got, "tpreturn") {
		t.Errorf("error leg survived:\n%s", got)
	}
}

// TestStripFMLProbesUnmappedBails pins the conservative contract: a fallback
// whose probe index has no mapped request read leaves the cycle untouched
// (only the dead array lines drop).
func TestStripFMLProbesUnmappedBails(t *testing.T) {
	block := "INIT(i_err, TOTAL_FML);\n" +
		"i_err[1] = Fget32(ptr_fml_Ibuffer, FML_UNMAPPED, 0, (char*)&x, 0);\n" +
		"i_ferr[1] = Ferror32;\n" +
		"for(i_loop = 0; i_loop < 4 ; i_loop++)\n" +
		"{\n" +
		"  if(i_err[i_loop] == -1)\n" +
		"  {\n" +
		"    if(i_loop == 1 && i_ferr[i_loop] == FNOTPRES)\n" +
		"    {\n" +
		"      strcpy(x.arr, \"%\");\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	got, _ := stripFMLProbes(block)
	if !strings.Contains(got, "for(i_loop = 0; i_loop < 4 ; i_loop++)") {
		t.Errorf("unmapped probe cycle was rewritten:\n%s", got)
	}
	if strings.Contains(got, "Ferror32") || strings.Contains(got, "INIT(") {
		t.Errorf("dead array lines survived:\n%s", got)
	}
}

// TestStripFMLProbesUntouched pins the no-op path: non-probe views stay
// byte-identical.
func TestStripFMLProbesUntouched(t *testing.T) {
	view := "s.store.GetUserInfo(c, request.MatchAccnt, request.UsrId)\n" +
		"if err != nil {\n\treturn nil, err\n}\n" +
		"data = append(data, &models.X{PointType: \"X\"})\n"
	if got, n := stripFMLProbes(view); n != 0 || got != view {
		t.Errorf("non-probe view altered (n=%d):\n%s", n, got)
	}
}
