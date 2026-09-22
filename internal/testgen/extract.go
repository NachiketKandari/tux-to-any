// Per-function fact extraction (PRD-2026-09-09 GT-D3): gentest reads the
// converted Go code itself — never the conversion IR — so it works on any
// tree, including hand-edited ones. Extraction is parse-only (no type
// checking): shapes are recognized by the reference conventions (store
// receivers, `query := `+backtick literals, sqlx call names, request
// selectors).
package testgen

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Param is one function parameter (name + type as written).
type Param struct {
	Name string
	Type string
}

// dbFact is one store method's extracted shape.
type dbFact struct {
	Name       string
	CtxName    string
	Params     []Param
	Query      string
	Tables     []string // FROM list in source order (DML: target table)
	Shape      string   // multi | single | scalar | dml
	RowType    string   // models.NavDetails (as written)
	Scalar     string   // int64 / string / float64
	Args       []string // call args after the query arg, rendered verbatim
	IsTx       bool     // takes tx *sqlx.Tx (tx-variant: runs on tx, not g.db)
	Recv       string   // sqlx receiver: "g.db" | "tx" (as written)
	ReturnType string   // first non-error result type (tx scalar reads)
}

// storeCall is one dependency call inside a controller body.
type storeCall struct {
	Method string
	Args   []string // rendered verbatim, ctx arg dropped
}

// ctrlFact is one controller endpoint's extracted shape.
type ctrlFact struct {
	Name         string
	CtxName      string
	RequestType  string // models.NavRequest
	ResponseType string
	StoreCalls   []storeCall
	Passthrough  bool   // body returns the store result without field mapping
	Src          string // verbatim function source (LLM prompt input)
}

// handlerFact is one gin handler's extracted shape.
type handlerFact struct {
	Name        string
	RequestType string
	CtrlCall    string
}

// fieldInfo is one struct field of the models layer.
type fieldInfo struct {
	Name string
	Type string
	JSON string
	DB   string
}

// modelsInfo is the service's models inventory (model/ and models/ both).
type modelsInfo struct {
	Structs map[string][]fieldInfo
}

// ctrlIfaceSig is one controller interface method's request/response shape.
type ctrlIfaceSig struct {
	Request  string // models.NavHistoryRequest (pointer stripped)
	Response string // []*models.NavHistoryResponse (first result, as written)
}

// layerFacts is one layer directory's extraction outcome.
type layerFacts struct {
	DB          map[string]*dbFact
	Ctrl        map[string]*ctrlFact
	Handler     map[string]*handlerFact
	DBCtor      string
	DBCtorArgs  int
	CtrlCtor    string
	HandlerCtor string
}

// sourceFiles lists the layer's non-test, non-mock .go files.
func sourceFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" ||
			strings.HasSuffix(name, "_test.go") ||
			strings.HasSuffix(name, "_mock.go") || strings.HasPrefix(name, "mock_") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// testFiles lists the layer's *_test.go files.
func testFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".go" && strings.HasSuffix(e.Name(), "_test.go") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// extractLayer parses one layer directory into facts; unparseable files are
// skipped (the scan already warned).
func extractLayer(dir string, layer string) *layerFacts {
	lf := &layerFacts{DB: map[string]*dbFact{}, Ctrl: map[string]*ctrlFact{}, Handler: map[string]*handlerFact{}}
	fset := token.NewFileSet()
	for _, name := range sourceFiles(dir) {
		af, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			switch {
			case layer == "db":
				if fd.Recv != nil {
					if f := extractDBFact(fd, fset); f != nil {
						lf.DB[f.Name] = f
					}
				} else if strings.HasPrefix(fd.Name.Name, "New") && fd.Type.Results != nil && fd.Type.Results.NumFields() == 1 {
					if lf.DBCtor == "" {
						lf.DBCtor = fd.Name.Name
						lf.DBCtorArgs = fd.Type.Params.NumFields()
					}
				}
			case layer == "controller":
				if fd.Recv != nil {
					if f := extractCtrlFact(fd, fset); f != nil {
						lf.Ctrl[f.Name] = f
					}
				} else if strings.HasPrefix(fd.Name.Name, "New") && fd.Type.Results != nil && fd.Type.Results.NumFields() == 1 && lf.CtrlCtor == "" {
					lf.CtrlCtor = fd.Name.Name
				}
			case layer == "handler":
				if fd.Recv != nil {
					if f := extractHandlerFact(fd, fset); f != nil {
						lf.Handler[f.Name] = f
					}
				} else if strings.HasPrefix(fd.Name.Name, "New") && fd.Type.Results != nil && fd.Type.Results.NumFields() == 1 && lf.HandlerCtor == "" {
					lf.HandlerCtor = fd.Name.Name
				}
			}
		}
	}
	return lf
}

