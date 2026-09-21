package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationSurfaceAudit is the Phase 0 read-only audit (uniform-ir plan
// §4 Phase 0): it documents how far the backend migration has come — every
// backend still reads raw ir today, the deprecated shims live only in ir,
// and the parity tests cover the projections. It asserts direction, never
// exact counts, so deadline-day refactors move it monotonically instead of
// churning it.
func TestMigrationSurfaceAudit(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("repo root not found: %v", err)
	}
	importsIR := map[string]bool{}
	for _, pkg := range []string{"gen", "pygen", "csgen", "pyplan", "csplan", "plan"} {
		importsIR[pkg] = packageImportsIR(filepath.Join(root, "internal", pkg))
		t.Logf("internal/%s imports internal/ir: %v", pkg, importsIR[pkg])
	}
	// The migration direction: contract and namer are the only new-code
	// homes for derivation. Both must keep importing ir (they project
	// FROM it) — the assertion is that backends gain parity tests, not
	// that they drop ir overnight.
	for _, pkg := range []string{"csplan", "pyplan"} {
		if !hasFile(filepath.Join(root, "internal", pkg), "contract_parity_test.go") {
			t.Errorf("internal/%s lacks contract_parity_test.go", pkg)
		}
	}

	// Deprecated shims are read only by the legacy carriers: ir (defines
	// them), gen (their new home: GoTypeFor/TemplateFor), contract (docs
	// the zero-language-names rule), plan + convert (thread TemplateID as
	// opaque unit data set from the old values — converge in Phase 4).
	// Readers anywhere else are new-code drift back onto the old seams.
	shimReaders := filesWithToken(root, []string{"internal"}, "TemplateID", "GoHint")
	for _, f := range shimReaders {
		rel, _ := filepath.Rel(root, f)
		if strings.HasPrefix(rel, "internal/ir/") ||
			strings.HasPrefix(rel, "internal/gen/") ||
			strings.HasPrefix(rel, "internal/contract/") ||
			strings.HasPrefix(rel, "internal/plan/") ||
			strings.HasPrefix(rel, "internal/convert/") {
			continue
		}
		t.Errorf("deprecated shim read outside ir/gen/contract/plan/convert: %s", rel)
	}
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func packageImportsIR(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, e.Name(), src, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "tux-to-any/internal/ir" {
				return true
			}
		}
	}
	return false
}

func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func filesWithToken(root string, pkgs []string, tokens ...string) []string {
	var out []string
	for _, pkg := range pkgs {
		_ = filepath.Walk(filepath.Join(root, pkg), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				return nil
			}
			found := false
			ast.Inspect(f, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || found {
					return !found
				}
				for _, tok := range tokens {
					if id.Name == tok {
						found = true
						return false
					}
				}
				return true
			})
			if found {
				out = append(out, path)
			}
			return nil
		})
	}
	return out
}
