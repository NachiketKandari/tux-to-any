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
		desc          string
		CompCode      string
		mockInput     []any
		expectedError string
		expectFailure bool
	}{
		{
			desc:          "Success",
			CompCode:      "fmlcompcd",
			mockInput:     []any{[]*models.OrderResponse{{CompCode: "fmlcompcd", CompName: "fmlcompname"}}, nil},
			expectedError: "",
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
			//
			// The envelope's own Status and description are asserted, not a
			// numeric status code. The code is chosen by the host's
			// GinContext — Success→200, Failure→404, BadRequest→400 in the
			// corpus — which is outside the service being scanned, so
			// asserting a number here would be a guess. The old template
			// asserted 500 for a controller error and 204 for "No Data
			// Found", neither of which the service produces.
			if testCase.expectedError != "" {
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else if testCase.expectFailure {
				// The binder refused the request. The failure Status is this
				// service's own choice; the description is the validator
				// library's wording and is deliberately not asserted.
				assert.Equal(t, httpResponse.Status, "failure")
			} else {
				// An empty payload is a legitimate 204, not a failure: the
				// corpus answers No Content when the controller returns no
				// data, and gin writes no body for it.
				if response.Code == http.StatusNoContent {
					return
				}
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.OrderResponse](httpResponse.Data)
				if testCase.mockInput[0] == nil {
					// An empty success carries no data. TypeConverter hands
					// back a non-nil POINTER to the (nil) payload, so the
					// nil check is on the dereferenced value: that is where
					// the nil pointer or nil slice lives. Asserting on the
					// pointer itself was "Expected nil, but got:
					// (**models.AssessQnAResponse)(0x…)" on every empty case.
					assert.Nil(t, *actualResponse)
				} else {
					assert.Equal(t, testCase.mockInput[0], *actualResponse)
				}
			}
		})
	}
}

// run test | debug test
func (suite *DemoHandlerSuite) TestOrderMarksFetch() {
	testCases := []struct {
		desc          string
		CompCode      string
		Marks         []string
		mockInput     []any
		expectedError string
		expectFailure bool
	}{
		{
			desc:          "Success",
			CompCode:      "fmlcompcd",
			Marks:         []string{"marks"},
			mockInput:     []any{[]*models.MarksResponse{{CompCode: "fmlcompcd", Marks: "fmlmarks"}}, nil},
			expectedError: "",
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			request := models.MarksRequest{CompCode: testCase.CompCode, Marks: testCase.Marks}

			response, ctx := utils.CreateTestGinContext(http.MethodPost, request, nil, nil, nil)
			if testCase.mockInput != nil {
				suite.demoController.
					EXPECT().
					OrderMarks(ctx).
					Return(testCase.mockInput...)
			}

			// Triggering Function
			suite.demoHandler.OrderMarksFetch(ctx)
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
			//
			// The envelope's own Status and description are asserted, not a
			// numeric status code. The code is chosen by the host's
			// GinContext — Success→200, Failure→404, BadRequest→400 in the
			// corpus — which is outside the service being scanned, so
			// asserting a number here would be a guess. The old template
			// asserted 500 for a controller error and 204 for "No Data
			// Found", neither of which the service produces.
			if testCase.expectedError != "" {
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else if testCase.expectFailure {
				// The binder refused the request. The failure Status is this
				// service's own choice; the description is the validator
				// library's wording and is deliberately not asserted.
				assert.Equal(t, httpResponse.Status, "failure")
			} else {
				// An empty payload is a legitimate 204, not a failure: the
				// corpus answers No Content when the controller returns no
				// data, and gin writes no body for it.
				if response.Code == http.StatusNoContent {
					return
				}
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.MarksResponse](httpResponse.Data)
				if testCase.mockInput[0] == nil {
					// An empty success carries no data. TypeConverter hands
					// back a non-nil POINTER to the (nil) payload, so the
					// nil check is on the dereferenced value: that is where
					// the nil pointer or nil slice lives. Asserting on the
					// pointer itself was "Expected nil, but got:
					// (**models.AssessQnAResponse)(0x…)" on every empty case.
					assert.Nil(t, *actualResponse)
				} else {
					assert.Equal(t, testCase.mockInput[0], *actualResponse)
				}
			}
		})
	}
}

