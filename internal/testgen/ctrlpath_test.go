package testgen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The path model is the replacement for "which calls ran, according to one
// captured log". These pins run the two corpus controller methods that broke
// the log route through the enumerator and assert the paths it finds.

// corpusStoreIface is the subset of RiskProfileStore's declarations the
// enumerator reads: arity and result count per method.
var corpusStoreIface = map[string]dbIfaceSig{
	"GetNextQuestionID":    {ArgCount: 1, Result: "string", Results: 2},
	"QuestionNumberExists": {ArgCount: 2, Result: "bool", Results: 2},
	"GetDB":                {ArgCount: 0, Result: "*sqlx.DB", Results: 1},
	"AddQuestion":          {ArgCount: 7, Results: 1},
	"AddAnswer":            {ArgCount: 6, Results: 1},
	"GetHNICustomerType":   {ArgCount: 2, Result: "string", Results: 2},
	"DeleteQnA":            {ArgCount: 4, Results: 1},
	"GetMarks":             {ArgCount: 4, Result: "int", Results: 2},
	"UpdateMarks":          {ArgCount: 7, Results: 1},
	"InsertMarks":          {ArgCount: 7, Results: 1},
	"GetMarksScored":       {ArgCount: 3, Result: "int", Results: 2},
	"GetRiskProfile":       {ArgCount: 3, Result: "string", Results: 2},
}

// pathsOf runs the enumerator over the named method of a controller source.
func pathsOf(t *testing.T, src, name string) []ctrlPath {
	t.Helper()
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "riskprofile.go", "package controller\n"+src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		return enumerateCtrlPaths(fd, fset, corpusStoreIface, "")
	}
	t.Fatalf("method %s not found", name)
	return nil
}

