package gen

import (
	"fmt"
	"slices"
	"strings"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/common"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/templates"
)

// ConditionOf returns the condition backing an endpoint name.
func (s *Service) ConditionOf(endpoint string) *ir.Condition {
	for _, e := range s.Mapping.Endpoints {
		if e.Name == endpoint {
			return s.conditionOf(e)
		}
	}
	return nil
}

// ScenarioOf returns the scenario slice backing an endpoint name (nil for
// condition/conditionRef endpoints) — the flattened-view input the
// controller prompt consumes (G-SCEN6). Memoized: one fold per endpoint.
func (s *Service) ScenarioOf(endpoint string) *flow.Scenario {
	e := s.endpointOf(endpoint)
	if e == nil || e.ScenarioRef == "" {
		return nil
	}
	if sc, ok := s.scenMemo[endpoint]; ok {
		return sc
	}
	sc := s.scenarioOf(*e)
	if s.scenMemo == nil {
		s.scenMemo = map[string]*flow.Scenario{}
	}
	s.scenMemo[endpoint] = sc
	return sc
}

func (s *Service) endpointOf(endpoint string) *plan.Endpoint {
	for i := range s.Mapping.Endpoints {
		if s.Mapping.Endpoints[i].Name == endpoint {
			return &s.Mapping.Endpoints[i]
		}
	}
	return nil
}

// scenarioOf resolves a scenarioRef endpoint's slice from the flow tree
// (SCEN-D7: re-derivation from source — the stale-artifact-proof pattern).
// Degrades to nil on any mismatch; the plan already validated the ref, so
// this is defensive only.
func (s *Service) scenarioOf(e plan.Endpoint) *flow.Scenario {
	key, value, err := plan.ParseScenarioRef(e.ScenarioRef)
	if err != nil {
		return nil
	}
	t := s.treeFor()
	if t == nil {
		return nil
	}
	axis := t.DispatchAxisFor([]byte(s.source))
	if axis == nil || axis.Key() != key {
		return nil
	}
	if !slices.Contains(axis.Domain, value) && !(axis.HasDefault && value == axis.DefaultKey()) {
		return nil
	}
	return flow.ScenarioFor(t, axis, value)
}

// treeFor builds the entry's flow tree once (the shared flow.TreeFor
// derivation — conditionRef and scenarioRef limbs share the memo).
func (s *Service) treeFor() *flow.Tree {
	if s.flowTree != nil {
		return s.flowTree
	}
	if strings.TrimSpace(s.source) == "" {
		return nil
	}
	t, err := flow.TreeFor(s.source, s.Main)
	if err != nil {
		return nil
	}
	s.flowTree = t
	return t
}

// BranchCalls returns the condition's canonical queries re-based to the
// branch slice plus their resolved DBCall map — the input budget.ReplaceQueries
// needs to rewrite the branch view (plan-conversion §4 step 4).
func (s *Service) BranchCalls(c *ir.Condition, p *plan.Plan) ([]*ir.Query, map[string]budget.DBCall, error) {
	// Collect the branch's queries (explicit references, else line overlap).
	var ids []string
	seen := map[string]bool{}
	ids = c.QueryIDs
	if len(ids) == 0 {
		for _, q := range s.Main.Queries {
			if q.StartLine >= c.StartLine && q.StartLine <= c.EndLine {
				ids = append(ids, q.ID)
			}
		}
	}
	calls, err := s.StoreCallsFor(ids, p)
	if err != nil {
		return nil, nil, err
	}
	// The physical SQL region replaced in THIS branch is the referenced
	// site's — a shared query (q5→q3) has its own lines in each branch.
	var queries []*ir.Query
	for _, id := range ids {
		orig := s.queries[id]
		if orig == nil || seen[orig.ID] {
			continue
		}
		seen[orig.ID] = true
		lq := *orig
		lq.StartLine -= c.StartLine - 1
		lq.EndLine -= c.StartLine - 1
		queries = append(queries, &lq)
	}
	return queries, calls, nil
}

// StoreCallsFor resolves the DBCall per referenced query site: the canonical
// plan unit names the method, the pin + binds name the args, and tx-variant
// DML units insert the tx handle the template signature demands (G-SCEN6).
func (s *Service) StoreCallsFor(ids []string, p *plan.Plan) (map[string]budget.DBCall, error) {
	dbUnitByQuery := map[string]plan.Unit{}
	for _, u := range p.Units {
		if u.Kind == plan.KindDBMethod && len(u.QueryIDs) > 0 {
			dbUnitByQuery[u.QueryIDs[0]] = u
		}
	}
	calls := make(map[string]budget.DBCall, len(ids))
	for _, id := range ids {
		orig := s.queries[id]
		if orig == nil {
			return nil, fmt.Errorf("gen: condition references unknown query %q", id)
		}
		if _, dup := calls[orig.ID]; dup {
			continue
		}
		canon := orig
		if orig.DuplicateOf != "" {
			canon = s.queries[orig.DuplicateOf]
		}
		u, ok := dbUnitByQuery[canon.ID]
		if !ok {
			return nil, fmt.Errorf("gen: query %s has no db plan unit", canon.ID)
		}
		pin, _ := s.Pin(canon.ID)
		params, err := s.dbParams(canon, pin)
		if err != nil {
			return nil, err
		}
		args := make([]string, len(params))
		for j, pp := range params {
			args[j] = pp.Name
		}
		call := budget.DBCall{Receiver: "s.store", Name: u.Name, CtxName: "c", Args: args}
		if q := s.Query(canon.ID); u.Tx && q != nil && q.Type.IsDML() && q.Type != ir.QueryMerge {
			call.Tx = "tx"
		}
		calls[orig.ID] = call
	}
	return calls, nil
}

