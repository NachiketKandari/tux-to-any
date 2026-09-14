package csplan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
)

// Param is one named Oracle parameter of a repo method: the Oracle param
// name, the bind host var it came from, and the request property that
// supplies it ("" when the mapping leaves it unmapped — the service body
// must supply the value, which is LLM-seam/TODO territory).
type Param struct {
	Name        string `json:"name"`
	Bind        string `json:"bind"`
	RequestProp string `json:"request_prop,omitempty"`
}

// Prop is one DTO property of a response class, derived from the query's
// row shape (the INTO host var, indicator dropped, type prefixes stripped,
// upper-snake named like the reference convention).
type Prop struct {
	Name  string `json:"name"`
	Bind  string `json:"bind"`
	Alias string `json:"alias,omitempty"`
}

// QueryPlan is one database unit's C# shape: the NamedQueries const, the
// repo method kind, the ordered named parameters, and the DTO row
// properties for SELECTs.
type QueryPlan struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"` // select-single | select-multi | dml | merge
	Name     string  `json:"name"` // const name (GetCUSTDetailsQuery)
	SQL      string  `json:"sql"`
	Params   []Param `json:"params"`
	RowProps []Prop  `json:"row_props,omitempty"`
	DML      bool    `json:"dml,omitempty"`
}

// EndpointPlan is one action's C# shape: the controller action, its
// response DTO, the request properties it reads, and its queries.
type EndpointPlan struct {
	Name       string   `json:"name"`
	Route      string   `json:"route"`
	Scenario   string   `json:"scenario,omitempty"` // scenarioRef key when arm-sliced
	DTOName    string   `json:"dto_name"`
	Requests   []string `json:"requests"` // request properties read, in bind order
	QueryIDs   []string `json:"query_ids"`
	Residue    []string `json:"residue,omitempty"` // loud slice residue (SCEN-D4)
	LineSpan   [2]int   `json:"line_span"`         // source span of the arm (the LLM seam's code view)
	SourceSpan string   `json:"source_span,omitempty"`
}

// Plan is the deterministic convertcs decomposition for one entry file.
type Plan struct {
	Source string `json:"source"`
	Entry  string `json:"entry"`

	Namespace  string `json:"namespace"`
	Area       string `json:"area"`
	Component  string `json:"component"`
	ApiVersion string `json:"api_version"`
	RequestDTO string `json:"request_dto"`
	Validator  string `json:"validator,omitempty"`

	Controller string `json:"controller"` // <Component>Controller
	Service    string `json:"service"`    // <Component>Service
	Repo       string `json:"repo"`       // <Component>Repository
	QueriesCls string `json:"queries_cls"`
	DTOCls     string `json:"dto_cls"`

	Endpoints []EndpointPlan `json:"endpoints"`
	Queries   []QueryPlan    `json:"queries"`
}

// Options carries the plan inputs: the entry IR, the source text, and the
// user mapping.
type Options struct {
	Main    *ir.File
	Source  string
	Mapping *Mapping
}

