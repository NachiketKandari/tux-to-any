package testgen

import (
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/testscan"
)

// TestOutPathForInPlace pins the same-folder contract: an empty BaseDir
// writes the test file into the same folder as the converted code that was
// scanned (the layer dir itself), so `gentest <converted-tree>` lands next
// to the conversion output without a -base override.
func TestOutPathForInPlace(t *testing.T) {
	sc := &serviceCtx{name: "nav", dir: "pkg/services/nav", moduleRoot: "/tree"}
	got, err := outPathFor("", sc, "pkg/services/nav/db", "nav_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("pkg/services/nav/db", "nav_test.go"); got != want {
		t.Errorf("in-place path = %q, want %q", got, want)
	}
}

// TestRenderDBMethodPlainSingleNoRows pins the per-query-kind case table:
// a plain single-row GetContext read tolerates sql.ErrNoRows (the converted
// method returns the zero struct with a nil error), so its block carries
// the Success-NoRows case with the zero-struct expectation — while a
// multi-row SelectContext read does not.
func TestRenderDBMethodPlainSingleNoRows(t *testing.T) {
	sc := &serviceCtx{
		name: "nav",
		models: &modelsInfo{Structs: map[string][]fieldInfo{
			"NavDetails": {
				{Name: "CompCd", Type: "sql.NullString", DB: "COMP_CD"},
				{Name: "CompName", Type: "sql.NullString", DB: "COMP_NAME"},
			},
		}},
		fixtures: &AssumedFixtureSource{Models: &modelsInfo{Structs: map[string][]fieldInfo{
			"NavDetails": {
				{Name: "CompCd", Type: "sql.NullString", DB: "COMP_CD"},
				{Name: "CompName", Type: "sql.NullString", DB: "COMP_NAME"},
			},
		}}},
	}
	sc.fixtures = &AssumedFixtureSource{Models: sc.models}

	single := &unit{
		sc: sc, layer: "db", dir: "pkg/services/nav/db",
		outFile: "nav_test.go", suite: "NavStoreSuite",
		fn: testscan.Func{Name: "GetNavDetail"},
		db: &dbFact{
			Name: "GetNavDetail", CtxName: "ctx",
			Params:     []Param{{Name: "ctx", Type: "context.Context"}, {Name: "compCd", Type: "string"}},
			Query:      "SELECT COMP_CD FROM DEMO_COMPANY WHERE COMP_CD = :1",
			Tables:     []string{"DEMO_COMPANY"},
			Shape:      "single",
			RowType:    "models.NavDetails",
			ReturnType: "*models.NavDetails",
		},
	}
	m, err := renderDBMethod(single)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"query := `SELECT COMP_CD FROM DEMO_COMPANY WHERE COMP_CD = :1`",
		"ExpectQuery(regexp.QuoteMeta(query))",
		`desc:           "Success-NoRows"`,
		"expectedOutput: &models.NavDetails{}",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("single block missing %q\n---\n%s", want, m)
		}
	}

	multi := &unit{
		sc: sc, layer: "db", dir: "pkg/services/nav/db",
		outFile: "nav_test.go", suite: "NavStoreSuite",
		fn: testscan.Func{Name: "GetNavDetails"},
		db: &dbFact{
			Name: "GetNavDetails", CtxName: "ctx",
			Params:     []Param{{Name: "ctx", Type: "context.Context"}, {Name: "compCd", Type: "string"}},
			Query:      "SELECT COMP_CD FROM DEMO_COMPANY WHERE COMP_CD = :1",
			Tables:     []string{"DEMO_COMPANY"},
			Shape:      "multi",
			RowType:    "models.NavDetails",
			ReturnType: "[]*models.NavDetails",
		},
	}
	mm, err := renderDBMethod(multi)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mm, "Success-NoRows") {
		t.Errorf("multi block must not carry the NoRows case (empty slice is success):\n%s", mm)
	}
	if !strings.Contains(mm, "ExpectQuery(regexp.QuoteMeta(query))") {
		t.Errorf("multi block must reuse the query variable:\n%s", mm)
	}
}
