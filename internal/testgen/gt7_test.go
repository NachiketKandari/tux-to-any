package testgen

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The GT-7 end-to-end fixture: a small converted riskprofile-shaped service
// plus a runtime log modeled on riskPipelineTest/logfile.txt. The log carries
// a successful and a failed AddQuestion trace (the plan's example), a
// ViewQuestions trace with real request/response bodies, and a tx scalar
// read (GetMarks) — enough to exercise every extraction rule.
const (
	gt7GoMod = "module demo-be\n\ngo 1.26.4\n"

	gt7Models = `package models

import "database/sql"

type ViewQuestionsRequest struct {
	RequestType  string ` + "`" + `json:"FML_RQST_TYP"` + "`" + `
	UserID       string ` + "`" + `json:"FML_USR_ID"` + "`" + `
	CustomerType string ` + "`" + `json:"FML_TYP_OF_USR"` + "`" + `
}

type ViewQuestionsResponse struct {
	QuestionSection string ` + "`" + `json:"FML_USR_ADDRSS2_LN1,omitempty"` + "`" + `
	QuestionID      string ` + "`" + `json:"FML_PRTFLO_ID,omitempty"` + "`" + `
	QuestionNo      string ` + "`" + `json:"FML_URQ_RQST_NMBR,omitempty"` + "`" + `
	QuestionText    string ` + "`" + `json:"FML_USR_ADDRSS2_LN2,omitempty"` + "`" + `
	AnswerID        string ` + "`" + `json:"FML_POINT_TYPE,omitempty"` + "`" + `
	AnswerText      string ` + "`" + `json:"FML_SEC_GRP,omitempty"` + "`" + `
	Marks           string ` + "`" + `json:"FML_VLME,omitempty"` + "`" + `
	QuestionId      string ` + "`" + `json:"FML_CDM_CD_ID,omitempty"` + "`" + `
}

type ViewQuestionsResult struct {
	QuestionSection sql.NullString ` + "`" + `db:"QuestionSection"` + "`" + `
	QuestionNo      sql.NullString ` + "`" + `db:"QuestionNo"` + "`" + `
	QuestionID      sql.NullString ` + "`" + `db:"QuestionID"` + "`" + `
	QuestionText    sql.NullString ` + "`" + `db:"QuestionText"` + "`" + `
	AnswerID        sql.NullString ` + "`" + `db:"AnswerID"` + "`" + `
	AnswerText      sql.NullString ` + "`" + `db:"AnswerText"` + "`" + `
	Marks           sql.NullString ` + "`" + `db:"Marks"` + "`" + `
}

type AddQuestionRequest struct {
	QuestionNo string ` + "`" + `json:"FML_URQ_RQST_NMBR"` + "`" + `
}
`

	gt7DBStore = `package db

import (
	"context"
	"database/sql"

	"demo-be/pkg/services/riskprofile/models"

	"github.com/jmoiron/sqlx"
)

type store struct{ db *sqlx.DB }

func (g *store) ViewQuestions(ctx context.Context, customerType string) ([]*models.ViewQuestionsResult, error) {
	var rows []*models.ViewQuestionsResult
	query := ` + "`" + `SELECT QuestionSection FROM RPQM_RP_QUESTION_MASTER WHERE RPQM_TYP = :1` + "`" + `
	err := g.db.SelectContext(ctx, &rows, query, customerType)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (g *store) GetMarks(ctx context.Context, tx *sqlx.Tx, questionId, answerId string) (string, error) {
	var marks sql.NullString
	query := ` + "`" + `SELECT MARKS FROM RPQM_MARKS WHERE Q = :1 AND A = :2` + "`" + `
	err := tx.GetContext(ctx, &marks, query, questionId, answerId)
	if err != nil {
		return "", err
	}
	return marks.String, nil
}

func (g *store) GetNextQuestionID(ctx context.Context) (string, error) {
	var questionID sql.NullString
	query := ` + "`" + `SELECT MAX(RPQM_QSTN_ID) + 1 FROM RPQM_RP_QUESTION_MASTER` + "`" + `
	err := g.db.GetContext(ctx, &questionID, query)
	if err != nil {
		return "", err
	}
	return questionID.String, nil
}

func (g *store) QuestionNumberExists(ctx context.Context, questionNo string) (bool, error) {
	var exists sql.NullInt16
	query := ` + "`" + `SELECT COUNT(*) FROM RPQM_RP_QUESTION_MASTER WHERE RPQM_QSTN_NO = :1` + "`" + `
	err := g.db.GetContext(ctx, &exists, query, questionNo)
	if err != nil {
		return false, err
	}
	return exists.Int16 == 1, nil
}

func (g *store) AddQuestion(ctx context.Context, questionNo string) error {
	query := ` + "`" + `INSERT INTO RPQM_RP_QUESTION_MASTER (RPQM_QSTN_NO) VALUES (:1)` + "`" + `
	_, err := g.db.ExecContext(ctx, query, questionNo)
	return err
}
`

	gt7DBIface = `package db

import (
	"context"

	"demo-be/pkg/services/riskprofile/models"

	"github.com/jmoiron/sqlx"
)

type RiskProfileStore interface {
	ViewQuestions(ctx context.Context, customerType string) ([]*models.ViewQuestionsResult, error)
	GetMarks(ctx context.Context, tx *sqlx.Tx, questionId, answerId string) (string, error)
	GetNextQuestionID(ctx context.Context) (string, error)
	QuestionNumberExists(ctx context.Context, questionNo string) (bool, error)
	AddQuestion(ctx context.Context, questionNo string) error
}

func NewRiskProfileStore(db *sqlx.DB) RiskProfileStore { return &store{} }
`

	gt7Controller = `package controller

import (
	"context"
	"errors"

	"demo-be/pkg/services/riskprofile/db"
	"demo-be/pkg/services/riskprofile/models"
)

type controller struct {
	store db.RiskProfileStore
}

func (c *controller) ViewQuestions(ctx context.Context, request *models.ViewQuestionsRequest) (data []*models.ViewQuestionsResponse, err error) {
	result, err := c.store.ViewQuestions(ctx, request.CustomerType)
	if err != nil {
		return nil, err
	}
	for _, row := range result {
		data = append(data, &models.ViewQuestionsResponse{
			QuestionSection: row.QuestionSection.String,
			QuestionID:      row.QuestionID.String,
			QuestionNo:      row.QuestionNo.String,
			QuestionText:    row.QuestionText.String,
			AnswerID:        row.AnswerID.String,
			AnswerText:      row.AnswerText.String,
			Marks:           row.Marks.String,
			QuestionId:      row.QuestionID.String,
		})
	}
	return data, err
}

func (c *controller) AddQuestion(ctx context.Context, request *models.AddQuestionRequest) (string, error) {
	if _, err := c.store.GetNextQuestionID(ctx); err != nil {
		return "", err
	}
	exists, err := c.store.QuestionNumberExists(ctx, request.QuestionNo)
	if err != nil {
		return "", err
	}
	if exists {
		return "", errors.New("Question number must be unique across customer type")
	}
	if err := c.store.AddQuestion(ctx, request.QuestionNo); err != nil {
		return "", err
	}
	return "Question Added Successfully", nil
}
`

	gt7CtrlIface = `package controller

import (
	"context"

	"demo-be/pkg/services/riskprofile/db"
	"demo-be/pkg/services/riskprofile/models"
)

type RiskProfileController interface {
	ViewQuestions(ctx context.Context, request *models.ViewQuestionsRequest) (data []*models.ViewQuestionsResponse, err error)
	AddQuestion(ctx context.Context, request *models.AddQuestionRequest) (string, error)
}

func NewRiskProfileController(store db.RiskProfileStore) RiskProfileController { return &controller{store: store} }
`

	gt7Handler = `package handler

import (
	"demo-be/pkg/services/riskprofile/models"

	"github.com/gin-gonic/gin"
)

type handler struct {
	controller RiskProfileController
}

func (f *handler) ViewQuestions(c *gin.Context) {
	var request models.ViewQuestionsRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	data, err := f.controller.ViewQuestions(c, &request)
	if err != nil {
		return
	}
	c.JSON(200, data)
}

func (f *handler) AddQuestion(c *gin.Context) {
	var request models.AddQuestionRequest
	if err := c.BindJSON(&request); err != nil {
		return
	}
	data, err := f.controller.AddQuestion(c, &request)
	if err != nil {
		return
	}
	c.JSON(200, data)
}
`

	gt7HandlerIface = `package handler

import (
	"demo-be/pkg/services/riskprofile/models"

	"github.com/gin-gonic/gin"
)

type RiskProfileController interface {
	ViewQuestions(ctx context.Context, request *models.ViewQuestionsRequest) (data []*models.ViewQuestionsResponse, err error)
	AddQuestion(ctx context.Context, request *models.AddQuestionRequest) (string, error)
}

type RiskProfileHandler interface {
	ViewQuestions(c *gin.Context)
	AddQuestion(c *gin.Context)
}

func NewRiskProfileHandler(controller RiskProfileController) RiskProfileHandler {
	return &handler{controller: controller}
}
`
)