// run test | debug test
func (suite *DemoHandlerSuite) TestOrderMarkList() {
	testCases := []struct {
		desc          string
		CompCode      string
		Marks         []string
		mockInput     []any
		expectedError string
		expectFailure bool
	}{
		{
			desc:          "Success",
			CompCode:      "fmlcompcd",
			Marks:         []string{"marks"},
			mockInput:     []any{[]*models.MarksResponse{{CompCode: "fmlcompcd", Marks: "fmlmarks"}}, nil},
			expectedError: "",
		},
	}

	for _, testCase := range testCases {
		suite.T().Run(testCase.desc, func(t *testing.T) {
			// Mocking and Setting Expected Result
			request := models.MarksRequest{CompCode: testCase.CompCode, Marks: testCase.Marks}

			response, ctx := utils.CreateTestGinContext(http.MethodPost, request, nil, nil, nil)
			if testCase.mockInput != nil {
				suite.demoController.
					EXPECT().
					OrderMarkList(ctx, &request).
					Return(testCase.mockInput...)
			}

			// Triggering Function
			suite.demoHandler.OrderMarkList(ctx)
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
			//
			// The envelope's own Status and description are asserted, not a
			// numeric status code. The code is chosen by the host's
			// GinContext — Success→200, Failure→404, BadRequest→400 in the
			// corpus — which is outside the service being scanned, so
			// asserting a number here would be a guess. The old template
			// asserted 500 for a controller error and 204 for "No Data
			// Found", neither of which the service produces.
			if testCase.expectedError != "" {
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else if testCase.expectFailure {
				// The binder refused the request. The failure Status is this
				// service's own choice; the description is the validator
				// library's wording and is deliberately not asserted.
				assert.Equal(t, httpResponse.Status, "failure")
			} else {
				// An empty payload is a legitimate 204, not a failure: the
				// corpus answers No Content when the controller returns no
				// data, and gin writes no body for it.
				if response.Code == http.StatusNoContent {
					return
				}
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.MarksResponse](httpResponse.Data)
				if testCase.mockInput[0] == nil {
					// An empty success carries no data. TypeConverter hands
					// back a non-nil POINTER to the (nil) payload, so the
					// nil check is on the dereferenced value: that is where
					// the nil pointer or nil slice lives. Asserting on the
					// pointer itself was "Expected nil, but got:
					// (**models.AssessQnAResponse)(0x…)" on every empty case.
					assert.Nil(t, *actualResponse)
				} else {
					assert.Equal(t, testCase.mockInput[0], *actualResponse)
				}
			}
		})
	}
}

// run test | debug test
func (suite *DemoHandlerSuite) TestOrderEither() {
	testCases := []struct {
		desc          string
		CompCode      string
		mockInput     []any
		expectedError string
		expectFailure bool
	}{
		{
			desc:          "Success",
			CompCode:      "L",
			mockInput:     []any{[]*models.OrderResponse{{CompCode: "fmlcompcd", CompName: "fmlcompname"}}, nil},
			expectedError: "",
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
					OrderDirect(ctx, &request).
					Return(testCase.mockInput...)
			}

			// Triggering Function
			suite.demoHandler.OrderEither(ctx)
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
			//
			// The envelope's own Status and description are asserted, not a
			// numeric status code. The code is chosen by the host's
			// GinContext — Success→200, Failure→404, BadRequest→400 in the
			// corpus — which is outside the service being scanned, so
			// asserting a number here would be a guess. The old template
			// asserted 500 for a controller error and 204 for "No Data
			// Found", neither of which the service produces.
			if testCase.expectedError != "" {
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else if testCase.expectFailure {
				// The binder refused the request. The failure Status is this
				// service's own choice; the description is the validator
				// library's wording and is deliberately not asserted.
				assert.Equal(t, httpResponse.Status, "failure")
			} else {
				// An empty payload is a legitimate 204, not a failure: the
				// corpus answers No Content when the controller returns no
				// data, and gin writes no body for it.
				if response.Code == http.StatusNoContent {
					return
				}
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.OrderResponse](httpResponse.Data)
				if testCase.mockInput[0] == nil {
					// An empty success carries no data. TypeConverter hands
					// back a non-nil POINTER to the (nil) payload, so the
					// nil check is on the dereferenced value: that is where
					// the nil pointer or nil slice lives. Asserting on the
					// pointer itself was "Expected nil, but got:
					// (**models.AssessQnAResponse)(0x…)" on every empty case.
					assert.Nil(t, *actualResponse)
				} else {
					assert.Equal(t, testCase.mockInput[0], *actualResponse)
				}
			}
		})
	}
}

