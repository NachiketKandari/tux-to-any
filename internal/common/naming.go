package common

import "strings"

// EscapeLeadingDigit prefixes "X" when s starts with a digit ("1ST_AMT" →
// "X1ST_AMT"), keeping source-derived names valid Go identifiers. The
// escape is a letter, not "_", so struct fields, row/request types and
// store methods stay exported — JSON tags and cross-package references
// keep working. Every Go-path naming policy applies it, so digit-leading
// FML/SQL tokens never fail the render gate (goast.Emit's parse check).
func EscapeLeadingDigit(s string) string {
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		return "X" + s
	}
	return s
}

// Export upper-cases the first rune ("nav" → "Nav"). One home for
// plan.interfaceName and gen.exportName (A3.2).
func Export(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return EscapeLeadingDigit(strings.ToUpper(string(r[0])) + string(r[1:]))
}

// LowerFirst lower-cases the first rune ("NavController" → "navController").
// The receiver-name convention (gen.lowerFirst).
func LowerFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToLower(string(r[0])) + string(r[1:])
}

// CamelGo renders a delimited identifier as exported CamelCase, lowercasing
// each segment first ("DEMO_ACC", "demo.acc", "demo acc" → "DemoAcc").
// Splits on '_' '.' ' '. The Go-path policy (plan.camel) — byte-identical
// moves only; the other casing policies are deliberately distinct (AD3).
func CamelGo(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '.' || r == ' ' })
	var sb strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		r := []rune(strings.ToLower(p))
		sb.WriteRune([]rune(strings.ToUpper(string(r[0])))[0])
		sb.WriteString(string(r[1:]))
	}
	return EscapeLeadingDigit(sb.String())
}

// CamelLowerGo renders a snake_case identifier with the first segment kept
// verbatim and later segments capitalized ("comp_cd" → "compCd"). The Go
// field-name policy (gen.camelLower) — the inverse of Export∘CamelGo.
func CamelLowerGo(s string) string {
	parts := strings.Split(s, "_")
	var sb strings.Builder
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i == 0 {
			sb.WriteString(p)
			continue
		}
		r := []rune(p)
		sb.WriteRune([]rune(strings.ToUpper(string(r[0])))[0])
		sb.WriteString(string(r[1:]))
	}
	return EscapeLeadingDigit(sb.String())
}

// CamelPy renders an identifier as CamelCase preserving inner case, after
// sanitizing non-identifier bytes to '_' ("FML_MF-NAV" → "FMLMfNav"-shaped).
// The Python-path policy (pyplan.Camel) — case-preserving where CamelGo
// lowercases; splits on '_' '-' '.' (AD3: the policies are load-bearing
// and distinct).
func CamelPy(s string) string {
	parts := strings.FieldsFunc(PyIdent(s), func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	var sb strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]))
		sb.WriteString(p[1:])
	}
	return sb.String()
}

// PyIdent maps every non [_a-zA-Z0-9] byte to '_' — the Python-identifier
// sanitizer pyplan.Camel composes with (pyplan.pyIdent).
func PyIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Pascal renders a delimited identifier as PascalCase, stripping the corpus
// Hungarian/type prefixes (sql_, vc_, v_, c_, l_, d_, i_, f_) first
// (uniform-ir plan §3.3: promoted from csplan.pascalOf so every backend
// shares one rule). "sql_cst_pan_no" → "CstPanNo".
func Pascal(s string) string {
	name := StripHungarianPrefix(strings.TrimPrefix(s, ":"))
	parts := strings.Split(name, "_")
	var sb strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(part[:1]))
		sb.WriteString(part[1:])
	}
	out := sb.String()
	if out == "" {
		return "Param"
	}
	return out
}

// UpperSnake renders a row-shape host var as an upper-snake DTO/column token
// (uniform-ir plan §3.3: promoted from csplan.propNameOf).
// "sql_mar_form_no" → "MAR_FORM_NO". Leading digits take the underscore
// escape ("sql_17dim_val" → "_17DIM_VAL").
func UpperSnake(s string) string {
	name := StripHungarianPrefix(strings.TrimPrefix(s, ":"))
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ToUpper(name)
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name
}

// Snake renders an identifier as lower_snake, sanitizing non-identifier
// bytes to '_' first ("FML_MF-NAV" → "fml_mf_nav").
func Snake(s string) string {
	return strings.ToLower(PyIdent(s))
}

// StripHungarianPrefix strips the Pro*C type/hungarian prefixes the corpus
// carries (sql_, vc_, v_, c_, l_, d_, i_, f_) before naming. It loops so
// stacked prefixes ("sql_vc_x") collapse fully.
func StripHungarianPrefix(s string) string {
	for {
		lower := strings.ToLower(s)
		stripped := false
		for _, p := range []string{"sql_", "vc_", "v_", "c_", "l_", "d_", "i_", "f_"} {
			if strings.HasPrefix(lower, p) {
				s = s[len(p):]
				lower = strings.ToLower(s)
				stripped = true
				break
			}
		}
		if !stripped {
			return s
		}
	}
}
