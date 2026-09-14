// Package csgen is the deterministic C# generator for the convertcs
// target: a csplan.Plan + the embedded templates → the seven-file
// .NET Core tree per component (Controller / DTO / NamedQueries /
// Repository interface+impl / Service interface+impl). The SQL consts
// carry the source SQL verbatim (fidelity-first; cschk compares them
// normalized). The service body renders deterministically — repo calls,
// row mapping, structured logging — and leaves the arm's residual logic
// as a tuxgo:TODO seam (the LLM seam fills it when enabled; -no-llm
// leaves the placeholder for a resume).
package csgen

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tux-to-any/internal/csplan"
	"tux-to-any/internal/templates"
)

// Options carries one generation run's inputs.
type Options struct {
	Plan  *csplan.Plan
	NoLLM bool
}

// Result is one generated tree: relative file paths → content, in
// deterministic write order.
type Result struct {
	Files    map[string]string
	Order    []string
	LLMCalls int
	Notes    []string
}

// EpData is one endpoint's controller/service/DTO view.
type EpData struct {
	Name       string
	Route      string
	DTOName    string
	RetType    string
	Props      []string // DTO properties (union of the select queries' row props)
	Queries    []QData
	ReturnExpr string
	Todo       bool
	Span       string
}

// QData is one query's repo/service view.
type QData struct {
	Name       string // NamedQueries const
	MethodName string // repo method
	SQL        string
	Params     []csplan.Param
	SigArgs    string // "string MobileNo, " ("" when none)
	Args       string // "request.MobileNo, " ("" when none)
	OracleArgs string // new OracleParameter("bind", Name), ...
	CallLHS    string // var tblRes / var affected
	VarName    string
	DML        bool
	MapSingle  bool
	MapMulti   bool
	Props      []string
	LogLine    string // full _logger.LogInformation(...); statement
}

type fileData struct {
	Namespace    string
	Usings       []string
	RootNS       string
	Controller   string
	Service      string
	ServiceField string
	Repo         string
	RepoField    string
	QueriesCls   string
	DTOCls       string
	ApiVersion   string
	RequestDTO   string
	Validator    string
	Endpoints    []EpData
	Queries      []QData
}

// Generate renders the seven-file tree for one plan. Deterministic in
// -no-llm mode (and milestone-one overall): identical plans produce
// byte-identical files.
func Generate(ctx context.Context, opts Options) (Result, error) {
	res := Result{Files: map[string]string{}}
	p := opts.Plan
	tz := templates.NewEmbeddedProvider()

	root := p.Namespace
	rootArea := root
	if p.Area != "" {
		rootArea = root + "." + p.Area
	}
	ns := func(layer string) string { return rootArea + "." + layer }
	dtoNS, svcNS, repoNS, nqNS := ns("DTO"), ns("Service"), ns("Repository"), ns("NamedQueries")

	epDatas, queriesFlat := buildEndpoints(p)
	dtoEps := make([]EpData, 0, len(epDatas))
	for _, ep := range epDatas {
		if len(ep.Props) > 0 {
			dtoEps = append(dtoEps, ep)
		}
	}

	serviceField := strings.ToLower(p.Component[:1]) + p.Component[1:]

	ctrl := fileData{
		Namespace: ns("Controller"), RootNS: root,
		Usings: []string{
			"System.Net",
			root + ".Common",
			root + ".Helpers",
			svcNS,
		},
		Controller: p.Controller, Service: p.Service, ServiceField: serviceField,
		ApiVersion: p.ApiVersion, RequestDTO: p.RequestDTO, Validator: p.Validator,
		Endpoints: epDatas,
	}
	dto := fileData{Namespace: dtoNS, DTOCls: p.DTOCls, Endpoints: dtoEps}
	nq := fileData{Namespace: nqNS, QueriesCls: p.QueriesCls, Queries: queriesFlat}
	repoIface := fileData{Namespace: repoNS, Repo: p.Repo, Endpoints: epDatas}
	repo := fileData{
		Namespace: ns("Repository"),
		Usings: []string{
			root + ".Common",
			root + ".Common.DbHelper",
			"Oracle.ManagedDataAccess.Client",
			"System.Data",
		},
		RootNS: nqNS, Repo: p.Repo, QueriesCls: p.QueriesCls, Endpoints: epDatas,
	}
	svcIface := fileData{
		Namespace: svcNS,
		Usings: []string{
			"static " + root + ".Common.CommonRequestDTO",
			"static " + dtoNS + "." + p.DTOCls,
		},
		Service: p.Service, RequestDTO: p.RequestDTO, Endpoints: epDatas,
	}
	svc := fileData{
		Namespace: svcNS,
		Usings: []string{
			root + ".Common",
			repoNS,
			"static " + dtoNS + "." + p.DTOCls,
		},
		Service: p.Service, ServiceField: serviceField, Repo: p.Repo, RepoField: serviceField,
		RequestDTO: p.RequestDTO, Endpoints: epDatas,
	}

	files := []struct {
		rel  string
		id   templates.ID
		data fileData
	}{
		{"Controller/" + p.Controller + ".cs", templates.CsControllerFile, ctrl},
		{"DTO/" + p.DTOCls + ".cs", templates.CsDTOFile, dto},
		{"NamedQueries/" + p.QueriesCls + ".cs", templates.CsNamedQueriesFile, nq},
		{"Repository/I" + p.Repo + ".cs", templates.CsRepoInterfaceFile, repoIface},
		{"Repository/" + p.Repo + ".cs", templates.CsRepoFile, repo},
		{"Service/I" + p.Service + ".cs", templates.CsServiceInterfaceFile, svcIface},
		{"Service/" + p.Service + ".cs", templates.CsServiceFile, svc},
	}
	for _, f := range files {
		out, err := tz.Render(f.id, f.data)
		if err != nil {
			return Result{}, fmt.Errorf("csgen: render %s: %w", f.rel, err)
		}
		res.Files[f.rel] = out
		res.Order = append(res.Order, f.rel)
	}
	sort.Strings(res.Notes)
	return res, nil
}