// Build constructs the plan. Deterministic: identical inputs produce a
// byte-identical plan.
func Build(opts Options) (*Plan, error) {
	if opts.Main == nil || opts.Mapping == nil {
		return nil, fmt.Errorf("csplan: main IR and mapping are required")
	}
	if strings.TrimSpace(opts.Source) == "" {
		return nil, fmt.Errorf("csplan: %s needs its source text", opts.Main.Path)
	}
	m := opts.Mapping
	p := &Plan{
		Source:     opts.Main.Path,
		Entry:      opts.Main.Entry,
		Namespace:  m.Namespace,
		Area:       strings.Trim(m.Area, "."),
		Component:  m.Component,
		ApiVersion: m.ApiVersion,
		RequestDTO: m.RequestDTO,
		Validator:  m.Validator,
		Controller: m.Component + "Controller",
		Service:    m.Component + "Service",
		Repo:       m.Component + "Repository",
		QueriesCls: m.Component + "Queries",
		DTOCls:     m.Component + "DTO",
	}
	if p.ApiVersion == "" {
		p.ApiVersion = "1.0"
	}
	if p.RequestDTO == "" {
		p.RequestDTO = "object"
	}

	// The flow tree powers both reference forms: scenarioRef endpoints fold
	// through the dispatch axis, conditionRef endpoints through the tree.
	var tree *flow.Tree
	treeFor := func() (*flow.Tree, error) {
		if tree != nil {
			return tree, nil
		}
		t, err := flow.TreeFor(opts.Source, opts.Main)
		if err != nil {
			return nil, fmt.Errorf("csplan: flow tree for %s: %w", opts.Main.Path, err)
		}
		tree = t
		return t, nil
	}

	queryByID := make(map[string]*ir.Query, len(opts.Main.Queries))
	for _, q := range opts.Main.Queries {
		queryByID[q.ID] = q
	}
	hostVars := map[string]bool{}
	for _, hv := range opts.Main.HostVars {
		// Only typed host vars count: the extractor's format-literal leak
		// (the `:mi:ss` inside 'dd-Mon-yyyy hh24:mi:ss' surfacing as binds)
		// carries no declaration, so those pseudo-binds never reach the
		// repo signature.
		if hv.CType != "" {
			hostVars[hv.Name] = true
		}
	}

	canonical := map[string]bool{}
	seenEp := map[string]bool{}
	for _, e := range m.Endpoints {
		ep := EndpointPlan{
			Name:    e.Name,
			Route:   e.Route,
			DTOName: e.Name + "Response",
		}
		var cond *ir.Condition
		switch {
		case e.ScenarioRef != "":
			t, err := treeFor()
			if err != nil {
				return nil, err
			}
			axis := t.DispatchAxisFor([]byte(opts.Source))
			key, value, err := plan.ParseScenarioRef(e.ScenarioRef)
			if err != nil {
				return nil, fmt.Errorf("csplan: endpoint %s: %w", e.Name, err)
			}
			if axis == nil || axis.Key() != key {
				return nil, fmt.Errorf("csplan: endpoint %s references scenario axis %q — the file dispatches on %s",
					e.Name, key, axisRefSummary(t, opts.Source))
			}
			scen := flow.ScenarioFor(t, axis, value)
			if scen == nil {
				return nil, fmt.Errorf("csplan: endpoint %s: no scenario slice for %s", e.Name, e.ScenarioRef)
			}
			ep.Scenario = scen.Key
			ep.Residue = scen.Residue
			cond = flow.ScenarioCondition(scen, t)
			if cond == nil {
				return nil, fmt.Errorf("csplan: endpoint %s: scenario %s has no condition", e.Name, e.ScenarioRef)
			}
		case e.ConditionRef != "":
			t, err := treeFor()
			if err != nil {
				return nil, err
			}
			c, err := flow.ConditionFor(t, opts.Main.Conditions, e.ConditionRef)
			if err != nil {
				return nil, fmt.Errorf("csplan: endpoint %s: %w", e.Name, err)
			}
			cond = c
		default:
			cond = opts.Main.Condition(e.Condition)
			if cond == nil {
				return nil, fmt.Errorf("csplan: endpoint %s maps condition %d — inventory has %d conditions",
					e.Name, e.Condition, len(opts.Main.Conditions))
			}
		}

		ids := cond.QueryIDs
		if len(ids) == 0 {
			ids = queriesInSpan(opts.Main, cond)
		}
		ep.LineSpan = [2]int{cond.StartLine, cond.EndLine}
		ep.SourceSpan = fmt.Sprintf("%d-%d", cond.StartLine, cond.EndLine)
		for _, id := range ids {
			q := queryByID[id]
			if q == nil {
				return nil, fmt.Errorf("csplan: endpoint %s references unknown query %q", e.Name, id)
			}
			if q.DuplicateOf != "" {
				q = queryByID[q.DuplicateOf]
			}
			ep.QueryIDs = append(ep.QueryIDs, q.ID)
			if !canonical[q.ID] {
				canonical[q.ID] = true
				qp, err := buildQueryPlan(q, m, hostVars)
				if err != nil {
					return nil, fmt.Errorf("csplan: query %s: %w", q.ID, err)
				}
				p.Queries = append(p.Queries, qp)
			}
		}
		if len(ep.QueryIDs) == 0 {
			return nil, fmt.Errorf("csplan: endpoint %s (%s) owns no SQL — map an arm that carries queries, or convert the file as a fn library", e.Name, ep.Scenario)
		}
		// Request properties: the endpoint's params that the request
		// supplies, in bind order of its first query.
		seenReq := map[string]bool{}
		for _, id := range ep.QueryIDs {
			for _, qp := range p.Queries {
				if qp.ID != id {
					continue
				}
				for _, prm := range qp.Params {
					if prm.RequestProp != "" && !seenReq[prm.RequestProp] {
						seenReq[prm.RequestProp] = true
						ep.Requests = append(ep.Requests, prm.RequestProp)
					}
				}
			}
		}
		if seenEp[e.Name] {
			return nil, fmt.Errorf("csplan: endpoint %s mapped twice", e.Name)
		}
		seenEp[e.Name] = true
		p.Endpoints = append(p.Endpoints, ep)
	}
	return p, nil
}