// extractCtrlIface parses the controller layer's interface declarations into
// per-method signatures. Handler tests need the controller's response type;
// the interface declaration is the authoritative fallback (it carries the
// type even when bodies are still the LLM seam), so the handler gate can stay
// deterministic instead of degrading to unsupported.
func extractCtrlIface(serviceDir string) map[string]ctrlIfaceSig {
	out := map[string]ctrlIfaceSig{}
	dir := filepath.Join(serviceDir, "controller")
	fset := token.NewFileSet()
	for _, name := range sourceFiles(dir) {
		af, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				for _, m := range it.Methods.List {
					ft, ok := m.Type.(*ast.FuncType)
					if !ok || len(m.Names) == 0 {
						continue
					}
					sig := ctrlIfaceSig{}
					if ft.Params != nil && ft.Params.NumFields() > 0 {
						last := ft.Params.List[len(ft.Params.List)-1]
						if rt := renderExpr(last.Type, fset); strings.HasPrefix(rt, "*models.") {
							sig.Request = strings.TrimPrefix(rt, "*")
						}
					}
					if ft.Results != nil && ft.Results.NumFields() > 0 {
						sig.Response = renderExpr(ft.Results.List[0].Type, fset)
					}
					if sig.Response != "" {
						out[m.Names[0].Name] = sig
					}
				}
			}
		}
	}
	return out
}

// extractModels parses the service's models dir (model/ or models/).
func extractModels(serviceDir string) *modelsInfo {
	mi := &modelsInfo{Structs: map[string][]fieldInfo{}}
	dir := ""
	for _, cand := range []string{"models", "model"} {
		if fi, err := os.Stat(filepath.Join(serviceDir, cand)); err == nil && fi.IsDir() {
			dir = filepath.Join(serviceDir, cand)
			break
		}
	}
	if dir == "" {
		return mi
	}
	fset := token.NewFileSet()
	for _, name := range sourceFiles(dir) {
		af, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				var fields []fieldInfo
				for _, f := range st.Fields.List {
					info := fieldInfo{Type: renderExpr(f.Type, fset)}
					for _, n := range f.Names {
						info.Name = n.Name
						break
					}
					if f.Tag != nil {
						tag, _ := strconv.Unquote(f.Tag.Value)
						info.JSON = tagValue(tag, "json")
						info.DB = tagValue(tag, "db")
					}
					if info.Name != "" {
						fields = append(fields, info)
					}
				}
				mi.Structs[ts.Name.Name] = fields
			}
		}
	}
	return mi
}

func tagValue(tag, key string) string {
	for _, part := range strings.Split(tag, " ") {
		if strings.HasPrefix(part, key+":") {
			v, _ := strconv.Unquote(strings.TrimPrefix(part, key+":"))
			return v
		}
	}
	return ""
}

