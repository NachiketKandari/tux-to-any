package testgen

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// txStoreSrc is a synthetic db layer covering every tx-based variant the db
// test layer must render (PRD §4.2.3 decision 27): insert/update tx (Exec on
// tx + RowsAffected→ErrNoRows), delete tx (Exec on tx, no RowsAffected
// check), plain DML + MERGE (Exec on g.db), and the tx single-row read
// (GetMarks: GetContext on tx, sql.NullString scan → string return).
const txStoreSrc = `package db

import (
	"context"

	"github.com/jmoiron/sqlx"
)

type store struct {
	db *sqlx.DB
}

func (g *store) InsertRiskProfile(ctx context.Context, tx *sqlx.Tx, userId string, riskProfile string) error {
	query := ` + "`" + `INSERT INTO URF_USR_RISK_PROF(URF_USR_ID, URF_RISK_PROF) VALUES(:1, :2)` + "`" + `
	result, err := tx.ExecContext(ctx, query, userId, riskProfile)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errNoRows
	}
	return nil
}

func (g *store) UpdateIBFStatus(ctx context.Context, tx *sqlx.Tx, userId string) error {
	query := ` + "`" + `UPDATE IBF_INFO_BOOKMARK_FORMS SET IBF_STATUS = 'C' WHERE IBF_USER_ID = :1` + "`" + `
	result, err := tx.ExecContext(ctx, query, userId)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errNoRows
	}
	return nil
}

func (g *store) DeleteQnA(ctx context.Context, tx *sqlx.Tx, userId string, customerType string) error {
	query := ` + "`" + `DELETE FROM RPQA_RP_QUESTION_ANS WHERE RPQA_USR_ID = :1 AND RPQA_CUST_TYPE = :2` + "`" + `
	_, err := tx.ExecContext(ctx, query, userId, customerType)
	if err != nil {
		return err
	}
	return nil
}

func (g *store) InsertStatus(ctx context.Context, userId string) error {
	query := ` + "`" + `INSERT INTO T(USR_ID) VALUES (:1)` + "`" + `
	result, err := g.db.ExecContext(ctx, query, userId)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errNoRows
	}
	return nil
}

func (g *store) MergeAccounts(ctx context.Context, accountId string) error {
	query := ` + "`" + `MERGE INTO DEMO_ACCOUNTS a USING (SELECT :1 AS ID FROM DUAL) s ON (a.ACCOUNT_ID = s.ID) WHEN MATCHED THEN UPDATE SET a.BALANCE = :2` + "`" + `
	result, err := g.db.ExecContext(ctx, query, accountId)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errNoRows
	}
	return nil
}

func (g *store) GetMarks(ctx context.Context, tx *sqlx.Tx, questionId string, answerId string) (string, error) {
	var marks sql_NullString
	query := ` + "`" + `SELECT RPAM_MARKS FROM RPAM_RP_ANSWER_MASTER WHERE RPAM_QSTN_ID = :1 AND RPAM_ANSWER_ID = :2` + "`" + `
	err := tx.GetContext(ctx, &marks, query, questionId, answerId)
	if err != nil {
		return "", err
	}
	return marks.String, nil
}

type RiskProfile struct {
	UserId   string ` + "`" + `db:"URF_USR_ID"` + "`" + `
	RiskProf string ` + "`" + `db:"URF_USR_RISK_PROF"` + "`" + `
}

func (g *store) GetRiskProfile(ctx context.Context, tx *sqlx.Tx, userId string) (*RiskProfile, error) {
	var profile RiskProfile
	query := ` + "`" + `SELECT URF_USR_ID, URF_USR_RISK_PROF FROM URF_USR_RISK_PROF WHERE URF_USR_ID = :1` + "`" + `
	err := tx.GetContext(ctx, &profile, query, userId)
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (g *store) ListRiskProfiles(ctx context.Context, tx *sqlx.Tx) ([]*RiskProfile, error) {
	var profiles []*RiskProfile
	query := ` + "`" + `SELECT URF_USR_ID, URF_USR_RISK_PROF FROM URF_USR_RISK_PROF` + "`" + `
	err := tx.SelectContext(ctx, &profiles, query)
	if err != nil {
		return nil, err
	}
	return profiles, nil
}

func (g *store) UpdateStatus(ctx context.Context, userId string) error {
	query := ` + "`" + `UPDATE T SET S = 'X' WHERE USR_ID = :1` + "`" + `
	_, err := g.db.ExecContext(ctx, query, userId)
	if err != nil {
		return err
	}
	return nil
}

func (g *store) DeleteStatus(ctx context.Context, userId string) error {
	query := ` + "`" + `DELETE FROM T WHERE USR_ID = :1` + "`" + `
	_, err := g.db.ExecContext(ctx, query, userId)
	if err != nil {
		return err
	}
	return nil
}

var riskCountQuery = "SELECT COUNT(*) FROM URF_USR_RISK_PROF"

func (g *store) CountRiskProfiles(ctx context.Context) (int64, error) {
	var count int64
	err := g.db.GetContext(ctx, &count, riskCountQuery)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (g *store) Health(ctx context.Context) error {
	return g.db.PingContext(ctx)
}
`

