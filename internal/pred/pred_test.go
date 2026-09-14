package pred

import (
	"encoding/json"
	"reflect"
	"testing"
)

func eOr(items ...Expr) Expr { return Expr{Kind: KindOr, Items: items} }

func eAnd(items ...Expr) Expr { return Expr{Kind: KindAnd, Items: items} }

func eNot(inner Expr) Expr { return Expr{Kind: KindNot, Inner: &inner} }

func eCmp(l Expr, op string, r Expr) Expr {
	return Expr{Kind: KindCmp, L: &l, Op: op, R: &r}
}

func eCall(name string, args ...string) Expr {
	return Expr{Kind: KindCall, Name: name, Args: args}
}

func eLit(text string) Expr { return Expr{Kind: KindLit, Text: text} }

func eIdent(name string) Expr { return Expr{Kind: KindIdent, Name: name} }

func TestKindValues(t *testing.T) {
	tests := []struct {
		k    Kind
		want string
	}{
		{KindOr, "or"}, {KindAnd, "and"}, {KindNot, "not"}, {KindCmp, "cmp"},
		{KindCall, "call"}, {KindLit, "lit"}, {KindIdent, "ident"}, {KindRaw, "raw"},
	}
	for _, tt := range tests {
		if string(tt.k) != tt.want {
			t.Errorf("Kind %q = %q, want %q", tt.want, string(tt.k), tt.want)
		}
	}
}

