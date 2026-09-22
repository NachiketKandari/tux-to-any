// Per-method block renderers: each turns one unit's extracted facts plus
// fixture values into a single table-driven suite-method block via the
// test templates (PRD-2026-09-09 GT-3).
package testgen

import (
	"regexp"

	"fmt"
	"strings"

	"tux-to-any/internal/templates"
)

// dbCols returns the mock-row columns for a db fact: the row struct's
// db-tagged fields in declaration order, or the scalar heuristic column.
func dbCols(sc *serviceCtx, f *dbFact) []string {
	if f.RowType == "" {
		if strings.Contains(strings.ToLower(f.Name), "count") {
			return []string{"count"}
		}
		return []string{"value"}
	}
	base := structBase(f.RowType)
	var cols []string
	for _, fl := range sc.models.Structs[base] {
		if fl.DB != "" {
			cols = append(cols, fl.DB)
		}
	}
	return cols
}

// structBase strips slice/pointer wrappers and the models qualifier,
// leaving the bare struct name for the models inventory lookup.
func structBase(t string) string {
	t = strings.TrimPrefix(t, "[]*")
	t = strings.TrimPrefix(t, "[]")
	t = strings.TrimPrefix(t, "*")
	return strings.TrimPrefix(t, "models.")
}

// dbRegex builds the ExpectQuery regex over the FROM table list. It is
// case-insensitive and whitespace-tolerant — converted SQL keeps the
// source's casing and line breaks (`select ... from dual` with no WHERE is
// legal) — so the mock matches what the store actually executes instead of
// failing the SQLError case and cascading stale expectations through the
// suite. Backslashes are doubled for the generated Go string literal. A
// query with no resolvable table (literal built elsewhere) anchors on the
// statement's first word, else matches permissively — a test that still
// exercises the method is better than a regex that never matches.
func dbRegex(f *dbFact) string {
	if len(f.Tables) == 0 {
		if f.Query == "" {
			return `(?i)^`
		}
		return `(?i)^` + strings.ToLower(firstWord(f.Query)) + `\\s+`
	}
	tables := make([]string, len(f.Tables))
	for i, t := range f.Tables {
		tables[i] = regexp.QuoteMeta(t)
	}
	return `(?i)^select\\s+(.+)\\s+from\\s+` + strings.Join(tables, `\\s*,\\s*`) + `(\\s+where\\s+(.+))?$`
}

// dbExecRegex builds the ExpectExec regex over the DML target table. Like
// dbRegex it is case-insensitive and whitespace-tolerant; the verb anchors
// the statement (INSERT INTO / UPDATE / DELETE FROM / MERGE INTO) so the
// mock matches the tx-variant ExecContext bodies (decision 27). A query
// with no literal renders a permissive regex — the call site still runs
// through the Exec contract.
func dbExecRegex(f *dbFact) string {
	table := `(.+)`
	if len(f.Tables) > 0 && f.Tables[0] != "" {
		table = regexp.QuoteMeta(f.Tables[0])
	}
	up := strings.ToUpper(strings.TrimSpace(f.Query))
	switch {
	case strings.HasPrefix(up, "INSERT"):
		return `(?i)^insert\\s+into\\s+` + table + `(\\s+.+)?$`
	case strings.HasPrefix(up, "UPDATE"):
		return `(?i)^update\\s+` + table + `(\\s+set\\s+(.+))?$`
	case strings.HasPrefix(up, "DELETE"):
		return `(?i)^delete\\s+from\\s+` + table + `(\\s+where\\s+(.+))?$`
	case strings.HasPrefix(up, "MERGE"):
		return `(?i)^merge\\s+into\\s+` + table + `(\\s+.+)?$`
	case f.Query == "":
		return `(?i)^`
	default:
		return `(?i)^` + strings.ToLower(firstWord(f.Query)) + `\\s+(.+)$`
	}
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return regexp.QuoteMeta(f[0])
	}
	return `(.+)`
}

