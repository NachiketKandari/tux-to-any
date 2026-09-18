package convert

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"tux-to-any/internal/common"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/pred"
)

// parseWrappedFile parses the synthetic wrap; nil on parse failure (the
// parse gate owns those bodies).
func parseWrappedFile(fset *token.FileSet, wrapped string) (*ast.File, error) {
	return parser.ParseFile(fset, "body.go", wrapped, 0)
}

// emptyIfErrs unconditionally rejects `if cond {}` bodies with no statements
// and no else — the exact staged shape an OR-arm merge failure leaves
// behind. Deterministic over the same synthetic wrap validateBody uses, so
// positions come back body-relative.
func emptyIfErrs(body string) []string {
	fset, block, offset, ok := parseBodyWrapped(body)
	if !ok {
		return nil
	}
	var errs []string
	ast.Inspect(block, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if ifs.Body != nil && len(ifs.Body.List) == 0 && ifs.Else == nil {
			line := fset.Position(ifs.Pos()).Line - offset
			errs = append(errs, fmt.Sprintf("line %d: empty if — the branch carries no statements and no else; implement the legacy branch with its effect instead of leaving the header", line))
		}
		return true
	})
	return errs
}

// conditionPresenceErrs requires every census condition to show up in the
// body: some IfStmt whose condition is skeleton-equal or shares ≥1
// normalized identifier, and whose body (or else-chain) assigns every
// census Effect. Misses feed a targeted retry note naming the lost source
// line. An empty census (or an unparseable body, which the parse gate owns)
// passes silently.
func conditionPresenceErrs(census []flow.CensusCond, body string) []string {
	if len(census) == 0 {
		return nil
	}
	wrapped := bodyParseWrap(body)
	fset := token.NewFileSet()
	f, err := parseWrappedFile(fset, wrapped)
	if err != nil {
		return nil
	}
	var block *ast.BlockStmt
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			block = fd.Body
			break
		}
	}
	if block == nil {
		return nil
	}
	type goIf struct {
		skeleton string
		idents   map[string]bool
		assigned map[string]bool
	}
	var ifs []goIf
	ast.Inspect(block, func(n ast.Node) bool {
		ist, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		condText := condSource(wrapped, fset, ist)
		pe := pred.Parse(condText)
		g := goIf{
			skeleton: flow.Skeleton(&pe),
			idents:   map[string]bool{},
			assigned: map[string]bool{},
		}
		for _, id := range flow.ExprIdents(&pe) {
			g.idents[common.CamelLowerGo(id)] = true
		}
		collectIfAssigns(ist, g.assigned)
		ifs = append(ifs, g)
		return true
	})
	var errs []string
	for _, c := range census {
		wantIdents := map[string]bool{}
		for _, id := range c.Idents {
			wantIdents[common.CamelLowerGo(id)] = true
		}
		wantEffects := map[string]bool{}
		for _, e := range c.Effects {
			wantEffects[common.CamelLowerGo(e)] = true
		}
		satisfied := false
		for _, g := range ifs {
			match := g.skeleton != "" && g.skeleton == c.Skeleton
			if !match {
				for id := range wantIdents {
					if g.idents[id] {
						match = true
						break
					}
				}
			}
			if !match {
				continue
			}
			covers := true
			for e := range wantEffects {
				if !g.assigned[e] {
					covers = false
					break
				}
			}
			if covers {
				satisfied = true
				break
			}
		}
		if !satisfied {
			errs = append(errs, fmt.Sprintf("condition at line %d (`%s`) lost — implement the branch with its effect", c.Line, c.Cond))
		}
	}
	return errs
}

// isConditionGap reports whether a gate note comes from the condition
// transparency gates (empty-if or lost-condition), for run-level surfacing.
func isConditionGap(note string) bool {
	return strings.Contains(note, "empty if —") ||
		strings.Contains(note, "lost — implement the branch")
}

// recordConditionGaps appends a unit's condition-transparency notes to the
// run-level ConditionGaps (deduped per unit — retries repeat the same miss).
func recordConditionGaps(res *Result, u plan.Unit, notes []string) {
	seen := map[string]bool{}
	for _, g := range res.ConditionGaps {
		seen[g] = true
	}
	for _, n := range notes {
		// A combined-gate error joins several notes with "; " — split so
		// each gap reads on its own line in the summary.
		for _, part := range strings.Split(n, "; ") {
			part = strings.TrimSpace(part)
			if part == "" || !isConditionGap(part) {
				continue
			}
			entry := u.Name + ": " + part
			if !seen[entry] {
				seen[entry] = true
				res.ConditionGaps = append(res.ConditionGaps, entry)
			}
		}
	}
}

// conditionCensusRecord is the per-unit condition-census.json audit payload:
// the legacy census the draft walk collected plus the gap notes (if any) —
// never write-only, the ledger failure note points at it.
type conditionCensusRecord struct {
	Unit       string            `json:"unit"`
	Conditions []flow.CensusCond `json:"conditions"`
	Gaps       []string          `json:"gaps,omitempty"`
}

// writeConditionCensusAudit records one unit's census trail. It runs only
// when a census exists (the draft degrade contract) — scenario slices and
// draft-off runs leave no file.
func writeConditionCensusAudit(ctx context.Context, opts Options, u plan.Unit, census []flow.CensusCond, notes []string) {
	if len(census) == 0 {
		return
	}
	var gaps []string
	for _, n := range notes {
		for _, part := range strings.Split(n, "; ") {
			part = strings.TrimSpace(part)
			if part != "" && isConditionGap(part) {
				gaps = append(gaps, part)
			}
		}
	}
	writeAuditJSON(ctx, opts, "condition-census-"+u.Name+".json", conditionCensusRecord{
		Unit:       u.Name,
		Conditions: census,
		Gaps:       gaps,
	})
}

// condSource slices a condition's source text from the wrapped file via its
// token offsets.
func condSource(wrapped string, fset *token.FileSet, ist *ast.IfStmt) string {
	if ist.Cond == nil {
		return ""
	}
	start := fset.Position(ist.Cond.Pos()).Offset
	end := fset.Position(ist.Cond.End()).Offset
	if start < 0 || end > len(wrapped) || start >= end {
		return ""
	}
	return wrapped[start:end]
}

// collectIfAssigns records every identifier assigned in an IfStmt's body or
// else-chain (else blocks and else-if bodies recursively), normalized for
// the rename-robust comparison.
func collectIfAssigns(ist *ast.IfStmt, out map[string]bool) {
	if ist.Body != nil {
		collectBlockAssigns(ist.Body, out)
	}
	switch els := ist.Else.(type) {
	case *ast.BlockStmt:
		collectBlockAssigns(els, out)
	case *ast.IfStmt:
		collectIfAssigns(els, out)
	}
}

func collectBlockAssigns(b *ast.BlockStmt, out map[string]bool) {
	for _, s := range b.List {
		ast.Inspect(s, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range as.Lhs {
				for _, id := range assignTargetIdents(lhs) {
					out[common.CamelLowerGo(id)] = true
				}
			}
			return true
		})
	}
}

// assignTargetIdents extracts assigned names from an assignment target:
// plain identifiers and selector bases (`row.X` counts as X's statement —
// callers compare normalized effect names, and response shaping assigns
// through selectors).
func assignTargetIdents(e ast.Expr) []string {
	switch x := e.(type) {
	case *ast.Ident:
		return []string{x.Name}
	case *ast.SelectorExpr:
		return []string{x.Sel.Name}
	}
	return nil
}
