package convert

import (
	"regexp"
	"strings"

	"tux-to-any/internal/ir"
)

// Deterministic legacy-seam rewrites (micro-chunk step 2, measured run
// 16092026_184233): the two fragment-level killers left after scaffold
// elision were the model echoing FML unpack/error-append spellings the
// tuxedo gate forbids — Fget32 unpack blocks, Fadd32(FML_ERR_MSG) error
// legs, chk_sssn session checks. All three carry a single mechanical intent
// with a deterministic Go shape, so the VIEW carries the mapped Go instead
// of the legacy spelling — the same pass-through pattern ReplaceQueries
// established for SQL → store calls. The model copies; the tuxedo gate
// never has to reject.

// seamCounts tallies one rewriteLegacySeams pass for telemetry.
type seamCounts struct {
	Gets    int // Fget32 unpack lines rewritten to request reads
	ErrAdds int // Fadd32(ERR-field) error legs rewritten to err = fmt.Errorf
	Ssn     int // chk_sssn session checks neutralized
}

var (
	// seamStmtRe matches a statement-leading call: optional provenance
	// markers, then either `Fget32(`/`Fadd32(` bare or `lhs = Call(`.
	// Branch conditions (`if (Fget32(...) == -1)`) never match — their
	// `if` prefix blocks the anchor.
	seamStmtRe = regexp.MustCompile(`^\s*(?:/\*[^*]*\*/\s*)*(?:[A-Za-z_][A-Za-z0-9_]*(?:\s*\[[^\]]*\])?\s*=\s*)?(Fget32|Fadd32)\s*\(`)
	// seamAssignRe strips a trailing `lhs = ` assignment from pre-call text.
	seamAssignRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\s*\[[^\]]*\])?\s*=\s*$`)
	// seamCastRe strips a C cast prefix on a call argument.
	seamCastRe = regexp.MustCompile(`(?i)\(\s*[a-zA-Z_][a-zA-Z0-9_ ]*\*+\s*\)`)
)

// rewriteLegacySeams maps the deterministic FML/session seams in a
// flattened branch view before prompting:
//
//   - `i_err[k] = Fget32(buf, FML_X, 0, (char*)&tgt, 0)` with FML_X in the
//     endpoint's request map → `tgt := request.<GoField>;` (verbatim
//     normalized target — the model's own accepted bodies spell it exactly
//     this way). Unmapped fields stay legacy; the model still owns them.
//   - `Fadd32(inbuf, FML_ERR_MSG, expr, 0);` error-append statements →
//     `err = fmt.Errorf("%s", expr);` — the error leg's S-code (carried by
//     expr's writer) stays in the error text per the prompt contract.
//     Response-shaping adds (non-ERR fields, Obuffer fan-outs) and
//     condition-context adds are left for the AI to shape.
//   - `x = chk_sssn(...)` → `x = 0;` — session prologue the prompt already
//     orders dropped; the timeout guard becomes dead, which is correct
//     (Go middleware owns sessions).
//
// Returns the rewritten source, the counts, and whether anything changed.
func rewriteLegacySeams(src string, reqMap map[string]string) (string, seamCounts, bool) {
	var out []string
	var c seamCounts
	changed := false
	for _, line := range strings.Split(src, "\n") {
		rewritten, kind := rewriteSeamLine(line, reqMap)
		switch kind {
		case seamGet:
			c.Gets++
			changed = true
		case seamErrAdd:
			c.ErrAdds++
			changed = true
		case seamSsn:
			c.Ssn++
			changed = true
		}
		out = append(out, rewritten)
	}
	return strings.Join(out, "\n"), c, changed
}

type seamKind int

const (
	seamNone seamKind = iota
	seamGet
	seamErrAdd
	seamSsn
)

// rewriteSeamLine applies the three seam rewrites to one line, returning
// the (possibly original) line and which rewrite fired.
func rewriteSeamLine(line string, reqMap map[string]string) (string, seamKind) {
	// chk_sssn first: the unpack block's `l_sssn_id_chk = chk_sssn(...)`
	// precedes any Fget line ordering — order within a line never collides
	// (one call per line in the flattened views).
	if i := strings.Index(line, "chk_sssn("); i >= 0 {
		if eq := assignmentEq(line, i); eq >= 0 {
			if _, close, ok := balancedParens(line, i+len("chk_sssn")); ok && lineEndsWithStmt(line, close) {
				return line[:eq+1] + " 0;" + trailingSuffix(line, close), seamSsn
			}
		}
		return line, seamNone
	}
	m := seamStmtRe.FindStringSubmatchIndex(line)
	if m == nil {
		return line, seamNone
	}
	name := line[m[2]:m[3]]
	args, close, ok := callArgs(line, m[1]-1)
	if !ok || len(args) < 4 {
		return line, seamNone
	}
	// Prefix keeps provenance markers and whitespace, drops any
	// `lhs = ` assignment — the rewritten statement replaces it whole.
	head := seamAssignRe.ReplaceAllString(line[:m[2]], "")
	switch name {
	case "Fget32":
		field := strings.TrimSpace(args[1])
		goField, mapped := reqMap[field]
		if !mapped {
			return line, seamNone
		}
		target := normalizeSeamTarget(args[3])
		if target == "" || !isPlainIdent(target) {
			return line, seamNone
		}
		return head + target + " := request." + goField + ";" + trailingSuffix(line, close), seamGet
	case "Fadd32":
		field := strings.TrimSpace(args[1])
		if !ir.IsErrField(field) {
			return line, seamNone
		}
		expr := normalizeSeamTarget(args[2])
		if expr == "" {
			return line, seamNone
		}
		return head + "err = fmt.Errorf(\"%s\", " + expr + ");" + trailingSuffix(line, close), seamErrAdd
	}
	return line, seamNone
}

// assignmentEq returns the offset of the `=` that assigns the call whose
// name starts at i (the RHS target), or -1 when the call is not a plain
// assignment's RHS (comparison, condition, bare statement).
func assignmentEq(line string, callStart int) int {
	head := line[:callStart]
	eq := strings.LastIndex(head, "=")
	if eq < 0 {
		return -1
	}
	if j := strings.LastIndex(head[:eq], "=="); j >= 0 && eq >= j && eq <= j+1 {
		return -1 // `x == chk_sssn(` — a comparison, never rewrite
	}
	lhs := strings.TrimSpace(head[:eq])
	if lhs == "" || strings.ContainsAny(lhs, "(=<>!&|") {
		return -1
	}
	return eq
}

// lineEndsWithStmt reports whether the statement after the call's closing
// paren at close ends the line (a `;` — optionally followed by a comment).
func lineEndsWithStmt(line string, close int) bool {
	rest := strings.TrimSpace(line[close+1:])
	return rest == "" || rest == ";" || strings.HasPrefix(rest, ";") || strings.HasPrefix(rest, "/*")
}

// trailingSuffix keeps a line's trailing comment (provenance markers or a
// C comment the flattened render may suffix) after a rewritten statement.
// close is the offset of the call's closing paren; the scan starts past it.
func trailingSuffix(line string, close int) string {
	rest := line[close+1:]
	if i := strings.Index(rest, ";"); i >= 0 {
		if j := strings.Index(rest[i:], "/*"); j >= 0 {
			return " " + rest[i+j:]
		}
		return ""
	}
	if j := strings.Index(rest, "/*"); j >= 0 {
		return " " + rest[j:]
	}
	return ""
}

// isPlainIdent guards the rewrite targets: a bare Go-safe identifier only.
var isPlainIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isPlainIdent(s string) bool { return isPlainIdentRe.MatchString(s) }

// normalizeSeamTarget reduces a call argument to the bare host-var name the
// Go assignment writes: `(char *)&c_user_id` → c_user_id,
// `(char *)sql_rpd_prdt_id.arr` → sql_rpd_prdt_id. The verbatim C spelling
// is deliberate — the model transliterates view identifiers byte-for-byte,
// so the unpack declaration stays consistent with the condition uses of the
// same name.
func normalizeSeamTarget(arg string) string {
	s := strings.TrimSpace(arg)
	if j := strings.Index(s, "/*"); j >= 0 { // trailing provenance marker
		if k := strings.Index(s[j:], "*/"); k >= 0 {
			s = s[:j] + s[j+k+2:]
		}
	}
	s = strings.TrimSpace(seamCastRe.ReplaceAllString(s, ""))
	s = strings.TrimPrefix(s, "&")
	if j := strings.LastIndex(s, "."); j >= 0 && strings.HasSuffix(s, ".arr") {
		s = s[:j]
	}
	if j := strings.Index(s, "["); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// callArgs extracts the top-level comma-split arguments of the call whose
// opening paren sits at offset open (the index of `(`). Honors string and
// char literals and all bracket kinds. ok=false when the parens never
// close on this line.
func callArgs(line string, open int) (args []string, close int, ok bool) {
	content, c, ok := balancedParens(line, open)
	if !ok {
		return nil, 0, false
	}
	return splitTopArgs(content), c, true
}

// balancedParens returns the content between the paren at start and its
// matching close, honoring string/char literals and nested brackets.
func balancedParens(s string, start int) (string, int, bool) {
	depth := 0
	inStr, inChar, esc := false, false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case esc:
			esc = false
		case inStr:
			if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inStr = false
			}
		case inChar:
			if ch == '\\' {
				esc = true
			} else if ch == '\'' {
				inChar = false
			}
		case ch == '"':
			inStr = true
		case ch == '\'':
			inChar = true
		case ch == '(' || ch == '[' || ch == '{':
			depth++
		case ch == ')' || ch == ']' || ch == '}':
			depth--
			if depth == 0 {
				return s[start+1 : i], i, true
			}
		}
	}
	return "", 0, false
}

// splitTopArgs splits on commas at bracket depth zero, honoring literals.
func splitTopArgs(s string) []string {
	var parts []string
	depth := 0
	inStr, inChar, esc := false, false, false
	last := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case esc:
			esc = false
		case inStr:
			if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inStr = false
			}
		case inChar:
			if ch == '\\' {
				esc = true
			} else if ch == '\'' {
				inChar = false
			}
		case ch == '"':
			inStr = true
		case ch == '\'':
			inChar = true
		case ch == '(' || ch == '[' || ch == '{':
			depth++
		case ch == ')' || ch == ']' || ch == '}':
			depth--
		case ch == ',' && depth == 0:
			parts = append(parts, s[last:i])
			last = i + 1
		}
	}
	parts = append(parts, s[last:])
	return parts
}
