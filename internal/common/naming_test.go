package common

import "testing"

// TestNamingPolicies pins the load-bearing policy differences (AD3): the
// three camel variants are deliberately distinct, byte-identical to their
// origins in plan, gen, and pyplan.
func TestNamingPolicies(t *testing.T) {
	if got := Export("nav"); got != "Nav" {
		t.Errorf("Export: %q", got)
	}
	if got := LowerFirst("NavController"); got != "navController" {
		t.Errorf("LowerFirst: %q", got)
	}
	for in, want := range map[string]string{
		"DEMO_ACC": "DemoAcc", "demo.acc": "DemoAcc", "demo acc": "DemoAcc", "demo_acc": "DemoAcc",
	} {
		if got := CamelGo(in); got != want {
			t.Errorf("CamelGo(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"comp_cd": "compCd", "comp": "comp", "comp_cd_id": "compCdId",
	} {
		if got := CamelLowerGo(in); got != want {
			t.Errorf("CamelLowerGo(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"DEMO_ACC": "DEMOACC", "mf-nav-hist": "MfNavHist", "FML_MF_NAV": "FMLMFNAV",
	} {
		if got := CamelPy(in); got != want {
			t.Errorf("CamelPy(%q) = %q, want %q", in, got, want)
		}
	}
	// Digit-leading tokens take the X escape: valid Go identifiers that
	// stay exported (the render-gate failure class).
	if got := Export("1st"); got != "X1st" {
		t.Errorf("Export(1st): %q", got)
	}
	for in, want := range map[string]string{"1_NAV": "X1Nav", "9": "X9"} {
		if got := CamelGo(in); got != want {
			t.Errorf("CamelGo(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"1st_amt": "X1stAmt", "1qty": "X1qty"} {
		if got := CamelLowerGo(in); got != want {
			t.Errorf("CamelLowerGo(%q) = %q, want %q", in, got, want)
		}
	}
	if got := PyIdent("mf-nav.x"); got != "mf_nav_x" {
		t.Errorf("PyIdent: %q", got)
	}
	// Empty and edge inputs must not panic.
	for _, f := range []func(string) string{Export, LowerFirst, CamelGo, CamelLowerGo, CamelPy, PyIdent} {
		_ = f("")
	}
}