// gt7Log is the synthetic runtime log: two AddQuestion traces (success +
// business failure), one ViewQuestions trace, one GetMarks scalar read.
const gt7Log = `09-29-2026 14:58:15 DEBUG riskprofile.go:16 demo-be/pkg/services/riskprofile/handler.(*handler).ViewQuestions SERVICE-START {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null}
09-29-2026 14:58:15 DEBUG backoffice.go:30 demo-be/pkg/services/riskprofile/controller.(*controller).ViewQuestions START {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null}
09-29-2026 14:58:15 DEBUG riskprofile.go:32 demo-be/pkg/services/riskprofile/db.(*store).ViewQuestions ViewQuestions {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null}
09-29-2026 14:58:15 DEBUG riskprofile.go:64 demo-be/pkg/services/riskprofile/db.(*store).ViewQuestions Result: {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null, "result:": [{"QuestionSection":{"String":"SECTION A: RISK TAKING ABILITY","Valid":true},"QuestionNo":{"String":"2","Valid":true},"QuestionID":{"String":"87","Valid":true},"QuestionText":{"String":"Your current annual income","Valid":true},"AnswerID":{"String":"a","Valid":true},"AnswerText":{"String":"Under RS 10 Lacs","Valid":true},"Marks":{"String":"1","Valid":true}}]}
09-29-2026 14:58:15 DEBUG backoffice.go:67 demo-be/pkg/services/riskprofile/controller.(*controller).ViewQuestions END {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null}
09-29-2026 14:58:15 DEBUG httpResponse.go:87 demo-be/pkg/network.(*GinContext).SuccessJSON data {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null, "data": [{"FML_USR_ADDRSS2_LN1":"SECTION A: RISK TAKING ABILITY","FML_PRTFLO_ID":"87","FML_URQ_RQST_NMBR":"2","FML_USR_ADDRSS2_LN2":"Your current annual income","FML_POINT_TYPE":"a","FML_SEC_GRP":"Under RS 10 Lacs","FML_VLME":"1","FML_CDM_CD_ID":"87"}]}
09-29-2026 14:58:15 DEBUG riskprofile.go:46 demo-be/pkg/services/riskprofile/handler.(*handler).ViewQuestions SERVICE-END {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null}
09-29-2026 14:58:15 INFO custom_logger.go:53 demo-be/pkg/middlewares.CustomLogger.func1 API Call End {"requestID": "11111111-1111-4111-8111-111111111111", "matchAccount": null, "path": "/v1/riskprofile/viewquestions", "status": 200, "method": "POST", "requestBody": "{\n    \"FML_RQST_TYP\": \"B\" ,\n    \"FML_USR_ID\": \"system\",\n    \"FML_TYP_OF_USR\": \"R\"\n}\n", "responseBody": "{\"status\":\"success\"}"}
09-29-2026 14:58:42 DEBUG assessQnA.go:73 demo-be/pkg/services/riskprofile/db.(*store).GetMarks Result: {"requestID": "22222222-2222-4222-8222-222222222222", "matchAccount": "8509001825", "result:": "3"}
09-29-2026 14:58:42 INFO custom_logger.go:53 demo-be/pkg/middlewares.CustomLogger.func1 API Call End {"requestID": "22222222-2222-4222-8222-222222222222", "matchAccount": "8509001825", "path": "/v1/riskprofile/assessqna", "status": 200, "method": "POST", "requestBody": "{\"FML_RQST_TYP\": \"C\"}", "responseBody": "{\"status\":\"success\",\"data\":{\"FML_USR_ADDRSS2_LN1\":\"High Growth\"}}"}
09-29-2026 15:03:06 DEBUG riskprofile.go:50 demo-be/pkg/services/riskprofile/handler.(*handler).AddQuestion SERVICE-START {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG backoffice.go:111 demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion START {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG riskprofile.go:111 demo-be/pkg/services/riskprofile/db.(*store).GetNextQuestionID GetNextQuestionID {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG riskprofile.go:123 demo-be/pkg/services/riskprofile/db.(*store).GetNextQuestionID QuestionID: {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null, "QuestionID:": "91"}
09-29-2026 15:03:06 DEBUG riskprofile.go:141 demo-be/pkg/services/riskprofile/db.(*store).QuestionNumberExists QuestionNumberExists {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG riskprofile.go:153 demo-be/pkg/services/riskprofile/db.(*store).QuestionNumberExists The Value for Exists: {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null, "Exists:": {"Int16":0,"Valid":true}}
09-29-2026 15:03:06 DEBUG backoffice.go:115 demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion ADD QUESTION {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG riskprofile.go:170 demo-be/pkg/services/riskprofile/db.(*store).AddQuestion AddQuestion {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG riskprofile.go:180 demo-be/pkg/services/riskprofile/db.(*store).AddQuestion Question Added Successfully {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG backoffice.go:122 demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion END {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 DEBUG httpResponse.go:87 demo-be/pkg/network.(*GinContext).SuccessJSON data {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null, "data": "Question Added Successfully"}
09-29-2026 15:03:06 DEBUG riskprofile.go:60 demo-be/pkg/services/riskprofile/handler.(*handler).AddQuestion SERVICE-END {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null}
09-29-2026 15:03:06 INFO custom_logger.go:53 demo-be/pkg/middlewares.CustomLogger.func1 API Call End {"requestID": "33333333-3333-4333-8333-333333333333", "matchAccount": null, "path": "/api/riskprofile/addquestion", "status": 200, "method": "POST", "requestBody": "{ \"FML_URQ_RQST_NMBR\": \"929\" }", "responseBody": "{\"status\":\"success\",\"data\":\"Question Added Successfully\"}"}
09-29-2026 15:04:06 DEBUG riskprofile.go:50 demo-be/pkg/services/riskprofile/handler.(*handler).AddQuestion SERVICE-START {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 DEBUG backoffice.go:111 demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion START {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 DEBUG riskprofile.go:111 demo-be/pkg/services/riskprofile/db.(*store).GetNextQuestionID GetNextQuestionID {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 DEBUG riskprofile.go:123 demo-be/pkg/services/riskprofile/db.(*store).GetNextQuestionID QuestionID: {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null, "QuestionID:": "91"}
09-29-2026 15:04:06 DEBUG riskprofile.go:141 demo-be/pkg/services/riskprofile/db.(*store).QuestionNumberExists QuestionNumberExists {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 DEBUG riskprofile.go:153 demo-be/pkg/services/riskprofile/db.(*store).QuestionNumberExists The Value for Exists: {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null, "Exists:": {"Int16":1,"Valid":true}}
09-29-2026 15:04:06 ERROR backoffice.go:125 demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion Question number must be unique across customer type {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion
        /tmp/backoffice.go:125
09-29-2026 15:04:06 DEBUG backoffice.go:130 demo-be/pkg/services/riskprofile/controller.(*controller).AddQuestion END {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 ERROR httpResponse.go:110 demo-be/pkg/network.(*GinContext).internalServerFailureJSON Something went wrong {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 DEBUG riskprofile.go:60 demo-be/pkg/services/riskprofile/handler.(*handler).AddQuestion SERVICE-END {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null}
09-29-2026 15:04:06 INFO custom_logger.go:53 demo-be/pkg/middlewares.CustomLogger.func1 API Call End {"requestID": "44444444-4444-4444-8444-444444444444", "matchAccount": null, "path": "/api/riskprofile/addquestion", "status": 500, "method": "POST", "requestBody": "{ \"FML_URQ_RQST_NMBR\": \"909\" }", "responseBody": "{\"status\":\"failure\",\"error\":{\"errCode\":1001,\"errType\":\"InternalServerError\",\"shortError\":\"Internal server error\",\"description\":\"Question number must be unique across customer type\"},\"FML_ERROR_MSG\":\"Question number must be unique across customer type\"}"}
`