// run test | debug test
func (suite *DemoHandlerSuite) TestOrderAudit() {
	testCases := []struct {
		desc          string
		CompCode      string
		mockInput     []any
		expectedError string
		expectFailure bool
	}{
		{
			desc:          "Success",
			CompCode:      "fmlcompcd",
			mockInput:     []any{[]*models.OrderResponse{{CompCode: "fmlcompcd", CompName: "fmlcompname"}}, nil},
			expectedError: "",
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
					OrderAudit(ctx, &request).
					Return(testCase.mockInput...)
			}

			// Triggering Function
			suite.demoHandler.OrderAudit(ctx)
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
			//
			// The envelope's own Status and description are asserted, not a
			// numeric status code. The code is chosen by the host's
			// GinContext — Success→200, Failure→404, BadRequest→400 in the
			// corpus — which is outside the service being scanned, so
			// asserting a number here would be a guess. The old template
			// asserted 500 for a controller error and 204 for "No Data
			// Found", neither of which the service produces.
			if testCase.expectedError != "" {
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else if testCase.expectFailure {
				// The binder refused the request. The failure Status is this
				// service's own choice; the description is the validator
				// library's wording and is deliberately not asserted.
				assert.Equal(t, httpResponse.Status, "failure")
			} else {
				// An empty payload is a legitimate 204, not a failure: the
				// corpus answers No Content when the controller returns no
				// data, and gin writes no body for it.
				if response.Code == http.StatusNoContent {
					return
				}
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.OrderResponse](httpResponse.Data)
				if testCase.mockInput[0] == nil {
					// An empty success carries no data. TypeConverter hands
					// back a non-nil POINTER to the (nil) payload, so the
					// nil check is on the dereferenced value: that is where
					// the nil pointer or nil slice lives. Asserting on the
					// pointer itself was "Expected nil, but got:
					// (**models.AssessQnAResponse)(0x…)" on every empty case.
					assert.Nil(t, *actualResponse)
				} else {
					assert.Equal(t, testCase.mockInput[0], *actualResponse)
				}
			}
		})
	}
}

// run test | debug test
func (suite *DemoHandlerSuite) TestOrderEnvelope() {
	testCases := []struct {
		desc          string
		CompCode      string
		mockInput     []any
		expectedError string
		expectFailure bool
	}{
		{
			desc:          "OrderEnvelopeError",
			CompCode:      "fmlcompcd",
			mockInput:     []any{nil, errors.New("error while fetching data")},
			expectedError: "error while fetching data",
		},
		{
			desc:          "EmptyResult",
			CompCode:      "fmlcompcd",
			mockInput:     []any{nil, nil},
			expectedError: "",
		},
		{
			desc:          "BadRequest",
			CompCode:      "",
			mockInput:     nil,
			expectedError: "",
			expectFailure: true,
		},
		{
			desc:          "Success",
			CompCode:      "fmlcompcd",
			mockInput:     []any{[]*models.OrderResponse{{CompCode: "fmlcompcd", CompName: "fmlcompname"}}, nil},
			expectedError: "",
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
			suite.demoHandler.OrderEnvelope(ctx)
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
			//
			// The envelope's own Status and description are asserted, not a
			// numeric status code. The code is chosen by the host's
			// GinContext — Success→200, Failure→404, BadRequest→400 in the
			// corpus — which is outside the service being scanned, so
			// asserting a number here would be a guess. The old template
			// asserted 500 for a controller error and 204 for "No Data
			// Found", neither of which the service produces.
			if testCase.expectedError != "" {
				assert.Equal(t, httpResponse.Status, "failure")
				assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)
			} else if testCase.expectFailure {
				// The binder refused the request. The failure Status is this
				// service's own choice; the description is the validator
				// library's wording and is deliberately not asserted.
				assert.Equal(t, httpResponse.Status, "failure")
			} else {
				// An empty payload is a legitimate 204, not a failure: the
				// corpus answers No Content when the controller returns no
				// data, and gin writes no body for it.
				if response.Code == http.StatusNoContent {
					return
				}
				assert.Equal(t, response.Code, http.StatusOK)
				assert.Equal(t, httpResponse.Status, "success")

				actualResponse, _ := utils.TypeConverter[[]*models.OrderResponse](httpResponse.Data)
				if testCase.mockInput[0] == nil {
					// An empty success carries no data. TypeConverter hands
					// back a non-nil POINTER to the (nil) payload, so the
					// nil check is on the dereferenced value: that is where
					// the nil pointer or nil slice lives. Asserting on the
					// pointer itself was "Expected nil, but got:
					// (**models.AssessQnAResponse)(0x…)" on every empty case.
					assert.Nil(t, *actualResponse)
				} else {
					assert.Equal(t, testCase.mockInput[0], *actualResponse)
				}
			}
		})
	}
}