// buildQueryPlan derives one query's C# shape: kind, const name, params
// (binds filtered to declared host variables — the extractor's
// format-literal leak like `:mi` never reaches the repo signature), and
// DTO row props for SELECTs.
func buildQueryPlan(q *ir.Query, m *Mapping, hostVars map[string]bool) (QueryPlan, error) {
	qp := QueryPlan{ID: q.ID, SQL: cleanSQL(q.SQL)}
	switch q.Type {
	case ir.QuerySelectSingle:
		qp.Kind = "select-single"
	case ir.QuerySelectMulti:
		qp.Kind = "select-multi"
	case ir.QueryInsert, ir.QueryUpdate, ir.QueryDelete:
		qp.Kind = "dml"
		qp.DML = true
	case ir.QueryMerge:
		qp.Kind = "merge"
		qp.DML = true
	default:
		return QueryPlan{}, fmt.Errorf("unsupported query type %q", q.Type)
	}
	if pin, ok := m.DBMethods[q.ID]; ok && pin.Name != "" {
		qp.Name = pin.Name
	} else {
		qp.Name = defaultQueryName(q, qp.DML)
	}

	// Params: first-seen bind order, host-var filtered, deduped.
	seen := map[string]bool{}
	for _, b := range q.Binds {
		if !hostVars[b] || seen[b] {
			continue
		}
		seen[b] = true
		prm := Param{Name: b, Bind: b}
		if n, ok := m.ParamNames[b]; ok && n != "" {
			prm.Name = n
		} else if n, ok := m.RequestFields[b]; ok && n != "" {
			prm.Name = n
		} else {
			prm.Name = pascalOf(b)
		}
		prm.RequestProp = m.RequestFields[b]
		qp.Params = append(qp.Params, prm)
	}

	// Row props: the INTO shape with indicator variables already dropped
	// by the IR; type prefixes stripped, upper-snake named.
	if !qp.DML {
		for _, rs := range q.RowShape {
			bind := rs
			alias := ""
			if i := strings.Index(rs, "["); i >= 0 {
				bind = rs[:i]
			}
			if bind == "" {
				continue
			}
			qp.RowProps = append(qp.RowProps, Prop{Name: propNameOf(bind), Bind: bind, Alias: alias})
		}
	}
	return qp, nil
}

// cleanSQL prepares a query's SQL for the C# verbatim const: the Pro*C
// host plumbing (the `INTO :host:ind` clause) is stripped — the C# side
// reads columns by name — and the trailing statement semicolon goes
// (the const's C# syntax carries its own terminator). Binds in the
// surviving predicate text stay verbatim: the OracleParameter names must
// match them exactly.
func cleanSQL(sql string) string {
	s := strings.TrimSpace(sql)
	s = strings.TrimSuffix(s, ";")
	upper := strings.ToUpper(s)
	if i := strings.Index(upper, " INTO "); i >= 0 {
		if j := strings.Index(upper[i:], " FROM "); j > 0 {
			s = strings.TrimSpace(s[:i] + " " + strings.TrimLeft(s[i+j:], " "))
		}
	}
	return s
}

// prefixRe strips the Pro*C type/hungarian prefixes the corpus carries
// (sql_, vc_, v_, c_, l_, d_, i_, f_) from a host var before naming.
var prefixRe = regexp.MustCompile(`^(sql_|vc_|v_|c_|l_|d_|i_|f_)+`)

// propNameOf derives a DTO property name from a row-shape host var:
// prefixes stripped, snake segments upper-snake joined (sql_mar_form_no →
// MAR_FORM_NO).
func propNameOf(bind string) string {
	name := prefixRe.ReplaceAllString(strings.TrimPrefix(bind, ":"), "")
	name = strings.ReplaceAll(name, ".", "_")
	return strings.ToUpper(name)
}

// pascalOf derives a parameter name from a host var when the mapping
// leaves it unset (sql_cst_pan_no → CstPanNo).
func pascalOf(bind string) string {
	name := prefixRe.ReplaceAllString(strings.TrimPrefix(bind, ":"), "")
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

// defaultQueryName derives the NamedQueries const when unpinned:
// <Verb><Table>Query (GetCstMblAccopnRqstQuery / Update…Query).
func defaultQueryName(q *ir.Query, dml bool) string {
	verb := "Get"
	switch q.Type {
	case ir.QueryInsert:
		verb = "Insert"
	case ir.QueryUpdate:
		verb = "Update"
	case ir.QueryDelete:
		verb = "Delete"
	case ir.QueryMerge:
		verb = "Merge"
	}
	table := "Row"
	if len(q.Tables) > 0 && q.Tables[0] != "" {
		table = pascalOf(q.Tables[0])
	}
	return verb + table + "Query"
}

// queriesInSpan collects the queries whose span sits inside the
// condition's branch span (the fallback when the IR's QueryIDs are
// unset).
func queriesInSpan(f *ir.File, cond *ir.Condition) []string {
	var ids []string
	for _, q := range f.Queries {
		if q.DuplicateOf != "" {
			continue
		}
		if q.StartLine >= cond.StartLine && q.EndLine <= cond.EndLine {
			ids = append(ids, q.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// axisRefSummary names the file's dispatch axis for the mapping-mismatch
// error (or "no axis" when the entry has no dispatch spine).
func axisRefSummary(t *flow.Tree, src string) string {
	axis := t.DispatchAxisFor([]byte(src))
	if axis == nil {
		return "no dispatch axis"
	}
	return axis.Key() + " " + strings.Join(axis.Domain, "/")
}
