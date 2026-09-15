package flow

import (
	"slices"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// The symbolic-dispatch idiom (SCEN-1 recognizer 2): the file's main
// if-chain compares a scalar against its own defined constants
// (`c_fee_typ == MAKE_FEE`). The RHS counts because it IS defined —
// membership from the IR's scoped define table (G-DEF3), never case
// shape — and the domain value is the resolved literal (DEF-D2), so the
// fold universe and the axis domain agree.
const symbolicSrc = `#define MAKE_FEE 'A'
#define KILL_FEE 'B'
#define MIX_FEE 'C'
void SVC_DEMO(TPSVCINFO *rqst)
{
	char c_fee_typ = '\0';
	i_err[0] = Fget32(ptr_fml_Ibuffer, FML_FEE_TYP, 0, (char *)&c_fee_typ, 0);
	if (c_fee_typ == MAKE_FEE) {
		fadd_fee();
	} else if (c_fee_typ == KILL_FEE) {
		kill_fee();
	} else if (c_fee_typ == MIX_FEE) {
		mix_fee();
	} else {
		userlog("unknown fee type");
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisSymbolicConstants(t *testing.T) {
	facts, err := scanner.ScanBytes([]byte(symbolicSrc), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	// The define table the IR records for the fixture's three constants:
	// file scope (no Function), visible from line 0.
	irf := &ir.File{Defines: []ir.Define{
		{Name: "MAKE_FEE", Value: "'A'", Line: 0},
		{Name: "KILL_FEE", Value: "'B'", Line: 0},
		{Name: "MIX_FEE", Value: "'C'", Line: 0},
	}}
	a := DispatchAxisFor([]byte(symbolicSrc), facts, "SVC_DEMO", irf)
	if a == nil {
		t.Fatal("no axis detected for the symbolic-dispatch idiom")
	}
	if a.Ref != "c_fee_typ" || a.Alias != "" {
		t.Errorf("ref/alias = %q/%q, want c_fee_typ/<none>", a.Ref, a.Alias)
	}
	if !slices.Equal(a.Domain, []string{"A", "B", "C"}) {
		t.Errorf("domain = %v, want [A B C] — the resolved literals, not the define names", a.Domain)
	}
	if a.Sites != 3 {
		t.Errorf("sites = %d, want 3", a.Sites)
	}
}

func TestDispatchAxisSymbolicNeedsDefinedRHS(t *testing.T) {
	// The same compares without usable defines: an undefined RHS ident is
	// runtime data, and guessing case shape is exactly what this rule
	// avoids — no axis, even when the IR is present but the names are not
	// defined in it.
	undeffed := `void SVC_DEMO(TPSVCINFO *rqst)
{
	char c_fee_typ = '\0';
	if (c_fee_typ == MAKE_FEE) {
		fadd_fee();
	} else if (c_fee_typ == KILL_FEE) {
		kill_fee();
	} else if (c_fee_typ == MIX_FEE) {
		mix_fee();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`
	facts, err := scanner.ScanBytes([]byte(undeffed), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	if a := DispatchAxisFor([]byte(undeffed), facts, "SVC_DEMO", &ir.File{}); a != nil {
		t.Errorf("axis = %v, want nil — an undefined RHS ident is not a defined constant", a)
	}
}
