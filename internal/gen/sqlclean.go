package gen

import "strings"

// stripInto removes the `INTO :a, :b …` span from a SELECT's SQL text.
// The host-var INTO list is a Pro*C construct, not SQL (BP-8, the same
// tolerance the fidelity gate applies): the Go driver scans columns into
// the row struct by name, and the IR's RowShape already carries the INTO
// targets, so the emitted query string must not contain the clause. The
// leading-`:` guard keeps `INSERT INTO t` untouched; the FROM scan is
// paren-aware and literal-aware. Port of the python planner's proven
// stripInto (pyplan.stripInto).
func stripInto(sql string) string {
	lower := strings.ToLower(sql)
	for i := 0; i+4 <= len(lower); i++ {
		if lower[i:i+4] != "into" || !wordBoundary(sql, i, 4) {
			continue
		}
		j := i + 4
		for j < len(sql) && (sql[j] == ' ' || sql[j] == '\t' || sql[j] == '\n' || sql[j] == '\r') {
			j++
		}
		if j >= len(sql) || sql[j] != ':' {
			continue // INSERT INTO t — not a host-var list
		}
		depth := 0
		for k := j; k < len(sql); k++ {
			switch sql[k] {
			case '\'':
				k = skipLit(sql, k)
			case '(':
				depth++
			case ')':
				depth--
			case 'f', 'F':
				if depth == 0 && k+4 < len(sql) && strings.EqualFold(sql[k:k+4], "from") && wordBoundary(sql, k, 4) {
					return strings.TrimSpace(sql[:i] + sql[k:])
				}
			}
		}
		return strings.TrimSpace(sql[:i])
	}
	return sql
}

func wordBoundary(sql string, i, n int) bool {
	before := i == 0 || !isIdentByte(sql[i-1])
	after := i+n >= len(sql) || !isIdentByte(sql[i+n])
	return before && after
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func skipLit(sql string, i int) int {
	for j := i + 1; j < len(sql); j++ {
		if sql[j] == '\'' {
			if j+1 < len(sql) && sql[j+1] == '\'' {
				j++
				continue
			}
			return j
		}
	}
	return len(sql) - 1
}
