// Deterministic no-llm controller and fn-helper synthesis.
//
// When the run is deterministic-only (--no-llm / run.llm: false), the
// pipeline used to skip every controller and fn-helper unit: no file was
// written and a later LLM-enabled resume filled the gap. That left --no-llm
// trees without compilable controllers at all.
//
// This file closes that gap with a best-effort deterministic synthesizer:
// every endpoint renders a Tier-A-clean method body from facts the plan and
// IR already carry — store calls in legacy order with captured results and
// error checks, row→response shaping from the FML contract, and the
// ExecTransaction wrapper when the unit's calls take tx. Anything the facts
// cannot name (unmapped bind args, unresolved helpers, tpcall sites, scalar
// placement) rides an explicit `tuxgo:TODO` comment — never a guess.
//
// The bodies carry a `tuxgo:deterministic-*` marker comment so an
// LLM-enabled resume can tell best-effort methods apart from accepted LLM
// output and upgrade exactly those. Synthesis is pure: identical inputs
// produce byte-identical bodies.
package gen

import (
	"fmt"
	"sort"
	"strings"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/common"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/templates"
)

// Deterministic markers. The endpoint/Go name rides the marker line so the
// resume probe is exact (a prefix match on "Nav" must not fire on "NavList"
// — callers match with a trailing space).
const (
	DeterministicControllerMark = "tuxgo:deterministic-controller"
	DeterministicFnHelperMark   = "tuxgo:deterministic-fnhelper"
)

// IsDeterministicControllerMethod reports whether src carries a
// deterministic no-llm body for endpoint name.
func IsDeterministicControllerMethod(src, name string) bool {
	return strings.Contains(src, "// "+DeterministicControllerMark+" "+name+" ")
}

// IsDeterministicFnHelper reports whether src carries a deterministic
// no-llm method for the fn helper's Go name.
func IsDeterministicFnHelper(src, goName string) bool {
	return strings.Contains(src, "// "+DeterministicFnHelperMark+" "+goName+" ")
}

// detCall is one ordered store call with its resolved query view: the canon
// query (duplicate-resolved), the call expression, the store signature
// params, and the capture contract the body emits.
type detCall struct {
	queryID   string // orig ID — the calls-map key
	canonKey  string // duplicate-resolved ID for pins/rows
	line      int    // absolute source line (legacy order)
	query     *ir.Query
	call      budget.DBCall
	params    []templates.ParamSpec
	method    string
	tx        bool
	shape     string // "error" | "single" | "scalar" | "rows"
	capture   string
	rowName   string // models row type (reads only)
	rowByHost map[string]string // normalized host → row Go field
	rowFields []templates.FieldSpec
}

// detHelper is one same-file helper call the body needs.
type detHelper struct {
	line   int // absolute source line of first occurrence (legacy order)
	goName string
	args   []string
	retInt bool
	cap    string
}

// detFieldMap is one response-field assignment from a row field.
type detFieldMap struct {
	Resp string // response Go field
	Row  string // row Go field
}

// detProducer is one earlier read a later call's string arg can reference.
type detProducer struct {
	capture string // getItemHistoryRows
	field   string // DemoCompCd (sql.NullString — read via .String)
}

// DeterministicControllerBody synthesizes one endpoint's controller body
// (bare statements for the controller-method template) with zero LLM calls.
func (s *Service) DeterministicControllerBody(endpoint string, p *plan.Plan) (string, error) {
	e := s.endpointOf(endpoint)
	if e == nil {
		return "", fmt.Errorf("gen: no endpoint %s", endpoint)
	}
	c := s.conditionOf(*e)
	if c == nil {
		return "", fmt.Errorf("gen: no condition for endpoint %s", endpoint)
	}
	queries, calls, err := s.BranchCalls(c, p)
	if err != nil {
		return "", err
	}
	ordered, err := s.detOrderedCalls(queries, calls)
	if err != nil {
		return "", err
	}
	reqFields := s.requestFields(*e, c)
	respFields := contractFields(c.FmlOps, ir.FmlAdd)
	adds := detAdds(c)
	respType := s.responseType(endpoint)
	byName := detFieldSet(reqFields)
	reqMap := detRequestMap(s, c)
	branchSrc := detBranchSource(s.source, c.StartLine, c.EndLine)

	// Resolve every call's argument expressions in legacy order so later
	// calls can reference earlier reads: request fields by host provenance
	// or name, earlier row results for string params, zero literals
	// otherwise (with a TODO trail for the LLM upgrade).
	argTODOs := map[string][]string{}
	producers := map[string]detProducer{}
	for _, dc := range ordered {
		dc.call.Args = detCallArgs(dc, reqMap, byName, producers, argTODOs)
		detRegisterProducer(producers, dc)
	}
	helpers := detHelperCalls(p, branchSrc, c.StartLine, reqFields, argTODOs)
	stubTODOs := detStubTODOs(p, branchSrc)
	tpTODOs := detTpTODOs(p, c)

	var sb strings.Builder
	fmt.Fprintf(&sb, "// %s %s — no-llm best-effort body (store calls + row→response mapping); an LLM-enabled resume upgrades it\n",
		DeterministicControllerMark, endpoint)
	fmt.Fprintf(&sb, "data = make([]*models.%s, 0)\n", respType)
	if len(ordered) == 0 {
		sb.WriteString("// tuxgo:TODO no store calls resolved — legacy logic needs the LLM seam\n")
	}
	for _, t := range stubTODOs {
		sb.WriteString(t + "\n")
	}
	for _, t := range tpTODOs {
		sb.WriteString(t + "\n")
	}
	for _, dc := range ordered {
		for _, t := range argTODOs[dc.method] {
			sb.WriteString(t + "\n")
		}
	}
	for _, h := range helpers {
		for _, t := range argTODOs["helper:"+h.goName] {
			sb.WriteString(t + "\n")
		}
	}

	inner := &strings.Builder{}
	detEmitControllerEvents(inner, s, ordered, helpers, adds, respFields, respType)

	if !detHasTx(ordered) {
		sb.WriteString(strings.TrimRight(inner.String(), "\n") + "\n")
		sb.WriteString("return data, nil")
		return sb.String(), nil
	}
	sb.WriteString("err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {\n")
	for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
		sb.WriteString(detClosureLine(line) + "\n")
	}
	sb.WriteString("return nil\n})")
	sb.WriteString("\nif err != nil {\n\treturn nil, err\n}\nreturn data, nil")
	return sb.String(), nil
}

