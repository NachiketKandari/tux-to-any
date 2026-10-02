// Per-method block renderers: each turns one unit's extracted facts plus
// fixture values into a single table-driven suite-method block via the
// test templates (PRD-2026-09-09 GT-3). GT-7 threads a method-scoped
// FixtureSource through every renderer: when the run has a parsed log, the
// scoped source supplies real request/response/db values and the first
// failed complete trace becomes a second case.
package testgen

import (
	"encoding/json"

	"fmt"
	"strconv"
	"strings"

	"regexp"

	"tux-to-any/internal/templates"
	"tux-to-any/internal/testscan"
)

// fixtureFor returns the unit's method-scoped fixture source plus its
// method-log view (nil for assumed-only sources).
func (sc *serviceCtx) fixtureFor(layer testscan.Layer, method string) (FixtureSource, MethodLogValues) {
	fx := sc.fixtures
	if fx == nil {
		fx = &AssumedFixtureSource{Models: sc.models}
	}
	if scoped, ok := fx.(ScopedFixtureSource); ok {
		if s := scoped.ForUnit(sc.name, string(layer), method); s != nil {
			fx = s
		}
	}
	if mv, ok := fx.(MethodLogValues); ok {
		return fx, mv
	}
	return fx, nil
}

// fixtureTag renders the per-method provenance tag ("log <short-id>" or
// "assumed") for a run with a log; callers skip the comment when no log ran.
func (sc *serviceCtx) fixtureTag(layer testscan.Layer, method string) string {
	_, mv := sc.fixtureFor(layer, method)
	if mv != nil {
		return mv.Provenance()
	}
	return "assumed"
}

// fieldValuesFor resolves a struct's field values from the scoped source,
// preferring logged response values when the caller asks for a response.
func fieldValuesFor(fx FixtureSource, structName string, response bool) [][2]string {
	if response {
		if rs, ok := fx.(ResponseFieldSource); ok {
			return rs.ResponseFieldValues(structName)
		}
	}
	return fx.FieldValues(structName)
}

// quoted wraps a pre-escaped fixture value in a Go string literal; sources
// return body text escaped for exactly this embedding.
func quoted(v string) string { return `"` + v + `"` }

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
		return `(?i)^` + strings.ToLower(firstWord(f.Query)) + `\\\\s+`
	}
	tables := make([]string, len(f.Tables))
	for i, t := range f.Tables {
		tables[i] = regexp.QuoteMeta(t)
	}
	return `(?i)^select\\\\s+(.+)\\\\s+from\\\\s+` + strings.Join(tables, `\\\\s*,\\\\s*`) + `(\\\\s+where\\\\s+(.+))?$`
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
		return `(?i)^insert\\\\s+into\\\\s+` + table + `(\\\\s+.+)?$`
	case strings.HasPrefix(up, "UPDATE"):
		return `(?i)^update\\\\s+` + table + `(\\\\s+set\\\\s+(.+))?$`
	case strings.HasPrefix(up, "DELETE"):
		return `(?i)^delete\\\\s+from\\\\s+` + table + `(\\\\s+where\\\\s+(.+))?$`
	case strings.HasPrefix(up, "MERGE"):
		return `(?i)^merge\\\\s+into\\\\s+` + table + `(\\\\s+.+)?$`
	case f.Query == "":
		return `(?i)^`
	default:
		return `(?i)^` + strings.ToLower(firstWord(f.Query)) + `\\\\s+(.+)$`
	}
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return regexp.QuoteMeta(f[0])
	}
	return `(.+)`
}

