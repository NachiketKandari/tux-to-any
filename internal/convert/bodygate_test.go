package convert

import (
	"strings"
	"testing"
)

// TestUnusedLocalErrs pins the declared-and-not-used gate: the LLM body that
// declares a value it never reads must be rejected (compile error on a wired
// target, invisible to the parse-only gate).
func TestUnusedLocalErrs(t *testing.T) {
	unused := "count, err := s.store.CountD2UMatch(c, lsMatchAcc)\nif err != nil {\n\treturn nil, err\n}\nreturn data, nil"
	errs := unusedLocalErrs(unused)
	if len(errs) != 1 || !strings.Contains(errs[0], "declared and not used: count") {
		t.Fatalf("unusedLocalErrs = %v, want one declared-and-not-used note", errs)
	}
	if !strings.HasPrefix(errs[0], "line 1:") {
		t.Errorf("line not body-relative: %q", errs[0])
	}

	used := "count, err := s.store.CountD2UMatch(c, lsMatchAcc)\nif err != nil {\n\treturn nil, err\n}\nif count > 0 {\n\tdata = append(data, nil)\n}\nreturn data, nil"
	if errs := unusedLocalErrs(used); len(errs) != 0 {
		t.Errorf("used local flagged: %v", errs)
	}
}

// TestNonConstFormatErrs pins the fmt.Errorf gate: a variable-format call is
// a go vet failure (Tier B) and an error-text corruption risk.
func TestNonConstFormatErrs(t *testing.T) {
	bad := "err = fmt.Errorf(c_errmsg)\nreturn nil, err"
	errs := nonConstFormatErrs(bad)
	if len(errs) != 1 || !strings.Contains(errs[0], "non-constant format string") {
		t.Fatalf("nonConstFormatErrs = %v, want one note", errs)
	}
	if !strings.HasPrefix(errs[0], "line 1:") {
		t.Errorf("line not body-relative: %q", errs[0])
	}

	if errs := nonConstFormatErrs(`err = fmt.Errorf("S31005: %s", c_errmsg)`); len(errs) != 0 {
		t.Errorf("constant format flagged: %v", errs)
	}
	if errs := nonConstFormatErrs("err = errors.New(c_errmsg)"); len(errs) != 0 {
		t.Errorf("errors.New flagged: %v", errs)
	}
}

// TestRuneLiteralErrs pins the FML-flag transliteration gate: a rune never
// compares against the string flags the contract uses.
func TestRuneLiteralErrs(t *testing.T) {
	errs := runeLiteralErrs("cEnableD2uFlg := 'Y'\nreturn data, nil")
	if len(errs) != 1 || !strings.Contains(errs[0], "rune literal") {
		t.Fatalf("runeLiteralErrs = %v, want one rune note", errs)
	}
	if errs := runeLiteralErrs(`cEnableD2uFlg := "Y"` + "\nreturn data, nil"); len(errs) != 0 {
		t.Errorf("string literal flagged: %v", errs)
	}
}

// TestTerminatingReturnErr pins the fall-off-the-end gate: named results do
// not make an implicit return legal.
func TestTerminatingReturnErr(t *testing.T) {
	if errs := terminatingReturnErr("if count > 0 {\n\tdata = append(data, nil)\n}"); len(errs) != 1 {
		t.Fatalf("terminatingReturnErr = %v, want one note", errs)
	}
	if errs := terminatingReturnErr("if count > 0 {\n\tdata = append(data, nil)\n}\nreturn data, nil"); len(errs) != 0 {
		t.Errorf("terminated body flagged: %v", errs)
	}
}

// TestUndeclaredIdentErrs pins the legacy-leak gate: C names the parser
// cannot resolve are rejected; request reads, store calls and allowlisted
// stub helpers pass.
func TestUndeclaredIdentErrs(t *testing.T) {
	errs := undeclaredIdentErrs(`if c_flag == "H" {`+"\n\treturn data, nil\n}", nil)
	if len(errs) != 1 || !strings.Contains(errs[0], `"c_flag"`) {
		t.Fatalf("undeclaredIdentErrs = %v, want c_flag note", errs)
	}

	good := "lsMatchAcc := request.MatchAccnt\n" +
		"rows, err := s.store.CountD2UMatch(c, lsMatchAcc)\n" +
		"if err != nil {\n\treturn nil, err\n}\n" +
		"if fnIsD2uActive(lsMatchAcc) {\n\tdata = append(data, nil)\n}\n" +
		"return data, nil"
	if errs := undeclaredIdentErrs(good, map[string]bool{"fnIsD2uActive": true}); len(errs) != 0 {
		t.Errorf("clean body flagged: %v", errs)
	}
}

// TestRepairArmWrapper pins the deterministic unwrap: a re-added arm
// wrapper is removed and the exposed tail gains its terminal return.
func TestRepairArmWrapper(t *testing.T) {
	body := "if c_flag == 'H' {\n\tdata = append(data, nil)\n}"
	fixed, ok := repairArmWrapper(body, "c_flag")
	if !ok {
		t.Fatal("wrapper not repaired")
	}
	if fixed != "data = append(data, nil)\nreturn data, err" {
		t.Errorf("repaired body = %q", fixed)
	}
	if _, ok := repairArmWrapper(body, "other_var"); ok {
		t.Error("unrelated wrapper must not unwrap")
	}
	if _, ok := repairArmWrapper("data = nil\nreturn data, nil", "c_flag"); ok {
		t.Error("unwrapped body must not report a repair")
	}
}
