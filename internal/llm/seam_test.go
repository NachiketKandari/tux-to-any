package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/audit"
	"tux-to-any/internal/budget"
)

// stubClient is a scripted Client for seam tests: each pop of the script
// returns one chat outcome; the last entry repeats.
type stubClient struct {
	responses []chatOutcome
	calls     int
	lastReq   ChatRequest
}

type chatOutcome struct {
	content      string
	err          error
	finishReason string
	usage        Usage
}

func (s *stubClient) Chat(_ context.Context, req ChatRequest) (Response, error) {
	if s.calls < len(s.responses) {
		o := s.responses[s.calls]
		s.calls++
		s.lastReq = req
		if o.err != nil {
			return Response{}, o.err
		}
		return Response{Content: o.content, FinishReason: o.finishReason, Usage: o.usage}, nil
	}
	o := s.responses[len(s.responses)-1]
	s.calls++
	s.lastReq = req
	if o.err != nil {
		return Response{}, o.err
	}
	return Response{Content: o.content, FinishReason: o.finishReason, Usage: o.usage}, nil
}

func (s *stubClient) Stream(ctx context.Context, req ChatRequest, onDelta func(string) error) (Response, error) {
	return s.Chat(ctx, req)
}

func newRecorder(t *testing.T) *audit.Recorder {
	t.Helper()
	rec, err := audit.New(t.TempDir(), "test-run")
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	return rec
}

// mustReadExchange reads one archived exchange from the recorder's folder,
// failing the test when it is missing or unparsable.
func mustReadExchange(t *testing.T, rec *audit.Recorder, name string) audit.Exchange {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(rec.Dir(), name))
	if err != nil {
		t.Fatalf("read exchange %s: %v", name, err)
	}
	var e audit.Exchange
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("parse exchange %s: %v", name, err)
	}
	return e
}

func seamInput(c Client, rec *audit.Recorder) SeamInput {
	return SeamInput{
		Unit: "u01", Kind: "testseam", Name: "demo", Audit: rec,
		Client: c, MaxRetries: 2,
		Prompt: func(_ string, notes []string) (string, []Message) {
			return "prompt", []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "prompt"}}
		},
	}
}

func TestRunSeamSuccessFirstAttempt(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{{content: "```go\nbody\n```"}}}
	rec := newRecorder(t)
	in := seamInput(c, rec)
	in.Extract = func(content string) string { return ExtractFenced(content, "go") }
	payload, calls, notes, err := RunSeam(context.Background(), in)
	if err != nil {
		t.Fatalf("RunSeam: %v", err)
	}
	if payload != "body" || calls != 1 || len(notes) != 0 {
		t.Fatalf("payload=%q calls=%d notes=%v", payload, calls, notes)
	}
	e := mustReadExchange(t, rec, "testseam-demo-attempt0.json")
	if e.Outcome != "ok" || e.Unit != "u01" || e.Prompt != "prompt" || e.Response != "```go\nbody\n```" {
		t.Fatalf("archived exchange: %+v", e)
	}
	if c.lastReq.Temperature != 0 {
		t.Fatalf("temperature not passed through: %v", c.lastReq.Temperature)
	}
}

func TestRunSeamGateRetryFeedsNotesAndSucceeds(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{
		{content: "bad"},
		{content: "good"},
	}}
	rec := newRecorder(t)
	in := seamInput(c, rec)
	in.Gate = func(payload string) []string {
		if payload == "bad" {
			return []string{"gate: not table-driven"}
		}
		return nil
	}
	seen := ""
	in.Prompt = func(_ string, notes []string) (string, []Message) {
		seen = strings.Join(notes, "|")
		return "prompt", []Message{{Role: "user", Content: "p"}}
	}
	payload, calls, notes, err := RunSeam(context.Background(), in)
	if err != nil {
		t.Fatalf("RunSeam: %v", err)
	}
	if payload != "good" || calls != 2 {
		t.Fatalf("payload=%q calls=%d", payload, calls)
	}
	if seen != "gate: not table-driven" {
		t.Fatalf("retry prompt did not carry the gate note: %q", seen)
	}
	if len(notes) != 1 || notes[0] != "gate: not table-driven" {
		t.Fatalf("notes=%v", notes)
	}
}

func TestRunSeamExhaustionReturnsJoinedGateError(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{{content: "bad"}}}
	in := seamInput(c, nil)
	in.MaxRetries = 1
	in.Gate = func(string) []string { return []string{"e1", "e2"} }
	_, calls, notes, err := RunSeam(context.Background(), in)
	if err == nil || err.Error() != "e1; e2" {
		t.Fatalf("err=%v", err)
	}
	if calls != 2 || len(notes) != 4 {
		t.Fatalf("calls=%d notes=%v", calls, notes)
	}
}

