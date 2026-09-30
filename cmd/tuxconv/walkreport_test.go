package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/config"
	"tux-to-any/internal/walkreport"
)

// The walkreport CLI seam. The census logic itself is tested in
// internal/walkreport against the real corpus; what matters here is the
// wiring — that the command resolves a target, measures coverage for every
// function (not just the entry), censuses a tree named by a space-separated
// -controller, and that -strict is the only thing that turns a finding into
// a non-zero exit.

const walkreportFixture = "../../testdata/fixtures/stripped/SVC_MIN_KITCHEN.pc"

func TestRunWalkreportMeasuresCoverageWithoutAController(t *testing.T) {
	out := filepath.Join(t.TempDir(), "walkreport.json")
	if err := runWalkreport(context.Background(), []string{walkreportFixture, "-out", out}); err != nil {
		t.Fatalf("runWalkreport: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep walkreport.WalkReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rep.Coverage) != 1 {
		t.Fatalf("coverage = %d files, want 1", len(rep.Coverage))
	}
	if len(rep.Coverage[0].Functions) == 0 {
		t.Fatal("no functions measured")
	}
	// No -controller and no staged tree for this fixture, so the census is
	// absent rather than empty-and-silently-zero. Tidy says so.
	if rep.Census != nil {
		t.Errorf("census = %+v, want nil when there is no tree to census", rep.Census)
	}
	findings := rep.Tidy()
	if len(findings) != 1 || !strings.Contains(findings[0], "-controller") {
		t.Errorf("findings = %v, want one naming -controller", findings)
	}
}

// TestRunWalkreportMeasuresEveryFunctionNotJustTheEntry is the difference
// from `flow`, whose CLI narrows to the entry. P2 works on the helper bodies,
// so coverage that omitted them would be measuring the wrong thing.
func TestRunWalkreportMeasuresEveryFunctionNotJustTheEntry(t *testing.T) {
	target := filepath.Join("..", "..", "testdata", "fixtures", "pf")
	rep, err := walkreportCorpus(context.Background(), target, config.Default(), "")
	if err != nil {
		t.Fatal(err)
	}
	fns := map[string]bool{}
	for _, cf := range rep.Coverage {
		for _, f := range cf.Functions {
			fns[f.Function] = true
		}
	}
	if len(fns) < 2 {
		t.Errorf("measured %d function(s) (%v), want the helpers as well as the entry", len(fns), fns)
	}
}

// TestRunWalkreportCensusesAControllerInSpaceForm exercises the value-flag
// split. -controller is registered in flags.go's commandFlags table for
// exactly this reason: without the entry, reorderArgs drops the path and the
// command fails with "flag needs an argument" — which is what happened on
// the first run of this feature.
func TestRunWalkreportCensusesAControllerInSpaceForm(t *testing.T) {
	out := filepath.Join(t.TempDir(), "walkreport.json")
	ctl := "../../testdata/walkreport/controller"
	if err := runWalkreport(context.Background(), []string{walkreportFixture, "-controller", ctl, "-out", out}); err != nil {
		t.Fatalf("runWalkreport: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep walkreport.WalkReport
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Census == nil {
		t.Fatal("no census despite -controller")
	}
	if rep.Census.Total == 0 {
		t.Error("census is empty")
	}
	if rep.Controller != ctl {
		t.Errorf("Controller = %q, want %q", rep.Controller, ctl)
	}
	// The fixture tree is healthy, so Tidy must be silent — a finding here
	// would mean the committed fixture stopped classifying.
	if f := rep.Tidy(); len(f) != 0 {
		t.Errorf("findings = %v, want none for the committed fixture", f)
	}
}

// TestRunWalkreportStrictFailsOnAnUnregisteredShape is the CI gate: a new
// emitter gap shape that nobody registered must not pass unnoticed, or the
// census silently under-counts from that point on.
func TestRunWalkreportStrictFailsOnAnUnregisteredShape(t *testing.T) {
	dir := t.TempDir()
	body := "package controller\n\nfunc (s *c) M() {\n\t// tuxgo:TODO a brand new gap shape\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "tux.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// Without -strict a finding is reported but is not a failure: the
	// default is "tell me", not "break my run".
	if err := runWalkreport(context.Background(), []string{walkreportFixture, "-controller", dir}); err != nil {
		t.Errorf("default run returned an error for a finding: %v", err)
	}
	// With -strict it is a failure, which is the point of the flag.
	err := runWalkreport(context.Background(), []string{walkreportFixture, "-controller", dir, "-strict"})
	if err == nil {
		t.Fatal("-strict returned no error for an unclassified gap")
	}
	// The message must name the COUNT, not the code: the code is an
	// internal vocabulary the operator does not read, while "1 TODO(s)"
	// tells them exactly how much is unaccounted for.
	if !strings.Contains(err.Error(), "1 TODO(s) matched no registered reason code") {
		t.Errorf("error = %v, want it to name the unclassified count", err)
	}
}

// TestRunWalkreportStrictIsQuietOnACleanCensus guards the other direction: a
// gate that always fires is a gate nobody runs.
func TestRunWalkreportStrictIsQuietOnACleanCensus(t *testing.T) {
	err := runWalkreport(context.Background(), []string{
		walkreportFixture, "-controller", "../../testdata/walkreport/controller", "-strict",
	})
	if err != nil {
		t.Errorf("-strict failed on a clean census: %v", err)
	}
}

// TestRunWalkreportMissingControllerIsNotAnError keeps the "how did the last
// run go" path working: pointing at a tree that is not there reports
// coverage only rather than failing, and Tidy explains the absence.
func TestRunWalkreportMissingControllerIsNotAnError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	rep, err := walkreportCorpus(context.Background(), walkreportFixture, config.Default(), missing)
	if err != nil {
		t.Fatalf("a missing tree should not fail the command: %v", err)
	}
	if rep.Census != nil {
		t.Errorf("census = %+v, want nil for a missing tree", rep.Census)
	}
	if len(rep.Coverage) == 0 {
		t.Error("coverage was not measured")
	}
}

func TestRunWalkreportRejectsBadInput(t *testing.T) {
	if err := runWalkreport(context.Background(), []string{"no-such-file.pc"}); err == nil {
		t.Error("expected an error for a missing target")
	}
	if err := runWalkreport(context.Background(), nil); err == nil {
		t.Error("expected an error for no target")
	}
}

// TestWalkreportWritesToNestedOutPath covers the MkdirAll: -out into a
// directory that does not exist yet is the normal first-run case.
func TestWalkreportWritesToNestedOutPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a", "b", "c", "walkreport.json")
	if err := runWalkreport(context.Background(), []string{walkreportFixture, "-out", out}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("report not written to a nested path: %v", err)
	}
}
