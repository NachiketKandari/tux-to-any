package templates

import (
	"strings"
	"testing"
)

// TestRenderTestDBMethodTxVariants pins the tx-based db test blocks for the
// db layer (PRD §4.2.3 decision 27): DML-tx (insert/update) through ExpectExec
// + Beginx with the NoRows→ErrNoRows case, DELETE-tx tolerating zero rows,
// plain DML without the tx handle, and tx SELECT reads through ExpectQuery +
// Beginx with the tx second arg.
func TestRenderTestDBMethodTxVariants(t *testing.T) {
	// Insert-tx: ExecError / NoRows(ErrNoRows) / Success, Beginx + tx arg.
	insertTx := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "InsertRiskProfile",
		Regex:    `(?i)^insert\\s+into\\s+URF_USR_RISK_PROF(\\s+.+)?$`,
		CallArgs: []string{`"userid"`, `"riskprofile"`},
		IsDML:    true, IsTx: true,
	})
	parseTestFile(t, "insert_tx_test.go", "package db\n"+insertTx)
	for _, want := range []string{
		"func (suite *NavStoreSuite) TestInsertRiskProfile() {",
		`suite.sqlMock.ExpectBegin()`,
		`ExpectExec("(?i)^insert\\s+into\\s+URF_USR_RISK_PROF`,
		`WillReturnResult(sqlmock.NewResult(1, testCase.rowsAffected))`,
		`tx, _ := suite.sqlDB.Beginx()`,
		`suite.navStore.InsertRiskProfile(suite.ctx, tx, "userid", "riskprofile")`,
		`desc:          "ExecError",`,
		`desc:          "NoRows",`,
		`expectedError: "sql: no rows in result set",`,
		`desc:          "Success",`,
		`err := suite.navStore.InsertRiskProfile`,
		`assert.ErrorContains(t, err, testCase.expectedError)`,
		`assert.NoError(t, err)`,
	} {
		if !strings.Contains(insertTx, want) {
			t.Errorf("insert-tx test missing %q\n---\n%s", want, insertTx)
		}
	}
	if strings.Contains(insertTx, "ExpectQuery") || strings.Contains(insertTx, "actualOutput") {
		t.Errorf("insert-tx must use the Exec contract (no ExpectQuery/actualOutput):\n%s", insertTx)
	}

	// Delete-tx: zero rows is success (no RowsAffected check).
	deleteTx := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "DeleteQnA",
		Regex:    `(?i)^delete\\s+from\\s+RPQA_RP_QUESTION_ANS(\\s+where\\s+(.+))?$`,
		CallArgs: []string{`"userid"`},
		IsDML:    true, IsTx: true, DeleteTx: true,
	})
	parseTestFile(t, "delete_tx_test.go", "package db\n"+deleteTx)
	for _, want := range []string{
		`suite.sqlMock.ExpectBegin()`,
		`ExpectExec("(?i)^delete\\s+from\\s+RPQA_RP_QUESTION_ANS`,
		`tx, _ := suite.sqlDB.Beginx()`,
		`suite.navStore.DeleteQnA(suite.ctx, tx, "userid")`,
		`desc:          "Success-NoRows",`,
	} {
		if !strings.Contains(deleteTx, want) {
			t.Errorf("delete-tx test missing %q\n---\n%s", want, deleteTx)
		}
	}
	if strings.Contains(deleteTx, `"NoRows",`) {
		t.Errorf("delete-tx must not carry the ErrNoRows NoRows case:\n%s", deleteTx)
	}

	// Plain DML: no Beginx, no tx arg.
	plain := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "InsertStatus",
		Regex:    `(?i)^insert\\s+into\\s+T(\\s+.+)?$`,
		CallArgs: []string{`"userid"`},
		IsDML:    true,
	})
	parseTestFile(t, "plain_test.go", "package db\n"+plain)
	if strings.Contains(plain, "ExpectBegin") || strings.Contains(plain, "Beginx") || strings.Contains(plain, ", tx") {
		t.Errorf("plain DML must not take the tx handle:\n%s", plain)
	}
	for _, want := range []string{
		`ExpectExec("(?i)^insert\\s+into\\s+T`,
		`suite.navStore.InsertStatus(suite.ctx, "userid")`,
		`desc:          "NoRows",`,
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain DML test missing %q\n---\n%s", want, plain)
		}
	}

	// Tx SELECT single (GetMarks): ExpectQuery + Beginx + tx second arg,
	// no Success-NoRows case (every error propagates to the flow).
	selectTx := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "GetMarks",
		Regex: `(?i)^select\\s+(.+)\\s+from\\s+RPAM`,
		Cols:  []string{"MARKS"}, Row: []string{"marks"},
		ExpectType: "string", ExpectExpr: `""`,
		CallArgs: []string{`"qid"`},
		IsTx:     true,
	})
	parseTestFile(t, "select_tx_test.go", "package db\n"+selectTx)
	for _, want := range []string{
		`suite.sqlMock.ExpectBegin()`,
		`ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+RPAM")`,
		`tx, _ := suite.sqlDB.Beginx()`,
		`suite.navStore.GetMarks(suite.ctx, tx, "qid")`,
		`expectedOutput string`,
	} {
		if !strings.Contains(selectTx, want) {
			t.Errorf("select-tx test missing %q\n---\n%s", want, selectTx)
		}
	}
	if strings.Contains(selectTx, "Success-NoRows") {
		t.Errorf("select-tx must not carry the tolerated NoRows case (every error propagates):\n%s", selectTx)
	}
}