// DeterministicFnHelperBody synthesizes one fn helper's complete Go method
// (declaration + body) with zero LLM calls. The signature matches the
// prescribed helper contract so callers generated against it keep compiling.
func (s *Service) DeterministicFnHelperBody(goName string, p *plan.Plan) (string, error) {
	var h *plan.FnHelper
	for i := range p.FnHelpers {
		if p.FnHelpers[i].GoName == goName {
			h = &p.FnHelpers[i]
			break
		}
	}
	if h == nil {
		return "", fmt.Errorf("gen: no fn helper %s", goName)
	}
	structName := common.LowerFirst(s.Mapping.Service) + "Controller"
	ids := helperQueryIDs(p, goName)
	calls, err := s.StoreCallsFor(ids, p)
	if err != nil {
		return "", err
	}
	ordered, err := s.detFnCalls(ids, calls)
	if err != nil {
		return "", err
	}
	fnSrc := detBranchSource(s.source, h.StartLine, h.EndLine)
	argTODOs := map[string][]string{}
	producers := map[string]detProducer{}
	for _, dc := range ordered {
		dc.call.Args = detFnCallArgs(dc, h.Params, producers, argTODOs)
		detRegisterProducer(producers, dc)
	}
	nested := detNestedHelperCalls(p, fnSrc, h, argTODOs)
	stubTODOs := detStubTODOs(p, fnSrc)

	decl := "func (s *" + structName + ") " + h.GoName + "(c context.Context"
	for _, pr := range h.Params {
		decl += ", " + pr.Name + " " + pr.Type
	}
	decl += ")"
	if h.Return != "" {
		decl += " " + h.Return
	}
	voidRet := h.Return == ""
	outs := outParams(h.Params)

	var sb strings.Builder
	sb.WriteString(decl + " {\n")
	fmt.Fprintf(&sb, "// %s %s — no-llm best-effort body; an LLM-enabled resume upgrades it\n",
		DeterministicFnHelperMark, goName)
	for _, t := range stubTODOs {
		sb.WriteString(t + "\n")
	}
	for _, dc := range ordered {
		for _, t := range argTODOs[dc.method] {
			sb.WriteString(t + "\n")
		}
	}
	for _, nh := range nested {
		for _, t := range argTODOs["helper:"+nh.goName] {
			sb.WriteString(t + "\n")
		}
	}

	inner := &strings.Builder{}
	detEmitFnEvents(inner, ordered, nested, outs, voidRet, false)
	body := strings.TrimRight(inner.String(), "\n")

	if !detHasTx(ordered) {
		sb.WriteString(body + "\n")
		sb.WriteString(detFnTail(fnSrc, h) + "\n}")
		return sb.String(), nil
	}
	sb.WriteString("err := utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {\n")
	closure := &strings.Builder{}
	detEmitFnEvents(closure, ordered, nested, outs, voidRet, true)
	for _, line := range strings.Split(strings.TrimRight(closure.String(), "\n"), "\n") {
		sb.WriteString(detClosureLineFn(line) + "\n")
	}
	sb.WriteString("return nil\n})")
	sb.WriteString("\nif err != nil {\n")
	sb.WriteString(detFnErrTail(h))
	sb.WriteString("\n}\n" + detFnTail(fnSrc, h) + "\n}")
	return sb.String(), nil
}

// helperQueryIDs returns the plan unit's query IDs for a fn helper Go name.
func helperQueryIDs(p *plan.Plan, goName string) []string {
	for _, u := range p.Units {
		if u.Kind == plan.KindFnHelper && u.Name == goName {
			return u.QueryIDs
		}
	}
	return nil
}

