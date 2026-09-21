package pyplan

import (
	"reflect"
	"strings"
	"testing"

	"tux-to-any/internal/contract"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/namer"
)

// TestContractProjectionParity proves the Python projection derives from the
// uniform contract without drift: emitSQL == contract SQL, bindsOf ==
// contract binds.
func TestContractProjectionParity(t *testing.T) {
	q := &ir.Query{
		ID: "q1", Type: ir.QuerySelectSingle,
		SQL:    "select a into :h1 from T where x = :bind_a;",
		Tables: []string{"T"}, Binds: []string{"bind_a"},
		RowShape: []string{"h1"},
	}
	u := contract.QueryUnitFor(q, nil, false)
	if got := emitSQL(q); got != u.SQL {
		t.Errorf("SQL drift: emitSQL %q vs contract %q", got, u.SQL)
	}
	if got := bindsOf(q); !reflect.DeepEqual(got, u.Binds) {
		t.Errorf("binds drift: bindsOf %v vs contract %v", got, u.Binds)
	}
}

// TestVerbDelegation pins the Phase 4 verb cutover: verbOf routes through
// the contract kind, so the plan verb and the PyNamer method verb agree on
// every query type.
func TestVerbDelegation(t *testing.T) {
	py := namer.PyNamer{}
	for _, qt := range []ir.QueryType{ir.QuerySelectSingle, ir.QuerySelectMulti, ir.QueryInsert, ir.QueryUpdate, ir.QueryDelete, ir.QueryMerge} {
		u := contract.QueryUnit{Kind: contract.QueryKindOf(qt), Tables: []string{"demo_t"}}
		if !strings.HasPrefix(py.Method(u), verbOf(qt)) {
			t.Errorf("verbOf(%s) = %q, namer method = %q", qt, verbOf(qt), py.Method(u))
		}
	}
}