// TestRunSeamRejectedCallback pins the visible-fallback hook: every gated
// payload reaches Rejected (the last one on total failure), while chat
// errors have no payload to offer.
func TestRunSeamRejectedCallback(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{{content: "v1"}, {content: "v2"}}}
	in := seamInput(c, nil)
	in.MaxRetries = 1
	in.Gate = func(string) []string { return []string{"bad"} }
	var got []string
	in.Rejected = func(p string) { got = append(got, p) }
	payload, calls, _, err := RunSeam(context.Background(), in)
	if err == nil || payload != "" || calls != 2 {
		t.Fatalf("payload=%q calls=%d err=%v", payload, calls, err)
	}
	if strings.Join(got, ",") != "v1,v2" {
		t.Fatalf("rejected payloads = %v, want [v1 v2]", got)
	}

	boom := errors.New("connection refused")
	c2 := &stubClient{responses: []chatOutcome{{err: boom}}}
	in2 := seamInput(c2, nil)
	in2.MaxRetries = 0
	got = nil
	in2.Rejected = func(p string) { got = append(got, p) }
	_, _, _, err = RunSeam(context.Background(), in2)
	if err == nil || len(got) != 0 {
		t.Fatalf("chat error fired Rejected (%v, err=%v)", got, err)
	}
}