// detOrderedCalls resolves every branch query into legacy-ordered detCalls
// with capture names assigned.
func (s *Service) detOrderedCalls(queries []*ir.Query, calls map[string]budget.DBCall) ([]*detCall, error) {
	var out []*detCall
	for _, q := range queries {
		call, ok := calls[q.ID]
		if !ok {
			continue
		}
		dc, err := s.detResolveCall(q.ID, call)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	used := map[string]int{}
	for _, dc := range out {
		dc.capture = detCaptureName(dc.method, dc.shape, used)
	}
	return out, nil
}

// detFnCalls resolves a fn helper's query IDs into legacy-ordered detCalls.
func (s *Service) detFnCalls(ids []string, calls map[string]budget.DBCall) ([]*detCall, error) {
	var out []*detCall
	for _, id := range ids {
		call, ok := calls[id]
		if !ok {
			continue
		}
		dc, err := s.detResolveCall(id, call)
		if err != nil {
			return nil, err
		}
		out = append(out, dc)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	used := map[string]int{}
	for _, dc := range out {
		dc.capture = detCaptureName(dc.method, dc.shape, used)
	}
	return out, nil
}

// detResolveCall builds one detCall: canon query, store params, result
// shape, and row contract for reads.
func (s *Service) detResolveCall(origID string, call budget.DBCall) (*detCall, error) {
	orig := s.Query(origID)
	canon, canonKey := orig, origID
	if orig != nil && orig.DuplicateOf != "" {
		if alias := canonicalAliasID(origID, orig); s.Query(alias) != nil {
			canon, canonKey = s.Query(alias), alias
		} else if cq := s.Query(orig.DuplicateOf); cq != nil {
			canon, canonKey = cq, orig.DuplicateOf
		}
	}
	method := call.Name
	dc := &detCall{queryID: origID, canonKey: canonKey, query: canon, call: call, method: method, tx: call.Tx != ""}
	if abs := s.Query(origID); abs != nil {
		dc.line = abs.StartLine
	}
	if canon == nil {
		// No IR record (should not happen — the caller resolved it):
		// keep the call error-only so the body still checks it.
		dc.shape = "error"
		return dc, nil
	}
	pin, _ := s.Pin(canonKey)
	params, err := s.dbParams(canon, pin)
	if err != nil {
		return nil, err
	}
	dc.params = params
	switch {
	case canon.Type.IsDML():
		dc.shape = "error"
	case canon.Type == ir.QuerySelectSingle && isCountQuery(canon):
		dc.shape = "scalar"
	case canon.Type == ir.QuerySelectSingle:
		dc.shape = "single"
	default:
		dc.shape = "rows"
	}
	if dc.shape == "rows" || dc.shape == "single" {
		dc.rowName = s.RowName(canonKey, method)
		if fields, ferr := s.rowFields(canonKey, canon); ferr == nil {
			dc.rowFields = fields
			byHost := map[string]string{}
			for i, hv := range canon.RowShape {
				if i < len(fields) {
					byHost[normHost(hv)] = fields[i].Name
				}
			}
			dc.rowByHost = byHost
		}
	}
	return dc, nil
}

// detCaptureName assigns the deterministic per-body capture variable:
// lowerFirst(method)[+Rows], numeric suffix on repeats (mirrors the seam's
// capture contract the gates already pin).
func detCaptureName(method, shape string, used map[string]int) string {
	base := common.LowerFirst(method)
	if shape == "rows" {
		base += "Rows"
	}
	if n := used[base]; n > 0 {
		used[base]++
		return fmt.Sprintf("%s%d", base, n+1)
	}
	used[base]++
	return base
}

// detAdds returns the condition's contract FML adds (response drivers).
func detAdds(c *ir.Condition) []ir.FmlOp {
	var out []ir.FmlOp
	for _, op := range c.FmlOps {
		if op.Kind == ir.FmlAdd && !op.Dropped && !op.Error {
			out = append(out, op)
		}
	}
	return out
}

// detFieldSet indexes request Go names (lowercased) for the name-fallback
// arg match.
func detFieldSet(fields []templates.FieldSpec) map[string]string {
	out := map[string]string{}
	for _, f := range fields {
		out[strings.ToLower(f.Name)] = f.Name
	}
	return out
}

// detRequestMap builds the normalized-host → request-field map from the
// same FmlGet derivation the request struct uses (branch gets plus consumed
// preamble gets — mirrors the prompt's provenance).
func detRequestMap(s *Service, c *ir.Condition) map[string]string {
	out := map[string]string{}
	branchSrc := ""
	if s.source != "" && c.EndLine > c.StartLine {
		if lines := strings.Split(s.source, "\n"); c.EndLine <= len(lines) && c.StartLine >= 1 {
			branchSrc = strings.Join(lines[c.StartLine-1:c.EndLine], "\n")
		}
	}
	for _, op := range c.FmlOps {
		if op.Kind == ir.FmlGet && !op.Dropped && !op.Error && op.Target != "" {
			out[normHost(op.Target)] = fieldFromFML(op.Field)
		}
	}
	if s.Main == nil || branchSrc == "" {
		return out
	}
	for _, op := range s.Main.FmlOps {
		if op.Kind != ir.FmlGet || op.Dropped || op.Target == "" || !strings.Contains(branchSrc, op.Target) {
			continue
		}
		if _, ok := out[normHost(op.Target)]; !ok {
			out[normHost(op.Target)] = fieldFromFML(op.Field)
		}
	}
	return out
}

// detBranchSource slices absolute [from, to] lines out of src.
func detBranchSource(src string, from, to int) string {
	lines := strings.Split(src, "\n")
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from > to {
		return ""
	}
	return strings.Join(lines[from-1:to], "\n")
}

// detRegisterProducer indexes one read's row fields (normalized host →
// capture/field) for later string args. First producer wins — deterministic.
func detRegisterProducer(producers map[string]detProducer, dc *detCall) {
	if dc.shape != "rows" && dc.shape != "single" {
		return
	}
	for norm, field := range dc.rowByHost {
		if norm == "" || field == "" {
			continue
		}
		if _, ok := producers[norm]; !ok {
			producers[norm] = detProducer{capture: dc.capture, field: field}
		}
	}
}

// detCallArgs resolves one store call's argument expressions: request
// fields by host provenance, then by param-name match, then an earlier
// read's row field for string params, else the type's zero literal with a
// TODO trail for the LLM upgrade.
func detCallArgs(dc *detCall, reqMap, byName map[string]string, producers map[string]detProducer, todos map[string][]string) []string {
	binds := []string{}
	if dc.query != nil {
		binds = dc.query.Binds
	}
	out := make([]string, 0, len(dc.params))
	for i, pr := range dc.params {
		var bind string
		if i < len(binds) {
			bind = binds[i]
		}
		if bind != "" {
			if f, ok := reqMap[normHost(bind)]; ok && f != "" {
				out = append(out, "request."+f)
				continue
			}
		}
		if f, ok := byName[strings.ToLower(pr.Name)]; ok {
			out = append(out, "request."+f)
			continue
		}
		if pr.Type == "string" && bind != "" {
			if prd, ok := producers[normHost(bind)]; ok {
				out = append(out, prd.capture+"."+prd.field+".String")
				continue
			}
		}
		out = append(out, detZero(pr.Type))
		todos[dc.method] = append(todos[dc.method], fmt.Sprintf(
			"// tuxgo:TODO %s.%s: no request-field provenance for %q — zero value passed; LLM maps it",
			dc.method, pr.Name, bind))
	}
	return out
}

// detFnCallArgs resolves one store call's args against a fn helper's own
// params (bare identifiers — no request struct here), then earlier reads
// for string params, else zero literals with a TODO trail.
func detFnCallArgs(dc *detCall, params []plan.FnParam, producers map[string]detProducer, todos map[string][]string) []string {
	binds := []string{}
	if dc.query != nil {
		binds = dc.query.Binds
	}
	var declared []string
	for _, pr := range params {
		declared = append(declared, pr.Name)
	}
	out := make([]string, 0, len(dc.params))
	for i, pr := range dc.params {
		if name := detMatchIdent(pr.Name, declared); name != "" {
			out = append(out, name)
			continue
		}
		var bind string
		if i < len(binds) {
			bind = binds[i]
		}
		if name := detMatchIdent(bind, declared); name != "" {
			out = append(out, name)
			continue
		}
		if pr.Type == "string" && bind != "" {
			if prd, ok := producers[normHost(bind)]; ok {
				out = append(out, prd.capture+"."+prd.field+".String")
				continue
			}
		}
		out = append(out, detZero(pr.Type))
		todos[dc.method] = append(todos[dc.method], fmt.Sprintf(
			"// tuxgo:TODO %s.%s: no helper-param provenance for %q — zero value passed; LLM maps it",
			dc.method, pr.Name, bind))
	}
	return out
}

// detMatchIdent matches a db param (or bind host) to a declared identifier:
// normalized-host equality first, then lowercased-name equality.
func detMatchIdent(want string, declared []string) string {
	if want == "" {
		return ""
	}
	if nw := normHost(want); nw != "" {
		for _, d := range declared {
			if normHost(d) == nw {
				return d
			}
		}
	}
	lw := strings.ToLower(want)
	for _, d := range declared {
		if strings.ToLower(d) == lw {
			return d
		}
	}
	return ""
}

// detZero renders the Go zero literal for a store/param type. time.Time{}
// needs only the content-gated time import.
func detZero(typ string) string {
	switch strings.TrimSpace(typ) {
	case "string":
		return `""`
	case "int", "int64", "int32", "float64", "float32":
		return "0"
	case "bool":
		return "false"
	case "time.Time":
		return "time.Time{}"
	default:
		if t := strings.TrimSpace(typ); strings.HasPrefix(t, "*") || strings.HasPrefix(t, "[]") {
			return "nil"
		}
		return `""`
	}
}

// detHelperCalls finds the same-file helper calls a branch body needs: plan
// helpers whose legacy name appears as a call in the branch source, ordered
// by first occurrence. Args resolve against the request fields.
func detHelperCalls(p *plan.Plan, branchSrc string, baseLine int, reqFields []templates.FieldSpec, todos map[string][]string) []detHelper {
	if p == nil || branchSrc == "" {
		return nil
	}
	byName := detFieldSet(reqFields)
	lines := strings.Split(branchSrc, "\n")
	var out []detHelper
	used := map[string]int{}
	for _, h := range p.FnHelpers {
		line := -1
		for i, ln := range lines {
			if strings.Contains(ln, h.Name+"(") {
				line = baseLine + i
				break
			}
		}
		if line < 0 {
			continue
		}
		dh := detHelper{line: line, goName: h.GoName, retInt: h.Return == "int"}
		for _, pr := range h.Params {
			if f, ok := byName[strings.ToLower(pr.Name)]; ok {
				dh.args = append(dh.args, "request."+f)
				continue
			}
			dh.args = append(dh.args, detZero(pr.Type))
			todos["helper:"+h.GoName] = append(todos["helper:"+h.GoName], fmt.Sprintf(
				"// tuxgo:TODO %s(%s): no request-field provenance — zero value passed; LLM maps it", h.GoName, pr.Name))
		}
		cap := common.LowerFirst(h.GoName) + "St"
		if n := used[cap]; n > 0 {
			cap = fmt.Sprintf("%s%d", cap, n+1)
		}
		used[cap]++
		dh.cap = cap
		out = append(out, dh)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// detNestedHelperCalls finds same-file helper calls inside a fn helper's
// own source (args resolve against the helper's params).
func detNestedHelperCalls(p *plan.Plan, fnSrc string, h *plan.FnHelper, todos map[string][]string) []detHelper {
	if p == nil || fnSrc == "" {
		return nil
	}
	var declared []string
	for _, pr := range h.Params {
		declared = append(declared, pr.Name)
	}
	lines := strings.Split(fnSrc, "\n")
	var out []detHelper
	used := map[string]int{}
	for _, nh := range p.FnHelpers {
		if nh.GoName == h.GoName {
			continue
		}
		line := -1
		for i, ln := range lines {
			if strings.Contains(ln, nh.Name+"(") {
				line = h.StartLine + i
				break
			}
		}
		if line < 0 {
			continue
		}
		dh := detHelper{line: line, goName: nh.GoName, retInt: nh.Return == "int"}
		for _, pr := range nh.Params {
			if name := detMatchIdent(pr.Name, declared); name != "" {
				dh.args = append(dh.args, name)
				continue
			}
			dh.args = append(dh.args, detZero(pr.Type))
			todos["helper:"+nh.GoName] = append(todos["helper:"+nh.GoName], fmt.Sprintf(
				"// tuxgo:TODO %s(%s): no helper-param provenance — zero value passed; LLM maps it", nh.GoName, pr.Name))
		}
		cap := common.LowerFirst(nh.GoName) + "St"
		if n := used[cap]; n > 0 {
			cap = fmt.Sprintf("%s%d", cap, n+1)
		}
		used[cap]++
		dh.cap = cap
		out = append(out, dh)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// detStubTODOs lists the unresolved-helper TODOs for stubs the source
// references: omitted from the deterministic body by design (the package
// stub's out-pointer contract needs the LLM), visible for the upgrade.
func detStubTODOs(p *plan.Plan, src string) []string {
	if p == nil || len(p.Stubs) == 0 || src == "" {
		return nil
	}
	var out []string
	for _, st := range p.Stubs {
		if strings.Contains(src, st.Fn) {
			out = append(out, "// tuxgo:TODO unresolved helper "+st.Fn+" call omitted — LLM maps it to the package stub")
		}
	}
	sort.Strings(out)
	return out
}

// detTpTODOs lists the tpcall-site TODOs for placeholders owned by the
// tpcall units inside the condition span.
func detTpTODOs(p *plan.Plan, c *ir.Condition) []string {
	if p == nil || c == nil {
		return nil
	}
	var out []string
	for _, u := range p.Units {
		if u.Kind != plan.KindTPCall || u.TP == nil {
			continue
		}
		if u.TP.StartLine >= c.StartLine && u.TP.EndLine <= c.EndLine {
			svc := u.TP.Service
			if svc == "" {
				svc = u.Name
			}
			out = append(out, "// tuxgo:TODO tpcall "+svc+" site omitted — placeholder in tpcall_placeholders.go")
		}
	}
	sort.Strings(out)
	return out
}

// detHasTx reports whether any call takes the tx handle.
func detHasTx(ordered []*detCall) bool {
	for _, dc := range ordered {
		if dc.tx {
			return true
		}
	}
	return false
}

// detCallLine renders one store invocation (capture assignment excluded).
func detCallLine(dc *detCall) string {
	var sb strings.Builder
	sb.WriteString(dc.call.Receiver + "." + dc.call.Name + "(" + dc.call.CtxName)
	if dc.call.Tx != "" {
		sb.WriteString(", " + dc.call.Tx)
	}
	for _, a := range dc.call.Args {
		sb.WriteString(", " + a)
	}
	sb.WriteString(")")
	return sb.String()
}

// detEmitControllerEvents renders the interleaved helper + store calls in
// legacy order plus the response shaping. outerErr reports whether an err
// variable is already in scope (the controller's named return): without
// one, the first error-only call declares it; reads always capture with :=.
func detEmitControllerEvents(sb *strings.Builder, s *Service, ordered []*detCall, helpers []detHelper, adds []ir.FmlOp, respFields []templates.FieldSpec, respType string) {
	type event struct {
		line int
		kind int // 0 = store, 1 = helper
		dc   *detCall
		h    *detHelper
	}
	var evs []event
	for _, dc := range ordered {
		evs = append(evs, event{line: dc.line, kind: 0, dc: dc})
	}
	for i := range helpers {
		evs = append(evs, event{line: helpers[i].line, kind: 1, h: &helpers[i]})
	}
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].line != evs[j].line {
			return evs[i].line < evs[j].line
		}
		return evs[i].kind < evs[j].kind
	})
	errDecl := false
	for _, ev := range evs {
		if ev.kind == 1 {
			detEmitHelper(sb, ev.h)
			continue
		}
		detEmitStoreCall(sb, ev.dc, &errDecl, true)
	}
	detEmitShaping(sb, ordered, adds, respFields, respType)
}

// detEmitStoreCall renders one checked store call: error-only assigns err,
// reads capture with := (reused err after the first capture).
func detEmitStoreCall(sb *strings.Builder, dc *detCall, errDecl *bool, outerErr bool) {
	call := detCallLine(dc)
	if dc.shape == "error" {
		if outerErr || *errDecl {
			sb.WriteString("err = " + call + "\n")
		} else {
			sb.WriteString("err := " + call + "\n")
			*errDecl = true
		}
		sb.WriteString("if err != nil {\n\treturn nil, err\n}\n")
		return
	}
	sb.WriteString(dc.capture + ", err := " + call + "\n")
	*errDecl = true
	sb.WriteString("if err != nil {\n\treturn nil, err\n}\n")
}

// detEmitHelper renders one same-file helper call: int-status helpers check
// -1 with a named error, void helpers are fire-and-forget.
func detEmitHelper(sb *strings.Builder, h *detHelper) {
	call := "s." + h.goName + "(c"
	for _, a := range h.args {
		call += ", " + a
	}
	call += ")"
	if !h.retInt {
		sb.WriteString(call + "\n")
		return
	}
	sb.WriteString(h.cap + " := " + call + "\n")
	sb.WriteString("if " + h.cap + " == -1 {\n\treturn nil, errors.New(\"" + h.goName + " failed\")\n}\n")
}

// detEmitShaping renders the data appends: one range loop per multi-row
// read, one nil-guarded append per single-row read, the scalar guess, or
// the read-less empty append. Unmapped response fields ride a TODO (zero
// values).
func detEmitShaping(sb *strings.Builder, ordered []*detCall, adds []ir.FmlOp, respFields []templates.FieldSpec, respType string) {
	var rows, singles []*detCall
	var scalar *detCall
	for _, dc := range ordered {
		switch dc.shape {
		case "rows":
			rows = append(rows, dc)
		case "single":
			singles = append(singles, dc)
		case "scalar":
			if scalar == nil {
				scalar = dc
			}
		}
	}
	if len(rows) == 0 && len(singles) == 0 && scalar == nil {
		detEmitReadless(sb, ordered, respFields, respType)
		return
	}
	for _, dc := range rows {
		pairs, unmapped := detRowPairs(dc, adds, respFields)
		if len(unmapped) > 0 {
			sb.WriteString("// tuxgo:TODO response fields without row match (zero values): " + strings.Join(unmapped, ", ") + "\n")
		}
		sb.WriteString("for _, row := range " + dc.capture + " {\n")
		sb.WriteString("\tdata = append(data, " + detLiteral(respType, pairs, "row") + ")\n")
		sb.WriteString("}\n")
	}
	for _, dc := range singles {
		pairs, unmapped := detRowPairs(dc, adds, respFields)
		if len(pairs) == 0 {
			// The single feeds no response field (a lookup whose result
			// drives later args): keep it for its error check, shape
			// nothing.
			sb.WriteString("// tuxgo:TODO " + dc.capture + " (" + dc.rowName + ") has no response-field match — kept for its error check; LLM maps its role\n")
			sb.WriteString("_ = " + dc.capture + "\n")
			continue
		}
		if len(unmapped) > 0 {
			sb.WriteString("// tuxgo:TODO response fields without row match (zero values): " + strings.Join(unmapped, ", ") + "\n")
		}
		sb.WriteString("if " + dc.capture + " != nil {\n")
		sb.WriteString("\tdata = append(data, " + detLiteral(respType, pairs, dc.capture) + ")\n")
		sb.WriteString("}\n")
	}
	if scalar == nil {
		return
	}
	if len(rows) == 0 && len(singles) == 0 && len(respFields) > 0 {
		sb.WriteString("// tuxgo:TODO scalar " + scalar.capture + " mapped to " + respFields[0].Name + " by position — verify\n")
		sb.WriteString("data = append(data, &models." + respType + "{" + respFields[0].Name + ": fmt.Sprintf(\"%d\", " + scalar.capture + ")})\n")
		return
	}
	sb.WriteString("// tuxgo:TODO scalar " + scalar.capture + " unused in shaping — LLM maps its branch role\n")
	sb.WriteString("_ = " + scalar.capture + "\n")
}

// detRowPairs maps one read's row fields onto the endpoint response fields:
// FML-add target → row field via RowShape position, substring fallback,
// deterministic response order. Unmapped fields return for the TODO trail.
func detRowPairs(dc *detCall, adds []ir.FmlOp, respFields []templates.FieldSpec) ([]detFieldMap, []string) {
	byHost := map[string]string{}
	if dc != nil {
		byHost = dc.rowByHost
	}
	targetOf := map[string]string{} // response Go name → FML-add target
	for _, op := range adds {
		name := fieldFromFML(op.Field)
		if _, ok := targetOf[name]; !ok && op.Target != "" {
			targetOf[name] = op.Target
		}
	}
	var pairs []detFieldMap
	var unmapped []string
	for _, rf := range respFields {
		rowField := ""
		if t, ok := targetOf[rf.Name]; ok {
			rowField = byHost[normHost(t)]
		}
		if rowField == "" && dc != nil {
			rowField = detFuzzyRowField(dc.rowFields, rf.Name)
		}
		if rowField == "" {
			unmapped = append(unmapped, rf.Name)
			continue
		}
		pairs = append(pairs, detFieldMap{Resp: rf.Name, Row: rowField})
	}
	return pairs, unmapped
}

// detLiteral renders one `&models.Resp{...}` literal: mapped row fields
// read through .String, the receiver selecting row vs single captures.
func detLiteral(respType string, pairs []detFieldMap, recv string) string {
	var sb strings.Builder
	sb.WriteString("&models." + respType + "{")
	for i, pr := range pairs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(pr.Resp + ": " + recv + "." + pr.Row + ".String")
	}
	sb.WriteString("}")
	return sb.String()
}

// detEmitReadless renders the read-less append: every response field is a
// zero value with a TODO (no row source exists to map from).
func detEmitReadless(sb *strings.Builder, ordered []*detCall, respFields []templates.FieldSpec, respType string) {
	_ = ordered
	if len(respFields) == 0 {
		return
	}
	sb.WriteString("// tuxgo:TODO no row source — response fields are zero values; LLM shapes them\n")
	sb.WriteString("data = append(data, &models." + respType + "{})\n")
}

// detClosureLine maps one method-body line into the tx-closure form:
// method two-value returns collapse to the error (data rides the outer
// named return), bare returns carry err.
func detClosureLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "return") {
		return line
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "return"))
	if rest == "" {
		return indent + "return err"
	}
	if i := strings.LastIndex(rest, ","); i >= 0 {
		return indent + "return " + strings.TrimSpace(rest[i+1:])
	}
	return line
}

