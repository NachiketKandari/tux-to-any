package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
)

// fnLibSrc is a fn-library fixture: one helper fn with one SQL query and
// the legacy status-return contract — the testdata/fixtures/nav/fn_demo_lib.pc
// shape (synthetic; same layout line for line).
const fnLibSrc = `int fn_is_demo_active(char* c_ServiceName, char* c_mtch_accnt, char* c_is_active_flg, char* c_err_msg)
{
    char c_active_flag;

    EXEC SQL
    SELECT DECODE(COUNT(*), 0, 'N', 'Y')
    INTO   :c_active_flag
    FROM   DEMO_CLIENT_MAP, DEMO_USER_ACCOUNTS
    WHERE  DEMO_MATCH_ACC = :c_mtch_accnt
    AND    DEMO_USER_ID = DEMO_ACCT_USER_ID
    AND    DEMO_PLAN_ACTIVE_FLAG = 'A'
    AND    ROWNUM = 1;

    if(SQLCODE != 0)
    {
        errlog(c_ServiceName,"S31240",SQLMSG,(char *)DEF_USR,DEF_SSSN,c_err_msg);
        return -1;
    }

    *c_is_active_flg = c_active_flag;

    return 1;
}
`

func fnLibFixture(t *testing.T) (*ir.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fn_demo_lib.pc")
	if err := os.WriteFile(path, []byte(fnLibSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f, fnLibSrc
}

func TestBuildFnLib(t *testing.T) {
	f, src := fnLibFixture(t)
	if f.Entry != "" {
		t.Fatalf("fixture entry = %q, want empty (a fn library)", f.Entry)
	}
	p, err := BuildFnLib(Options{Main: f, Source: src, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if !p.FnLib {
		t.Error("FnLib must be set for a fn-library plan")
	}
	if p.Service != "fn_demo_lib" || p.Mapping.Module != "fn_demo_lib" {
		t.Errorf("service/module = %q/%q, want fn_demo_lib both (the file stem)", p.Service, p.Mapping.Module)
	}
	// Unit shape: models → db method → db interface → fn helper. No
	// controller/handler/router units, no endpoints.
	var kinds []Kind
	var fnUnit, dbUnit *Unit
	for i := range p.Units {
		u := &p.Units[i]
		kinds = append(kinds, u.Kind)
		switch u.Kind {
		case KindFnHelper:
			fnUnit = u
		case KindDBMethod:
			dbUnit = u
		}
	}
	want := []Kind{KindModels, KindDBMethod, KindDBInterface, KindFnHelper}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("unit kinds = %v, want %v", kinds, want)
	}
	if dbUnit.Name != "GetDemoClientMap" {
		t.Errorf("db method name = %q, want the deterministic table-derived name", dbUnit.Name)
	}
	if dbUnit.Tx {
		t.Error("fn-library db units carry no scenario tx votes — plain variant")
	}
	if fnUnit.Name != "FnIsDemoActive" || !fnUnit.LLM {
		t.Errorf("fn unit = %q (llm %v), want FnIsDemoActive with the LLM seam", fnUnit.Name, fnUnit.LLM)
	}
	if len(fnUnit.QueryIDs) != 1 || fnUnit.QueryIDs[0] != "q1" {
		t.Errorf("fn unit query ids = %v, want [q1]", fnUnit.QueryIDs)
	}
	if len(p.FnHelpers) != 1 {
		t.Fatalf("fn helpers = %d, want 1", len(p.FnHelpers))
	}
	h := p.FnHelpers[0]
	if h.Name != "fn_is_demo_active" || h.GoName != "FnIsDemoActive" {
		t.Errorf("fn helper = %q/%q, want fn_is_demo_active/FnIsDemoActive", h.Name, h.GoName)
	}
	if !(h.StartLine <= 5 && h.EndLine >= 12) {
		t.Errorf("fn span %d-%d does not cover the fn body (the SQL at lines 5-12)", h.StartLine, h.EndLine)
	}
}

func TestBuildFnLibRejectsService(t *testing.T) {
	f, src := scenarioFixture(t) // an entry service, not a fn library
	if _, err := BuildFnLib(Options{Main: f, Source: src, Budget: budget.New(12000, 4000, 4)}); err == nil {
		t.Error("BuildFnLib must reject a file with a Tuxedo entry")
	}
}
