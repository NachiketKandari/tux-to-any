package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
)

// sameFileSrc is a minimal service whose entry calls a fn_* function the
// file itself defines — the same-file helper case (risk.pc's
// fn_save_risk_profile shape, reduced).
const sameFileSrc = `#include <stdio.h>

int fn_helper(char *c_ServiceName,
              char *c_match_accnt,
              long l_sssn_id,
              char *c_err_msg)
{
    EXEC SQL
      SELECT UAC_USR_ID
      INTO   :sql_usr_id
      FROM   DEMO_ACCNTS
      WHERE  UAC_CLM_MTCH_ACCNT = :c_match_accnt;

    if(SQLCODE != 0)
    {
        errlog(c_ServiceName, "S31000", SQLMSG, DEF_USR, DEF_SSSN, c_err_msg);
        return (-1);
    }
    return 1;
}

void SVC_DEMO(TPSVCINFO *rqst)
{
    if(c_rqst_typ == 'A')
    {
        i_ret = fn_helper(c_ServiceName, c_match_accnt, l_sssn_id, c_err_msg);
        tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
    }
    else
    {
        EXEC SQL
          SELECT X
          INTO :sql_x
          FROM T1;
    }
}
`

// TestPlanSameFileHelper pins the user directive (2026-09-17): a fn_*
// function the main file defines and calls becomes a KindFnHelper unit with
// a deterministic Go signature (session params dropped), its queries become
// db units, and none of them ride the unmapped-query skip list.
func TestPlanSameFileHelper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc_demo.pc")
	if err := os.WriteFile(path, []byte(sameFileSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	main, err := ir.ExtractFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if main.Entry != "SVC_DEMO" {
		t.Fatalf("entry = %q, want SVC_DEMO", main.Entry)
	}
	opts := Options{
		Main:   main,
		Source: sameFileSrc,
		Mapping: &Mapping{
			Service:   "demo",
			Endpoints: []Endpoint{{Condition: 1, Name: "Demo", Route: "/demo"}},
		},
		Budget: budget.New(12000, 4000, 4),
	}
	p, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.FnHelpers) != 1 {
		t.Fatalf("fn helpers = %+v, want 1 (fn_helper)", p.FnHelpers)
	}
	h := p.FnHelpers[0]
	if h.Name != "fn_helper" || h.GoName != "FnHelper" {
		t.Errorf("helper = %q/%q, want fn_helper/FnHelper", h.Name, h.GoName)
	}
	if len(h.Params) != 1 || h.Params[0].Name != "c_match_accnt" || h.Params[0].Type != "string" {
		t.Errorf("helper params = %+v, want [c_match_accnt string]", h.Params)
	}
	if h.Return != "int" {
		t.Errorf("helper return = %q, want int", h.Return)
	}
	var helperUnit *Unit
	for i := range p.Units {
		if p.Units[i].Kind == KindFnHelper {
			helperUnit = &p.Units[i]
		}
	}
	if helperUnit == nil {
		t.Fatal("KindFnHelper unit missing")
	}
	if helperUnit.Name != "FnHelper" || !strings.HasSuffix(helperUnit.TargetPath, "/controller/fns.go") {
		t.Errorf("helper unit = %+v", helperUnit)
	}
	if len(helperUnit.QueryIDs) != 1 {
		t.Fatalf("helper query IDs = %v, want the SELECT owned by fn_helper", helperUnit.QueryIDs)
	}
	// The helper query is a db unit, never a skip.
	var dbUnit *Unit
	for i := range p.Units {
		if p.Units[i].Kind == KindDBMethod && len(p.Units[i].QueryIDs) == 1 && p.Units[i].QueryIDs[0] == helperUnit.QueryIDs[0] {
			dbUnit = &p.Units[i]
		}
	}
	if dbUnit == nil {
		t.Errorf("no db unit for helper query %s", helperUnit.QueryIDs[0])
	}
	for _, s := range p.Skipped {
		if s.QueryID == helperUnit.QueryIDs[0] {
			t.Errorf("helper query %s must not be skipped (%s)", s.QueryID, s.Reason)
		}
	}
}