// extractDBFact recognizes the store-method conventions: ctx first param, a
// backtick query literal (assigned, var or const), one sqlx call
// (SelectContext/GetContext/ExecContext and their aliases on g.db or tx),
// row/scalar type from the scan target's declaration. Tx-variant methods
// (tx *sqlx.Tx second param, tx.ExecContext / tx.GetContext receiver) set
// IsTx — the db test layer renders them with the Beginx handle instead of
// skipping them as unsupported.
func extractDBFact(fd *ast.FuncDecl, fset *token.FileSet) *dbFact {
	f := &dbFact{Name: fd.Name.Name}
	if fd.Type.Params != nil && fd.Type.Params.NumFields() > 0 {
		if names := fd.Type.Params.List[0].Names; len(names) > 0 {
			f.CtxName = names[0].Name
		}
	}
	for _, p := range fd.Type.Params.List {
		typ := renderExpr(p.Type, fset)
		for _, n := range p.Names {
			f.Params = append(f.Params, Param{Name: n.Name, Type: typ})
			if typ == "*sqlx.Tx" || typ == "sqlx.Tx" || (n.Name == "tx" && strings.Contains(typ, "Tx")) {
				f.IsTx = true
			}
		}
	}
	// ReturnType is the first non-error result (tx scalar reads scan into
	// sql.Null* but return the extracted Go type — e.g. GetMarks scans
	// sql.NullString, returns string).
	if fd.Type.Results != nil {
		for _, r := range fd.Type.Results.List {
			t := renderExpr(r.Type, fset)
			if t == "error" {
				continue
			}
			f.ReturnType = t
			break
		}
	}
	var queryLit string
	var call *ast.CallExpr
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, rhs := range x.Rhs {
				if lit, ok := rhs.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.HasPrefix(lit.Value, "`") {
					q, err := strconv.Unquote(lit.Value)
					if err == nil {
						queryLit = q
					}
				}
			}
		case *ast.GenDecl:
			// `var query = `…`` / `const query = `…`` package or local
			// declarations carry the literal outside an assignment.
			if x.Tok != token.VAR && x.Tok != token.CONST {
				return true
			}
			for _, spec := range x.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.HasPrefix(lit.Value, "`") {
						q, err := strconv.Unquote(lit.Value)
						if err == nil {
							queryLit = q
						}
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if dbCallShape(sel.Sel.Name) != "" {
					call = x
				}
			}
		}
		return true
	})
	if call == nil {
		return nil
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		f.Recv = renderExpr(sel.X, fset)
		if f.Recv == "tx" {
			f.IsTx = true
		}
		f.Shape = dbCallShape(sel.Sel.Name)
	}
	for i, a := range call.Args {
		if i < 2 {
			continue // ctx, scan target
		}
		if lit, ok := a.(*ast.Ident); ok && lit.Name == "query" {
			continue
		}
		f.Args = append(f.Args, renderExpr(a, fset))
	}
	if queryLit != "" {
		f.Query = queryLit
		f.Tables = tablesOf(queryLit)
	}
	if f.Shape == "multi" || f.Shape == "scan" {
		target := ""
		if len(call.Args) > 1 {
			if star, ok := call.Args[1].(*ast.UnaryExpr); ok {
				if id, ok := star.X.(*ast.Ident); ok {
					target = id.Name
				}
			}
		}
		if target == "" {
			// QueryRowx/QueryRow shapes pass the query where the scan
			// target sits on Select/Get — the destination is the Scan arg.
			target = scanVarOf(fd)
		}
		f.RowType, f.Scalar = scanTargetType(fd, target, fset)
		if f.Shape == "scan" {
			if f.Scalar != "" {
				f.Shape = "scalar"
			} else if f.RowType != "" {
				f.Shape = "single"
			}
		}
	}
	return f
}

// dbCallShape maps a sqlx / database-sql call name onto the db block shape
// it renders through: multi (Select), single/scalar (Get/QueryRow, resolved
// by the scan target) and dml (Exec/NamedExec). "" = not a query call.
func dbCallShape(name string) string {
	switch name {
	case "SelectContext", "Select":
		return "multi"
	case "GetContext", "Get", "QueryRowxContext", "QueryRowx", "QueryRowContext", "QueryRow":
		return "scan"
	case "ExecContext", "Exec", "NamedExecContext", "NamedExec":
		return "dml"
	}
	return ""
}

// scanVarOf finds the destination of a row Scan call (`row.Scan(&x)`) — the
// QueryRowx/QueryRow shapes carry no scan-target argument.
func scanVarOf(fd *ast.FuncDecl) string {
	var name string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Scan" {
			return true
		}
		if un, ok := call.Args[0].(*ast.UnaryExpr); ok {
			if id, ok := un.X.(*ast.Ident); ok {
				name = id.Name
			}
		}
		return true
	})
	return name
}

