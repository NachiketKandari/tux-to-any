package pygen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/batchflow"
	"tux-to-any/internal/budget"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/pyplan"
	scanner "tux-to-any/internal/tsscan"
)

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #2/#3) fixed
// two runtime-correctness gaps: repo-shape cursor binds counted the SELECT's
// INTO targets (and the fetch-iterator template dropped binds entirely), and
// the orchestration contract never covered post-loop/inter-loop source
// regions while demanding "exactly these blocks … nothing else". This
// fixture pins both wirings end-to-end on one synthetic batch.
const cursorBindSrc = `/* synthetic: cursor SELECT_MULTI with a WHERE bind + epilogue */
#include <stdio.h>
#include <stdlib.h>
#include <sqlca.h>
#include <atmi.h>
#include <userlog.h>

char c_ServiceName[33];
char c_errmsg[100];

void fn_close_demo(char *);

void main(int argc, char *argv[])
{
  EXEC SQL INCLUDE "table/demo_x.h";

  int   i_count;
  long  l_id;
  char  c_rowid[19];
  char  c_flg[2];

  EXEC SQL
  DECLARE cur_demo_x CURSOR FOR
      SELECT  DEMO_X_ID,
              ROWID
      FROM    DEMO_X
      WHERE   DEMO_X_FLG = :c_flg;

  EXEC SQL
      OPEN cur_demo_x;

  while(1)
  {
    EXEC SQL
        FETCH cur_demo_x INTO :l_id, :c_rowid;

    EXEC SQL
        SELECT  DEMO_Y_ID,
                ROWID
        INTO
                :l_id,
                :c_rowid
        FROM    DEMO_Y
        WHERE   DEMO_Y_FLG = :c_flg2
        AND     ROWNUM<2;

    if(SQLCODE != 0)
    {
      break;
    }
    i_count++;
  }

  EXEC SQL
      CLOSE cur_demo_x;

  fn_close_demo(c_ServiceName);
  exit(0);
}
`

func buildCursorPlan(t *testing.T, shape string) *pyplan.Plan {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bat_demo_x.pc")
	if err := os.WriteFile(path, []byte(cursorBindSrc), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	facts, err := scanner.ScanFile(path)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	flow := batchflow.Build(f, facts, cursorBindSrc)
	return pyplan.Build(flow, pyplan.Options{
		Shape: shape, DMLLoop: "batch", ChunkSize: 1000,
		LoggerPrefix: "app.", Entrypoint: "process_daily_batch",
		Wrapper: pyplan.Wrapper{Import: "core.db_router", RouterClass: "DatabaseRouter", ReadMode: "DbMode.READ", WriteMode: "DbMode.WRITE"},
	})
}

// generateSrc mirrors the shared generate harness but takes the source
// string directly (the fixture lives in a temp dir, not testdata/).
func generateSrc(t *testing.T, p *pyplan.Plan, src string, o Options) Result {
	t.Helper()
	o.Plan = p
	o.Source = src
	o.SourcePath = "bat_demo_x.pc"
	o.Budget = budget.New(12000, 4000, 4)
	o.MaxRetries = 2
	res, err := Generate(context.Background(), o)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return res
}

func TestCursorBindsMatchExecutableSQL(t *testing.T) {
	p := buildCursorPlan(t, "repo")
	var fetchMulti, fetchSingle *pyplan.RepoMethod
	for i := range p.Repo {
		switch p.Repo[i].QueryKind {
		case "SELECT_MULTI":
			fetchMulti = &p.Repo[i]
		case "SELECT_SINGLE":
			fetchSingle = &p.Repo[i]
		}
	}
	if fetchMulti == nil || fetchSingle == nil {
		t.Fatalf("missing repo fetch methods in plan: %+v", p.Repo)
	}
	if got, want := fetchMulti.Binds, []string{"c_flg"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("cursor fetch binds = %v, want %v (INTO targets l_id/c_rowid must not be binds)", got, want)
	}
	// The direct SELECT…INTO: the const strips the INTO span, so its only
	// real bind is the WHERE one. Pre-fix this listed the INTO targets too
	// (the bat_demo_returns golden bug — five kwargs against a bind-less
	// const, a guaranteed runtime failure).
	if got, want := fetchSingle.Binds, []string{"c_flg2"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("direct SELECT INTO binds = %v, want %v (INTO targets must not be binds)", got, want)
	}
}

func TestFetchIteratorRendersBinds(t *testing.T) {
	p := buildCursorPlan(t, "repo")
	res := generateSrc(t, p, cursorBindSrc, Options{NoLLM: true})
	if !strings.Contains(res.Content, `cur.execute(FETCH_DEMO_X_QUERY, {"c_flg": c_flg})`) {
		t.Errorf("iterator execute must bind the const's real binds, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, `cur.execute(FETCH_DEMO_Y_QUERY, {"c_flg2": c_flg2})`) {
		t.Errorf("fetch-one execute must bind the const's real binds, got:\n%s", res.Content)
	}
	if strings.Contains(res.Content, `"l_id"`) || strings.Contains(res.Content, `"c_rowid"`) {
		t.Errorf("INTO targets leaked into the rendered binds:\n%s", res.Content)
	}
}

func TestOrchestrationCoversEpilogue(t *testing.T) {
	p := buildCursorPlan(t, "repo")
	opts := Options{NoLLM: true, Source: cursorBindSrc, SourcePath: "bat_demo_x.pc"}
	prompt := userPrompt(p, opts, nil)
	if !strings.Contains(prompt, "epilogue (source") {
		t.Errorf("orchestration contract missing the epilogue block — post-loop logic would be dropped from the seam body:\n%s", prompt)
	}
	if !strings.Contains(prompt, "fn_close_demo(c_ServiceName);") {
		t.Errorf("epilogue block view must carry the tail statements the LLM must implement:\n%s", prompt)
	}
	res := generateSrc(t, p, cursorBindSrc, opts)
	if !res.PyOK {
		t.Errorf("generated module failed the gate: %s %s", res.PyMode, res.PyDetail)
	}
}