// parseDBFact parses one FuncDecl from src by name.
func parseDBFact(t *testing.T, src, name string) *dbFact {
	t.Helper()
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "store.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range af.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s not declared", name)
	}
	// Reuse the real extractor over a temp dir layer.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "store.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	lf := extractLayer(dir, "db")
	f := lf.DB[name]
	if f == nil {
		t.Fatalf("%s not extracted: %v", name, lf.DB)
	}
	return f
}

// TestExtractDBTxVariants pins tx detection across the db layer: tx param +
// tx receiver set IsTx, Exec shapes are dml, the tx read keeps its string
// return type, and DML target tables feed the Exec regex. It also pins the
// extended call/declaration coverage: tx single/multi reads, plain
// update/delete, a query that lives in a package-level var (no literal in
// the body) and a method with no recognized query call at all.
func TestExtractDBTxVariants(t *testing.T) {
	// Fix the NullString placeholder so the fixture parses (type text only).
	src := strings.Replace(txStoreSrc, "sql_NullString", "sql.NullString", 1)
	cases := []struct {
		name       string
		shape      string
		isTx       bool
		table      string
		noQuery    bool
		returnType string
	}{
		{"InsertRiskProfile", "dml", true, "URF_USR_RISK_PROF", false, "error"},
		{"UpdateIBFStatus", "dml", true, "IBF_INFO_BOOKMARK_FORMS", false, "error"},
		{"DeleteQnA", "dml", true, "RPQA_RP_QUESTION_ANS", false, "error"},
		{"InsertStatus", "dml", false, "T", false, "error"},
		{"MergeAccounts", "dml", false, "DEMO_ACCOUNTS", false, "error"},
		{"UpdateStatus", "dml", false, "T", false, "error"},
		{"DeleteStatus", "dml", false, "T", false, "error"},
		{"GetMarks", "scalar", true, "RPAM_RP_ANSWER_MASTER", false, "string"},
		{"GetRiskProfile", "single", true, "URF_USR_RISK_PROF", false, ""},
		{"ListRiskProfiles", "multi", true, "URF_USR_RISK_PROF", false, ""},
		{"CountRiskProfiles", "scalar", false, "", true, "int64"},
	}
	for _, c := range cases {
		f := parseDBFact(t, src, c.name)
		if f.Shape != c.shape {
			t.Errorf("%s shape = %q, want %q", c.name, f.Shape, c.shape)
		}
		if f.IsTx != c.isTx {
			t.Errorf("%s IsTx = %v, want %v", c.name, f.IsTx, c.isTx)
		}
		if !c.noQuery && (len(f.Tables) == 0 || f.Tables[0] != c.table) {
			t.Errorf("%s tables = %v, want [%s]", c.name, f.Tables, c.table)
		}
		if c.noQuery && f.Query != "" {
			t.Errorf("%s query = %q, want empty (literal lives outside the body)", c.name, f.Query)
		}
		if c.returnType == "string" && f.ReturnType != "string" {
			t.Errorf("GetMarks ReturnType = %q, want string", f.ReturnType)
		}
		if c.returnType == "string" && f.Scalar != "sql.NullString" {
			t.Errorf("GetMarks Scalar = %q, want sql.NullString scan var", f.Scalar)
		}
		if c.returnType == "int64" && f.ReturnType != "int64" {
			t.Errorf("%s ReturnType = %q, want int64", c.name, f.ReturnType)
		}
	}

	// A store method with no query call extracts nothing (skip, never a
	// wrong-shaped block).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "store.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if lf := extractLayer(dir, "db"); lf.DB["Health"] != nil {
		t.Errorf("Health extracted as %+v, want nil (no query call)", lf.DB["Health"])
	}

	// tablesOf: paren-glued INSERT target + UPDATE/DELETE/MERGE fallbacks.
	if got := tablesOf("INSERT INTO URF_USR_RISK_PROF(URF_USR_ID) VALUES (:1)"); len(got) != 1 || got[0] != "URF_USR_RISK_PROF" {
		t.Errorf("paren-glued tablesOf = %v", got)
	}
	if got := tablesOf("UPDATE IBF SET X = 1"); len(got) != 1 || got[0] != "IBF" {
		t.Errorf("update tablesOf = %v", got)
	}

	// Exec regex anchors verb + table.
	f := parseDBFact(t, src, "InsertRiskProfile")
	if got := dbExecRegex(f); !strings.Contains(got, "insert") || !strings.Contains(got, "URF_USR_RISK_PROF") {
		t.Errorf("exec regex = %q", got)
	}
	if !isDeleteTx(parseDBFact(t, src, "DeleteQnA")) {
		t.Error("DeleteQnA must be the tolerated delete-tx")
	}
	if isDeleteTx(parseDBFact(t, src, "InsertRiskProfile")) {
		t.Error("InsertRiskProfile must not be delete-tx")
	}
	if isDeleteTx(parseDBFact(t, src, "InsertStatus")) {
		t.Error("plain DML must not be delete-tx")
	}

	// dbCallArgs excludes the tx handle.
	if args := dbCallArgs(&serviceCtx{fixtures: &AssumedFixtureSource{Models: &modelsInfo{Structs: map[string][]fieldInfo{}}}}, parseDBFact(t, src, "InsertRiskProfile")); len(args) != 2 {
		t.Errorf("tx CallArgs = %v, want 2 bind literals (tx excluded)", args)
	}

	// A query built outside the body still renders: the regex falls back to
	// a permissive anchor and the block keeps the query/Exec contract.
	if got := dbRegex(parseDBFact(t, src, "CountRiskProfiles")); got != `(?i)^` {
		t.Errorf("no-literal query regex = %q, want the permissive anchor", got)
	}
	if got := dbExecRegex(&dbFact{Shape: "dml"}); got != `(?i)^` {
		t.Errorf("no-literal exec regex = %q, want the permissive anchor", got)
	}

	// dbFactIssue gates unrenderable facts with a reason and leaves the
	// renderable ones alone.
	sc := &serviceCtx{models: &modelsInfo{Structs: map[string][]fieldInfo{
		"RiskProfile": {{Name: "UserId", Type: "string", DB: "URF_USR_ID"}},
	}}}
	if issue := dbFactIssue(sc, &dbFact{Shape: "multi", RowType: "RiskProfile"}); issue != "" {
		t.Errorf("multi RiskProfile issue = %q, want none", issue)
	}
	if issue := dbFactIssue(sc, &dbFact{Shape: "multi"}); issue == "" {
		t.Error("multi without a row type must be gated")
	}
	if issue := dbFactIssue(sc, &dbFact{Shape: "single", RowType: "Unknown"}); issue == "" {
		t.Error("single with an unknown row type must be gated")
	}
	if issue := dbFactIssue(sc, &dbFact{Shape: "scalar", Scalar: "[]string"}); issue == "" {
		t.Error("scalar with a slice type must be gated")
	}
	if issue := dbFactIssue(sc, &dbFact{Shape: "scalar", Scalar: "int64"}); issue != "" {
		t.Errorf("scalar int64 issue = %q, want none", issue)
	}
	if issue := dbFactIssue(sc, &dbFact{Shape: "dml"}); issue != "" {
		t.Errorf("dml without a literal issue = %q, want none", issue)
	}
}

