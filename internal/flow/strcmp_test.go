package flow

import (
	"strings"
	"testing"

	"tux-to-any/internal/pred"
)

// TestStrcmpEqualityRendersTheDispatchIdiom is the positive case, and it is
// what unblocks the risk-profile dispatch: `fn_save_risk_profile` in the
// corpus is one if / else-if / else chain over
// strcmp(sql_oiv_ovp_lvl1_cd, "CSH" | "EQT" | "DBT"), and that chain is the
// whole business logic of the helper. C's strcmp returns 0 iff the two strings
// are byte-equal and Go's `==` on strings is byte-equal, so this is an
// identity, not an approximation.
func TestStrcmpEqualityRendersTheDispatchIdiom(t *testing.T) {
	cases := []struct{ in, want string }{
		{`strcmp(sql_oiv_ovp_lvl1_cd,"CSH")==0`, `sql_oiv_ovp_lvl1_cd == "CSH"`},
		{`strcmp(a,"EQT")==0`, `a == "EQT"`},
		{`strcmp(a,"DBT")==0`, `a == "DBT"`},
		{`strcmp(a,"CSH")!=0`, `a != "CSH"`},
		{`strcmp( a , "X" ) == 0`, `a == "X"`},
		// The literal on the left is legal C and still an equality test.
		{`strcmp("CSH",a)==0`, `"CSH" == a`},
		// Both sides identifiers: strcmp(a,b)==0 is a==b in C too.
		{`strcmp(a,b)==0`, `a == b`},
		// A char literal operand, which charLitToGo already normalises.
		{`strcmp(c_flg,'A')==0`, `c_flg == "A"`},
	}
	for _, c := range cases {
		e := pred.Parse(c.in)
		if got := exprGo(&e); got != c.want {
			t.Errorf("exprGo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestStrcmpEqualityDeclinesWhatItCannotExpress is the half that matters more
// than the half above. Every case here would render as compiling Go that is
// wrong, or as a runtime difference C does not have, so each must stay "" and
// leave the caller's TODO in place.
func TestStrcmpEqualityDeclinesWhatItCannotExpress(t *testing.T) {
	cases := []struct{ in, why string }{
		{`strncmp(a,"CSH",3)==0`, "strncmp stops at a length; a == \"CSH\" is a different test"},
		{`strcasecmp(a,"csh")==0`, "strcasecmp ignores case; == does not"},
		{`strcmp(a,"CSH")<0`, "an ordering test is not expressible as =="},
		{`strcmp(a,"CSH")>0`, "an ordering test is not expressible as =="},
		{`strcmp(a,"CSH")==1`, "only the zero result means equal"},
		{`strcmp(a,"CSH",3)==0`, "three args is not the two-arg idiom"},
		{`strcmp(a)==0`, "one arg is not the idiom"},
		{`strcmp(sql_ovp_lvl1_desc.arr,"X")==0`, ".arr is a Tuxedo struct member, not a Go field"},
		{`strcmp(p->f,"X")==0`, "arrow access has no determined Go spelling"},
		{`strcmp(get(),"X")==0`, "a call operand is not a value"},
		{`strcmp(a,"X" "Y")==0`, "concatenated literals are not handled"},
		{`strcmp(a,"unterminated)==0`, "an unterminated literal is not a literal"},
	}
	for _, c := range cases {
		e := pred.Parse(c.in)
		if got := exprGo(&e); got != "" {
			t.Errorf("exprGo(%q) = %q, want \"\" — %s", c.in, got, c.why)
		}
	}
}

// TestStrncmpIsStillHarvestedButNotRendered pins the deliberate split between
// the two strcmp recognizers. The axis harvester matches `strn?cmp` with one
// regex because strncmp(a,"CSH",3) and strcmp(a,"CSH") do select the same
// dispatch value — the value domain is the same either way. Rendering is not
// the same question, which is why strcmpEquality demands exactly "strcmp".
// If someone later unifies the two regexes, this test is what catches it.
func TestStrncmpIsStillHarvestedButNotRendered(t *testing.T) {
	if !strcmpSiteRe.MatchString(`strncmp(a,"CSH",3)==0`) {
		t.Error("the axis harvester no longer matches strncmp — the value " +
			"domain for a length-bounded compare would be lost")
	}
	e := pred.Parse(`strncmp(a,"CSH",3)==0`)
	if got := exprGo(&e); got != "" {
		t.Errorf("exprGo rendered a strncmp as %q; a length-bounded compare is "+
			"not an equality test and must not become one", got)
	}
}

// TestStrcmpEqualityInsideCompoundConditions checks the idiom survives the
// boolean combinators, since the corpus writes membership tests as
// `strcmp(a,"X")==0 && strcmp(a,"Y")!=0`.
func TestStrcmpEqualityInsideCompoundConditions(t *testing.T) {
	e := pred.Parse(`strcmp(a,"X")==0 && strcmp(a,"Y")!=0`)
	if got := exprGo(&e); got != `a == "X" && a != "Y"` {
		t.Errorf("compound dispatch = %q, want %q", got, `a == "X" && a != "Y"`)
	}
	// And a negation of the whole call, the `!strcmp(...)` spelling the
	// normalise-chain idiom uses.
	n := pred.Parse(`!strcmp(a,"X")`)
	if got := exprGo(&n); got != "" {
		t.Errorf("!strcmp(a,\"X\") = %q — expected \"\" so the spelling stays a "+
			"visible TODO rather than a guessed `a != \"X\"`", got)
	}
}

// TestIsGoIdentRejectsWhatWouldNotCompile keeps the operand rule honest at the
// unit level: anything that is not a bare identifier must be declined, since
// a member access slipped through would produce Go that references a field
// the target struct does not have.
func TestIsGoIdentRejectsWhatWouldNotCompile(t *testing.T) {
	for _, ok := range []string{"a", "_x", "sql_oiv_ovp_lvl1_cd", "a1", "A_b_9"} {
		if !isGoIdent(ok) {
			t.Errorf("isGoIdent(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "1a", "a.b", "a->b", "a b", "a-b", "a()", "(a)", "a*", "*a", "9"} {
		if isGoIdent(bad) {
			t.Errorf("isGoIdent(%q) = true, want false", bad)
		}
	}
}

// TestStrcmpOperandLeavesNoRoomForSilentTruncation: a string literal is
// accepted verbatim, so a malformed one must be rejected rather than
// normalised into something that happens to parse.
func TestStrcmpOperandLeavesNoRoomForSilentTruncation(t *testing.T) {
	if got, ok := strcmpOperand(`"CSH"`); !ok || got != `"CSH"` {
		t.Errorf("strcmpOperand(%q) = %q,%v", `"CSH"`, got, ok)
	}
	if got, ok := strcmpOperand(`""`); !ok || got != `""` {
		t.Errorf("an empty literal should be accepted: %q,%v", got, ok)
	}
	for _, bad := range []string{`"`, `"a`, `a"b"`, `"a" "b"`, `a b`, `a.b`, ``} {
		if got, ok := strcmpOperand(bad); ok {
			t.Errorf("strcmpOperand(%q) = %q, true; want declined", bad, got)
		}
	}
	// Whatever it accepts must never carry a line break or a stray quote
	// into the rendered condition.
	for _, arg := range []string{`"CSH"`, "a", `'A'`} {
		got, ok := strcmpOperand(arg)
		if !ok {
			continue
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("strcmpOperand(%q) = %q carries a line break", arg, got)
		}
	}
}
