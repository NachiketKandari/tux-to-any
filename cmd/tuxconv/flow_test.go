package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tux-to-any/internal/config"
)

// The flow inspection command was the one tuxconv subcommand the
// convert-tux-to-go → tux-to-any rename dropped (`tuxgo flow`). It is
// restored on the new stack (tsscan extraction + flow.Build/Match/
// RenderSpan + discover's scenario artifacts). These pin the restored
// surface: target resolution, the JSON report, the draft flag, and the
// scenario emission on a dispatch-axis fixture.
func TestFlowTargetsEntryAndLibrary(t *testing.T) {
	files, err := extractFlowIR(filepath.Join("..", "..", "testdata", "fixtures", "stripped", "SVC_MIN_KITCHEN.pc"), config.Default())
	if err != nil {
		t.Fatalf("extractFlowIR: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
}

func TestRunFlowSummaryAndJSON(t *testing.T) {
	target := filepath.Join("..", "..", "testdata", "fixtures", "stripped", "SVC_MIN_KITCHEN.pc")
	out := filepath.Join(t.TempDir(), "flow.json")
	if err := runFlow(context.Background(), []string{target, "-out", out}); err != nil {
		t.Fatalf("runFlow: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep flowReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if len(rep.Files) != 1 || len(rep.Files[0].Functions) == 0 {
		t.Fatalf("report = %+v, want one file with functions", rep)
	}
	if rep.Files[0].Functions[0].Tree == nil {
		t.Errorf("function tree missing from report")
	}
}

func TestRunFlowDraftAndScenarios(t *testing.T) {
	target := filepath.Join("..", "..", "testdata", "fixtures", "nav", "SVC_DEMO_LIST.pc")
	if err := runFlow(context.Background(), []string{target, "-go"}); err != nil {
		t.Fatalf("runFlow -go: %v", err)
	}
	scenDir := t.TempDir()
	if err := runFlow(context.Background(), []string{target, "-scenarios", "-scenarios-dir", scenDir}); err != nil {
		t.Fatalf("runFlow -scenarios: %v", err)
	}
	entries, err := os.ReadDir(scenDir)
	if err != nil {
		t.Fatalf("read scenarios dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no scenario artifacts written to %s", scenDir)
	}
}

func TestRunFlowMissingTarget(t *testing.T) {
	if err := runFlow(context.Background(), []string{"no-such-file.pc"}); err == nil {
		t.Fatalf("expected an error for a missing target")
	}
	if err := runFlow(context.Background(), nil); err == nil {
		t.Fatalf("expected an error for no target")
	}
}