// toleratesNoRows reports whether a DML method treats zero rows as success.
//
// The answer comes from the body and nowhere else. The tool used to decide this
// from the SQL verb — "a DELETE-tx discards its result" — which the corpus
// contradicts twice over: riskprofile's DeleteQuestion IS a DELETE-tx that
// checks RowsAffected and returns errors.New("unable to delete the question"),
// and its zero-rows cases then failed with "Received unexpected error".
//
// A method whose body states no zero-rows error has no code path that turns
// zero rows into one, so the case asserts success. Asserting the sentinel for
// it instead would be inventing an error the method cannot produce — the same
// mistake F2 was written to stop, one level up. Every DML method in the corpus
// that DOES check RowsAffected also returns a domain error, so this rule loses
// no coverage; it only stops claiming errors that are not there.
func toleratesNoRows(f *dbFact) bool {
	return f.NoRowsError == ""
}

// dmlNoRowsError is the error a DML method's zero-rows case asserts. It is
// always the method's own text: there is no fallback, because a method that
// states no zero-rows error has no way to produce one, and asserting the
// sentinel anyway is the invented expectation F2 exists to eliminate.
// toleratesNoRows is what distinguishes the two template branches.
func dmlNoRowsError(f *dbFact) string {
	return f.NoRowsError
}

// dbExpectType renders the expectedOutput Go type. Scalar reads that scan
// into a sql.Null* return the extracted Go type (GetMarks: NullString scan,
// string return; QuestionNumberExists: NullInt16 scan, bool return) — the
// return type is what the method's callers see, and the human reference
// suite asserts exactly that.
func dbExpectType(f *dbFact) string {
	switch f.Shape {
	case "multi":
		return "[]*" + f.RowType
	case "single":
		return "*" + f.RowType
	default:
		if strings.HasPrefix(f.Scalar, "sql.Null") && f.ReturnType != "" {
			return f.ReturnType
		}
		return f.Scalar
	}
}

// scalarRowValue renders the assumed Success mock-row literal for a scalar
// scan: a value the scan type absorbs and that pairs with the zero of the
// resolved return type (NullString → "", NullInt* → 0, NullBool → false).
func scalarRowValue(scan string) string {
	switch {
	case strings.Contains(scan, "NullString"), scan == "string":
		return ""
	case scan == "bool", strings.Contains(scan, "NullBool"):
		return "false"
	default:
		return "0"
	}
}

