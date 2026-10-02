package db

import (
	"context"
	"database/sql"
	"demo-be/pkg/logger"
	"demo-be/pkg/services/demo/models"
	"demo-be/pkg/utils"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

/*
MockGen
mockgen -source=pkg/services/demo/db/interface.go -destination=pkg/services/demo/db/mock_store.go -package=db

Coverage
go test pkg/services/demo/db/demo_test.go pkg/services/demo/db/demo.go pkg/services/demo/db/interface.go -v -coverprofile=coverage.txt -covermode count && go tool cover -html=coverage.txt
*/

type DemoStoreSuite struct {
	suite.Suite
	ctx       context.Context
	sqlDB     *sqlx.DB
	sqlMock   sqlmock.Sqlmock
	demoStore DemoStore
}

func TestDemoStoreSuite(t *testing.T) {
	suite.Run(t, new(DemoStoreSuite))
}

func (suite *DemoStoreSuite) SetupSuite() {
	logger.LoggerInit("", -1)

	suite.ctx = context.TODO()
	suite.sqlDB, suite.sqlMock = utils.NewSqlxMockDB()
	suite.demoStore = NewDemoStore(suite.sqlDB, suite.sqlDB)
}

func (suite *DemoStoreSuite) TestGetOrderDetails() {
	// The store's own SQL literal, reused in every expectation below.
	query := `SELECT DEMO_ORDER_COMP_CD AS "COMP_CD", DEMO_ORDER_CO_NAME AS "COMP_NAME"
	          FROM DEMO_ORDER, DEMO_COMPANY
	          WHERE DEMO_ORDER_CO_ID = :1`
	testCases := []struct {
		desc           string
		mockInput      *sqlmock.Rows
		expectedError  string
		expectedOutput []*models.OrderDetails
	}{
		{
			desc:          "SQLError",
			mockInput:     nil,
			expectedError: "ORA Error",
		},
		{
			desc:           "Success",
			mockInput:      sqlmock.NewRows([]string{"COMP_CD", "COMP_NAME"}).AddRow("compcd", "compname"),
			expectedError:  "",
			expectedOutput: []*models.OrderDetails{{CompCd: sql.NullString{String: "compcd", Valid: true}, CompName: sql.NullString{String: "compname", Valid: true}}},
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			if testCase.mockInput != nil {
				suite.sqlMock.
					ExpectQuery(regexp.QuoteMeta(query)).
					WillReturnRows(testCase.mockInput)
			} else {
				suite.sqlMock.
					ExpectQuery(regexp.QuoteMeta(query)).
					WillReturnError(errors.New("ORA Error"))
			}

			// Triggering Function
			actualOutput, err := suite.demoStore.GetOrderDetails(suite.ctx, "compcd")

			// Validations
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, actualOutput, testCase.expectedOutput)
			}
		})
	}
}

func (suite *DemoStoreSuite) TestGetOrderCount() {
	// The store's own SQL literal, reused in every expectation below.
	query := `SELECT COUNT(*) AS "count" FROM DEMO_ORDER_MAP WHERE DEMO_MATCH_ACC = :1`
	testCases := []struct {
		desc           string
		mockInput      *sqlmock.Rows
		expectedError  string
		expectedOutput int64
	}{
		{
			desc:          "SQLError",
			mockInput:     nil,
			expectedError: "ORA Error",
		},
		{
			desc:           "Success-NoRows",
			mockInput:      sqlmock.NewRows([]string{"count"}),
			expectedError:  "sql: no rows in result set",
			expectedOutput: 0,
		},
		{
			desc:           "Success",
			mockInput:      sqlmock.NewRows([]string{"count"}).AddRow("0"),
			expectedError:  "",
			expectedOutput: 0,
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			if testCase.mockInput != nil {
				suite.sqlMock.
					ExpectQuery(regexp.QuoteMeta(query)).
					WillReturnRows(testCase.mockInput)
			} else {
				suite.sqlMock.
					ExpectQuery(regexp.QuoteMeta(query)).
					WillReturnError(errors.New("ORA Error"))
			}

			// Triggering Function
			actualOutput, err := suite.demoStore.GetOrderCount(suite.ctx, "matchaccount")

			// Validations
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, actualOutput, testCase.expectedOutput)
			}
		})
	}
}