// outParams lists the pointer (out) params of a fn helper.
func outParams(params []plan.FnParam) []plan.FnParam {
	var out []plan.FnParam
	for _, pr := range params {
		if strings.HasPrefix(strings.TrimSpace(pr.Type), "*") {
			out = append(out, pr)
		}
	}
	return out
}

// detEmitFnEvents renders a fn helper's store + nested-helper calls with
// the legacy int-status contract: store errors zero the out-params and
// return -1, row results stay visible via the blank identifier. Inside a tx
// closure (inClosure) failure returns carry err (the closure returns error
// only); tails are mapped by detClosureLineFn.
func detEmitFnEvents(sb *strings.Builder, ordered []*detCall, nested []detHelper, outs []plan.FnParam, voidRet, inClosure bool) {
	type event struct {
		line int
		kind int
		dc   *detCall
		h    *detHelper
	}
	var evs []event
	for _, dc := range ordered {
		evs = append(evs, event{line: dc.line, kind: 0, dc: dc})
	}
	for i := range nested {
		evs = append(evs, event{line: nested[i].line, kind: 1, h: &nested[i]})
	}
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].line != evs[j].line {
			return evs[i].line < evs[j].line
		}
		return evs[i].kind < evs[j].kind
	})
	errDecl := false
	for _, ev := range evs {
		if ev.kind == 1 {
			detEmitFnHelper(sb, ev.h, outs, voidRet, inClosure)
			continue
		}
		detEmitFnStoreCall(sb, ev.dc, outs, voidRet, inClosure, &errDecl)
	}
}