// isDeleteTx reports whether the fact is the DELETE-tx variant: the only
// DML shape that tolerates zero rows (DeleteQnA convention — result
// discarded, no RowsAffected check). Every other DML shape returns
// sql.ErrNoRows when RowsAffected == 0.
func isDeleteTx(f *dbFact) bool {
	if !f.IsTx || f.Shape != "dml" {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(f.Query)), "DELETE")
}

// dbExpectType renders the expectedOutput Go type. Tx scalar reads scan
// into sql.Null* but return the extracted Go type (GetMarks: NullString var,
// string return) — the return type wins when the scan type is a Null*.
func dbExpectType(f *dbFact) string {
	switch f.Shape {
	case "multi":
		return "[]*" + f.RowType
	case "single":
		return "*" + f.RowType
	default:
		if f.IsTx && strings.HasPrefix(f.Scalar, "sql.Null") && f.ReturnType != "" {
			return f.ReturnType
		}
		return f.Scalar
	}
}

// rowLiteral renders a row-struct composite literal with one element, from
// the fixture source.
func rowLiteral(sc *serviceCtx, rowType string) string {
	base := structBase(rowType)
	var fields []string
	for _, fl := range sc.models.Structs[base] {
		if fl.DB == "" {
			continue
		}
		fields = append(fields, fl.Name+": "+sc.fixtures.ColExpr(fl.DB, fl.Type))
	}
	joined := strings.Join(fields, ", ")
	switch {
	case strings.HasPrefix(rowType, "[]*"):
		if joined == "" {
			return rowType + "{{}}"
		}
		return rowType + "{{" + joined + "}}"
	case strings.HasPrefix(rowType, "*"):
		return "&" + rowType[1:] + "{" + joined + "}"
	default:
		return rowType + "{" + joined + "}"
	}
}

// dbExpectExpr renders the Success expectedOutput literal. Tx scalar reads
// return the extracted type, not the Null* scan var — the fixture zero of
// the return type matches what the method returns (GetMarks: "").
func dbExpectExpr(sc *serviceCtx, f *dbFact) string {
	switch f.Shape {
	case "multi":
		return rowLiteral(sc, "[]*"+f.RowType)
	case "single":
		return rowLiteral(sc, "*"+f.RowType)
	default:
		if f.IsTx && strings.HasPrefix(f.Scalar, "sql.Null") && f.ReturnType != "" {
			return sc.fixtures.ZeroExpr(f.ReturnType)
		}
		return sc.fixtures.ZeroExpr(f.Scalar)
	}
}

// dbCallArgs renders the bind literals after ctx, by param type. The tx
// handle (*sqlx.Tx) is excluded — the test supplies it via Beginx, not a
// fixture value.
func dbCallArgs(sc *serviceCtx, f *dbFact) []string {
	var out []string
	for _, p := range f.Params {
		if p.Type == "context.Context" {
			continue
		}
		if p.Type == "*sqlx.Tx" || p.Type == "sqlx.Tx" || p.Name == "tx" {
			continue
		}
		out = append(out, sc.fixtures.ArgValue(p.Name, p.Type))
	}
	return out
}

