package convert

import (
	"context"
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

// TestFixRuneLiterals pins the deterministic flag fix: plain letter runes
// become strings in both assignment and comparison contexts, while
// arithmetic/index uses, escapes, digits, and quoted apostrophes survive
// untouched.
func TestFixRuneLiterals(t *testing.T) {
	body := "flg := 'Y'\nif flg == 'N' {\n\treturn nil, err\n}\n" +
		"idx := arr['A']\nn := 'a' + 1\nesc := '\\n'\nd := '0'\n" +
		`s := "it's fine"` + "\nreturn data, nil"
	want := "flg := \"Y\"\nif flg == \"N\" {\n\treturn nil, err\n}\n" +
		"idx := arr['A']\nn := 'a' + 1\nesc := '\\n'\nd := '0'\n" +
		`s := "it's fine"` + "\nreturn data, nil"
	if got := fixRuneLiterals(body); got != want {
		t.Fatalf("fixRuneLiterals:\n got %q\nwant %q", got, want)
	}

	// cleanBody applies the fix as part of extract-side normalization.
	if got := cleanBody("```go\nflg := 'Y'\n```"); got != `flg := "Y"` {
		t.Fatalf("cleanBody rune fix = %q", got)
	}
}

// TestFixRuneLiteralsSliceBounds pins the crash fix: missing slice bounds
// (s[i:], s[:n], s[:]) leave nil AST fields, and the unsafe-marking walk
// must not Inspect them (ast.Inspect(nil) panics).
func TestFixRuneLiteralsSliceBounds(t *testing.T) {
	for _, body := range []string{
		"b := s[:3]\nreturn b, nil",
		"b := s[2:]\nreturn b, nil",
		"b := s[:]\nreturn b, nil",
		"b := s[1:2:3]\nreturn b, nil",
		"if arr[0] == 'Y' {\n\tb := s[:1]\n\t_ = b\n}\nreturn data, nil",
	} {
		got := fixRuneLiterals(body)
		if got == "" {
			t.Errorf("fixRuneLiterals returned empty for %q", body)
		}
	}
	// The rune fix still applies next to a slice expression.
	if got := fixRuneLiterals("flg := 'Y'\nb := s[:1]\n_ = b\nreturn flg, nil"); !strings.Contains(got, `flg := "Y"`) {
		t.Errorf("rune fix lost with slice present: %q", got)
	}
}

// TestRepairControllerBodyStripsArmThenFixesRunes is the GetMfFreed-attempt0
// regression: the payload re-added the leading else-if header AND carried
// 'N'/'Y' rune literals. cleanBody's fix no-ops on the unparseable header,
// so the repair must re-apply the rune fix after the strip — otherwise the
// gate burns a retry on literals the pipeline could have owned.
func TestRepairControllerBodyStripsArmThenFixesRunes(t *testing.T) {
	in := "else if request.MfGrowthFlg == \"F\" {\n" +
		"\tc_d2u_active_flg := 'N'\n" +
		"\tc_enable_d2u_flg := 'N'\n" +
		"\tif c_d2u_active_flg == 'Y' {\n" +
		"\t\tc_enable_d2u_flg = 'Y'\n" +
		"\t} else {\n" +
		"\t\tc_enable_d2u_flg = 'N'\n" +
		"\t}\n" +
		"\t_ = c_enable_d2u_flg\n" +
		"}\n"
	got := repairControllerBody(context.Background(), "GetMfFreed", "", in)
	if strings.Contains(got, "else if") {
		t.Fatalf("arm header survived repair:\n%s", got)
	}
	if errs := runeLiteralErrs(got); len(errs) != 0 {
		t.Fatalf("rune literals survived repair: %v\n%s", errs, got)
	}
	for _, want := range []string{`"N"`, `"Y"`} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s after repair:\n%s", want, got)
		}
	}
}

// TestStripLeadingArmChain pins the branch-continuation repair: a leading
// else-if/else header (with or without the orphan `}`) unwraps to its inner
// statements plus any tail; other bodies stay byte-identical.
func TestStripLeadingArmChain(t *testing.T) {
	in := "else if request.RqstTyp == \"W\" {\n" +
		"\ts.store.FetchAnalyzerMstr(c)\n" +
		"\treturn data, nil\n" +
		"}\n" +
		"s.store.GetUserInfo(c)\n" +
		"return data, nil\n"
	got, ok := stripLeadingArmChain(in)
	if !ok {
		t.Fatalf("else-if header not stripped:\n%s", in)
	}
	for _, want := range []string{"s.store.FetchAnalyzerMstr(c)", "s.store.GetUserInfo(c)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q after strip:\n%s", want, got)
		}
	}
	for _, gone := range []string{"else if", "request.RqstTyp"} {
		if strings.Contains(got, gone) {
			t.Errorf("header residue %q survived:\n%s", gone, got)
		}
	}

	// The orphan-`}` form and the plain else form unwrap too.
	if got, ok := stripLeadingArmChain("} else {\n\treturn data, nil\n}\n"); !ok || strings.TrimSpace(got) != "return data, nil" {
		t.Errorf("orphan else strip = %q, %v", got, ok)
	}
	if got, ok := stripLeadingArmChain("else { s.store.One(c) }"); !ok || got != "s.store.One(c)" {
		t.Errorf("bare else strip = %q, %v", got, ok)
	}

	// Ordinary bodies never match.
	for _, body := range []string{
		"if request.MfGrowthFlg == \"H\" {\n\treturn data, nil\n}",
		"s.store.One(c)\nelsewhere()\nreturn data, nil",
		"else if (unclosed {\n",
	} {
		if got, ok := stripLeadingArmChain(body); ok || got != body {
			t.Errorf("non-header body altered:\n in: %q\nout: %q", body, got)
		}
	}
}

// TestUncapturedStoreErrs pins the stale-err gate: bare store-call
// statements reject; assignments and inline error tests pass.
func TestUncapturedStoreErrs(t *testing.T) {
	bad := "s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, request.UsrAddrss2Stte)\n" +
		"if err != nil {\n\treturn nil, err\n}\n" +
		"return data, nil\n"
	errs := uncapturedStoreErrs(bad, "s.store.")
	if len(errs) != 1 || !strings.Contains(errs[0], "discarded") {
		t.Fatalf("uncapturedStoreErrs = %v, want one discard note", errs)
	}
	good := []string{
		"err = s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, request.UsrAddrss2Stte)\nif err != nil {\n\treturn nil, err\n}\nreturn data, nil",
		"rows, err := s.store.GetRiskProfile(c)\nif err != nil {\n\treturn nil, err\n}\n_ = rows\nreturn data, nil",
		"if err := s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, request.UsrAddrss2Stte); err != nil {\n\treturn nil, err\n}\nreturn data, nil",
		"fnIsD2uActive(lsMatchAcc, &cD2uActiveFlg)\nreturn data, nil",
		"err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {\n\treturn nil\n})\nreturn data, err",
	}
	for _, body := range good {
		if errs := uncapturedStoreErrs(body, "s.store."); len(errs) != 0 {
			t.Errorf("captured body flagged: %v\n%s", errs, body)
		}
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
