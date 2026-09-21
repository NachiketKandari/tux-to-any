// Package archtest is the machine check for the layering law (PRD
// 2026-09-10-architecture-first §3, A6.3, ported from convert-tux-to-go):
// dependency direction only ever points downward. Forbidden arrows live in
// the tests below; every rule runs over `go list` output (stdlib-only, R4)
// and fails the test run when violated. A new violation = the layering law
// was broken — fix the import or amend the law deliberately.
package archtest

import (
	"os/exec"
	"strings"
	"testing"
)

const mod = "tux-to-any"

// deps maps each internal package to its internal dependencies (go list).
type deps map[string][]string

func loadDeps(t *testing.T) deps {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{join .Imports \",\"}}", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	d := deps{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		var internal []string
		if parts[1] != "" {
			for _, imp := range strings.Split(parts[1], ",") {
				if strings.HasPrefix(imp, mod+"/internal/") {
					internal = append(internal, imp)
				}
			}
		}
		d[parts[0]] = internal
	}
	return d
}

// internalPkg extracts "plan" from the module's internal import path.
func internalPkg(importPath string) string {
	rel, ok := strings.CutPrefix(importPath, mod+"/internal/")
	if !ok {
		return ""
	}
	if i := strings.Index(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return rel
}

// TestNoPackageImportsCmd is forbidden arrow 1: nothing imports cmd/…
// (shared seams live under internal/).
func TestNoPackageImportsCmd(t *testing.T) {
	for pkg, imps := range loadDeps(t) {
		for _, imp := range imps {
			if strings.Contains(imp, "/cmd/") {
				t.Errorf("%s imports %s — cmd is pure wiring, imported by nothing", pkg, imp)
			}
		}
	}
}

// TestGenPlanNeverLLM is forbidden arrow 2: plan and gen never import llm —
// the determinism contract ("generation is zero-LLM") as an import fact.
// Scoped to plan/gen only: csgen and pygen legitimately consume the LLM
// seam for service bodies, so they stay out of this rule by design.
func TestGenPlanNeverLLM(t *testing.T) {
	d := loadDeps(t)
	for _, pkg := range []string{"plan", "gen"} {
		for _, imp := range d[mod+"/internal/"+pkg] {
			if imp == mod+"/internal/llm" {
				t.Errorf("%s imports llm — generation is zero-LLM by contract", pkg)
			}
		}
	}
}

// TestCommonIsLeaf is the common package law (AD1): internal/common imports
// no internal package — it is the leaf-of-leaves so the parse stack can
// always depend on it.
func TestCommonIsLeaf(t *testing.T) {
	for _, imp := range loadDeps(t)[mod+"/internal/common"] {
		t.Errorf("internal/common imports %s — the leaf law forbids internal imports", imp)
	}
}

// TestProfilesLiveAboveSharedCore is the kind-separation rule (A8.2): the
// Go-service and Python-batch drivers never import each other's stacks —
// profile drivers stay thin per kind over the shared core. A violation
// means Go-shaped facts leaked into the batch stack (or reverse).
func TestProfilesLiveAboveSharedCore(t *testing.T) {
	d := loadDeps(t)
	goOnly := map[string]bool{
		"plan": true, "gen": true, "convert": true, "testgen": true, "testscan": true,
	}
	batchOnly := map[string]bool{
		"batchflow": true, "pyplan": true, "pygen": true, "pychk": true,
	}
	for pkg, imps := range d {
		base := internalPkg(pkg)
		if base == "" {
			continue
		}
		for _, imp := range imps {
			impBase := internalPkg(imp)
			if goOnly[base] && batchOnly[impBase] {
				t.Errorf("%s imports %s — Go-kind and batch-kind drivers never mix", base, impBase)
			}
			if batchOnly[base] && goOnly[impBase] {
				t.Errorf("%s imports %s — batch-kind and Go-kind drivers never mix", base, impBase)
			}
		}
	}
}

// TestParseStackNeverLooksUp keeps the parse stack language-neutral
// (uniform-ir plan §3.2): tsscan/ir/pred/flow/sqltext/contract never import
// an emitter (Go, Python-batch, or C#). The one sanctioned upward edge is
// none — emitters project from the stack, never the reverse.
func TestParseStackNeverLooksUp(t *testing.T) {
	d := loadDeps(t)
	stack := map[string]bool{
		"tsscan": true, "ir": true, "pred": true, "flow": true,
		"sqltext": true, "contract": true,
	}
	forbidden := map[string]bool{
		"gen": true, "convert": true, "testgen": true, "testscan": true,
		"pyplan": true, "pygen": true, "pychk": true, "batchflow": true,
		"csplan": true, "csgen": true, "csdraft": true, "cschk": true,
	}
	for pkg, imps := range d {
		base := internalPkg(pkg)
		if !stack[base] {
			continue
		}
		for _, imp := range imps {
			impBase := internalPkg(imp)
			if forbidden[impBase] {
				t.Errorf("%s imports %s — the parse stack never looks up", base, impBase)
			}
		}
	}
}

// TestCsEmittersStaySeparate keeps the C# drivers from entangling with the
// Go/Python emitters: csplan/csgen/csdraft/cschk never import the Go,
// batch, or test emitters. The one sanctioned cross-kind edge is
// csplan→plan (the shared scenarioRef/mapping-schema helpers pending the
// uniform-IR Phase 6 unification) — same-kind (cs→cs) and horizontal
// (common, llm, budget, templates, …) edges stay free.
func TestCsEmittersStaySeparate(t *testing.T) {
	d := loadDeps(t)
	cs := map[string]bool{
		"csplan": true, "csgen": true, "csdraft": true, "cschk": true,
	}
	forbidden := map[string]bool{
		"gen": true, "convert": true, "testgen": true, "testscan": true,
		"pyplan": true, "pygen": true, "pychk": true, "batchflow": true,
	}
	for pkg, imps := range d {
		base := internalPkg(pkg)
		if !cs[base] {
			continue
		}
		for _, imp := range imps {
			if forbidden[internalPkg(imp)] {
				t.Errorf("%s imports %s — C# emitters never entangle with Go/batch emitters", base, internalPkg(imp))
			}
		}
	}
	// Reverse direction: no non-cs internal emitter reaches into cs
	// (cmd/* wiring, which `go list ./...` from here does not enumerate,
	// stays the sanctioned consumer of every emitter).
	for pkg, imps := range d {
		base := internalPkg(pkg)
		if base == "" || cs[base] {
			continue
		}
		for _, imp := range imps {
			if impBase := internalPkg(imp); cs[impBase] {
				t.Errorf("%s imports %s — only cmd wiring consumes the C# emitters", base, impBase)
			}
		}
	}
}
