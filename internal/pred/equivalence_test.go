package pred

import "testing"

// identKeyEq is the guard-check identifier rule: same IdentKey. Unmapped
// names fall back to their own key, so distinct variables never equate.
func identKeyEq(a, b string) bool { return IdentKey(a) == IdentKey(b) }

func TestEquivalentAcceptShapes(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"camel rename", "c_flag == 'F'", "cFlag == \"F\""},
		{"pascal rename", "c_flag != 'H'", "CFlag != \"H\""},
		{"and commutative", "a == 'F' && b > 0", "b > 0 && a == 'F'"},
		{"and nested flatten", "a && (b && c)", "c && b && a"},
		{"or commutative", "a || b || c", "c || a || b"},
		{"not inner", "!(a_flag == 'F')", "!(aFlag == \"F\")"},
		{"call renamed args", "fn_is_user_active(ls_match_acc.arr, &c_d2u_active_flg) == -1", "fnIsUserActive(lsMatchAcc, &cD2uActiveFlg) == -1"},
		{"char string", "c_flag == 'F'", "c_flag == \"F\""},
		{"null nil", "x != NULL", "x != nil"},
		{"cmp symmetric", "c_flag == 'F'", "\"F\" == c_flag"},
		{"numeric rename", "cnt_d2u > 0", "cntD2u > 0"},
	}
	for _, tc := range cases {
		a, b := ParseCode(tc.a), ParseCode(tc.b)
		if !Equivalent(&a, &b, identKeyEq) {
			t.Errorf("%s: not equivalent:\n  %s\n  %s", tc.name, a.String(), b.String())
		}
	}
}

func TestEquivalentRejectShapes(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"literal value", "c_flag == 'F'", "c_flag == 'I'"},
		{"inverted", "c_flag == 'F'", "!(c_flag == 'F')"},
		{"negated cmp", "c_flag == 'F'", "c_flag != 'F'"},
		{"weakened or", "c_flag == 'F'", "c_flag == 'F' || c_flag == 'I'"},
		{"strictened and", "c_flag == 'F'", "c_flag == 'F' && x > 0"},
		{"unrelated var", "c_flag == 'F'", "other == 'F'"},
		{"different op", "cnt > 0", "cnt >= 0"},
		{"or vs and", "a == 'F' || b == 'F'", "a == 'F' && b == 'F'"},
		{"call missing arg", "fn(a, b) == 1", "fn(a) == 1"},
		{"call renamed name", "fn_is_user_active(x) == 1", "otherFn(x) == 1"},
	}
	for _, tc := range cases {
		a, b := ParseCode(tc.a), ParseCode(tc.b)
		if Equivalent(&a, &b, identKeyEq) {
			t.Errorf("%s: unexpectedly equivalent:\n  %s\n  %s", tc.name, a.String(), b.String())
		}
	}
}

// TestEquivalentRawFallback pins the raw degrade path: predicates outside
// the grammar still compare token-wise with identifier renaming, never as
// an opaque whole-string match.
func TestEquivalentRawFallback(t *testing.T) {
	a, b := ParseCode("ptr->flag_byte == 'F'"), ParseCode("ptr->flagByte == \"F\"")
	if a.Kind != KindRaw || b.Kind != KindRaw {
		t.Fatalf("premise: want raw nodes, got %s / %s", a.Kind, b.Kind)
	}
	if !Equivalent(&a, &b, identKeyEq) {
		t.Errorf("raw renamed predicates must match:\n  %s\n  %s", a.Text, b.Text)
	}
	c := ParseCode("ptr->other_byte == 'F'")
	if Equivalent(&a, &c, identKeyEq) {
		t.Error("raw unrelated identifier matched")
	}
}

// TestEquivalentBoundSpelling pins the accepted-spelling map contract: an
// unmapped spelling never equates, a mapped one (the FML request field)
// does — the convert/csgen gate's identifier rule.
func TestEquivalentBoundSpelling(t *testing.T) {
	bound := func(a, b string) bool {
		canon := map[string]string{"cflag": "axis", "mfgrowthflg": "axis"}
		ka, kb := IdentKey(a), IdentKey(b)
		if v, ok := canon[ka]; ok {
			ka = v
		}
		if v, ok := canon[kb]; ok {
			kb = v
		}
		return ka == kb
	}
	a, b := ParseCode("c_flag == 'F'"), ParseCode("request.MfGrowthFlg == \"F\"")
	if !Equivalent(&a, &b, bound) {
		t.Errorf("mapped request-field spelling rejected:\n  %s\n  %s", a.String(), b.String())
	}
	c := ParseCode("request.OtherFlg == \"F\"")
	if Equivalent(&a, &c, bound) {
		t.Error("unmapped selector spelling accepted")
	}
}

func TestParseCodeSelectors(t *testing.T) {
	e := ParseCode("request.MfGrowthFlg == \"F\" && row.Cnt > 0")
	if e.Kind != KindAnd {
		t.Fatalf("kind = %s, want and (selector chains collapse)", e.Kind)
	}
	if got := e.Items[0].L.Name; got != "MfGrowthFlg" {
		t.Errorf("first ident = %q, want the selector's last segment", got)
	}
	if got := e.Items[1].L.Name; got != "Cnt" {
		t.Errorf("second ident = %q, want the selector's last segment", got)
	}
}