// detEmitFnStoreCall renders one checked store call under the int-status
// contract (no named err in scope — the first error-only call declares it).
func detEmitFnStoreCall(sb *strings.Builder, dc *detCall, outs []plan.FnParam, voidRet, inClosure bool, errDecl *bool) {
	call := detCallLine(dc)
	if dc.shape == "error" {
		if *errDecl {
			sb.WriteString("err = " + call + "\n")
		} else {
			sb.WriteString("err := " + call + "\n")
			*errDecl = true
		}
		sb.WriteString("if err != nil {\n")
		sb.WriteString(detZeroOuts(outs, "\t"))
		sb.WriteString(detFnErrReturn(voidRet, inClosure, "\t") + "\n}")
		return
	}
	sb.WriteString(dc.capture + ", err := " + call + "\n")
	*errDecl = true
	sb.WriteString("if err != nil {\n")
	sb.WriteString(detZeroOuts(outs, "\t"))
	sb.WriteString(detFnErrReturn(voidRet, inClosure, "\t") + "\n}")
	if dc.shape == "rows" || dc.shape == "single" || dc.shape == "scalar" {
		sb.WriteString("\n_ = " + dc.capture)
	}
}

// detEmitFnHelper renders one nested helper call under the int-status
// contract.
func detEmitFnHelper(sb *strings.Builder, h *detHelper, outs []plan.FnParam, voidRet, inClosure bool) {
	call := "s." + h.goName + "(c"
	for _, a := range h.args {
		call += ", " + a
	}
	call += ")"
	if !h.retInt {
		sb.WriteString(call + "\n")
		return
	}
	sb.WriteString(h.cap + " := " + call + "\n")
	sb.WriteString("if " + h.cap + " == -1 {\n")
	sb.WriteString(detZeroOuts(outs, "\t"))
	sb.WriteString(detFnErrReturn(voidRet, inClosure, "\t") + "\n}")
}