// buildEndpoints prepares the template view of every endpoint and the
// flat query list for NamedQueries.
func buildEndpoints(p *csplan.Plan) ([]EpData, []QData) {
	qpByID := make(map[string]csplan.QueryPlan, len(p.Queries))
	for _, q := range p.Queries {
		qpByID[q.ID] = q
	}
	var flat []QData
	eps := make([]EpData, 0, len(p.Endpoints))
	for _, ep := range p.Endpoints {
		data := EpData{
			Name: ep.Name, Route: ep.Route, DTOName: ep.DTOName,
			Todo: true, Span: ep.SourceSpan,
		}
		methodSfx := 0
		var props []string
		seenProp := map[string]bool{}
		hasSelect := false
		for i, id := range ep.QueryIDs {
			qp, ok := qpByID[id]
			if !ok {
				continue
			}
			q := QData{
				Name: qp.Name, SQL: qp.SQL, Params: qp.Params, DML: qp.DML,
			}
			methodSfx++
			q.MethodName = ep.Name
			if len(ep.QueryIDs) > 1 {
				q.MethodName = fmt.Sprintf("%s%d", ep.Name, methodSfx)
			}
			q.SigArgs = sigArgs(qp)
			q.Args = callArgs(qp)
			q.OracleArgs = oracleArgs(qp)
			if qp.DML {
				q.VarName = "affected"
				if i == 0 {
					q.CallLHS = "var " + q.VarName
				} else {
					q.CallLHS = fmt.Sprintf("var %s%d", q.VarName, methodSfx)
					q.VarName = fmt.Sprintf("%s%d", q.VarName, methodSfx)
				}
			} else {
				q.VarName = "tblRes"
				if i == 0 {
					q.CallLHS = "var " + q.VarName
				} else {
					q.CallLHS = fmt.Sprintf("var %s%d", q.VarName, methodSfx)
					q.VarName = fmt.Sprintf("%s%d", q.VarName, methodSfx)
				}
				for _, pr := range qp.RowProps {
					if !seenProp[pr.Name] {
						seenProp[pr.Name] = true
						props = append(props, pr.Name)
					}
				}
				q.Props = propNames(qp.RowProps)
				if !hasSelect {
					hasSelect = true
					if qp.Kind == "select-multi" {
						q.MapMulti = true
						data.RetType = "List<" + ep.DTOName + ">"
					} else {
						q.MapSingle = true
						data.RetType = ep.DTOName
					}
					q.LogLine = logLine(ep.Name, q.Props, data.RetType)
				}
			}
			data.Queries = append(data.Queries, q)
			flat = append(flat, q)
		}
		data.Props = props
		switch {
		case hasSelect:
			data.ReturnExpr = "response"
		case len(data.Queries) > 0 && data.Queries[0].DML:
			data.ReturnExpr = "affected"
			data.RetType = "int"
		default:
			data.ReturnExpr = "response"
			data.RetType = "int"
		}
		eps = append(eps, data)
	}
	return eps, flat
}

// sigArgs renders the repo method's parameter list: "string MobileNo, "
// per mapped param ("" when the query binds nothing).
func sigArgs(q csplan.QueryPlan) string {
	var sb strings.Builder
	for _, prm := range q.Params {
		sb.WriteString("string ")
		sb.WriteString(prm.Name)
		sb.WriteString(", ")
	}
	return sb.String()
}

// callArgs renders the service-side argument list: the request property
// when mapped, else a loud TODO null (the arm's residual logic supplies
// it — the LLM seam's job).
func callArgs(q csplan.QueryPlan) string {
	var parts []string
	for _, prm := range q.Params {
		if prm.RequestProp != "" {
			parts = append(parts, "request."+prm.RequestProp)
		} else {
			parts = append(parts, fmt.Sprintf("null /* tuxgo:TODO supply %s */", prm.Name))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ") + ", "
}

// oracleArgs renders the OracleParameter list. The bind name stays the
// source's own host var — the SQL const is verbatim, so the parameter
// must match it exactly.
func oracleArgs(q csplan.QueryPlan) string {
	var parts []string
	for _, prm := range q.Params {
		parts = append(parts, fmt.Sprintf("new OracleParameter(%q, %s)", prm.Bind, prm.Name))
	}
	return strings.Join(parts, ", ")
}

// propNames extracts the DTO property names of a query's row shape.
func propNames(props []csplan.Prop) []string {
	out := make([]string, 0, len(props))
	for _, pr := range props {
		out = append(out, pr.Name)
	}
	return out
}

// logLine pre-renders the structured-log statement for a mapped select:
// one placeholder per DTO property, the response fields as args
// (List-valued selects log the row count instead).
func logLine(action string, props []string, retType string) string {
	if strings.HasPrefix(retType, "List<") {
		return fmt.Sprintf("%q, response.Count", action+" rows fetched: {Count}")
	}
	if len(props) == 0 {
		return fmt.Sprintf("%q", action+" completed")
	}
	var heads, args []string
	for _, pr := range props {
		heads = append(heads, pr+"={"+pr+"}")
		args = append(args, "response."+pr)
	}
	return fmt.Sprintf("%q, %s", action+": "+strings.Join(heads, ", "), strings.Join(args, ", "))
}