// rowLiteralFrom renders a row-struct composite literal with one element.
// raw (field name → logged value) overrides per-field; nil keeps the
// assumed fixture values. rows values are already escaped body text.
func rowLiteralFrom(sc *serviceCtx, rowType string, raw map[string]json.RawMessage) string {
	base := structBase(rowType)
	var fields []string
	for _, fl := range sc.models.Structs[base] {
		if fl.DB == "" {
			continue
		}
		lit := ""
		if raw != nil {
			r, ok := raw[fl.Name]
			if !ok {
				r, ok = raw[fl.DB]
			}
			if ok {
				lit = colExprFromRaw(fl.Type, r)
			}
		}
		if lit == "" {
			lit = sc.fixtures.ColExpr(fl.DB, fl.Type)
		}
		fields = append(fields, fl.Name+": "+lit)
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

// rowLiteral renders a row-struct composite literal from the assumed
// fixture source (kept for callers without a scoped source).
func rowLiteral(sc *serviceCtx, rowType string) string {
	return rowLiteralFrom(sc, rowType, nil)
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
		return sc.fixtures.ZeroExpr(dbExpectType(f))
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

// firstValuedCall returns the first db call of the method carrying a result
// payload (method-entry debug lines carry no value and are skipped).
func firstValuedCall(t *LogTrace, method string) (LogDBCall, bool) {
	for _, c := range t.DBCalls(method) {
		if c.HasVal {
			return c, true
		}
	}
	return LogDBCall{}, false
}

// renderDBMethod renders one db suite method block: SELECT shapes through
// the query contract, DML shapes (plain + tx-variant) through the Exec
// contract, tx shapes with the Beginx handle. With a log, row 1 takes the
// first successful complete trace's values and the first failed complete
// trace appends a second "Logged#2" row.
func renderDBMethod(u *unit) (string, error) {
	sc := u.sc
	f := u.db
	fx, mv := sc.fixtureFor(u.layer, f.Name)
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
			// The two template branches are chosen by the same fact: a body
			// that states a zero-rows error asserts it, and a body that states
			// none asserts success because it has no path to produce one.
			ToleratesNoRows: toleratesNoRows(f),
			NoRowsError:     dmlNoRowsError(f),
		})
	}
	cols := dbCols(sc, f)
	row := fx.RowValues(cols)
	if f.RowType == "" {
		row = []string{scalarRowValue(f.Scalar)}
	}
	expectExpr := dbExpectExpr(sc, f)
	if mv != nil {
		if call, ok := firstValuedCall(mv.SuccessTrace(), f.Name); ok {
			if f.RowType != "" {
				if r, ok := loggedDBRow(sc, f.RowType, call); ok {
					row = r
				}
			} else if s, ok := loggedScalarRow(call); ok {
				row = []string{s}
			}
			if e, ok := loggedDBExpect(sc, f, call); ok {
				expectExpr = e
			}
		}
	}
	// The no-rows case's expected error is read from the method's own contract,
	// not assumed. Every converted GetContext read propagates err unchanged,
	// so a read that propagates must expect sql.ErrNoRows here; only a read
	// that swallows the error (return &T{}, nil, or an explicit
	// errors.Is(err, sql.ErrNoRows) branch) expects a nil error. Assuming
	// tolerance made this case fail against the very code it was generated
	// from. Multi-row SelectContext reads need no case (an empty slice is a
	// success), and tx reads carry every error to the flow, so neither takes
	// one.
	noRows := (f.Shape == "scalar" || f.Shape == "single") && !f.IsTx
	noRowsExpr := fx.ZeroExpr(dbExpectType(f))
	if f.Shape == "single" && f.RowType != "" {
		noRowsExpr = "&" + f.RowType + "{}"
	}
	noRowsErr := ""
	if f.NoRowsContract == "propagate" {
		noRowsErr = "sql: no rows in result set"
	}
	data := templates.TestDBMethodData{
		SuiteName:   u.suite,
		StoreVar:    strings.ToLower(sc.name) + "Store",
		Name:        f.Name,
		Query:       f.Query,
		Regex:       dbRegex(f),
		Shape:       f.Shape,
		Cols:        cols,
		Row:         row,
		NoRows:      noRows,
		NoRowsExpr:  noRowsExpr,
		NoRowsError: noRowsErr,
		ExpectType:  dbExpectType(f),
		ExpectExpr:  expectExpr,
		CallArgs:    dbCallArgs(sc, f),
		IsDML:       false,
		IsTx:        f.IsTx,
	}
	// First failed complete trace → second logged row (SELECT shapes only;
	// a DML failure has no result row to mock).
	if mv != nil {
		if call, ok := firstValuedCall(mv.FailedTrace(), f.Name); ok {
			alt := templates.TestDBMethodData{}
			if f.RowType != "" {
				if r, ok := loggedDBRow(sc, f.RowType, call); ok {
					alt.Row = r
				}
			} else if s, ok := loggedScalarRow(call); ok {
				alt.Row = []string{s}
			}
			if e, ok := loggedDBExpect(sc, f, call); ok {
				alt.ExpectExpr = e
			}
			if len(alt.Row) > 0 && alt.ExpectExpr != "" {
				data.HasAlt = true
				data.AltDesc = "Logged#2"
				data.AltRow = alt.Row
				data.AltExpect = alt.ExpectExpr
			}
		}
	}
	prov := u.sc.provider()
	return prov.Render(templates.TestDBMethod, data)
}