// scanTargetType finds how the scan target variable is declared inside the
// method body: `var x []*models.T`, `var x models.T`, `var x int64`,
// `x := []*models.T{}`, `x := models.T{}`, `x := &models.T{}`, or
// `x := make([]*models.T, 0)`. Unresolvable declarations leave both results
// empty — the build gate skips the method instead of composing a block with
// an empty type name.
func scanTargetType(fd *ast.FuncDecl, varName string, fset *token.FileSet) (rowType, scalar string) {
	if varName == "" {
		return "", ""
	}
	var typ string
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if typ != "" {
			return false
		}
		switch x := n.(type) {
		case *ast.DeclStmt:
			gd, ok := x.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR || len(gd.Specs) == 0 {
				return true
			}
			vs, ok := gd.Specs[0].(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				return true
			}
			for _, name := range vs.Names {
				if name.Name == varName {
					typ = renderExpr(vs.Type, fset)
				}
			}
		case *ast.AssignStmt:
			for i, lhs := range x.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name != varName || i >= len(x.Rhs) {
					continue
				}
				if t := declaredTypeOf(x.Rhs[i], fset); t != "" {
					typ = t
				}
			}
		}
		return true
	})
	switch {
	case strings.HasPrefix(typ, "[]*"):
		if inner := strings.TrimPrefix(typ, "[]*"); structishType(inner) {
			rowType = inner
		} else {
			scalar = typ
		}
	case strings.HasPrefix(typ, "[]"):
		if inner := strings.TrimPrefix(typ, "[]"); structishType(inner) {
			rowType = inner
		} else {
			scalar = typ
		}
	case strings.HasPrefix(typ, "*"):
		if inner := strings.TrimPrefix(typ, "*"); structishType(inner) {
			rowType = inner
		} else {
			scalar = typ
		}
	case structishType(typ):
		rowType = typ
	case typ != "":
		scalar = typ
	}
	return rowType, scalar
}

// structishType reports whether a declared type names a struct (exported or
// package-qualified) rather than a builtin scalar — `models.T`, `DateInfo`
// vs `int64`, `string`, `[]byte`, `sql.NullString`, `time.Time`.
func structishType(t string) bool {
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "sql.") || strings.HasPrefix(t, "time.") {
		return false
	}
	if strings.Contains(t, ".") {
		return true
	}
	r := rune(t[0])
	return r >= 'A' && r <= 'Z'
}

// declaredTypeOf renders the static type of a scan-target initializer:
// composite literals (with or without the address-of) and make() calls.
func declaredTypeOf(e ast.Expr, fset *token.FileSet) string {
	switch x := e.(type) {
	case *ast.CompositeLit:
		return renderExpr(x.Type, fset)
	case *ast.UnaryExpr:
		if t := declaredTypeOf(x.X, fset); t != "" {
			return "*" + t
		}
	case *ast.CallExpr:
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "make" && len(x.Args) > 0 {
			return renderExpr(x.Args[0], fset)
		}
	}
	return ""
}

