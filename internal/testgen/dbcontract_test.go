package testgen

// P1 pins (docs/gentest-fix-plan.md §3): the db layer's assertions must be
// derived from the method under test, not assumed, and the generated golden
// must actually be executable.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tux-to-any/internal/testscan"
)

// dbContractFixture writes one store method with the given body so the
// no-rows classification can be read from real source rather than a
// hand-built fact — the whole point of F2/F3 is that the contract comes from
// the body, so the pin must exercise the body too.
func dbContractFixture(t *testing.T, body string) *dbFact {
	t.Helper()
	// extractLayer takes the layer directory itself, so the file sits at the
	// root of the scratch dir rather than under a db/ subdirectory.
	dir := t.TempDir()
	src := "package db\n\nimport (\n\t\"context\"\n\n\t\"fx/models\"\n)\n\n" +
		"type store struct{ db *sqlx.DB }\n\n" +
		"func (g *store) M(ctx context.Context, k string) ([]*models.OrderDetails, error) {\n" +
		body + "\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "store.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f := extractLayer(dir, "db", nil).DB["M"]
	if f == nil {
		t.Fatalf("no db fact extracted from:\n%s", src)
	}
	return f
}

// TestNoRowsContractFromBody pins F3: the no-rows expectation is read from the
// method's own error branch. A propagating read must expect sql.ErrNoRows —
// assuming tolerance here is what made every generated no-rows case fail
// against the code it was generated from.
func TestNoRowsContractFromBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "propagating read",
			body: "\tvar out []*models.OrderDetails\n" +
				"\terr := g.db.SelectContext(ctx, &out, `SELECT 1`, k)\n" +
				"\tif err != nil {\n\t\treturn nil, err\n\t}\n" +
				"\treturn out, nil",
			want: "propagate",
		},
		{
			name: "tolerating read",
			body: "\tvar out models.OrderDetails\n" +
				"\terr := g.db.GetContext(ctx, &out, `SELECT 1`, k)\n" +
				"\tif err != nil {\n\t\treturn &out, nil\n\t}\n" +
				"\treturn &out, nil",
			want: "tolerate",
		},
		{
			name: "explicit ErrNoRows check tolerates",
			body: "\tvar out []*models.OrderDetails\n" +
				"\terr := g.db.SelectContext(ctx, &out, `SELECT 1`, k)\n" +
				"\tif errors.Is(err, sql.ErrNoRows) {\n\t\treturn nil, nil\n\t}\n" +
				"\tif err != nil {\n\t\treturn nil, err\n\t}\n" +
				"\treturn out, nil",
			want: "tolerate",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dbContractFixture(t, tc.body).NoRowsContract; got != tc.want {
				t.Errorf("NoRowsContract = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderDBMethodNoRowsError pins the rendered bytes, not just the fact: a
// propagating read's no-rows case asserts the sentinel, a tolerating one
// asserts nothing.
func TestRenderDBMethodNoRowsError(t *testing.T) {
	models := &modelsInfo{Structs: map[string][]fieldInfo{
		"OrderDetails": {{Name: "CompCd", Type: "sql.NullString", DB: "COMP_CD"}},
	}}
	sc := &serviceCtx{name: "demo", models: models}
	sc.fixtures = &AssumedFixtureSource{Models: models}

	render := func(contract string) string {
		u := &unit{
			sc: sc, layer: "db", dir: "db", outFile: "demo_test.go", suite: "DemoStoreSuite",
			fn: testscan.Func{Name: "GetOrderDetail"},
			db: &dbFact{
				Name: "GetOrderDetail", Shape: "single", RowType: "models.OrderDetails",
				Params: []Param{{Name: "ctx", Type: "context.Context"}, {Name: "k", Type: "string"}},
				Query:  "SELECT 1", Tables: []string{"T"},
				NoRowsContract: contract,
			},
		}
		m, err := renderDBMethod(u)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	propagate := render("propagate")
	if !strings.Contains(propagate, `expectedError:  "sql: no rows in result set"`) {
		t.Errorf("propagating read must expect the sentinel:\n%s", propagate)
	}
	tolerate := render("tolerate")
	if !strings.Contains(tolerate, `desc:           "Success-NoRows"`) {
		t.Errorf("tolerating read keeps its no-rows case:\n%s", tolerate)
	}
	if !strings.Contains(tolerate, `expectedError:  ""`) {
		t.Errorf("tolerating read must assert no error:\n%s", tolerate)
	}
}

// TestRenderDBMethodDMLNoRowsError pins F2: a DML method's zero-rows case
// asserts the error its own body returns — the domain message for the
// count-check shape, the sentinel for `return sql.ErrNoRows`.
func TestRenderDBMethodDMLNoRowsError(t *testing.T) {
	models := &modelsInfo{Structs: map[string][]fieldInfo{}}
	sc := &serviceCtx{name: "demo", models: models}
	sc.fixtures = &AssumedFixtureSource{Models: models}

	cases := []struct {
		name       string
		noRowsErr  string
		wantIn     string
		wantNotErr bool
	}{
		{name: "domain error", noRowsErr: "unable to add the question", wantIn: "unable to add the question"},
		{name: "sql sentinel", noRowsErr: "sql: no rows in result set", wantIn: "sql: no rows in result set"},
		// An unrecognised body keeps the pre-existing assertion rather than
		// silently downgrading the case to "no error".
		{name: "unrecognised body falls back", noRowsErr: "", wantIn: "sql: no rows in result set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &unit{
				sc: sc, layer: "db", dir: "db", outFile: "demo_test.go", suite: "DemoStoreSuite",
				fn: testscan.Func{Name: "AddQuestion"},
				db: &dbFact{
					Name: "AddQuestion", Shape: "dml",
					Params:      []Param{{Name: "ctx", Type: "context.Context"}, {Name: "q", Type: "string"}},
					Query:       "INSERT INTO DEMO_QN_A VALUES (:1)",
					NoRowsError: tc.noRowsErr,
				},
			}
			m, err := renderDBMethod(u)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(m, `desc:          "NoRows"`) {
				t.Errorf("no zero-rows case:\n%s", m)
			}
			if !strings.Contains(m, `expectedError: "`+tc.wantIn+`"`) {
				t.Errorf("zero-rows case must assert %q:\n%s", tc.wantIn, m)
			}
		})
	}
}

// TestDMLNoRowsErrorFromBody pins extraction of the two converted DML shapes.
func TestDMLNoRowsErrorFromBody(t *testing.T) {
	countShape := "\tcount, err := g.db.ExecContext(ctx, `INSERT INTO T VALUES (:1)`, k)\n" +
		"\tif err != nil {\n\t\treturn err\n\t}\n" +
		"\tif count > 0 {\n\t\treturn nil\n\t}\n" +
		"\treturn errors.New(\"unable to add the question\")"
	sentinelShape := "\tcount, err := g.db.ExecContext(ctx, `INSERT INTO T VALUES (:1)`, k)\n" +
		"\tif err != nil {\n\t\treturn err\n\t}\n" +
		"\tif count == 0 {\n\t\treturn sql.ErrNoRows\n\t}\n" +
		"\treturn nil"

	if got := dbContractFixture(t, countShape).NoRowsError; got != "unable to add the question" {
		t.Errorf("count-check shape: got %q, want the domain text", got)
	}
	if got := dbContractFixture(t, sentinelShape).NoRowsError; got != "sql: no rows in result set" {
		t.Errorf("sql.ErrNoRows shape: got %q, want the sentinel text", got)
	}
}

// TestDBCtorCallHandleVote pins F1: the live handle goes to the parameter the
// methods actually run on, chosen by majority vote over the receivers. Picking
// by position handed a nil handle to every read, so the suite panicked before
// asserting anything.
func TestDBCtorCallHandleVote(t *testing.T) {
	twoHandle := func(recvs ...string) *serviceCtx {
		db := map[string]*dbFact{}
		for i, r := range recvs {
			db[string(rune('A'+i))] = &dbFact{Name: string(rune('A' + i)), Shape: "multi", Recv: r}
		}
		return &serviceCtx{
			name: "rp",
			dbFacts: &layerFacts{
				DBCtor: "NewRiskProfileStore",
				DBCtorParams: []Param{
					{Name: "db", Type: "*sqlx.DB"},
					{Name: "writeDb", Type: "*sqlx.DB"},
				},
				DB: db,
			},
		}
	}

	// Reads dominate on the first handle.
	if got := dbCtorCall(twoHandle("g.db", "g.db", "g.db", "g.writeDb")); got != "NewRiskProfileStore(suite.sqlDB, nil)" {
		t.Errorf("majority on arg 1: got %q", got)
	}
	// Reads dominate on the second handle.
	if got := dbCtorCall(twoHandle("g.writeDb", "g.writeDb", "g.writeDb", "g.db")); got != "NewRiskProfileStore(nil, suite.sqlDB)" {
		t.Errorf("majority on arg 2: got %q", got)
	}
	// A single-argument constructor is unaffected.
	single := &serviceCtx{
		name:    "demo",
		dbFacts: &layerFacts{DBCtor: "NewDemoStore", DBCtorParams: []Param{{Name: "db", Type: "*sqlx.DB"}}},
	}
	if got := dbCtorCall(single); got != "NewDemoStore(suite.sqlDB)" {
		t.Errorf("single-handle ctor: got %q", got)
	}
	// A tx method runs on its own handle and must not vote.
	txOnly := &serviceCtx{
		name: "rp",
		dbFacts: &layerFacts{
			DBCtor:       "NewRiskProfileStore",
			DBCtorParams: []Param{{Name: "db", Type: "*sqlx.DB"}, {Name: "writeDb", Type: "*sqlx.DB"}},
			DB:           map[string]*dbFact{"A": {Name: "A", Shape: "multi", Recv: "g.db", IsTx: true}},
		},
	}
	if got := dbCtorCall(txOnly); got != "NewRiskProfileStore(suite.sqlDB, nil)" {
		t.Errorf("tx-only evidence must fall back to the first handle: got %q", got)
	}
}

// TestGoldenDBSuiteExecutes closes the gap the plan calls out in §5: no test
// in the repo ran the generated output, which is how F3 — a wrong no-rows
// expectation visible in the repo's own fixture — survived eleven findings
// worth of review.
//
// It runs the byte-pinned db golden against the fixture module in a scratch
// copy, so a wrong assertion fails here rather than being discovered by a
// reader. The db layer is the one with a trustworthy human baseline, so it is
// the one worth executing; the controller and handler goldens import host
// packages the fixture does not model (gin, the validator), which is why they
// stay byte-pinned only.
func TestGoldenDBSuiteExecutes(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	fixture := filepath.Join("..", "..", "testdata", "gentest")
	golden := filepath.Join(fixture, "expected", "pkg", "services", "demo", "db", "demo_test.go")
	if _, err := os.Stat(golden); err != nil {
		t.Fatalf("db golden missing: %v", err)
	}

	scratch := t.TempDir()
	if err := copyTree(t, fixture, scratch); err != nil {
		t.Fatal(err)
	}
	// The generated suite lives beside the store it exercises; copyTree above
	// brought the golden's expected/ subtree, not the package it belongs to.
	if err := os.MkdirAll(filepath.Join(scratch, "pkg", "services", "demo", "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "pkg", "services", "demo", "db", "demo_test.go"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./pkg/services/demo/db/...")
	cmd.Dir = scratch
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("generated db suite does not pass against the fixture:\n%s\n%s", err, out)
	}
}
