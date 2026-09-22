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
		Regex:  `(?i)^insert\\s+into\\s+URF_USR_RISK_PROF(\\s+.+)?$`,
		CallArgs: []string{`"userid"`, `"riskprofile"`},
		IsDML: true, IsTx: true,
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
		Regex:  `(?i)^delete\\s+from\\s+RPQA_RP_QUESTION_ANS(\\s+where\\s+(.+))?$`,
		CallArgs: []string{`"userid"`},
		IsDML: true, IsTx: true, DeleteTx: true,
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
		Regex:  `(?i)^insert\\s+into\\s+T(\\s+.+)?$`,
		CallArgs: []string{`"userid"`},
		IsDML: true,
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
		Cols: []string{"MARKS"}, Row: []string{"marks"},
		ExpectType: "string", ExpectExpr: `""`,
		CallArgs: []string{`"qid"`},
		IsTx: true,
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
