package db

import (
	"context"
	"database/sql"
	"demo-be/pkg/logger"
	"demo-be/pkg/services/demo/models"
	"demo-be/pkg/utils"
	"errors"
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
	suite.demoStore = NewDemoStore(suite.sqlDB)
}

func (suite *DemoStoreSuite) TestGetOrderDetails() {
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
					ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+DEMO_ORDER\\s*,\\s*DEMO_COMPANY(\\s+where\\s+(.+))?$").
					WillReturnRows(testCase.mockInput)
			} else {
				suite.sqlMock.
					ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+DEMO_ORDER\\s*,\\s*DEMO_COMPANY(\\s+where\\s+(.+))?$").
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
			expectedError:  "",
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
					ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+DEMO_ORDER_MAP(\\s+where\\s+(.+))?$").
					WillReturnRows(testCase.mockInput)
			} else {
				suite.sqlMock.
					ExpectQuery("(?i)^select\\s+(.+)\\s+from\\s+DEMO_ORDER_MAP(\\s+where\\s+(.+))?$").
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