func TestRunSeamChatErrorAbortPolicy(t *testing.T) {
	boom := errors.New("connection refused")
	c := &stubClient{responses: []chatOutcome{{err: boom}}}
	rec := newRecorder(t)
	in := seamInput(c, rec)
	in.AbortOnChatError = true
	_, calls, _, err := RunSeam(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "llm chat: connection refused") {
		t.Fatalf("err=%v", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	e := mustReadExchange(t, rec, "testseam-demo-attempt0.json")
	if e.Outcome != "failed" || len(e.Errors) != 1 || e.Errors[0] != "connection refused" {
		t.Fatalf("archived exchange: %+v", e)
	}
}

func TestRunSeamChatErrorRetryPolicy(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{
		{err: errors.New("transient")},
		{content: "ok"},
	}}
	in := seamInput(c, nil)
	payload, calls, notes, err := RunSeam(context.Background(), in)
	if err != nil || payload != "ok" || calls != 2 {
		t.Fatalf("payload=%q calls=%d err=%v", payload, calls, err)
	}
	if len(notes) != 1 || notes[0] != "transient" {
		t.Fatalf("notes=%v", notes)
	}
}

func TestRunSeamBudgetCeilings(t *testing.T) {
	long := strings.Repeat("x", 100)
	in := seamInput(&stubClient{responses: []chatOutcome{{content: long}}}, nil)
	in.Budget = budget.New(1000, 10, 4) // output ceiling trips
	_, _, notes, err := RunSeam(context.Background(), in)
	if err == nil {
		t.Fatal("over-budget output must fail after retries")
	}
	if len(notes) == 0 || !strings.HasPrefix(notes[0], "output over budget: ") {
		t.Fatalf("notes=%v", notes)
	}

	in = seamInput(&stubClient{responses: []chatOutcome{{content: "x"}}}, nil)
	in.Budget = budget.New(1, 100, 4) // prompt ceiling trips before any call
	in.Prompt = func(string, []string) (string, []Message) { return long, nil }
	payload, calls, _, err := RunSeam(context.Background(), in)
	if payload != "" || calls != 0 {
		t.Fatalf("payload=%q calls=%d", payload, calls)
	}
	if err == nil || !strings.Contains(err.Error(), "exceeds the 1-token ceiling") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunSeamMaxRetriesClampAndFileOverride(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{{content: "ok"}}}
	rec := newRecorder(t)
	in := seamInput(c, rec)
	in.MaxRetries = -5
	in.File = func(attempt int) string { return fmt.Sprintf("unit-u01-attempt%d.json", attempt) }
	if _, _, _, err := RunSeam(context.Background(), in); err != nil {
		t.Fatalf("RunSeam: %v", err)
	}
	if e := mustReadExchange(t, rec, "unit-u01-attempt0.json"); e.Unit != "u01" {
		t.Fatalf("custom file name not archived: %+v", e)
	}
}

func TestExtractFenced(t *testing.T) {
	cases := []struct {
		name, content, lang, want string
	}{
		{"tagged fence", "prose\n```python\nprint(1)\n```\ntail", "python", "print(1)"},
		{"bare fence fallback", "```\nx\n```", "python", "x"},
		{"no fence", "  raw\n", "go", "  raw"},
		{"padding only trimmed", "```go\n\n  indented keeps spaces\n\n```", "go", "  indented keeps spaces"},
		{"unclosed fence", "```go\npartial", "go", "partial"},
	}
	for _, tc := range cases {
		if got := ExtractFenced(tc.content, tc.lang); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestJSONObject(t *testing.T) {
	if got := JSONObject("noise {\"a\":1} noise"); got != `{"a":1}` {
		t.Errorf("got %q", got)
	}
	if got := JSONObject("no braces"); got != "" {
		t.Errorf("got %q", got)
	}
}

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #8) pinned
// the truncation signal: a finish_reason other than "stop" must be visible
// — in the archived Exchange (finish_reason + usage fields), in the run
// log, and in the notes that reach the next attempt's prompt. Pre-fix,
// RunSeam read only Content and a length-cut payload was
// indistinguishable from success.
func TestRunSeamSignalsTruncation(t *testing.T) {
	rec := newRecorder(t)
	client := &stubClient{responses: []chatOutcome{{
		content:      "func partial(",
		finishReason: "length",
		usage:        Usage{PromptTokens: 100, CompletionTokens: 42, TotalTokens: 142},
	}}}
	payload, calls, notes, err := RunSeam(context.Background(), SeamInput{
		Unit: "u1", Kind: "test", Name: "trunc", Audit: rec, Client: client,
		Prompt: func(string, []string) (string, []Message) { return "p", nil },
	})
	if err != nil {
		t.Fatalf("gate-less seam must accept the payload: %v", err)
	}
	if payload != "func partial(" || calls != 1 {
		t.Fatalf("payload/calls = %q/%d", payload, calls)
	}
	joined := strings.Join(notes, "; ")
	if !strings.Contains(joined, "truncated") || !strings.Contains(joined, "length") {
		t.Errorf("notes must carry the truncation signal, got %v", notes)
	}
	data, rerr := os.ReadFile(filepath.Join(rec.Dir(), "test-trunc-attempt0.json"))
	if rerr != nil {
		t.Fatalf("exchange artifact: %v", rerr)
	}
	var ex audit.Exchange
	if err := json.Unmarshal(data, &ex); err != nil {
		t.Fatal(err)
	}
	if ex.FinishReason != "length" {
		t.Errorf("exchange finish_reason = %q, want length", ex.FinishReason)
	}
	if ex.PromptTokens != 100 || ex.CompletionTokens != 42 || ex.TotalTokens != 142 || ex.UsageEstimated {
		t.Errorf("exchange usage = %+v estimated=%v", ex, ex.UsageEstimated)
	}
	if ex.Outcome != "ok" {
		t.Errorf("outcome = %q — truncation alone must not flip the outcome the gate decided", ex.Outcome)
	}
}

// TestRunSeamHandsPriorPayloadToPrompt pins the repair-retry contract: the
// Prompt callback sees "" on the first attempt and the prior gated payload
// on retries, and the Repair flag lands in the archived Exchange.
func TestRunSeamHandsPriorPayloadToPrompt(t *testing.T) {
	client := &stubClient{responses: []chatOutcome{{content: "bad"}, {content: "good"}}}
	rec := newRecorder(t)
	var prevs []string
	payload, calls, _, err := RunSeam(context.Background(), SeamInput{
		Unit: "u1", Kind: "test", Name: "prev", Client: client, MaxRetries: 1, Repair: true,
		Audit: rec,
		Prompt: func(prev string, notes []string) (string, []Message) {
			prevs = append(prevs, prev)
			return "p", nil
		},
		Gate: func(payload string) []string {
			if payload == "bad" {
				return []string{"nope"}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunSeam: %v", err)
	}
	if payload != "good" || calls != 2 {
		t.Fatalf("payload=%q calls=%d", payload, calls)
	}
	if len(prevs) != 2 || prevs[0] != "" || prevs[1] != "bad" {
		t.Fatalf("prev sequence = %q, want [\"\" \"bad\"]", prevs)
	}
	e := mustReadExchange(t, rec, "test-prev-attempt0.json")
	if !e.RetryRepair {
		t.Error("attempt 0 must archive retry_repair=true")
	}
}

// TestRunSeamCountsRetryTurnsAgainstInputBudget: feedback and repair turns
// are real input — a retry whose full message set exceeds the prompt ceiling
// fails before the chat call instead of silently blowing the context.
func TestRunSeamCountsRetryTurnsAgainstInputBudget(t *testing.T) {
	client := &stubClient{responses: []chatOutcome{{content: "bad"}, {content: "good"}}}
	base := strings.Repeat("x", 4)  // 1 token at 4 chars/token
	note := strings.Repeat("y", 12) // 3 tokens
	in := seamInput(client, nil)
	in.Budget = budget.New(3, 100, 4) // 3-token prompt ceiling
	in.MaxRetries = 1
	in.Prompt = func(_ string, notes []string) (string, []Message) {
		msgs := []Message{{Role: "system", Content: "s"}, {Role: "user", Content: base}}
		for _, n := range notes {
			msgs = append(msgs, Message{Role: "user", Content: n})
		}
		return base, msgs
	}
	in.Gate = func(payload string) []string {
		if payload == "bad" {
			return []string{note}
		}
		return nil
	}
	_, calls, _, err := RunSeam(context.Background(), in)
	if err == nil {
		t.Fatal("second attempt must trip the prompt ceiling")
	}
	if calls != 1 {
		t.Fatalf("calls = %d — the over-ceiling retry must fail before the chat call", calls)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want the prompt-ceiling failure", err)
	}
}

// TestRunSeamClearsPrevOnChatError: a transport failure leaves no payload to
// patch, so the next attempt's prev is empty rather than stale.
func TestRunSeamClearsPrevOnChatError(t *testing.T) {
	client := &stubClient{responses: []chatOutcome{
		{err: errors.New("dial tcp: refused")},
		{content: "good"},
	}}
	var prevs []string
	_, calls, _, err := RunSeam(context.Background(), SeamInput{
		Kind: "test", Name: "prevclear", Client: client, MaxRetries: 1, Repair: true,
		Prompt: func(prev string, notes []string) (string, []Message) {
			prevs = append(prevs, prev)
			return "p", nil
		},
	})
	if err != nil {
		t.Fatalf("RunSeam: %v", err)
	}
	if calls != 2 || len(prevs) != 2 || prevs[1] != "" {
		t.Fatalf("calls=%d prevs=%q — chat errors must clear prev", calls, prevs)
	}
}

// The truncation note rides into the next attempt's prompt notes when the
// gate rejects the truncated payload.
func TestRunSeamTruncationFeedsRetryNotes(t *testing.T) {
	client := &stubClient{responses: []chatOutcome{
		{content: "bad(", finishReason: "length"},
		{content: "good", finishReason: "stop"},
	}}
	var seen []string
	_, calls, notes, err := RunSeam(context.Background(), SeamInput{
		Kind: "test", Name: "retry", Client: client, MaxRetries: 1,
		Prompt: func(_ string, n []string) (string, []Message) {
			seen = append(seen, strings.Join(n, "|"))
			return "p", nil
		},
		Gate: func(payload string) []string {
			if payload == "bad(" {
				return []string{"unbalanced"}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("second attempt must pass: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if !strings.Contains(seen[1], "truncated") {
		t.Errorf("attempt 1 notes must carry the truncation signal, got %q", seen[1])
	}
	if !strings.Contains(strings.Join(notes, "; "), "unbalanced") {
		t.Errorf("final notes must carry the gate errors, got %v", notes)
	}
}

// TestRunSeamDynamicOutputCap pins the dynamic output policy: the request
// carries the computed room (context − input − reserve, clamped by the
// model completion cap), and CheckOutput judges against the same cap.
func TestRunSeamDynamicOutputCap(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{{content: strings.Repeat("x", 100)}}}
	in := seamInput(c, nil)
	in.Budget = budget.New(1000, 0, 4)
	in.Budget.ModelContextTokens = 1000
	in.Budget.ModelMaxOutputTokens = 600
	in.Budget.OutputReserveTokens = 0
	in.Prompt = func(string, []string) (string, []Message) {
		return strings.Repeat("x", 400), nil // 100 estimator tokens
	}
	payload, _, _, err := RunSeam(context.Background(), in)
	if err != nil || payload != strings.Repeat("x", 100) {
		t.Fatalf("payload=%q err=%v", payload, err)
	}
	if got := c.lastReq.MaxTokens; got != 600 {
		t.Errorf("request MaxTokens = %d, want the clamped dynamic cap 600", got)
	}
}

// TestRunSeamDynamicNoRoom pins the loud posture when the prompt consumes
// the context: the seam fails before any call instead of generating into a
// collapsed room.
func TestRunSeamDynamicNoRoom(t *testing.T) {
	c := &stubClient{responses: []chatOutcome{{content: "x"}}}
	in := seamInput(c, nil)
	in.Budget = budget.New(1000, 0, 4)
	in.Budget.ModelContextTokens = 1000
	in.Budget.ModelMaxOutputTokens = 16384
	in.Budget.OutputReserveTokens = 0
	in.Prompt = func(string, []string) (string, []Message) {
		return strings.Repeat("x", 3500), nil // 875 tokens → room 125 < 256
	}
	_, calls, _, err := RunSeam(context.Background(), in)
	if calls != 0 {
		t.Errorf("calls = %d, want 0 (no chat call)", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "output room") {
		t.Fatalf("err=%v, want the loud no-room failure", err)
	}
}
