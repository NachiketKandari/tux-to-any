package controller

import (
	"context"
	"database/sql"
	"demo-be/pkg/logger"
	"demo-be/pkg/services/demo/db"
	"demo-be/pkg/services/demo/models"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

/*
MockGen
mockgen -source=pkg/services/demo/controller/interface.go -destination=pkg/services/demo/controller/mock_controller.go -package=controller

Coverage
go test pkg/services/demo/controller/demo_test.go pkg/services/demo/controller/demo.go pkg/services/demo/controller/interface.go -v -coverprofile=coverage.txt -covermode count && go tool cover -html=coverage.txt
*/

type DemoControllerSuiteController struct {
	suite.Suite
	ctx            context.Context
	mockController *gomock.Controller
	demoStore      *db.MockDemoStore
	demoController DemoController
}

// run test | debug test
func TestDemoControllerSuiteController(t *testing.T) {
	suite.Run(t, new(DemoControllerSuiteController))
}

func (suite *DemoControllerSuiteController) SetupSuite() {
	logger.LoggerInit("", -1)

	suite.ctx = context.TODO()
}

func (suite *DemoControllerSuiteController) SetupTest() {
	suite.mockController = gomock.NewController(suite.T())
	suite.demoStore = db.NewMockDemoStore(suite.mockController)
	suite.demoController = NewDemoController(suite.demoStore)
}

func (suite *DemoControllerSuiteController) TearDownTest() {
	suite.mockController.Finish()
}

// run test | debug test
func (suite *DemoControllerSuiteController) TestOrderDirect() {
	testCases := []struct {
		desc           string
		CompCode       string
		mockInput      []any
		expectedError  string
		expectedOutput []*models.OrderResponse
	}{
		{
			desc:          "StoreError",
			CompCode:      "fmlcompcd",
			mockInput:     []any{nil, errors.New("store error")},
			expectedError: "store error",
		},
		{
			desc:           "Success",
			CompCode:       "fmlcompcd",
			mockInput:      []any{[]*models.OrderDetails{{CompCd: sql.NullString{String: "compcd", Valid: true}, CompName: sql.NullString{String: "compname", Valid: true}}}, nil},
			expectedError:  "",
			expectedOutput: []*models.OrderResponse{{CompCode: "fmlcompcd", CompName: "fmlcompname"}},
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result

			if testCase.mockInput != nil {
				suite.demoStore.
					EXPECT().
					GetOrderDetails(gomock.Any(), "fmlcompcd").
					Return(testCase.mockInput...)
			}

			// Triggering Function
			request := &models.OrderRequest{CompCode: testCase.CompCode}
			actualOutput, err := suite.demoController.OrderDirect(suite.ctx, request)

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