// ControllerPromptContext renders the fixed contract a controller prompt
// consumes: the exact method signature (named returns data/err), the
// endpoint's request/response structs, and the row structs its store calls
// return — all verbatim, so the model references parameter and field names
// exactly instead of guessing them (live-smoke finding: ActiveFlg vs
// CActiveFlag, req vs request).
func (s *Service) ControllerPromptContext(endpoint string, p *plan.Plan, storeMethods []string) (string, error) {
	c := s.ConditionOf(endpoint)
	if c == nil {
		return "", fmt.Errorf("gen: no condition for endpoint %s", endpoint)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "func (s *%s) %s(c context.Context, request *models.%s) (data []*models.%s, err error)\n",
		common.LowerFirst(s.Mapping.Service)+"Controller", endpoint, s.requestType(endpoint), s.responseType(endpoint))
	// The request contract mirrors ModelFile's derivation (§4.8.3 —
	// requestFields): branch FmlGet ops, unioned with the entry-preamble
	// Fgets whose host vars the branch consumes (live-run fix).
	var ep plan.Endpoint
	for _, e := range s.Mapping.Endpoints {
		if e.Name == endpoint {
			ep = e
			break
		}
	}
	sb.WriteString("\ntype " + s.requestType(endpoint) + " struct {\n")
	sb.WriteString(indentFields(s.requestFields(ep, c)))
	sb.WriteString("}\n")
	sb.WriteString("\ntype " + s.responseType(endpoint) + " struct {\n")
	sb.WriteString(indentFields(contractFields(c.FmlOps, ir.FmlAdd)))
	sb.WriteString("}\n")

	rows := map[string]bool{}
	for _, u := range p.Units {
		if u.Kind != plan.KindDBMethod || len(u.QueryIDs) == 0 || !slices.Contains(storeMethods, u.Name) {
			continue
		}
		q := s.Query(u.QueryIDs[0])
		if q == nil || q.Type.IsDML() || isCountQuery(q) {
			// DML units (incl. MERGE) return no rows; COUNT singles return
			// scalars — neither contributes a row struct to the contract
			// (live-run fix: a MERGE endpoint previously hard-errored here).
			continue
		}
		row := s.RowName(u.QueryIDs[0], u.Name)
		if rows[row] {
			continue
		}
		fields, err := s.rowFields(q)
		if err != nil {
			return "", err
		}
		rows[row] = true
		sb.WriteString("\ntype " + row + " struct {\n")
		sb.WriteString(indentFields(fields))
		sb.WriteString("}\n")
	}

	// External interactions surface as compilable placeholders (PF-4.5): the
	// body calls the stub instead of inventing an outbound call. Endpoints
	// without tpcalls keep prompts unchanged.
	if sigs := s.PlaceholderSignatures(c, p); len(sigs) > 0 {
		sb.WriteString("\nexternal service call placeholders (call these instead of the legacy tpcall; each returns an error — handle it like a store error and return nil, err):\n")
		for _, sig := range sigs {
			sb.WriteString(sig + "\n")
		}
	}
	return sb.String(), nil
}

// indentFields renders struct fields gofmt-shaped.
func indentFields(fields []templates.FieldSpec) string {
	var sb strings.Builder
	for _, f := range fields {
		sb.WriteString("\t" + f.Name + " " + f.Type)
		if tag := f.Tag(); tag != "" {
			sb.WriteString(" `" + tag + "`")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// RenderControllerMethod wraps an accepted LLM body in the controller
// method template — the template owns the shape, the LLM owns only the
// template-shaped gap.
func (s *Service) RenderControllerMethod(endpoint, body string) (string, error) {
	return render(templates.ControllerMethod, templates.ControllerMethodData{
		StructName:   common.LowerFirst(s.Mapping.Service) + "Controller",
		Name:         endpoint,
		CtxName:      "c",
		RequestType:  "models." + s.requestType(endpoint),
		ResponseType: "models." + s.responseType(endpoint),
		Body:         strings.TrimRight(body, "\n"),
	})
}