func TestParsePrecedence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Expr
	}{
		{"&& binds tighter than ||", "a || b && c", eOr(eIdent("a"), eAnd(eIdent("b"), eIdent("c")))},
		{"&& binds tighter than || (left)", "a && b || c", eOr(eAnd(eIdent("a"), eIdent("b")), eIdent("c"))},
		{"parens override precedence", "(a || b) && c", eAnd(eOr(eIdent("a"), eIdent("b")), eIdent("c"))},
		{"or of two ands", "a && b || c && d", eOr(eAnd(eIdent("a"), eIdent("b")), eAnd(eIdent("c"), eIdent("d")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAndChain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Expr
	}{
		{"two items", "a && b", eAnd(eIdent("a"), eIdent("b"))},
		{"three items flat", "a && b && c", eAnd(eIdent("a"), eIdent("b"), eIdent("c"))},
		{"cmp binds tighter than &&", "a == 1 && b != 2 && c >= 3",
			eAnd(eCmp(eIdent("a"), "==", eLit("1")), eCmp(eIdent("b"), "!=", eLit("2")), eCmp(eIdent("c"), ">=", eLit("3")))},
		{"single item is unwrapped", "a", eIdent("a")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseNestedParens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Expr
	}{
		{"double parens transparent", "((a))", eIdent("a")},
		{"parens of ors inside and", "((a || b) && (c || d))",
			eAnd(eOr(eIdent("a"), eIdent("b")), eOr(eIdent("c"), eIdent("d")))},
		{"inner grouping kept", "a && (b || c)", eAnd(eIdent("a"), eOr(eIdent("b"), eIdent("c")))},
		{"parenthesised cmps", "(a == 1) || (b >= 2)",
			eOr(eCmp(eIdent("a"), "==", eLit("1")), eCmp(eIdent("b"), ">=", eLit("2")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseNot(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Expr
	}{
		{"simple not", "!x", eNot(eIdent("x"))},
		{"spaced double not", "! !x", eNot(eNot(eIdent("x")))},
		{"glued double not", "!!x", eNot(eNot(eIdent("x")))},
		{"not then and", "!a && b", eAnd(eNot(eIdent("a")), eIdent("b"))},
		{"not of group", "!(a && b)", eNot(eAnd(eIdent("a"), eIdent("b")))},
		{"not of cmp", "!(a < b)", eNot(eCmp(eIdent("a"), "<", eIdent("b")))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseCmpOps(t *testing.T) {
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		t.Run(op, func(t *testing.T) {
			want := eCmp(eIdent("a"), op, eIdent("b"))
			if got := Parse("a " + op + " b"); !reflect.DeepEqual(got, want) {
				t.Errorf("Parse = %#v, want %#v", got, want)
			}
		})
	}
}

func TestParseCmpWithCall(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Expr
	}{
		{"FML comparand", "Fget32(buf,F,0,&x,0) == -1",
			eCmp(eCall("Fget32", "buf", "F", "0", "&x", "0"), "==", eLit("-1"))},
		{"call on right, comma inside string arg stays one arg",
			`ret == strcmp(a, "x,y")`,
			eCmp(eIdent("ret"), "==", eCall("strcmp", "a", ` "x,y"`))},
		{"escaped quote inside arg", `f("a\"b") == 1`,
			eCmp(eCall("f", `"a\"b"`), "==", eLit("1"))},
		{"zero-arg call", "chk() == 0", eCmp(eCall("chk"), "==", eLit("0"))},
		// Args after the first carry their original leading space.
		{"bare call parses", "tpcall(svc, buf, 0, 0)", eCall("tpcall", "svc", " buf", " 0", " 0")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseForHeader(t *testing.T) {
	// Discovery pin: `for (…)` tokenizes as a Call named "for" with the
	// header body as raw argument text — not Raw.
	got := Parse("for (i = 0; i < n; i++)")
	want := eCall("for", "i = 0; i < n; i++")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %#v, want %#v", got, want)
	}
}

func TestParseDegradeRaw(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantText string
	}{
		{"chained comparison", "a < b < c", "a < b < c"},
		{"chained comparison eq", "a == b == c", "a == b == c"},
		{"ternary after cmp", "x == 1 ? a : b", "x == 1 ? a : b"},
		{"ternary alone", "a ? b : c", "a ? b : c"},
		{"arithmetic after cmp", "x == 5 + 1", "x == 5 + 1"},
		{"lone pipe after cmp", "a == b | c", "a == b | c"},
		{"lone amp after cmp", "a == b & c", "a == b & c"},
		{"assignment", "x = 1", "x = 1"},
		{"numeric suffix", "x == 100L", "x == 100L"},
		{"dangling &&", "a &&", "a &&"},
		{"unterminated string", `x == "abc`, `x == "abc`},
		{"unterminated call", "f(a, b", "f(a, b"},
		{"space between sign and digits", "x == - 1", "x == - 1"},
		{"empty", "", ""},
		{"plain words", "a b c", "a b c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.input)
			want := Expr{Kind: KindRaw, Text: tt.wantText}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, want)
			}
			if !got.IsRaw() {
				t.Errorf("IsRaw() = false, want true")
			}
		})
	}
}

func TestParseLiterals(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Expr
	}{
		{"char", "'x'", eLit("'x'")},
		{"string", `"ab"`, eLit(`"ab"`)},
		{"int", "42", eLit("42")},
		{"float", "1.5", eLit("1.5")},
		{"negative int", "-1", eLit("-1")},
		{"positive sign", "+7", eLit("+7")},
		{"string with escape", `"a\nb"`, eLit(`"a\nb"`)},
		{"char in cmp", "c == 'x'", eCmp(eIdent("c"), "==", eLit("'x'"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNegativeNumberSemantics(t *testing.T) {
	// Parser side: a glued sign makes `-1` a Lit comparand.
	want := eCmp(eIdent("x"), "==", eLit("-1"))
	if got := Parse("x == -1"); !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %#v, want %#v", got, want)
	}
	// Substitution side: IsLitText rejects negatives, so a define whose
	// value is an expression stays unresolved.
	if IsLitText("-1") || IsLitText("+1") {
		t.Errorf("IsLitText(\"-1\")/IsLitText(\"+1\") = true, want false (a sign makes it an expression, not a literal)")
	}
	if IsLitText("1.5") != true || IsLitText("'a'") != true || IsLitText(`"s"`) != true {
		t.Errorf("IsLitText rejects a plain literal")
	}
}

func TestStringRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"ident", "a"},
		{"negative lit", "-1"},
		{"float lit", "1.5"},
		{"char lit", "'c'"},
		{"string lit", `"s"`},
		{"zero-arg call", "f()"},
		{"one-arg call", "f(x)"},
		{"not", "!x"},
		{"not chain", "!!x"},
		{"not of and", "!(a && b)"},
		{"cmp", "a == b"},
		{"cmp ge", "a >= b"},
		{"or", "(a || b)"},
		{"and chain", "(a && b && c)"},
		{"or of and", "(a || (b && c))"},
		{"and of ors", "((a || b) && (c || d))"},
		{"raw passthrough", "a b c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Parse(tt.input)
			first := e.String()
			if first != tt.input {
				t.Errorf("Parse(%q).String() = %q, want %q", tt.input, first, tt.input)
			}
			e2 := Parse(first)
			if second := e2.String(); second != first {
				t.Errorf("re-parse %q renders %q", first, second)
			}
		})
	}
}

func TestStringRendering(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"or always wraps", "a || b", "(a || b)"},
		{"and chain wraps once", "a && b && c", "(a && b && c)"},
		{"cmp spacing normalized", "a==b", "a == b"},
		{"call arg spacing normalized", "f(a,b)", "f(a, b)"},
		{"FML call", "Fget32(buf,F,0,&x,0) == -1", "Fget32(buf, F, 0, &x, 0) == -1"},
		// Call args are untrimmed raw text; the joiner adds another space.
		{"args are untrimmed", "f(x, y)", "f(x,  y)"},
		// Not-of-cmp loses its parens (only or/and self-parenthesize) — the
		// re-render does not round-trip; exprGo re-parenthesizes.
		{"not of cmp drops parens", "!(a < b)", "!a < b"},
		{"empty renders empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Parse(tt.input)
			if got := e.String(); got != tt.want {
				t.Errorf("Parse(%q).String() = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
	var nilExpr *Expr
	if got := nilExpr.String(); got != "" {
		t.Errorf("nil String() = %q, want \"\"", got)
	}
	if nilExpr.IsRaw() {
		t.Errorf("nil IsRaw() = true, want false")
	}
}

func TestRawAndIdentConstructors(t *testing.T) {
	if got := Raw("  a   b  "); !reflect.DeepEqual(got, Expr{Kind: KindRaw, Text: "a b"}) {
		t.Errorf("Raw normalizes whitespace: %#v", got)
	}
	if got := Ident("x"); !reflect.DeepEqual(got, eIdent("x")) {
		t.Errorf("Ident = %#v, want %#v", got, eIdent("x"))
	}
}

type recordingResolver struct {
	defs  map[string]string
	asked []string
}

// resolve mirrors the documented resolver policy: follow bare-ident chains
// with a seen-set (cycle guard), accept only pure literals, keep the ident
// otherwise.
func (r *recordingResolver) resolve(name string) (string, bool) {
	r.asked = append(r.asked, name)
	seen := map[string]bool{}
	cur := name
	for {
		if seen[cur] {
			return "", false
		}
		seen[cur] = true
		next, ok := r.defs[cur]
		if !ok {
			return "", false
		}
		if IsLitText(next) {
			return next, true
		}
		if !IsBareIdent(next) {
			return "", false
		}
		cur = next
	}
}

func TestSubstitute(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		defs      map[string]string
		want      Expr
		wantAsked []string
	}{
		{"literal swap", "A == 5", map[string]string{"A": "7"}, eCmp(eLit("7"), "==", eLit("5")), []string{"A"}},
		{"deep in tree", "!(A && b) || C", map[string]string{"A": "1", "C": "2"},
			eOr(eNot(eAnd(eLit("1"), eIdent("b"))), eLit("2")), []string{"A", "b", "C"}},
		{"chain A→B→5", "A == 0", map[string]string{"A": "B", "B": "5"}, eCmp(eLit("5"), "==", eLit("0")), []string{"A"}},
		{"cycle guarded", "A == 0", map[string]string{"A": "B", "B": "A"}, eCmp(eIdent("A"), "==", eLit("0")), []string{"A"}},
		{"self cycle guarded", "A", map[string]string{"A": "A"}, eIdent("A"), []string{"A"}},
		{"compound value keeps ident", "A == 0", map[string]string{"A": "MAX(1,2)"}, eCmp(eIdent("A"), "==", eLit("0")), []string{"A"}},
		{"arithmetic value keeps ident", "A == 0", map[string]string{"A": "x + 1"}, eCmp(eIdent("A"), "==", eLit("0")), []string{"A"}},
		{"negative value keeps ident", "x == N", map[string]string{"N": "-1"}, eCmp(eIdent("x"), "==", eIdent("N")), []string{"x", "N"}},
		{"call untouched", "Fget32(buf,F,0) == A", map[string]string{"buf": "1", "F": "2", "A": "3"},
			eCmp(eCall("Fget32", "buf", "F", "0"), "==", eLit("3")), []string{"A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &recordingResolver{defs: tt.defs}
			e := Parse(tt.input)
			got := Substitute(&e, r.resolve)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Substitute = %#v, want %#v", got, tt.want)
			}
			if !reflect.DeepEqual(r.asked, tt.wantAsked) {
				t.Errorf("resolver asked %v, want %v", r.asked, tt.wantAsked)
			}
		})
	}
}

func TestSubstituteRawAndNil(t *testing.T) {
	r := &recordingResolver{defs: map[string]string{"x": "1"}}
	got := Substitute(nil, r.resolve)
	if !reflect.DeepEqual(got, Expr{}) {
		t.Errorf("Substitute(nil) = %#v, want zero Expr", got)
	}
	raw := Parse("x y z")
	gotRaw := Substitute(&raw, r.resolve)
	wantRaw := Expr{Kind: KindRaw, Text: "x y z"}
	if !reflect.DeepEqual(gotRaw, wantRaw) {
		t.Errorf("Substitute(Raw) = %#v, want %#v", gotRaw, wantRaw)
	}
	if len(r.asked) != 0 {
		t.Errorf("resolver was asked %v, want nothing", r.asked)
	}
}

func TestSubstituteDoesNotMutateInput(t *testing.T) {
	r := &recordingResolver{defs: map[string]string{"A": "7"}}
	e := Parse("A == 5")
	_ = Substitute(&e, r.resolve)
	if e.L.Kind != KindIdent || e.L.Name != "A" {
		t.Errorf("input mutated: %#v", *e.L)
	}
}

func TestIsLitText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"int", "42", true},
		{"float", "3.14", true},
		{"negative rejected", "-1", false},
		{"positive rejected", "+1", false},
		{"char", "'a'", true},
		{"empty char", "''", true},
		{"string", `"ab"`, true},
		{"empty string", `""`, true},
		{"unterminated char", "'a", false},
		{"unterminated string", `"s`, false},
		{"ident rejected", "abc", false},
		{"alnum rejected", "12x", false},
		{"space rejected", "1 2", false},
		{"bare dot passes numeric scan", ".", true},
		{"leading dot", ".5", true},
		{"trailing dot", "1.", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLitText(tt.in); got != tt.want {
				t.Errorf("IsLitText(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsBareIdent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"alpha", "abc", true},
		{"underscore alone", "_", true},
		{"leading underscore", "_a1", true},
		{"letter then digit", "a9", true},
		{"mixed", "A_b9", true},
		{"trimmed", "  x  ", true},
		{"leading digit", "9x", false},
		{"leading digit then alpha", "1a", false},
		{"space inside", "a b", false},
		{"dot", "a.b", false},
		{"operator", "a+b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBareIdent(tt.in); got != tt.want {
				t.Errorf("IsBareIdent(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestJSONEncoding(t *testing.T) {
	tests := []struct {
		name string
		e    Expr
		want string
	}{
		{"cmp", eCmp(eIdent("a"), ">=", eLit("1")),
			`{"kind":"cmp","l":{"kind":"ident","name":"a"},"r":{"kind":"lit","text":"1"},"op":"\u003e="}`},
		{"or", eOr(eIdent("a"), eIdent("b")),
			`{"kind":"or","items":[{"kind":"ident","name":"a"},{"kind":"ident","name":"b"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.e)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Marshal = %s, want %s", got, tt.want)
			}
		})
	}
}