// buildGT7Tree materializes the synthetic service and returns the service dir.
func buildGT7Tree(t *testing.T, root string) string {
	t.Helper()
	svc := filepath.Join(root, "pkg", "services", "riskprofile")
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", gt7GoMod)
	write("pkg/services/riskprofile/models/models.go", gt7Models)
	write("pkg/services/riskprofile/db/store.go", gt7DBStore)
	write("pkg/services/riskprofile/db/interface.go", gt7DBIface)
	write("pkg/services/riskprofile/controller/controller.go", gt7Controller)
	write("pkg/services/riskprofile/controller/interface.go", gt7CtrlIface)
	write("pkg/services/riskprofile/handler/handler.go", gt7Handler)
	write("pkg/services/riskprofile/handler/interface.go", gt7HandlerIface)
	return svc
}

func gt7Output(t *testing.T, files []string, suffix string) string {
	t.Helper()
	for _, f := range files {
		if strings.HasSuffix(filepath.ToSlash(f), suffix) {
			return read(t, f)
		}
	}
	t.Fatalf("no output file ending %q in %v", suffix, files)
	return ""
}

// TestGT7LogDrivenGeneration is the GT-7 end-to-end: with -log-file values,
// db rows/expectations, controller request/response fields, mock returns and
// the logged business-error case all come from the log; provenance is
// recorded; the field-mapping controller becomes deterministic.
func TestGT7LogDrivenGeneration(t *testing.T) {
	root := t.TempDir()
	svc := buildGT7Tree(t, root)
	out := filepath.Join(root, "_staged")
	logData := ParseLog("riskprofile.log", []byte(gt7Log))
	if len(logData.Warnings) != 0 {
		t.Fatalf("unexpected log warnings: %v", logData.Warnings)
	}

	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{
		BaseDir: out, Workers: 1, NoLLM: true, Log: logData,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.LLMCalls != 0 {
		t.Errorf("no-llm run made %d llm calls", res.LLMCalls)
	}
	parseAll(t, res.Files)
	if len(res.Files) != 3 {
		t.Fatalf("want 3 test files, got %v", res.Files)
	}
	for _, want := range []string{
		filepath.Join(out, "pkg", "services", "riskprofile", "db", "store_test.go"),
		filepath.Join(out, "pkg", "services", "riskprofile", "controller", "controller_test.go"),
		filepath.Join(out, "pkg", "services", "riskprofile", "handler", "handler_test.go"),
	} {
		found := false
		for _, f := range res.Files {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing output %s in %v", want, res.Files)
		}
	}

	// Statuses: the field-mapping ViewQuestions/AddQuestion controllers are
	// deterministic under the log instead of llm-required.
	if got := unitStatus(res, "ViewQuestions"); got != StatusTemplate {
		t.Errorf("controller ViewQuestions: got %s, want generated", got)
	}
	if got := unitStatus(res, "AddQuestion"); got != StatusTemplate {
		t.Errorf("controller AddQuestion: got %s, want generated", got)
	}

	// Fixture gap report entries: every generated method carries provenance.
	sources := map[string]string{}
	for _, f := range res.Fixtures {
		sources[f.Layer+"/"+f.Func] = f.Source
	}
	if sources["db/ViewQuestions"] != "log 11111111" {
		t.Errorf("db/ViewQuestions provenance: %q", sources["db/ViewQuestions"])
	}
	if sources["controller/ViewQuestions"] != "log 11111111" {
		t.Errorf("controller/ViewQuestions provenance: %q", sources["controller/ViewQuestions"])
	}

	dbOut := gt7Output(t, res.Files, "db/store_test.go")
	for _, want := range []string{
		"// fixture: log 11111111",
		`sqlmock.NewRows([]string{"QuestionSection", "QuestionNo", "QuestionID", "QuestionText", "AnswerID", "AnswerText", "Marks"}).AddRow("SECTION A: RISK TAKING ABILITY", "2", "87", "Your current annual income", "a", "Under RS 10 Lacs", "1")`,
		`sql.NullString{String: "SECTION A: RISK TAKING ABILITY", Valid: true}`,
		// GetMarks: tv-variant scalar read with the logged value and the
		// extracted string return type (not the Null* scan var).
		`AddRow("3")`,
		`expectedOutput: "3"`,
		// QuestionNumberExists: bool flag inferred from the logged int16,
		// plus the failed trace's row as the second logged case.
		`desc:           "Logged#2"`,
		`AddRow("1")`,
		`expectedOutput: true`,
		`expectedOutput: false`,
	} {
		if !strings.Contains(dbOut, want) {
			t.Errorf("db output missing %q\n---\n%s", want, dbOut)
		}
	}

	ctrlOut := gt7Output(t, res.Files, "controller/controller_test.go")
	for _, want := range []string{
		"// fixture: log 11111111",
		"RequestType:",
		"UserID:",
		"CustomerType:",
		`ViewQuestions(gomock.Any(), "R")`,
		`expectedOutput: []*models.ViewQuestionsResponse{{QuestionSection: "SECTION A: RISK TAKING ABILITY", QuestionID: "87", QuestionNo: "2", QuestionText: "Your current annual income", AnswerID: "a", AnswerText: "Under RS 10 Lacs", Marks: "1", QuestionId: "87"}}`,
		// Multi-call AddQuestion with the logged scalar/bool/DML returns.
		`GetNextQuestionID(gomock.Any())`,
		// The question number differs between the success (929) and failed
		// (909) traces, so the shared EXPECT matcher degrades to Any.
		`QuestionNumberExists(gomock.Any(), gomock.Any())`,
		`[]any{"91", nil}`,
		`[]any{false, nil}`,
		// The DML call is `AddQuestion(ctx, questionNo string) error` — one
		// result — so its payload has one element. It used to be []any{nil,
		// nil}, which is the arity mismatch that broke riskprofile's
		// EditMarks on the corpus.
		`[]any{nil}`,
		// The logged business error from the failed trace, with its own
		// request values (909 vs 929) and executed-call returns.
		`"Logged-Error"`,
		`"909"`,
		`"Question number must be unique across customer type"`,
		`[]any{true, nil}`,
	} {
		if !strings.Contains(ctrlOut, want) {
			t.Errorf("controller output missing %q\n---\n%s", want, ctrlOut)
		}
	}

	hOut := gt7Output(t, res.Files, "handler/handler_test.go")
	for _, want := range []string{
		"// fixture: log 11111111",
		`"system"`,
		`"909"`,
		`"Logged-Error"`,
		`"Question number must be unique across customer type"`,
		// The logged error's own description is asserted, not a numeric
		// status code: the code belongs to the host's GinContext, which is
		// outside the scanned service. The old pin demanded 500, which this
		// service never returns.
		`assert.Contains(t, httpResponse.Error.Description, testCase.expectedError)`,
		`[]any{"Question Added Successfully", nil}`,
	} {
		if !strings.Contains(hOut, want) {
			t.Errorf("handler output missing %q\n---\n%s", want, hOut)
		}
	}
}

// TestGT7Deterministic: same tree + same log = byte-identical output across
// worker counts (the plan's determinism rule).
func TestGT7Deterministic(t *testing.T) {
	run := func(workers int) []byte {
		root := t.TempDir()
		svc := buildGT7Tree(t, root)
		out := filepath.Join(root, "_staged")
		tgt, rep := scanTarget(t, svc)
		logData := ParseLog("riskprofile.log", []byte(gt7Log))
		if _, err := Generate(context.Background(), tgt, rep, Options{
			BaseDir: out, Workers: workers, NoLLM: true, Log: logData,
		}); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		for _, f := range res(t, out) {
			rel, err := filepath.Rel(out, f)
			if err != nil {
				t.Fatal(err)
			}
			buf.WriteString("=== " + rel + "\n")
			buf.Write(readBytes(t, f))
		}
		return buf.Bytes()
	}
	a := run(1)
	b := run(3)
	if !bytes.Equal(a, b) {
		t.Fatal("log-driven output differs between workers=1 and workers=3")
	}
}

// TestGT7NoLogStaysAssumed pins the backward-compatibility contract: without
// -log-file the pipeline is byte-identical to the assumed-placeholder
// behavior and writes no provenance comment.
func TestGT7NoLogStaysAssumed(t *testing.T) {
	root := t.TempDir()
	svc := buildGT7Tree(t, root)
	out := filepath.Join(root, "_staged")
	tgt, rep := scanTarget(t, svc)
	res, err := Generate(context.Background(), tgt, rep, Options{BaseDir: out, Workers: 1, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fixtures) != 0 {
		t.Errorf("fixture provenance recorded without a log: %v", res.Fixtures)
	}
	for _, f := range res.Files {
		if strings.Contains(read(t, f), "// fixture:") {
			t.Errorf("%s carries a provenance comment without a log", f)
		}
	}
}
