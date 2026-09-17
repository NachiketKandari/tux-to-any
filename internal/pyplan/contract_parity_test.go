package pyplan

import (
	"reflect"
	"testing"

	"tux-to-any/internal/contract"
	"tux-to-any/internal/ir"
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