// TestRenderTestDBMethodQueryVar pins the query-variable contract: when the
// store SQL literal is known the block declares `query := ...` once and
// reuses it in every expectation via regexp.QuoteMeta (exact match, correct
// for every query kind — SELECT multi/single/scalar, INSERT/UPDATE/DELETE/
// MERGE, plain or tx). When the literal lives outside the method body the
// block falls back to the Regex anchor and declares no variable.
func TestRenderTestDBMethodQueryVar(t *testing.T) {
	const query = "SELECT COMP_CD FROM DEMO_COMPANY WHERE COMP_CD = :1"
	withQuery := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "GetNavDetails",
		Query: query, Regex: `(?i)^select\\s+(.+)\\s+from\\s+DEMO_COMPANY`,
		Shape: "multi",
		Cols:  []string{"COMP_CD"}, Row: []string{"compcd"},
		ExpectType: "[]*models.NavDetails",
		ExpectExpr: `[]*models.NavDetails{{CompCd: sql.NullString{String: "compcd", Valid: true}}}`,
		CallArgs:   []string{`"compcd"`},
	})
	parseTestFile(t, "query_var_test.go", "package db\n"+withQuery)
	for _, want := range []string{
		"query := `SELECT COMP_CD FROM DEMO_COMPANY WHERE COMP_CD = :1`",
		"ExpectQuery(regexp.QuoteMeta(query))",
	} {
		if !strings.Contains(withQuery, want) {
			t.Errorf("query-var block missing %q\n---\n%s", want, withQuery)
		}
	}
	if strings.Contains(withQuery, `(?i)^select`) {
		t.Errorf("query-var block must not carry the Regex fallback literal:\n%s", withQuery)
	}

	// DML reuses the same variable through the Exec contract.
	dml := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "InsertStatus",
		Query:    "INSERT INTO T(USR_ID) VALUES (:1)",
		Regex:    `(?i)^insert\\s+into\\s+T`,
		Shape:    "dml",
		CallArgs: []string{`"userid"`},
		IsDML:    true,
	})
	parseTestFile(t, "query_var_dml_test.go", "package db\n"+dml)
	for _, want := range []string{
		"query := `INSERT INTO T(USR_ID) VALUES (:1)`",
		"ExpectExec(regexp.QuoteMeta(query))",
	} {
		if !strings.Contains(dml, want) {
			t.Errorf("dml query-var block missing %q\n---\n%s", want, dml)
		}
	}

	// No literal (query built outside the body): Regex anchor, no variable.
	withoutQuery := render(t, TestDBMethod, TestDBMethodData{
		SuiteName: "NavStoreSuite", StoreVar: "navStore", Name: "CountRiskProfiles",
		Regex: `(?i)^`,
		Shape: "scalar",
		Cols:  []string{"count"}, Row: []string{"0"},
		NoRows: true, NoRowsExpr: "0",
		ExpectType: "int64", ExpectExpr: "0",
	})
	parseTestFile(t, "query_fallback_test.go", "package db\n"+withoutQuery)
	if strings.Contains(withoutQuery, "query :=") {
		t.Errorf("fallback block must not declare a query variable:\n%s", withoutQuery)
	}
	if !strings.Contains(withoutQuery, `ExpectQuery("(?i)^")`) {
		t.Errorf("fallback block must use the Regex anchor:\n%s", withoutQuery)
	}
}