// ctrlExpectArgs renders the EXPECT args for a controller's store call as
// concrete literals: request-field selectors resolve to the same fixture
// value the case struct carries, plain literals pass through verbatim, and
// anything else (tx handles, values from earlier call results) becomes
// gomock.Any() — a matcher that is true for every value, never a guessed
// literal that would fail the mock. Fields in ambiguous (the failed trace
// carries a different request value) also degrade to gomock.Any(), since one
// EXPECT list serves every case.
func ctrlExpectArgs(sc *serviceCtx, fx FixtureSource, f *ctrlFact, call storeCall, ambiguous map[string]bool) []string {
	fields := map[string]string{}
	for _, fv := range fieldValuesFor(fx, structBase(f.RequestType), false) {
		fields[fv[0]] = fv[1]
	}
	out := make([]string, 0, len(call.Args))
	for _, a := range call.Args {
		if field, ok := strings.CutPrefix(a, "request."); ok {
			if ambiguous[field] {
				out = append(out, "gomock.Any()")
				continue
			}
			if v, found := fields[field]; found {
				out = append(out, quoted(v))
				continue
			}
			out = append(out, sc.fixtures.ArgValue(field, "string"))
			continue
		}
		if isGoLiteral(a) {
			out = append(out, a)
			continue
		}
		out = append(out, "gomock.Any()")
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

// responseLiteral renders a response literal (one element) from the scoped
// fixture values. A response struct unknown to the models inventory degrades
// to nil — a literal over an unknown type would never compile — while a
// known scalar response (string/bool/…) renders its zero or logged value.
func responseLiteral(sc *serviceCtx, fx FixtureSource, responseType string) string {
	base := structBase(responseType)
	fields, known := sc.models.Structs[base]
	if len(fields) == 0 && !known {
		if isScalarType(responseType) {
			if mv, ok := fx.(MethodLogValues); ok {
				if lit := loggedScalarResponseLiteral(responseType, mv.ResponseRaw()); lit != "" {
					return lit
				}
			}
			return fx.ZeroExpr(responseType)
		}
		return "nil"
	}
	var rendered []string
	for _, fv := range fieldValuesFor(fx, base, true) {
		rendered = append(rendered, fv[0]+": "+quoted(fv[1]))
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

// zeroExpectLiteral renders the zero expectation for a response type.
func zeroExpectLiteral(fx FixtureSource, responseType string) string {
	if isScalarType(responseType) {
		return fx.ZeroExpr(responseType)
	}
	return "nil"
}

// mockReturnLiteral renders the gomock Return payload for a controller's
// store call from the assumed fixtures: the store's own row shape from its
// db fact (nil when unknown).
func mockReturnLiteral(sc *serviceCtx, method string) string {
	df := sc.dbFacts.DB[method]
	if df == nil {
		return "nil"
	}
	// A method whose only result is `error` takes Return(nil), not
	// Return(nil, nil). The row-shaped payloads below all carry two elements
	// (value, error) because every read shape returns both, and that assumption
	// is wrong for a DML method — riskprofile's EditMarks is
	// `EditMarks(ctx, …) error`, and the suite died on "wrong number of
	// arguments to Return for MockRiskProfileStore.EditMarks: got 2, want 1".
	if sig, ok := sc.dbIface[method]; ok && sig.Results == 1 && sig.Result == "error" {
		return "[]any{nil}"
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

// ctrlCaseInputs consumes the trace's per-method db occurrences in call
// order and renders one Return payload per store-call occurrence. A "nil"
// slot means the call never ran in that trace, so its EXPECT is skipped —
// mocking it would fail the suite's Finish check.
func ctrlCaseInputs(sc *serviceCtx, calls []storeCall, trace *LogTrace) []string {
	next := map[string]int{}
	out := make([]string, len(calls))
	for i, call := range calls {
		k := next[call.Method]
		next[call.Method] = k + 1
		list := trace.DBCalls(call.Method)
		if k >= len(list) {
			out[i] = "nil"
			continue
		}
		dc := list[k]
		if lit := loggedReturn(sc, call.Method, dc); lit != "" {
			out[i] = lit
			continue
		}
		// Executed but value-less (DML/void call): return nil, nil.
		out[i] = "[]any{nil, nil}"
	}
	return out
}

// renderCtrlMethod renders one controller suite method block. Passthrough
// single-call controllers keep the GT-2 shape; GT-7 field-mapping shapes
// render deterministically once the log carries traces for their store
// calls — request fields, mock returns, the expected response, and the
// logged business-error case all come from the selected traces.
func renderCtrlMethod(u *unit) (string, error) {
	sc := u.sc
	f := u.ctrl
	fx, mv := sc.fixtureFor(u.layer, f.Name)
	reqBase := structBase(f.RequestType)
	reqValues := fieldValuesFor(fx, reqBase, false)
	reqNames := make([]string, 0, len(reqValues))
	var reqFields []templates.ReqField
	successFields := make([]templates.CtrlCaseField, 0, len(reqValues))
	for _, fv := range reqValues {
		reqNames = append(reqNames, fv[0])
		typ, lit := fx.FieldLit(reqBase, fv[0])
		reqFields = append(reqFields, templates.ReqField{Name: fv[0], Value: fv[1], Type: typ, Literal: lit})
		successFields = append(successFields, templates.CtrlCaseField{Name: fv[0], Value: fv[1], Type: typ, Literal: lit})
	}

	// Request fields the failed trace changes must not be pinned as EXPECT
	// literals — one EXPECT list serves every case.
	ambiguous := map[string]bool{}
	if mv != nil && mv.FailedTrace() != nil {
		if fr, ok := mv.(interface{ FailedRequestValues(string) [][2]string }); ok {
			failedVals := map[string]string{}
			for _, fv := range fr.FailedRequestValues(reqBase) {
				failedVals[fv[0]] = fv[1]
			}
			for _, fv := range reqValues {
				if v, ok := failedVals[fv[0]]; ok && v != fv[1] {
					ambiguous[fv[0]] = true
				}
			}
		}
	}

	calls := make([]templates.CtrlCall, 0, len(f.StoreCalls))
	for i, call := range f.StoreCalls {
		field := "mockInput"
		if i > 0 {
			field = fmt.Sprintf("mockInput%d", i+1)
		}
		sig := sc.dbIface[call.Method]
		calls = append(calls, templates.CtrlCall{
			Method: call.Method,
			Args:   ctrlExpectArgs(sc, fx, f, call, ambiguous),
			Field:  field,
			// Unknown arity (-1) reads as TakesCtx, preserving the matcher
			// for a method whose declaration could not be read. Only a
			// declaration that positively says "no parameters" drops it.
			TakesCtx:      call.ArgCount != 0,
			ReturnsHandle: sig.Result == "*sqlx.DB" || sig.Result == "sqlx.DB",
		})
	}

	// Success inputs: logged occurrences when the method has a trace, the
	// assumed row literal otherwise.
	var successInputs []string
	if mv != nil && mv.SuccessTrace() != nil {
		successInputs = ctrlCaseInputs(sc, f.StoreCalls, mv.SuccessTrace())
	} else {
		successInputs = make([]string, len(f.StoreCalls))
		for i, call := range f.StoreCalls {
			successInputs[i] = mockReturnLiteral(sc, call.Method)
		}
	}
	expectExpr := responseLiteral(sc, fx, f.ResponseType)

	storeErrInputs := make([]string, len(calls))
	for i := range storeErrInputs {
		storeErrInputs[i] = "nil"
	}
	if len(storeErrInputs) > 0 {
		storeErrInputs[0] = `[]any{nil, errors.New("store error")}`
	}
	cases := []templates.CtrlCase{
		{
			Desc:           "StoreError",
			ReqFields:      successFields,
			Inputs:         storeErrInputs,
			ExpectedError:  "store error",
			ExpectedOutput: zeroExpectLiteral(fx, f.ResponseType),
		},
		{
			Desc:           "Success",
			ReqFields:      successFields,
			Inputs:         successInputs,
			ExpectedOutput: expectExpr,
		},
	}

	// First failed complete trace: request values, executed-call returns and
	// the ERROR message become the logged error case.
	if mv != nil && mv.FailedTrace() != nil {
		if msg := mv.ErrorMsg(); msg != "" {
			failFields := failedRequestFields(sc, mv, reqBase, reqNames, successFields)
			cases = append(cases, templates.CtrlCase{
				Desc:           "Logged-Error",
				ReqFields:      failFields,
				Inputs:         ctrlCaseInputs(sc, f.StoreCalls, mv.FailedTrace()),
				ExpectedError:  msg,
				ExpectedOutput: zeroExpectLiteral(fx, f.ResponseType),
			})
		}
	}

	prov := u.sc.provider()
	return prov.Render(templates.TestControllerMethod, templates.TestControllerMethodData{
		SuiteName: u.suite,
		StoreVar:  strings.ToLower(sc.name) + "Store",
		CtrlVar:   strings.ToLower(sc.name) + "Controller",
		Name:      f.Name,
		StoreCall: calls[0].Method,
		StoreArgs: calls[0].Args,
		ReqFields: reqFields,
		ReqExpr:   "models." + reqBase + "{" + caseRefs(reqValues) + "}",
		// An empty RequestType means the method takes no request. Rendering
		// the expression anyway yields `request := &{}`, which does not
		// parse — and a parse failure costs the entire controller suite, not
		// just this method.
		NoRequest:  f.RequestType == "",
		MockReturn: successInputs[0],
		ExpectType: f.ResponseType,
		ExpectExpr: expectExpr,
		Calls:      calls,
		Cases:      cases,
	})
}

// failedRequestFields renders the failure case's request values from the
// failed trace, falling back per field to the success case's value.
func failedRequestFields(sc *serviceCtx, mv MethodLogValues, reqBase string, names []string, fallback []templates.CtrlCaseField) []templates.CtrlCaseField {
	values := map[string]string{}
	for _, fv := range fieldValuesFor(mv, reqBase, false) {
		values[fv[0]] = fv[1]
	}
	if fr, ok := mv.(interface {
		FailedRequestValues(string) [][2]string
	}); ok {
		for _, fv := range fr.FailedRequestValues(reqBase) {
			values[fv[0]] = fv[1]
		}
	}
	out := make([]templates.CtrlCaseField, 0, len(names))
	for i, name := range names {
		// The declared type and literal carry over from the success case: the
		// log supplies a different VALUE for a failed request, not a different
		// type. A logged value for a slice field is already a slice literal by
		// the time it reaches here (FieldLit), and dropping it would put a
		// bare string back into a []string field.
		typ, lit := "", ""
		if i < len(fallback) {
			typ, lit = fallback[i].Type, fallback[i].Literal
		}
		v, ok := values[name]
		if !ok {
			v = fallback[i].Value
		}
		out = append(out, templates.CtrlCaseField{Name: name, Value: v, Type: typ, Literal: lit})
	}
	return out
}

// caseRefs renders the request-building pairs (CompCode: testCase.CompCode)
// from the success case's values.
func caseRefs(values [][2]string) string {
	refs := make([]string, 0, len(values))
	for _, fv := range values {
		refs = append(refs, fv[0]+": testCase."+fv[0])
	}
	return strings.Join(refs, ", ")
}

// renderHandlerMethod renders one handler suite method block: gin-context
// table cases (Error 500 / Failure 204 / Success 200) over the mocked
// controller, plus the logged-error case from the first failed trace.
func renderHandlerMethod(u *unit) (string, error) {
	sc := u.sc
	f := u.handler
	resp := u.respType
	fx, mv := sc.fixtureFor(u.layer, f.Name)
	reqBase := structBase(f.RequestType)
	reqValues := fieldValuesFor(fx, reqBase, false)
	var reqFields []templates.ReqField
	successFields := make([]templates.CtrlCaseField, 0, len(reqValues))
	for _, fv := range reqValues {
		typ, lit := fx.FieldLit(reqBase, fv[0])
		reqFields = append(reqFields, templates.ReqField{Name: fv[0], Value: fv[1], Type: typ, Literal: lit})
		successFields = append(successFields, templates.CtrlCaseField{Name: fv[0], Value: fv[1], Type: typ, Literal: lit})
	}
	expectExpr := responseLiteral(sc, fx, resp)
	cases := []templates.HandlerCase{
		{
			Desc:          f.Name + "Error",
			ReqFields:     successFields,
			Input:         `[]any{nil, errors.New("error while fetching data")}`,
			ExpectedError: "error while fetching data",
			HTTPCode:      "http.StatusInternalServerError",
		},
		{
			Desc:          "Failure",
			ReqFields:     successFields,
			Input:         "[]any{nil, nil}",
			ExpectedError: "No Data Found",
			HTTPCode:      "http.StatusNoContent",
		},
		{
			Desc:      "Success",
			ReqFields: successFields,
			Input:     "[]any{" + expectExpr + ", nil}",
		},
	}
	if mv != nil && mv.FailedTrace() != nil {
		if text, code := failureText(mv); text != "" {
			names := make([]string, 0, len(reqValues))
			fallback := make([]templates.CtrlCaseField, 0, len(reqValues))
			for _, fv := range successFields {
				names = append(names, fv.Name)
				fallback = append(fallback, fv)
			}
			cases = append(cases, templates.HandlerCase{
				Desc:          "Logged-Error",
				ReqFields:     failedRequestFields(sc, mv, reqBase, names, fallback),
				Input:         `[]any{nil, errors.New(` + strconv.Quote(text) + `)}`,
				ExpectedError: text,
				HTTPCode:      strconv.Itoa(code),
			})
		}
	}
	prov := u.sc.provider()
	return prov.Render(templates.TestHandlerMethod, templates.TestHandlerMethodData{
		SuiteName:   u.suite,
		CtrlMockVar: strings.ToLower(sc.name) + "Controller",
		HandlerVar:  strings.ToLower(sc.name) + "Handler",
		Name:        f.Name,
		// The EXPECT() names the controller method, which the handler's own
		// name only coincidentally matches; the invocation below stays on
		// Name because that one genuinely is the handler's method.
		CtrlMethod:   f.CtrlCall,
		CtrlCallArgs: f.CtrlCallArgs,
		ReqFields:    reqFields,
		ReqInit:      "models." + reqBase + "{" + caseRefs(reqValues) + "}",
		SuccessInput: "[]any{" + expectExpr + ", nil}",
		RespType:     resp,
		Cases:        cases,
	})
}

// failureText returns the handler-visible failure text and HTTP status from
// the method's failed trace: the response body's error description when the
// app serialized one, else the logged ERROR message; status defaults to 500.
func failureText(mv MethodLogValues) (string, int) {
	t := mv.FailedTrace()
	if t == nil {
		return "", 0
	}
	code := t.Status
	if code == 0 || code < 400 {
		code = 500
	}
	if body := strings.TrimSpace(t.ResponseBody); body != "" && body[0] == '{' {
		var m struct {
			Error struct {
				Description string `json:"description"`
			} `json:"error"`
			Msg string `json:"FML_ERROR_MSG"`
		}
		if err := json.Unmarshal([]byte(body), &m); err == nil {
			if m.Error.Description != "" {
				return m.Error.Description, code
			}
			if m.Msg != "" {
				return m.Msg, code
			}
		}
	}
	if msg := mv.ErrorMsg(); msg != "" {
		return msg, code
	}
	return "", 0
}
