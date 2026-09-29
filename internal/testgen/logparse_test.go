package testgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logCorpus is the real riskprofile runtime log shipped with the plan
// (docs/gt7-log-fixtures-plan.md "Log corpus"). The corpus is local-only —
// it carries internal endpoints and runtime data — so a missing copy skips
// the corpus pin instead of failing a fresh clone.
func logCorpus(t *testing.T) *LogData {
	t.Helper()
	path := filepath.Join("..", "..", "riskPipelineTest", "logfile.txt")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("local corpus not present (%v)", err)
	}
	data, err := ParseLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestParseLogCorpus pins the corpus-level parse: request groups, tolerance
// counters, and the absence of hard warnings over the real log.
func TestParseLogCorpus(t *testing.T) {
	d := logCorpus(t)
	if len(d.Traces) != 18 {
		t.Errorf("traces: got %d, want 18", len(d.Traces))
	}
	if len(d.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", d.Warnings)
	}
	if d.SkippedNull == 0 {
		t.Error("expected requestID: null boot lines to be counted")
	}
	if d.StackLines < 100 {
		t.Errorf("stacktrace continuations tolerated: got %d, want >= 100", d.StackLines)
	}
	// ANSI escapes must be stripped: no caller survives with escape bytes.
	for _, tr := range d.Traces {
		for _, c := range tr.Callers {
			if strings.ContainsRune(c.Service, 0x1b) || strings.ContainsRune(c.Method, 0x1b) {
				t.Fatalf("ANSI bytes leaked into caller %+v", c)
			}
		}
	}
}

// TestTraceSelection pins the plan's selection rules over the corpus:
// first successful complete trace wins, the first failed complete trace is
// the second row, incomplete traces (403/404 with no layer caller) are
// ignored, and the service match is case-insensitive while the method is
// case-sensitive.
func TestTraceSelection(t *testing.T) {
	d := logCorpus(t)

	view := d.Trace("riskprofile", "ViewQuestions", false)
	if view == nil || !view.Successful() {
		t.Fatalf("ViewQuestions success trace: %+v", view)
	}
	if view.ID != "ebb010eb-d0a3-4db4-880e-6cedc66e83ac" {
		t.Errorf("first success: got %s", view.ID)
	}
	if view.Status != 200 || view.Path != "/v1/riskprofile/viewquestions" {
		t.Errorf("end line not captured: %+v", view)
	}
	if !strings.Contains(view.RequestBody, "FML_RQST_TYP") {
		t.Errorf("requestBody not captured: %q", view.RequestBody)
	}
	if len(view.Data) == 0 {
		t.Error("SuccessJSON data not captured")
	}
	valued := 0
	for _, c := range view.DBCalls("ViewQuestions") {
		if c.HasVal {
			valued++
			if c.Key != "result:" {
				t.Errorf("ViewQuestions result key: %q", c.Key)
			}
		}
	}
	if valued != 1 {
		t.Errorf("ViewQuestions db results: got %d valued calls", valued)
	}
	if d.Trace("RISKPROFILE", "viewquestions", false) != nil {
		t.Error("method matching must be case-sensitive")
	}

	add := d.Trace("riskprofile", "AddQuestion", false)
	if add == nil || add.ID != "d32f1b29-28ce-4a12-9bd8-2c7c08d4b8b3" {
		t.Fatalf("AddQuestion success: %+v", add)
	}
	fail := d.Trace("riskprofile", "AddQuestion", true)
	if fail == nil || fail.ID != "00cdf9df-a915-49c4-8a32-ebe67dfb2611" {
		t.Fatalf("AddQuestion failure: %+v", fail)
	}
	if fail.Successful() {
		t.Error("failed trace reported successful")
	}
	if msg, ok := fail.ErrorFor("riskprofile", "AddQuestion"); !ok || msg != "Question number must be unique across customer type" {
		t.Errorf("error attribution: %q ok=%v", msg, ok)
	}
	if msg, ok := fail.ErrorFor("riskprofile", "QuestionNumberExists"); ok {
		t.Errorf("controller error must not attribute to db method, got %q", msg)
	}

	// The 403/404 traces carry no service caller — never selectable.
	for _, tr := range d.Traces {
		if tr.ID == "ec7a7ecd-7a8a-4408-ab85-465feaea5502" || tr.ID == "1315500c-d42f-4cf4-9c95-58ce6660c8ad" {
			if tr.Complete() {
				t.Errorf("trace %s must be incomplete (no layer caller)", tr.ID)
			}
		}
	}

	// A method absent from the log yields nil (assumed fixtures downstream).
	if got := d.Trace("riskprofile", "NoSuchMethod", false); got != nil {
		t.Errorf("unknown method trace: %+v", got)
	}
}

// TestParseLogSynthetic pins the parser's tolerance rules on a hand-built
// log: ANSI, null requestIDs, stacktraces after ERROR, and one malformed
// payload line.
func TestParseLogSynthetic(t *testing.T) {
	input := "\x1b[34m09-29-2026 14:57:48 INFO db.go:169 demo/pkg/repo.dbLogger.Info database {\"requestID\": null, \"msg\": \"boot\"}\n" +
		"09-29-2026 14:58:15 \x1b[35mDEBUG\x1b[0m riskprofile.go:16 demo/pkg/services/demo/handler.(*handler).Ping SERVICE-START {\"requestID\": \"abc123def\", \"matchAccount\": null}\n" +
		"09-29-2026 14:58:15 \x1b[31mERROR\x1b[0m backoffice.go:125 demo/pkg/services/demo/controller.(*controller).Ping boom {\"requestID\": \"abc123def\"}\n" +
		"demo/pkg/services/demo/controller.(*controller).Ping\n" +
		"        /tmp/backoffice.go:125\n" +
		"09-29-2026 14:58:15 INFO custom_logger.go:53 demo/pkg/middlewares.CustomLogger.func1 API Call End {\"requestID\": \"abc123def\", \"status\": 500, \"path\": \"/ping\"}\n" +
		"09-29-2026 14:58:16 INFO custom_logger.go:53 demo/pkg/middlewares.CustomLogger.func1 API Call End NOT JSON\n" +
		"09-29-2026 14:58:17 DEBUG x.go:1 demo/pkg/services/demo/db.(*store).Ping Result: {\"requestID\": \"abc123def\", \"result:\": \"pong\"}\n"

	d := ParseLog("synthetic.log", []byte(input))
	if len(d.Traces) != 1 {
		t.Fatalf("traces: got %d, want 1 (null lines skipped)", len(d.Traces))
	}
	if d.SkippedNull != 1 {
		t.Errorf("skipped null: got %d", d.SkippedNull)
	}
	if d.StackLines != 2 {
		t.Errorf("stack lines: got %d, want 2", d.StackLines)
	}
	if len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "unparsable payload") {
		t.Errorf("warnings: %v", d.Warnings)
	}
	tr := d.Traces[0]
	if tr.Status != 500 || tr.Path != "/ping" {
		t.Errorf("end line: %+v", tr)
	}
	if !tr.Complete() || tr.Successful() {
		t.Errorf("completeness/success: complete=%v successful=%v", tr.Complete(), tr.Successful())
	}
	if calls := tr.DBCalls("Ping"); len(calls) != 1 || !calls[0].HasVal || string(calls[0].Raw) != `"pong"` {
		t.Errorf("db call: %+v", calls)
	}
	if msg, ok := tr.ErrorFor("demo", "Ping"); !ok || msg != "boom" {
		t.Errorf("error: %q ok=%v", msg, ok)
	}
}