// buildTxTree materializes a service whose db layer is all tx variants.
func buildTxTree(t *testing.T, root string) string {
	t.Helper()
	svc := filepath.Join(root, "pkg", "services", "txsvc")
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := strings.Replace(txStoreSrc, "sql_NullString", "sql.NullString", 1)
	src = strings.Replace(src, "errNoRows", "sql.ErrNoRows", -1)
	// The fixture needs database/sql for ErrNoRows.
	src = strings.Replace(src, "import (\n\t\"context\"", "import (\n\t\"context\"\n\t\"database/sql\"", 1)
	write("go.mod", "module txsvc-be\n\ngo 1.26.4\n")
	write("pkg/services/txsvc/db/store.go", src)
	write("pkg/services/txsvc/db/interface.go", `package db

import (
	"context"

	"github.com/jmoiron/sqlx"
)

type TxsvcStore interface {
	InsertRiskProfile(ctx context.Context, tx *sqlx.Tx, userId string, riskProfile string) error
	UpdateIBFStatus(ctx context.Context, tx *sqlx.Tx, userId string) error
	DeleteQnA(ctx context.Context, tx *sqlx.Tx, userId string, customerType string) error
	InsertStatus(ctx context.Context, userId string) error
	MergeAccounts(ctx context.Context, accountId string) error
	UpdateStatus(ctx context.Context, userId string) error
	DeleteStatus(ctx context.Context, userId string) error
	GetMarks(ctx context.Context, tx *sqlx.Tx, questionId string, answerId string) (string, error)
	GetRiskProfile(ctx context.Context, tx *sqlx.Tx, userId string) (*RiskProfile, error)
	ListRiskProfiles(ctx context.Context, tx *sqlx.Tx) ([]*RiskProfile, error)
	CountRiskProfiles(ctx context.Context) (int64, error)
	Health(ctx context.Context) error
}

func NewTxsvcStore(db *sqlx.DB) TxsvcStore { return &store{} }
`)
	write("pkg/services/txsvc/models/models.go", `package models

type RiskProfile struct {
	UserId   string `+"`"+`db:"URF_USR_ID"`+"`"+`
	RiskProf string `+"`"+`db:"URF_USR_RISK_PROF"`+"`"+`
}
`)
	return svc
}