// renderDBMethod renders one db suite method block: SELECT shapes through
// the query contract, DML shapes (plain + tx-variant) through the Exec
// contract, tx shapes with the Beginx handle.
func renderDBMethod(u *unit) (string, error) {
	sc := u.sc
	f := u.db
	if f.Shape == "dml" {
		prov := u.sc.provider()
		return prov.Render(templates.TestDBMethod, templates.TestDBMethodData{
			SuiteName: u.suite,
			StoreVar:  strings.ToLower(sc.name) + "Store",
			Name:      f.Name,
			Query:     f.Query,
			Regex:     dbExecRegex(f),
			Shape:     f.Shape,
			CallArgs:  dbCallArgs(sc, f),
			IsDML:     true,
			IsTx:      f.IsTx,
			DeleteTx:  isDeleteTx(f),
		})
	}
	cols := dbCols(sc, f)
	row := sc.fixtures.RowValues(cols)
	expectScalar := f.Scalar
	if f.IsTx && strings.HasPrefix(f.Scalar, "sql.Null") && f.ReturnType != "" {
		expectScalar = f.ReturnType
	}
	if f.RowType == "" {
		// Scalar scan: the placeholder column name is not a scan-compatible
		// value ("count" cannot convert to int64); the zero of the scan
		// type is, and it matches the ZeroExpr expectation exactly.
		if expectScalar == "string" {
			row = []string{""}
		} else {
			row = []string{"0"}
		}
	}
	// Single-row and scalar reads tolerate sql.ErrNoRows (the converted
	// GetContext methods return the zero value with a nil error); multi-row
	// SelectContext reads do not need the case (an empty slice is success).
	// Tx reads propagate every error to the flow, so they never carry it.
	noRows := (f.Shape == "scalar" || f.Shape == "single") && !f.IsTx
	noRowsExpr := sc.fixtures.ZeroExpr(expectScalar)
	if f.Shape == "single" && f.RowType != "" {
		noRowsExpr = "&" + f.RowType + "{}"
	}
	prov := u.sc.provider()
	return prov.Render(templates.TestDBMethod, templates.TestDBMethodData{
		SuiteName:  u.suite,
		StoreVar:   strings.ToLower(sc.name) + "Store",
		Name:       f.Name,
		Query:      f.Query,
		Regex:      dbRegex(f),
		Shape:      f.Shape,
		Cols:       cols,
		Row:        row,
		NoRows:     noRows,
		NoRowsExpr: noRowsExpr,
		ExpectType: dbExpectType(f),
		ExpectExpr: dbExpectExpr(sc, f),
		CallArgs:   dbCallArgs(sc, f),
		IsDML:      false,
		IsTx:       f.IsTx,
	})
}

// ctrlExpectArgs renders the EXPECT args for a controller's store call as
// concrete literals (examples/nav/controller shape): request-field selectors
// resolve to the same assumed fixture value the case struct carries, plain
// literals pass through verbatim, anything else degrades to a fixture
// placeholder — never a request reference (the EXPECT block precedes the
// request declaration in the reference shape).
func ctrlExpectArgs(sc *serviceCtx, f *ctrlFact, call storeCall) []string {
	fields := map[string]string{}
	for _, fv := range sc.fixtures.FieldValues(structBase(f.RequestType)) {
		fields[fv[0]] = fv[1]
	}
	out := make([]string, 0, len(call.Args))
	for _, a := range call.Args {
		if field, ok := strings.CutPrefix(a, "request."); ok {
			if v, found := fields[field]; found {
				out = append(out, fmt.Sprintf("%q", v))
				continue
			}
			out = append(out, sc.fixtures.ArgValue(field, "string"))
			continue
		}
		if isGoLiteral(a) {
			out = append(out, a)
			continue
		}
		out = append(out, sc.fixtures.ArgValue(strings.TrimPrefix(a, "&"), "string"))
	}
	return out
}

// isGoLiteral reports whether the rendered arg is a basic literal.
func isGoLiteral(a string) bool {
	if a == "" {
		return false
	}
	switch a[0] {
	case '"', '`', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-':
		return true
	}
	return a == "true" || a == "false" || a == "nil" || strings.HasPrefix(a, "time.")
}