// shape renders a path as "Method[!]xN" entries, for readable assertions.
func shape(p ctrlPath) string {
	var parts []string
	for _, c := range p.Calls {
		s := c.Method
		if c.ErrBranch {
			s += "!"
			if c.ErrLiteral != "" {
				s += "(" + c.ErrLiteral + ")"
			}
		}
		if c.BoolTrue {
			s += "?true"
		}
		if c.Times != "" {
			s += "x" + c.Times
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " -> ")
}

// TestPathsCoverAddQuestion pins the whole method: every guard yields a path,
// GetDB appears only on the paths that reach it, and the loop call carries its
// trip count on the success path but not on the failing one.
//
// This is the method whose Success case used to die on "Unexpected call to
// GetDB", because GetDB runs no SQL and so was absent from the log.
func TestPathsCoverAddQuestion(t *testing.T) {
	src := `import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
	"mutual-fund-be/pkg/logger"
	"mutual-fund-be/pkg/models"
	"mutual-fund-be/pkg/utils"
	"go.uber.org/zap"
)

func (c *controller) AddQuestion(ctx context.Context, request *models.AddQuestionRequest) (string, error) {
	logger.Log(ctx).Debug("START")
	defer logger.Log(ctx).Debug("END")

	questionID, err := c.store.GetNextQuestionID(ctx)
	if err != nil {
		return "", err
	}

	questionNumExists, err := c.store.QuestionNumberExists(ctx, request.QuestionNo)
	if err != nil {
		return "", err
	}

	if questionNumExists {
		logger.Log(ctx).Error("Question number must be unique across customer type")
		return "", errors.New("Question number must be unique across customer type")
	}

	logger.Log(ctx).Debug("ADD QUESTION ", zap.String("ADD QUESTION", questionID))

	err = utils.ExecTransaction(ctx, c.store.GetDB(), func(tx *sqlx.Tx) error {
		if err := c.store.AddQuestion(ctx, tx, questionID, request.QuestionNo, request.QuestionSection, request.QuestionText, request.CustomerType); err != nil {
			return err
		}

		for index := range len(request.AnswerID) {
			if err := c.store.AddAnswer(ctx, tx, questionID, request.AnswerID[index], request.AnswerText[index], request.Marks[index]); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	return "Question Added Successfully", nil
}
`
	got := pathsOf(t, src, "AddQuestion")
	want := []string{
		// Each guard stops the path at its own call.
		"GetNextQuestionID!",
		"GetNextQuestionID -> QuestionNumberExists!",
		"GetNextQuestionID -> QuestionNumberExists?true",
		// Past the business guard, GetDB is reached and precedes the body.
		"GetNextQuestionID -> QuestionNumberExists -> GetDB -> AddQuestion!",
		// The loop call fails on its first iteration, so it has no trip count.
		"GetNextQuestionID -> QuestionNumberExists -> GetDB -> AddQuestion -> AddAnswer!",
		// Success: the loop call runs once per answer.
		"GetNextQuestionID -> QuestionNumberExists -> GetDB -> AddQuestion -> AddAnswerxlen(testCase.AnswerID)",
	}
	if len(got) != len(want) {
		for i, p := range got {
			t.Logf("got  %d: %s", i, shape(p))
		}
		t.Fatalf("path count = %d, want %d", len(got), len(want))
	}
	for i, p := range got {
		if s := shape(p); s != want[i] {
			t.Errorf("path %d:\n got %s\nwant %s", i, s, want[i])
		}
	}
}

// TestGetDBIsPathDependent is the pin for the fix that was tried and reverted:
// expecting GetDB unconditionally is as wrong as never expecting it. The
// enumerator has to place it on the success path and leave it off the paths
// that return earlier.
func TestGetDBIsPathDependent(t *testing.T) {
	src := `import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
	"mutual-fund-be/pkg/utils"
)

func (c *controller) Write(ctx context.Context, key string) error {
	if err := c.store.Check(ctx, key); err != nil {
		return err
	}
	return utils.ExecTransaction(ctx, c.store.GetDB(), func(tx *sqlx.Tx) error {
		return c.store.Put(ctx, tx, key)
	})
}
`
	got := pathsOf(t, src, "Write")
	withGetDB, withoutGetDB := 0, 0
	for _, p := range got {
		has := false
		for _, c := range p.Calls {
			if c.Method == "GetDB" {
				has = true
			}
		}
		if has {
			withGetDB++
		} else {
			withoutGetDB++
		}
	}
	if withGetDB == 0 {
		t.Error("no path expects GetDB, so the success path cannot run")
	}
	if withoutGetDB == 0 {
		t.Error("every path expects GetDB, but Check's failure returns before it")
	}
}

// TestPathsSplitTheNoRowsSentinel pins the upsert shape in AssessQnA. A guard
// that tests errors.Is(err, sql.ErrNoRows) has two error edges: the sentinel
// recovers and carries on, any other error returns. Emitting one path for both
// either drops the Insert call or claims the method stops early.
func TestPathsSplitTheNoRowsSentinel(t *testing.T) {
	src := `import (
	"context"
	"database/sql"
	"errors"

	"mutual-fund-be/pkg/models"
)

func (c *controller) Save(ctx context.Context, m models.Mark) error {
	err := c.store.Update(ctx, m.ID, m.Marks)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err := c.store.Insert(ctx, m.ID, m.Marks)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return c.store.Audit(ctx, m.ID)
}
`
	got := pathsOf(t, src, "Save")
	// Order is walk order: the recovery branch is explored depth-first, so its
	// own failure is emitted before the guard's non-sentinel edge, and the
	// success path is last because the caller appends it.
	want := []string{
		// Sentinel error, and the recovery insert itself fails.
		"Update!(sql.ErrNoRows) -> Insert!",
		// Generic error on Update: returns, never reaching Insert or Audit.
		"Update!",
		// The sentinel recovers and the method carries on to Audit.
		"Update!(sql.ErrNoRows) -> Insert -> Audit",
	}
	if len(got) != len(want) {
		for i, p := range got {
			t.Logf("got %d: %s", i, shape(p))
		}
		t.Fatalf("path count = %d, want %d", len(got), len(want))
	}
	for i, p := range got {
		if s := shape(p); s != want[i] {
			t.Errorf("path %d:\n got %s\nwant %s", i, s, want[i])
		}
	}
}

// TestPathsKeepDeclaredArity pins that a path call's arity comes from the
// interface declaration, so GetDB() takes no matcher.
func TestPathsKeepDeclaredArity(t *testing.T) {
	src := `import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
	"mutual-fund-be/pkg/utils"
)

func (c *controller) Write(ctx context.Context) error {
	return utils.ExecTransaction(ctx, c.store.GetDB(), func(tx *sqlx.Tx) error {
		return nil
	})
}
`
	got := pathsOf(t, src, "Write")
	if len(got) != 1 {
		t.Fatalf("path count = %d, want 1", len(got))
	}
	if len(got[0].Calls) != 1 {
		t.Fatalf("calls = %d, want 1 (GetDB only)", len(got[0].Calls))
	}
	if got[0].Calls[0].ArgCount != 0 {
		t.Errorf("GetDB ArgCount = %d, want 0", got[0].Calls[0].ArgCount)
	}
}

// TestPathWalkIsOrderStable pins determinism of the enumerator itself: the
// same body must yield the same paths in the same order, every time. A map
// iteration anywhere in the walk would reorder cases between runs.
func TestPathWalkIsOrderStable(t *testing.T) {
	src := `import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
	"mutual-fund-be/pkg/utils"
)

func (c *controller) Write(ctx context.Context, key string) error {
	if err := c.store.A(ctx, key); err != nil {
		return err
	}
	if err := c.store.B(ctx, key); err != nil {
		return err
	}
	return utils.ExecTransaction(ctx, c.store.GetDB(), func(tx *sqlx.Tx) error {
		return c.store.C(ctx, tx, key)
	})
}
`
	first := shape(pathsOf(t, src, "Write")[0])
	for i := 0; i < 20; i++ {
		got := pathsOf(t, src, "Write")
		if shape(got[0]) != first {
			t.Fatalf("run %d differs:\n got %s\nwant %s", i, shape(got[0]), first)
		}
		for _, p := range got {
			if s := shape(p); s == "" {
				t.Fatalf("run %d produced an empty path", i)
			}
		}
	}
}

// TestPathsCoverAssessQnA is the whole-method pin for the second failing
// corpus method: a method whose store field is c.userStore rather than
// c.store, whose body is a transaction with an upsert and a loop.
func TestPathsCoverAssessQnA(t *testing.T) {
	src := `import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
	"mutual-fund-be/pkg/logger"
	"mutual-fund-be/pkg/models"
	"mutual-fund-be/pkg/utils"
)

func (c *controller) AssessQnA(ctx context.Context, request *models.AssessQnARequest) (*models.AssessQnAResponse, error) {
	logger.Log(ctx).Debug("START")
	defer logger.Log(ctx).Debug("END")

	customerType, err := c.userStore.GetHNICustomerType(ctx, request.UserID)
	if err != nil {
		return nil, err
	}

	err = utils.ExecTransaction(ctx, c.store.GetDB(), func(tx *sqlx.Tx) error {
		err := c.store.DeleteQnA(ctx, tx, request.UserID, customerType)
		if err != nil {
			return err
		}

		for _, qNa := range request.QnA {
			marks, err := c.store.GetMarks(ctx, tx, qNa.QuestionID, qNa.AnswerID)
			if err != nil {
				return err
			}

			err = c.store.UpdateMarks(ctx, tx, qNa.QuestionID, qNa.AnswerID, marks, request.UserID, customerType)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					err := c.store.InsertMarks(ctx, tx, request.UserID, qNa.QuestionID, qNa.AnswerID, marks, customerType)
					if err != nil {
						return err
					}
				} else {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &models.AssessQnAResponse{RiskProfile: "x"}, nil
}
`
	got := pathsOf(t, src, "AssessQnA")

	// The userStore call is a step like any other: its own failure path exists.
	if shape(got[0]) != "GetHNICustomerType!" {
		t.Errorf("path 0 = %s, want GetHNICustomerType!", shape(got[0]))
	}
	// No path may reach the transaction without GetDB: that was the panic.
	for i, p := range got {
		reachesTx := false
		for _, c := range p.Calls {
			if c.Method == "GetRiskProfile" {
				reachesTx = true
			}
		}
		if !reachesTx {
			continue
		}
		hasGetDB := false
		for _, c := range p.Calls {
			if c.Method == "GetDB" {
				hasGetDB = true
			}
		}
		if !hasGetDB {
			t.Errorf("path %d reaches the transaction without GetDB: %s", i, shape(p))
		}
	}
	// GetHNICustomerType must precede GetDB wherever both appear.
	for i, p := range got {
		idxUser, idxDB := -1, -1
		for j, c := range p.Calls {
			if c.Method == "GetHNICustomerType" && idxUser < 0 {
				idxUser = j
			}
			if c.Method == "GetDB" && idxDB < 0 {
				idxDB = j
			}
		}
		if idxUser >= 0 && idxDB >= 0 && idxUser > idxDB {
			t.Errorf("path %d has GetDB before GetHNICustomerType: %s", i, shape(p))
		}
	}
	if len(got) < 5 {
		t.Errorf("path count = %d, want at least 5 (one per guard)", len(got))
	}
}