// TestGenerateDBTxVariants is the db-layer end-to-end: every tx-based and
// plain db variant generates a deterministic block (no unsupported skips),
// Exec shapes use ExpectExec + Beginx, tx reads use ExpectQuery + Beginx,
// the queryless scalar read keeps the permissive regex, the method with no
// query call skips with an honest reason, and the file parses.
func TestGenerateDBTxVariants(t *testing.T) {
	root := t.TempDir()
	svc := buildTxTree(t, root)
	out := filepath.Join(root, "_staged")

	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{BaseDir: out, Workers: 1, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"InsertRiskProfile", "UpdateIBFStatus", "DeleteQnA",
		"InsertStatus", "MergeAccounts", "UpdateStatus", "DeleteStatus",
		"GetMarks", "GetRiskProfile", "ListRiskProfiles", "CountRiskProfiles",
	} {
		if got := unitStatus(res, name); got != StatusTemplate {
			t.Errorf("%s: got %s, want generated", name, got)
		}
	}
	if got := unitStatus(res, "Health"); got != StatusUnsupported {
		t.Errorf("Health: got %s, want unsupported (no query call)", got)
	}
	if len(res.Files) != 1 {
		t.Fatalf("want 1 db file, got %v", res.Files)
	}
	parseAll(t, res.Files)
	dbOut := read(t, res.Files[0])
	for _, want := range []string{
		"func (suite *TxsvcStoreSuite) TestInsertRiskProfile() {",
		"func (suite *TxsvcStoreSuite) TestUpdateIBFStatus() {",
		"func (suite *TxsvcStoreSuite) TestDeleteQnA() {",
		"func (suite *TxsvcStoreSuite) TestInsertStatus() {",
		"func (suite *TxsvcStoreSuite) TestMergeAccounts() {",
		"func (suite *TxsvcStoreSuite) TestUpdateStatus() {",
		"func (suite *TxsvcStoreSuite) TestDeleteStatus() {",
		"func (suite *TxsvcStoreSuite) TestGetMarks() {",
		"func (suite *TxsvcStoreSuite) TestGetRiskProfile() {",
		"func (suite *TxsvcStoreSuite) TestListRiskProfiles() {",
		"func (suite *TxsvcStoreSuite) TestCountRiskProfiles() {",
		`ExpectExec("(?i)^insert\\s+into\\s+URF_USR_RISK_PROF`,
		`ExpectExec("(?i)^update\\s+IBF_INFO_BOOKMARK_FORMS`,
		`ExpectExec("(?i)^delete\\s+from\\s+RPQA_RP_QUESTION_ANS`,
		`ExpectExec("(?i)^merge\\s+into\\s+DEMO_ACCOUNTS`,
		`ExpectExec("(?i)^update\\s+T`,
		`ExpectExec("(?i)^delete\\s+from\\s+T`,
		`ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+RPAM_RP_ANSWER_MASTER`,
		`ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+URF_USR_RISK_PROF`,
		// The no-literal scalar read keeps the permissive anchor.
		`ExpectQuery("(?i)^")`,
		`suite.sqlMock.ExpectBegin()`,
		`tx, _ := suite.sqlDB.Beginx()`,
		`suite.txsvcStore.InsertRiskProfile(suite.ctx, tx, "userid", "riskprofile")`,
		`suite.txsvcStore.DeleteQnA(suite.ctx, tx, "userid", "customertype")`,
		`suite.txsvcStore.InsertStatus(suite.ctx, "userid")`,
		`suite.txsvcStore.UpdateStatus(suite.ctx, "userid")`,
		`suite.txsvcStore.DeleteStatus(suite.ctx, "userid")`,
		`suite.txsvcStore.GetMarks(suite.ctx, tx, "questionid", "answerid")`,
		`suite.txsvcStore.GetRiskProfile(suite.ctx, tx, "userid")`,
		`suite.txsvcStore.ListRiskProfiles(suite.ctx, tx)`,
		`suite.txsvcStore.CountRiskProfiles(suite.ctx)`,
		`desc:          "NoRows",`,
		`desc:          "Success-NoRows",`,
		`WillReturnResult(sqlmock.NewResult(1, testCase.rowsAffected))`,
	} {
		if !strings.Contains(dbOut, want) {
			t.Errorf("tx db test missing %q\n---\n%s", want, dbOut)
		}
	}
	// Tx handle count: 3 DML-tx + 3 select-tx; plain DML and the queryless
	// read carry no Beginx. The unsupported method contributes nothing.
	if got := strings.Count(dbOut, "ExpectBegin()"); got != 6 {
		t.Errorf("ExpectBegin count = %d, want 6 (3 DML-tx + 3 select-tx)", got)
	}
	// The unsupported skip reason must never promise a later pass.
	for _, u := range res.Units {
		if u.Status == StatusUnsupported && strings.Contains(u.Detail, "later pass") {
			t.Errorf("unsupported detail still promises a later pass: %s", u.Detail)
		}
	}
}
