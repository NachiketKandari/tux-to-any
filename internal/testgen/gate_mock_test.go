package testgen

// P0 pins (docs/gentest-fix-plan.md §2): the compile gate must be able to
// report a code error rather than a dependency error (F11), and a run must
// not mutate a tree that already ships its own doubles (F10).

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"tux-to-any/internal/gen"
)

// ---- fixture ----

// mockFixture writes a minimal but real one-service module: a go.mod, a db
// package whose interface.go declares the store, and an optional
// interface_mock.go standing in for a tree that ships its own double.
//
// withMock mirrors the riskprofile corpus, which hand-writes its doubles —
// that is the shape that produced "MockRiskProfileStore redeclared" when
// gentest generated a second one beside it.
func mockFixture(t *testing.T, withMock bool) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module fixture-be\n\ngo 1.21\n",
		filepath.Join("pkg", "services", "nav", "models", "models.go"): "package models\n\n" +
			"type NavDetails struct {\n" +
			"\tCompCd sql.NullString `db:\"COMP_CD\"`\n" +
			"}\n",
		filepath.Join("pkg", "services", "nav", "db", "interface.go"): "package db\n\n" +
			"import (\n\t\"context\"\n\n\t\"fixture-be/pkg/services/nav/models\"\n)\n\n" +
			"type NavStore interface {\n" +
			"\tGetNavDetails(ctx context.Context, compCd string) ([]*models.NavDetails, error)\n" +
			"}\n",
		filepath.Join("pkg", "services", "nav", "db", "nav.go"): "package db\n\n" +
			"import (\n\t\"context\"\n\n\t\"fixture-be/pkg/services/nav/models\"\n)\n\n" +
			"type store struct{ db *sqlx.DB }\n\n" +
			"func (g *store) GetNavDetails(ctx context.Context, compCd string) ([]*models.NavDetails, error) {\n" +
			"\tvar out []*models.NavDetails\n" +
			"\terr := g.db.SelectContext(ctx, &out, `SELECT 1`, compCd)\n" +
			"\tif err != nil {\n" +
			"\t\treturn nil, err\n" +
			"\t}\n" +
			"\treturn out, nil\n" +
			"}\n",
	}
	if withMock {
		files[filepath.Join("pkg", "services", "nav", "db", "interface_mock.go")] = `package db

// MockNavStore is the double the converted tree shipped with the service.
type MockNavStore struct{}
`
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// treeSnapshot returns every regular file under root as rel→content, so a
// test can assert a run left a tree byte-identical.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestInPlaceRunLeavesShippedMockTreeAlone is the F10 regression pin. An
// in-place gentest run over a tree that already declares MockNavStore must
// add nothing but its _test.go files: no mock_store.go beside the existing
// interface_mock.go (which redeclares the type and breaks the package), and
// no writes to any file it did not create. docs/RULES.md §6 forbids writing
// into the scanned tree, and before the fix this run wrote mock_store.go.
func TestInPlaceRunLeavesShippedMockTreeAlone(t *testing.T) {
	root := mockFixture(t, true)
	before := treeSnapshot(t, root)

	svc := filepath.Join(root, "pkg", "services", "nav")
	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{Workers: 1, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(svc, "db", "mock_store.go")); !os.IsNotExist(err) {
		t.Errorf("in-place run wrote mock_store.go next to the shipped interface_mock.go: the package would not compile")
	}

	after := treeSnapshot(t, root)
	var added, changed []string
	for rel, content := range after {
		prev, existed := before[rel]
		if !existed {
			added = append(added, rel)
			continue
		}
		if prev != content {
			changed = append(changed, rel)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	if len(changed) != 0 {
		t.Errorf("in-place run mutated existing files: %v", changed)
	}
	for _, rel := range added {
		if !strings.HasSuffix(rel, "_test.go") {
			t.Errorf("in-place run added a non-test file %q; only _test.go files are allowed", rel)
		}
	}
	_ = res
}

// TestInPlaceRunWarnsInsteadOfWritingMock covers the other half of F10: a
// tree with no double of its own still must not be mutated in place. The run
// reports the mockgen command instead, so the user is never left guessing
// why the generated suite does not compile.
func TestInPlaceRunWarnsInsteadOfWritingMock(t *testing.T) {
	root := mockFixture(t, false)
	before := treeSnapshot(t, root)

	svc := filepath.Join(root, "pkg", "services", "nav")
	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{Workers: 1, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(svc, "db", "mock_store.go")); !os.IsNotExist(err) {
		t.Errorf("in-place run wrote a mock into the scanned tree")
	}
	var warned bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "MockGen") && strings.Contains(w, "MockNavStore") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no missing-mock warning; got %v", res.Warnings)
	}
	for rel := range treeSnapshot(t, root) {
		if _, existed := before[rel]; !existed && !strings.HasSuffix(rel, "_test.go") {
			t.Errorf("in-place run added a non-test file %q", rel)
		}
	}
}

// TestMockAlreadyDeclared pins the redeclaration check itself: it reads the
// package's type declarations, not filenames, so a hand-written double under
// an unexpected name still suppresses a duplicate.
func TestMockAlreadyDeclared(t *testing.T) {
	root := mockFixture(t, true)
	dest := filepath.Join(root, "pkg", "services", "nav", "db", "mock_store.go")
	if !mockAlreadyDeclared(dest, "NavStore") {
		t.Errorf("MockNavStore declared under interface_mock.go not detected")
	}
	if mockAlreadyDeclared(dest, "OtherStore") {
		t.Errorf("reported a mock for an interface the package does not mock")
	}
	bare := t.TempDir()
	if mockAlreadyDeclared(filepath.Join(bare, "mock_store.go"), "NavStore") {
		t.Errorf("reported a mock in an empty directory")
	}
}

// TestTreeGomockFollowsTheModule pins the follow-the-tree half of F10: the
// generated mock must land in the gomock major the module vendors, because
// one package cannot hold both.
func TestTreeGomockFollowsTheModule(t *testing.T) {
	appendRequire := func(root, line string) {
		gomod := filepath.Join(root, "go.mod")
		b, err := os.ReadFile(gomod)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(gomod, append(b, []byte(line)...), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	legacy := mockFixture(t, false)
	appendRequire(legacy, "\nrequire github.com/golang/mock v1.6.0\n")
	if got := treeGomock(&serviceCtx{name: "nav", moduleRoot: legacy}); got != gen.LegacyGomockPath {
		t.Errorf("legacy-vendoring module: got %q, want %q", got, gen.LegacyGomockPath)
	}

	uber := mockFixture(t, false)
	appendRequire(uber, "\nrequire go.uber.org/mock v0.6.0\n")
	if got := treeGomock(&serviceCtx{name: "nav", moduleRoot: uber}); got != "" {
		t.Errorf("uber-vendoring module: got %q, want the uber default", got)
	}

	if got := treeGomock(&serviceCtx{name: "nav", moduleRoot: t.TempDir()}); got != "" {
		t.Errorf("no evidence at all: got %q, want the uber default", got)
	}
}

// TestCompileGateResolvesStagedDeps pins F11's contract in the shape that
// matters: under -out the gate resolves the staged module's dependencies and
// then reports a *code* error as a code error. Without the resolution step
// every line reads "missing go.sum entry" and the real compile error is
// invisible — the gate that cannot tell a dependency problem from a code
// problem cannot be used to judge whether a later fix worked.
func TestCompileGateResolvesStagedDeps(t *testing.T) {
	src := mockFixture(t, false)
	// A deliberate compile error in the store body: declared but undefined.
	broken := filepath.Join(src, "pkg", "services", "nav", "db", "nav.go")
	body, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, append(body, []byte("\nfunc brokenMethod() { undefinedSymbol() }\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	svc := filepath.Join(src, "pkg", "services", "nav")
	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{BaseDir: out, Workers: 1, NoLLM: true, Stage: true})
	if err != nil {
		t.Fatal(err)
	}

	var sawResolve, sawCodeGate bool
	for _, g := range res.Gates {
		if strings.Contains(g, "go mod tidy") || strings.Contains(g, "go mod download") {
			sawResolve = true
		}
		if strings.Contains(g, "go vet") || strings.Contains(g, "go test") {
			sawCodeGate = true
		}
	}
	if !sawResolve {
		t.Errorf("staged run emitted no dependency-resolution gate line; gates = %v", res.Gates)
	}
	// The dependency step must not replace the code gates: running it is
	// what makes them meaningful, and they still have to run.
	if !sawCodeGate {
		t.Errorf("dependency resolution displaced the code gates; gates = %v", res.Gates)
	}
}

// TestCompileGateInPlaceDoesNotResolveDeps pins the scope constraint on F11:
// the resolution step writes go.mod/go.sum, so it must never run against a
// user's own module.
func TestCompileGateInPlaceDoesNotResolveDeps(t *testing.T) {
	root := mockFixture(t, false)
	svc := filepath.Join(root, "pkg", "services", "nav")
	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{Workers: 1, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res.Gates {
		if strings.Contains(g, "go mod tidy") || strings.Contains(g, "go mod download") {
			t.Errorf("in-place run resolved module deps (writes go.mod/go.sum into the user's tree): %q", g)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Errorf("in-place run wrote a go.sum into the scanned tree")
	}
}
