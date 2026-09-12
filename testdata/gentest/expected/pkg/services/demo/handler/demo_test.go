package handler

import (
	"context"
	"demo-be/pkg/logger"
	"demo-be/pkg/network"
	"demo-be/pkg/services/demo/controller"
	"demo-be/pkg/services/demo/models"
	"demo-be/pkg/utils"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

/*
MockGen
mockgen -source=pkg/services/demo/handler/interface.go -destination=pkg/services/demo/handler/interface_mock.go -package=handler

Coverage
go test pkg/services/demo/handler/demo_test.go pkg/services/demo/handler/demo.go pkg/services/demo/handler/interface.go -v -coverprofile=coverage.txt -covermode count && go tool cover -html=coverage.txt
*/

type DemoHandlerSuite struct {
	suite.Suite
	ctx            context.Context
	demoController *controller.MockDemoController
	demoHandler    DemoHandler
}

// run test | debug test
func TestDemoHandlerSuite(t *testing.T) {
	suite.Run(t, new(DemoHandlerSuite))
}

func (suite *DemoHandlerSuite) SetupSuite() {
	gin.SetMode(gin.TestMode)
	logger.LoggerInit("", -1)

	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		utils.RegisterValidations(v)
	}

	suite.ctx = context.TODO()
	suite.demoController = controller.NewMockDemoController(gomock.NewController(suite.T()))
	suite.demoHandler = NewDemoHandler(suite.demoController)
}

// run test | debug test
func (suite *DemoHandlerSuite) TestOrderList() {
	testCases := []struct {
		desc                  string
		CompCode              string
		mockInput             []any
		expectedError         string
		expectedErrorHttpCode int
	}{
		{
			desc:                  "OrderListError",
			CompCode:              "fmlcompcd",
			mockInput:             []any{nil, errors.New("error while fetching data")},
			expectedError:         "error while fetching data",
			expectedErrorHttpCode: http.StatusInternalServerError,
		},
		{
			desc:                  "Failure",
			CompCode:              "fmlcompcd",
			mockInput:             []any{nil, nil},
			expectedError:         "No Data Found",
			expectedErrorHttpCode: http.StatusNoContent,
		},
		{
			desc:      "Success",
			CompCode:  "fmlcompcd",
			mockInput: []any{[]*models.OrderResponse{{CompCode: "fmlcompcd", CompName: "fmlcompname"}}, nil},
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			request := models.OrderRequest{CompCode: testCase.CompCode}

			response, ctx := utils.CreateTestGinContext(http.MethodPost, request, nil, nil, nil)
			if testCase.mockInput != nil {
				suite.demoController.
					EXPECT().
					OrderList(ctx, &request).
					Return(testCase.mockInput...)
			}

			// Triggering Function
			suite.demoHandler.OrderList(ctx)
			if response.Code == http.StatusNoContent {
				assert.Empty(t, response.Body.String(), "Expected empty body for 204 No Content")
				return
			}

			var httpResponse network.HttpResponse
			err := json.Unmarshal(response.Body.Bytes(), &httpResponse)
			if err != nil {
				suite.T().Errorf("unable to unmarshal response: %v\nresponse body: %s", err, response.Body.String())
				return
			}

			// Validations
			if testCase.expectedError != "" {
				assert.Equal(t, response.Code, testCase.expectedErrorHttpCode)
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else {
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.OrderResponse](httpResponse.Data)
				assert.Equal(t, testCase.mockInput[0], *actualResponse)
			}
		})
	}
}
