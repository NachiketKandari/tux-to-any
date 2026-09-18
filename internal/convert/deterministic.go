package convert

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/common"
	"tux-to-any/internal/gen"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
	"tux-to-any/internal/telemetry"
)

// Deterministic no-llm bodies (gen.DeterministicControllerBody /
// DeterministicFnHelperBody) land through the same append/validate/ledger
// path as LLM output, minus the seam: the body is synthesized, gated with
// the parse-level subset of the seam gates, and recorded as skipped (an
// LLM-enabled resume upgrades exactly these marker-carrying methods).

// deterministicController renders and gates one endpoint's deterministic
// body. Any error degrades to the legacy skip — a deterministic gap must
// never fail a --no-llm run.
func deterministicController(ctx context.Context, opts Options, svc *gen.Service, unitName string) (string, error) {
	body, err := svc.DeterministicControllerBody(unitName, opts.Plan)
	if err != nil {
		return "", err
	}
	c := svc.ConditionOf(unitName)
	if c == nil {
		return "", fmt.Errorf("convert: no condition for endpoint %s", unitName)
	}
	_, calls, err := svc.BranchCalls(c, opts.Plan)
	if err != nil {
		return "", err
	}
	if errs := detControllerGates(opts, body, c, calls); len(errs) > 0 {
		telemetry.Log(ctx).Warn("deterministic controller failed its gates — leaving skipped",
			"unit", unitName, "errors", strings.Join(errs, "; "))
		return "", fmt.Errorf("convert: deterministic body for %s failed its gates: %s", unitName, strings.Join(errs, "; "))
	}
	return body, nil
}

// detControllerGates runs the parse-level subset of the seam gates over a
// deterministic body: parse/shape, required calls (from the resolved call
// map, not a prompt view), tuxedo, tx, and the empty-if transparency gate.
// The condition-census gate stays LLM-only (no draft is rendered here).
func detControllerGates(opts Options, body string, c *ir.Condition, calls map[string]budget.DBCall) []string {
	var errs []string
	errs = append(errs, validateBody(opts, body)...)
	receiver := receiverOf(opts)
	for _, want := range detRequiredStoreCalls(calls, receiver) {
		if !strings.Contains(body, want) {
			errs = append(errs, "orchestration contract: "+strings.TrimSuffix(want, "(")+" missing from the deterministic body")
		}
	}
	for _, want := range detRequiredHelpers(opts, c) {
		if !strings.Contains(body, want) {
			errs = append(errs, "orchestration contract: "+strings.TrimSuffix(want, "(")+" missing from the deterministic body")
		}
	}
	errs = append(errs, controllerTuxedoErrs(body)...)
	errs = append(errs, txGateErrs(body, calls)...)
	errs = append(errs, emptyIfErrs(body)...)
	return errs
}

// deterministicFnHelper renders and gates one fn helper's deterministic
// method. Any error degrades to the legacy skip.
func deterministicFnHelper(ctx context.Context, opts Options, svc *gen.Service, goName string) (string, error) {
	body, err := svc.DeterministicFnHelperBody(goName, opts.Plan)
	if err != nil {
		return "", err
	}
	h, ok := fnHelperOf(opts.Plan, goName)
	if !ok {
		return "", fmt.Errorf("convert: no fn record for helper %s", goName)
	}
	calls, err := svc.StoreCallsFor(detHelperQueryIDs(opts.Plan, goName), opts.Plan)
	if err != nil {
		return "", err
	}
	structName := common.LowerFirst(svc.Mapping.Service) + "Controller"
	view := detSynthView(calls)
	if errs := fnHelperGate(body, h, structName, view, receiverOf(opts)); len(errs) > 0 {
		telemetry.Log(ctx).Warn("deterministic fn helper failed its gates — leaving skipped",
			"unit", goName, "errors", strings.Join(errs, "; "))
		return "", fmt.Errorf("convert: deterministic helper %s failed its gates: %s", goName, strings.Join(errs, "; "))
	}
	return body, nil
}

// detHelperQueryIDs returns the plan unit's query IDs for a fn helper.
func detHelperQueryIDs(p *plan.Plan, goName string) []string {
	for _, u := range p.Units {
		if u.Kind == plan.KindFnHelper && u.Name == goName {
			return u.QueryIDs
		}
	}
	return nil
}

// detControllerState reports whether the controller file carries a live
// method for the unit and whether that method is a deterministic no-llm
// body (the LLM-upgrade signal).
func detControllerState(path, name string) (landed, deterministic bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	src := string(data)
	return hasLiveMethod(src, name), gen.IsDeterministicControllerMethod(src, name)
}

// detControllerLanded reports whether the controller file's live method
// for the unit is deterministic.
func detControllerLanded(path, name string) bool {
	_, det := detControllerState(path, name)
	return det
}

// detFnHelperState is the fn-helper counterpart of detControllerState.
func detFnHelperState(path, name string) (landed, deterministic bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	src := string(data)
	return hasLiveMethod(src, name), gen.IsDeterministicFnHelper(src, name)
}

// detFnHelperLanded reports whether the fn file's live method for the
// helper is deterministic.
func detFnHelperLanded(path, name string) bool {
	_, det := detFnHelperState(path, name)
	return det
}

// stripMethodByName removes the live method named name from a Go source
// file (offsets from the parse tree — no brace counting). It lets an
// LLM-accepted body replace a deterministic placeholder instead of stacking
// a duplicate; formatting is normalized downstream by goast.Emit.
func stripMethodByName(src, name string) (string, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ctrl.go", src, parser.SkipObjectResolution)
	if err != nil {
		return src, false
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name.Name != name {
			continue
		}
		start := fset.Position(fd.Pos()).Offset
		end := fset.Position(fd.End()).Offset
		if start < 0 || end > len(src) || start >= end {
			return src, false
		}
		rest := strings.TrimLeft(src[end:], "\n")
		head := strings.TrimRight(src[:start], " \t")
		return head + rest, true
	}
	return src, false
}

// detRequiredStoreCalls lists the `recv.Name(` spellings every store call
// contributes (deterministic order).
func detRequiredStoreCalls(calls map[string]budget.DBCall, receiver string) []string {
	var names []string
	seen := map[string]bool{}
	for _, call := range calls {
		recv := call.Receiver
		if recv == "" {
			recv = strings.TrimSuffix(receiver, ".")
		}
		want := recv + "." + call.Name + "("
		if !seen[want] {
			seen[want] = true
			names = append(names, want)
		}
	}
	sort.Strings(names)
	return names
}

// detRequiredHelpers lists the `s.GoName(` spellings the branch's
// same-file helper calls contribute: plan helpers whose legacy name the
// branch source calls.
func detRequiredHelpers(opts Options, c *ir.Condition) []string {
	if opts.Plan == nil || c == nil {
		return nil
	}
	src := branchSource(opts.Source, c.StartLine, c.EndLine)
	var out []string
	seen := map[string]bool{}
	for _, h := range opts.Plan.FnHelpers {
		if !strings.Contains(src, h.Name+"(") {
			continue
		}
		want := "s." + h.GoName + "("
		if !seen[want] {
			seen[want] = true
			out = append(out, want)
		}
	}
	sort.Strings(out)
	return out
}

// detSynthView renders the synthetic view the fn-helper required-call gate
// consumes: one call line per resolved store call (the gate extracts the
// same required set the synthesizer emitted).
func detSynthView(calls map[string]budget.DBCall) string {
	lines := make([]string, 0, len(calls))
	for _, call := range calls {
		lines = append(lines, call.Line())
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