// detZeroOuts renders the out-param zeroing block (nil-guarded).
func detZeroOuts(outs []plan.FnParam, indent string) string {
	var sb strings.Builder
	for _, o := range outs {
		elem := strings.TrimSpace(strings.TrimPrefix(o.Type, "*"))
		sb.WriteString(indent + "if " + o.Name + " != nil {\n")
		sb.WriteString(indent + "\t*" + o.Name + " = " + detZero(elem) + "\n")
		sb.WriteString(indent + "}\n")
	}
	return sb.String()
}

// detFnErrReturn renders the failure return for the int-status contract:
// -1 (bare return for void), or the closure's err inside tx.
func detFnErrReturn(voidRet, inClosure bool, indent string) string {
	if inClosure {
		return indent + "return err"
	}
	if voidRet {
		return indent + "return"
	}
	return indent + "return -1"
}

// detFnTail renders the success tail for the int-status contract: the
// legacy success value read off the fn source (0 when undecidable).
func detFnTail(fnSrc string, h *plan.FnHelper) string {
	if h.Return == "" {
		return "return"
	}
	return "return " + detFnSuccessRet(fnSrc)
}

// detFnSuccessRet reads the legacy success value off the fn source: the
// single distinct non-(-1) integer return literal, else 0. Callers compare
// `== -1` for failure (the corpus convention), so the exact success value
// is advisory — but carrying it keeps status checks byte-faithful.
func detFnSuccessRet(fnSrc string) string {
	seen := map[string]bool{}
	var vals []string
	for _, line := range strings.Split(fnSrc, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "return") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "return"))
		rest = strings.TrimSuffix(rest, ";")
		if rest == "" || rest == "-1" {
			continue
		}
		v := strings.TrimSpace(rest)
		if strings.HasPrefix(v, "-") {
			continue // only the -1 failure literal is negative
		}
		ok := v != ""
		for _, r := range v {
			if r < '0' || r > '9' {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	if len(vals) == 1 {
		return vals[0]
	}
	return "0"
}

// detFnErrTail renders the post-wrapper failure tail.
func detFnErrTail(h *plan.FnHelper) string {
	if h.Return == "" {
		return "\treturn"
	}
	if outs := detZeroOuts(outParams(h.Params), "\t"); outs != "" {
		return outs + "\treturn -1"
	}
	return "\treturn -1"
}

// detClosureLineFn maps one fn-helper body line into the tx-closure form
// (the closure returns error only): failure returns carry err, every other
// single-value return is success (nil), bare void returns are nil.
func detClosureLineFn(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "return") {
		return line
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "return"))
	switch rest {
	case "", "0":
		return indent + "return nil"
	case "-1":
		return indent + "return err"
	case "err", "nil":
		return line
	}
	if i := strings.LastIndex(rest, ","); i >= 0 {
		return indent + "return " + strings.TrimSpace(rest[i+1:])
	}
	// Any other single value (e.g. the legacy success literal) is the
	// success path inside the closure.
	return indent + "return nil"
}

// detFuzzyRowField falls back to a substring match between response and row
// names (alias-shaped rows where the host var left no trace) — mirrors the
// prompt's responseShaping fallback.
func detFuzzyRowField(fields []templates.FieldSpec, resp string) string {
	rl := strings.ToLower(resp)
	for _, f := range fields {
		fl := strings.ToLower(f.Name)
		if strings.Contains(fl, rl) || strings.Contains(rl, fl) {
			return f.Name
		}
	}
	return ""
}