// responseLiteral renders a response-struct literal (one element) from
// fixture values. A response struct unknown to the models inventory (and
// carrying no json fields) degrades to nil — a literal over an unknown type
// would never compile.
func responseLiteral(sc *serviceCtx, responseType string) string {
	base := structBase(responseType)
	fields, known := sc.models.Structs[base]
	if len(fields) == 0 && !known {
		return "nil"
	}
	var rendered []string
	for _, fv := range sc.fixtures.FieldValues(base) {
		rendered = append(rendered, fv[0]+": "+fmt.Sprintf("%q", fv[1]))
	}
	switch {
	case strings.HasPrefix(responseType, "[]*"):
		inner := "{" + strings.Join(rendered, ", ") + "}"
		return responseType + "{" + inner + "}"
	case strings.HasPrefix(responseType, "*"):
		return "&" + responseType[1:] + "{" + strings.Join(rendered, ", ") + "}"
	default:
		return responseType + "{" + strings.Join(rendered, ", ") + "}"
	}
}

// mockReturnLiteral renders the gomock Return payload for a controller's
// store call: the store's own row shape from its db fact (nil when unknown).
func mockReturnLiteral(sc *serviceCtx, method string) string {
	df := sc.dbFacts.DB[method]
	if df == nil {
		return "nil"
	}
	switch df.Shape {
	case "multi":
		return "[]any{" + rowLiteral(sc, "[]*"+df.RowType) + ", nil}"
	case "single":
		return "[]any{" + rowLiteral(sc, "*"+df.RowType) + ", nil}"
	case "scalar":
		return "[]any{" + sc.fixtures.ZeroExpr(df.Scalar) + ", nil}"
	default:
		return "[]any{nil, nil}"
	}
}

// renderCtrlMethod renders one passthrough controller suite method block:
// request fields as case fields, guarded EXPECT with a gomock.Any ctx
// matcher and concrete args, request built from the case fields.
func renderCtrlMethod(u *unit) (string, error) {
	sc := u.sc
	f := u.ctrl
	call := f.StoreCalls[0]
	reqBase := structBase(f.RequestType)
	var reqFields []templates.ReqField
	var caseRefs []string
	for _, fv := range sc.fixtures.FieldValues(reqBase) {
		reqFields = append(reqFields, templates.ReqField{Name: fv[0], Value: fv[1]})
		caseRefs = append(caseRefs, fv[0]+": testCase."+fv[0])
	}
	prov := u.sc.provider()
	return prov.Render(templates.TestControllerMethod, templates.TestControllerMethodData{
		SuiteName:  u.suite,
		StoreVar:   strings.ToLower(sc.name) + "Store",
		CtrlVar:    strings.ToLower(sc.name) + "Controller",
		Name:       f.Name,
		StoreCall:  call.Method,
		StoreArgs:  ctrlExpectArgs(sc, f, call),
		ReqFields:  reqFields,
		ReqExpr:    "models." + reqBase + "{" + strings.Join(caseRefs, ", ") + "}",
		MockReturn: mockReturnLiteral(sc, call.Method),
		ExpectType: f.ResponseType,
		ExpectExpr: responseLiteral(sc, f.ResponseType),
	})
}

// renderHandlerMethod renders one handler suite method block.
func renderHandlerMethod(u *unit) (string, error) {
	sc := u.sc
	f := u.handler
	resp := u.respType
	reqBase := structBase(f.RequestType)
	var reqFields []templates.ReqField
	var caseRefs []string
	for _, fv := range sc.fixtures.FieldValues(reqBase) {
		reqFields = append(reqFields, templates.ReqField{Name: fv[0], Value: fv[1]})
		caseRefs = append(caseRefs, fv[0]+": testCase."+fv[0])
	}
	prov := u.sc.provider()
	return prov.Render(templates.TestHandlerMethod, templates.TestHandlerMethodData{
		SuiteName:    u.suite,
		CtrlMockVar:  strings.ToLower(sc.name) + "Controller",
		HandlerVar:   strings.ToLower(sc.name) + "Handler",
		Name:         f.Name,
		ReqFields:    reqFields,
		ReqInit:      "models." + reqBase + "{" + strings.Join(caseRefs, ", ") + "}",
		SuccessInput: "[]any{" + responseLiteral(sc, resp) + ", nil}",
		RespType:     resp,
	})
}
