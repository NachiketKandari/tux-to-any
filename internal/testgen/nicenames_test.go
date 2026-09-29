package testgen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/llm"
)

// TestPolishNamesGate pins the names-only contract: string literals and
// comments may change, any other token must match exactly.
func TestPolishNamesGate(t *testing.T) {
	block := `func (suite *S) TestX() {
	testCases := []struct{ desc string }{{desc: "old"}}
	_ = testCases
}`
	good := `func (suite *S) TestX() {
	// clearer comment: the row store case
	testCases := []struct{ desc string }{{desc: "stores the row"}}
	_ = testCases
}`
	if err := sameStructureExceptStrings(block, good); err != nil {
		t.Errorf("string/comment-only change rejected: %v", err)
	}
	renamed := strings.Replace(block, "testCase", "tc", 1)
	if err := sameStructureExceptStrings(block, renamed); err == nil {
		t.Error("identifier rename must be rejected")
	}
	extra := block + "\nfunc (suite *S) TestY() {}"
	if err := sameStructureExceptStrings(block, extra); err == nil {
		t.Error("added function must be rejected")
	}
	if err := sameStructureExceptStrings(block, "not go\n"+block); err == nil {
		t.Error("non-Go drift must be rejected")
	}
}

// TestPolishNamesSeam pins the best-effort behavior: a gate-rejected or empty
// response leaves the block untouched; an accepted polish replaces it.
func TestPolishNamesSeam(t *testing.T) {
	block := "func (suite *S) TestX() { testCases := []struct{ desc string }{{desc: \"old\"}}; _ = testCases }"
	srv := llm.NewFakeServer(
		llm.FakeResponse{Content: "```go\nfunc (suite *S) TestX() { testCases := []struct{ desc string }{{desc: \"nice name\"}}; _ = testCases }\n```"},
		llm.FakeResponse{Content: "```go\nfunc (suite *S) TestOther() {}\n```"},
		llm.FakeResponse{Content: "no code here"},
	)
	defer srv.Close()
	client := llm.New(llm.Endpoint{ProfileName: "test", Model: "m", APIBase: srv.URL})
	opts := Options{NiceNames: true, Client: client, Budget: budget.New(4096, 1024, 4)}

	got, err := polishNames(context.Background(), block, opts)
	if err != nil {
		t.Fatalf("accepted polish failed: %v", err)
	}
	if !strings.Contains(got, `"nice name"`) || !strings.Contains(got, "testCases := []struct{ desc string }") {
		t.Errorf("polished block = %q", got)
	}
	if _, err := polishNames(context.Background(), block, opts); err == nil {
		t.Error("structure-changing polish must be rejected")
	}
	if _, err := polishNames(context.Background(), block, opts); err == nil {
		t.Error("empty polish must be rejected")
	}
	// Disabled (or clientless) polish is a strict no-op.
	if out := (Options{}).polish(context.Background(), block); out != block {
		t.Error("disabled polish must not touch the block")
	}
}

// TestChecklistFile pins the advisory structural checklist.
func TestChecklistFile(t *testing.T) {
	clean := "func (suite *S) TestX() {\n testCases := []struct{}{}\nfor _, testCase := range testCases {\n suite.sqlMock.ExpectQuery(\"q\")\n assert.ErrorContains(t, err, \"x\")\nassert.NoError(t, err)\n}\n}"
	if got := checklistFile("db", clean); len(got) != 0 {
		t.Errorf("clean db file flagged: %v", got)
	}
	if got := checklistFile("db", "package db"); len(got) == 0 {
		t.Error("shapeless db file must be flagged")
	}
	if got := checklistFile("controller", "func (suite *S) TestX() {\ntestCases := []struct{}{}\nsuite.store.EXPECT().M(gomock.Any()).Return(nil)\nassert.ErrorContains(t, err, \"x\")\nassert.NoError(t, err)\n}"); len(got) != 0 {
		t.Errorf("clean controller file flagged: %v", got)
	}
	if got := checklistFile("handler", "utils.CreateTestGinContext(...)\nassert.Equal(t, a, b)"); len(got) != 0 {
		t.Errorf("clean handler file flagged: %v", got)
	}
}

// TestStageSourcesCollision pins the -out snapshot rule: existing
// destination names are left untouched and the staged copy gets the
// _convertgo suffix.
func TestStageSourcesCollision(t *testing.T) {
	root := t.TempDir()
	svc := buildConvertedTree(t, root)
	out := filepath.Join(root, "_staged")
	colliding := filepath.Join(out, "pkg", "services", "nav", "db", "interface.go")
	if err := os.MkdirAll(filepath.Dir(colliding), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(colliding, []byte("// human file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{
		BaseDir: out, Workers: 1, NoLLM: true, Stage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range res.Staged {
		if strings.HasSuffix(f, "interface_convertgo.go") {
			found = true
		}
		if f == colliding {
			t.Errorf("staging overwrote the colliding destination: %s", f)
		}
	}
	if !found {
		t.Errorf("collision rename missing, staged: %v", res.Staged)
	}
	if got := read(t, colliding); got != "// human file\n" {
		t.Errorf("existing destination modified: %q", got)
	}
	var stagedMod bool
	for _, f := range res.Staged {
		if strings.HasSuffix(f, "go.mod") {
			stagedMod = true
		}
	}
	if !stagedMod {
		t.Errorf("go.mod not staged: %v", res.Staged)
	}
}

// TestFullTestGate pins the non-fatal full-run gate: a passing package does
// not flip TestsFailed, a failing one does.
func TestFullTestGate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gatecheck\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(dir, "x_test.go")
	if err := os.WriteFile(testFile, []byte("package gatecheck\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := &Result{Files: []string{testFile}}
	fullTestGate(res, Options{FullTest: true})
	if res.TestsFailed {
		t.Errorf("passing package flipped TestsFailed: %v", res.Gates)
	}
	if len(res.Gates) == 0 || !strings.Contains(res.Gates[0], ": PASS") {
		t.Errorf("passing gate line missing: %v", res.Gates)
	}

	if err := os.WriteFile(testFile, []byte("package gatecheck\n\nimport \"testing\"\n\nfunc TestBad(t *testing.T) { t.Fatal(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res2 := &Result{Files: []string{testFile}}
	fullTestGate(res2, Options{FullTest: true})
	if !res2.TestsFailed {
		t.Errorf("failing package did not flip TestsFailed: %v", res2.Gates)
	}
	if len(res2.Gates) == 0 || !strings.Contains(res2.Gates[0], ": FAILED") {
		t.Errorf("failing gate line missing: %v", res2.Gates)
	}
}
