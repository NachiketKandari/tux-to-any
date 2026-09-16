package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestCollectRun pins the retrystats read-out: Exchange artifacts aggregate
// per unit (attempts, tokens, first-try, final outcome, failures), the mode
// distinguishes repair from re-roll, and non-Exchange artifacts in the same
// folder are ignored.
func TestCollectRun(t *testing.T) {
	dir := t.TempDir()
	writeEx := func(e Exchange) {
		t.Helper()
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("%s-%s-attempt%d.json", e.Kind, e.Name, e.Attempt)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeEx(Exchange{Kind: "controller_method", Name: "X", Attempt: 0, Outcome: "failed",
		Errors: []string{"rune literal 'N'"}, PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	writeEx(Exchange{Kind: "controller_method", Name: "X", Attempt: 1, Outcome: "ok", RetryRepair: true,
		PromptTokens: 200, CompletionTokens: 80, TotalTokens: 280})
	writeEx(Exchange{Kind: "fn_helper", Name: "Y", Attempt: 0, Outcome: "ok",
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	// Non-Exchange artifact in the same folder must be skipped.
	if err := os.WriteFile(filepath.Join(dir, "ir-main.json"), []byte(`{"entry":"X"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := CollectRun(dir)
	if err != nil {
		t.Fatalf("CollectRun: %v", err)
	}
	if r.Mode() != "mixed" {
		t.Errorf("mode = %q, want mixed (X repair, Y roll)", r.Mode())
	}
	x := r.Units["controller_method/X"]
	if x == nil {
		t.Fatal("controller_method/X missing")
	}
	if x.Attempts != 2 || !x.Accepted || x.FirstTry || x.Outcome != "ok" || x.TotalTokens != 430 {
		t.Errorf("X stats = %+v", x)
	}
	y := r.Units["fn_helper/Y"]
	if y == nil || !y.FirstTry || y.TotalTokens != 15 {
		t.Errorf("Y stats = %+v", y)
	}
	s := r.Summary()
	if s.Units != 2 || s.Accepted != 2 || s.Failed != 0 || s.FirstTry != 1 || s.Attempts != 3 || s.TotalTokens != 445 {
		t.Errorf("summary = %+v", s)
	}
}