// tablesOf extracts the FROM table list from a SQL query (source order,
// aliases dropped). DML statements carry no FROM: INSERT INTO / UPDATE /
// DELETE FROM / MERGE INTO yield their single target table so the Exec
// regex still anchors on it. The DML target wins over any FROM inside a
// subquery (MERGE ... USING (SELECT ... FROM DUAL) targets the MERGE table,
// not DUAL).
func tablesOf(query string) []string {
	up := strings.ToUpper(strings.TrimSpace(query))
	for _, verb := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
		if strings.HasPrefix(up, verb) {
			for _, prefix := range []string{"INSERT INTO ", "MERGE INTO ", "DELETE FROM ", "UPDATE "} {
				if idx := strings.Index(up, prefix); idx >= 0 {
					rest := strings.TrimSpace(query[idx+len(prefix):])
					if rest == "" {
						return nil
					}
					// Table is the first token; cut at "(" for paren-glued
					// targets (INSERT INTO T(col,...)) then strip punctuation.
					tok := strings.Fields(rest)[0]
					if i := strings.Index(tok, "("); i >= 0 {
						tok = tok[:i]
					}
					tok = strings.Trim(tok, "(),;")
					if tok != "" {
						return []string{tok}
					}
				}
			}
			return nil
		}
	}
	up2 := strings.ToUpper(query)
	i := strings.Index(up2, " FROM ")
	if i >= 0 {
		rest := query[i+len(" FROM "):]
		end := len(rest)
		for _, kw := range []string{"\nWHERE", " WHERE", "\nORDER", " ORDER", "\nGROUP", " GROUP", "\nJOIN", " JOIN", "\nHAVING", " HAVING"} {
			if j := strings.Index(strings.ToUpper(rest), kw); j >= 0 && j < end {
				end = j
			}
		}
		segment := rest[:end]
		var out []string
		for _, t := range strings.Split(segment, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if sp := strings.Fields(t); len(sp) > 0 {
				t = sp[0]
			}
			out = append(out, t)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// extractCtrlFact recognizes controller endpoints: (ctx context.Context,
// request *models.X) → (…, error), collecting `recv.store.M(...)` calls in
// body order.
func extractCtrlFact(fd *ast.FuncDecl, fset *token.FileSet) *ctrlFact {
	f := &ctrlFact{Name: fd.Name.Name, Passthrough: true}
	if fd.Type.Params == nil || fd.Type.Params.NumFields() < 2 {
		return nil
	}
	first := fd.Type.Params.List[0]
	if renderExpr(first.Type, fset) != "context.Context" {
		return nil
	}
	if len(first.Names) > 0 {
		f.CtxName = first.Names[0].Name
	}
	reqField := fd.Type.Params.List[len(fd.Type.Params.List)-1]
	rt := renderExpr(reqField.Type, fset)
	if !strings.HasPrefix(rt, "*models.") {
		return nil
	}
	f.RequestType = strings.TrimPrefix(rt, "*")
	if fd.Type.Results != nil && fd.Type.Results.NumFields() > 0 {
		f.ResponseType = renderExpr(fd.Type.Results.List[0].Type, fset)
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "store" {
			return true
		}
		sc := storeCall{Method: sel.Sel.Name}
		for i, a := range call.Args {
			if i == 0 {
				continue // ctx
			}
			sc.Args = append(sc.Args, renderExpr(a, fset))
		}
		f.StoreCalls = append(f.StoreCalls, sc)
		return true
	})
	// Field mapping (composite literals over models types or .String
	// projections) is what the deterministic template cannot shape.
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if t := renderExpr(x.Type, fset); strings.Contains(t, "models.") {
				f.Passthrough = false
			}
		case *ast.SelectorExpr:
			if x.Sel.Name == "String" {
				f.Passthrough = false
			}
		}
		return true
	})
	if len(f.StoreCalls) == 0 {
		return nil
	}
	f.Src = renderNode(fd, fset)
	return f
}

// extractHandlerFact recognizes gin handlers: (c *gin.Context), a request
// var decl, and one controller call.
func extractHandlerFact(fd *ast.FuncDecl, fset *token.FileSet) *handlerFact {
	f := &handlerFact{Name: fd.Name.Name}
	if fd.Type.Params == nil || fd.Type.Params.NumFields() != 1 || renderExpr(fd.Type.Params.List[0].Type, fset) != "*gin.Context" {
		return nil
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.DeclStmt:
			if gd, ok := x.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR && len(gd.Specs) > 0 {
				if vs, ok := gd.Specs[0].(*ast.ValueSpec); ok && vs.Type != nil {
					t := renderExpr(vs.Type, fset)
					if strings.HasPrefix(t, "models.") {
						f.RequestType = strings.TrimPrefix(t, "models.")
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "controller" {
					f.CtrlCall = sel.Sel.Name
				}
			}
		}
		return true
	})
	if f.CtrlCall == "" {
		return nil
	}
	return f
}

// renderExpr prints an expression/type back to Go source.
func renderExpr(e ast.Expr, fset *token.FileSet) string {
	if e == nil {
		return ""
	}
	return renderNode(e, fset)
}

// renderNode prints any AST node back to Go source.
func renderNode(n ast.Node, fset *token.FileSet) string {
	if n == nil {
		return ""
	}
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}