func (suite *DemoStoreSuite) TestAddOrder() {
	// The store's own SQL literal, reused in every expectation below.
	query := `INSERT INTO DEMO_ORDER (DEMO_ORDER_CO_ID, DEMO_ORDER_CO_NAME) VALUES (:1, :2)`
	testCases := []struct {
		desc          string
		rowsAffected  int64
		mockError     string
		expectedError string
	}{
		{
			desc:          "ExecError",
			mockError:     "ORA Error",
			expectedError: "ORA Error",
		},
		{
			desc:          "NoRows",
			rowsAffected:  0,
			expectedError: "unable to add the order",
		},
		{
			desc:          "Success",
			rowsAffected:  1,
			expectedError: "",
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			if testCase.mockError != "" {
				suite.sqlMock.
					ExpectExec(regexp.QuoteMeta(query)).
					WillReturnError(errors.New(testCase.mockError))
			} else {
				suite.sqlMock.
					ExpectExec(regexp.QuoteMeta(query)).
					WillReturnResult(sqlmock.NewResult(1, testCase.rowsAffected))
			}
			// Triggering Function
			err := suite.demoStore.AddOrder(suite.ctx, "compcd", "compname")

			// Validations
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func (suite *DemoStoreSuite) TestEditOrder() {
	// The store's own SQL literal, reused in every expectation below.
	query := `UPDATE DEMO_ORDER SET DEMO_ORDER_UPD = :1 WHERE DEMO_ORDER_CO_ID = :2`
	testCases := []struct {
		desc          string
		rowsAffected  int64
		mockError     string
		expectedError string
	}{
		{
			desc:          "ExecError",
			mockError:     "ORA Error",
			expectedError: "ORA Error",
		},
		{
			desc:          "Success-NoRows",
			rowsAffected:  0,
			expectedError: "",
		},
		{
			desc:          "Success",
			rowsAffected:  1,
			expectedError: "",
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			if testCase.mockError != "" {
				suite.sqlMock.
					ExpectExec(regexp.QuoteMeta(query)).
					WillReturnError(errors.New(testCase.mockError))
			} else {
				suite.sqlMock.
					ExpectExec(regexp.QuoteMeta(query)).
					WillReturnResult(sqlmock.NewResult(1, testCase.rowsAffected))
			}
			// Triggering Function
			err := suite.demoStore.EditOrder(suite.ctx, "compcd")

			// Validations
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func (suite *DemoStoreSuite) TestDeleteOrder() {
	// The store's own SQL literal, reused in every expectation below.
	query := `DELETE FROM DEMO_ORDER WHERE DEMO_ORDER_CO_ID = :1`
	testCases := []struct {
		desc          string
		rowsAffected  int64
		mockError     string
		expectedError string
	}{
		{
			desc:          "ExecError",
			mockError:     "ORA Error",
			expectedError: "ORA Error",
		},
		{
			desc:          "Success-NoRows",
			rowsAffected:  0,
			expectedError: "",
		},
		{
			desc:          "Success",
			rowsAffected:  1,
			expectedError: "",
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			suite.sqlMock.ExpectBegin()
			// Mocking and Setting Expected Result
			if testCase.mockError != "" {
				suite.sqlMock.
					ExpectExec(regexp.QuoteMeta(query)).
					WillReturnError(errors.New(testCase.mockError))
			} else {
				suite.sqlMock.
					ExpectExec(regexp.QuoteMeta(query)).
					WillReturnResult(sqlmock.NewResult(1, testCase.rowsAffected))
			}
			tx, _ := suite.sqlDB.Beginx()
			// Triggering Function
			err := suite.demoStore.DeleteOrder(suite.ctx, tx, "compcd")

			// Validations
			if testCase.expectedError != "" {
				assert.ErrorContains(t, err, testCase.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
