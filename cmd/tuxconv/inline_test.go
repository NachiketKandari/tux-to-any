package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tux-to-any/internal/ir"
)

// strippedDir is the demo corpus: one service that calls fn_min_check (SQL
// bearing) out of fn_min_lib.pc, plus chk_session, which the pass refuses.
const strippedDir = "../../testdata/stripped"

func loadStripped(t *testing.T) []*ir.File {
	t.Helper()
	files, err := ir.ExtractDir(strippedDir)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func svcOf(t *testing.T, files []*ir.File) *ir.File {
	t.Helper()
	for _, f := range files {
		if filepath.Base(f.Path) == "SVC_MIN_KITCHEN.pc" {
			return f
		}
	}
	t.Fatal("SVC_MIN_KITCHEN.pc missing")
	return nil
}

// TestExpandFnsMaterializesCaller is the wiring's whole point: after the CLI
// seam, the service's IR has the helper as a LOCAL function and its SELECT
// owned by it, so plan.Build plans it as a KindFnHelper and convert renders
// the body into controller/fns.go instead of leaving a stub + a TODO.
func TestExpandFnsMaterializesCaller(t *testing.T) {
	files := loadStripped(t)
	svc := svcOf(t, files)
	src, err := os.ReadFile(svc.Path)
	if err != nil {
		t.Fatal(err)
	}
	// Before: the helper is external.
	var beforeExternal bool
	for _, e := range svc.ExternalFns {
		if e.Name == "fn_min_check" {
			beforeExternal = true
		}
	}
	if !beforeExternal {
		t.Fatal("fixture no longer has fn_min_check external — the test lost its premise")
	}

	mainIR, mainSrc, err := expandFns(context.Background(), svc, files, string(src), false)
	if err != nil {
		t.Fatal(err)
	}
	if mainIR == svc {
		t.Fatal("expandFns returned the original IR — nothing was expanded")
	}
	var local bool
	for _, fn := range mainIR.Functions {
		if fn == "fn_min_check" {
			local = true
		}
	}
	if !local {
		t.Fatalf("fn_min_check is not a local function after expansion: %v", mainIR.Functions)
	}
	for _, e := range mainIR.ExternalFns {
		if e.Name == "fn_min_check" {
			t.Fatal("fn_min_check is still external after expansion")
		}
	}
	var owned bool
	for _, q := range mainIR.Queries {
		if q.OwningFunction == "fn_min_check" {
			owned = true
		}
	}
	if !owned {
		t.Fatal("the helper's SELECT is not owned by the helper — plan would not give it a db unit")
	}
	// The expanded SOURCE must carry the body, because plan slices each
	// helper's view out of Options.Source.
	if !contains(mainSrc, "int fn_min_check(") {
		t.Fatal("expanded source does not contain the helper definition — plan would slice nothing")
	}
	// And the caller's own bytes are untouched, so its line numbers hold.
	if !contains(mainSrc, "void SVC_MIN_KITCHEN(") {
		t.Fatal("expanded source lost the entry function")
	}
}

// TestExpandFnsOptOutIsByteExact pins the escape hatch: with the pass
// disabled, the same IR pointer and the same source string come back, so a
// user who hits an inlining surprise can reproduce the old run exactly.
func TestExpandFnsOptOutIsByteExact(t *testing.T) {
	files := loadStripped(t)
	svc := svcOf(t, files)
	src, err := os.ReadFile(svc.Path)
	if err != nil {
		t.Fatal(err)
	}
	gotIR, gotSrc, err := expandFns(context.Background(), svc, files, string(src), true)
	if err != nil {
		t.Fatal(err)
	}
	if gotIR != svc {
		t.Fatal("opt-out returned a different IR pointer")
	}
	if gotSrc != string(src) {
		t.Fatal("opt-out returned different source text")
	}
}

// TestExpandFnsSingleFileIsUntouched pins the scope rule: one file has no
// corpus, so there is nothing to resolve against and the pass must not run
// (an extract of a lone service file keeps reporting the helper as external,
// which is the truth about that file on its own).
func TestExpandFnsSingleFileIsUntouched(t *testing.T) {
	files := loadStripped(t)
	svc := svcOf(t, files)
	src, err := os.ReadFile(svc.Path)
	if err != nil {
		t.Fatal(err)
	}
	gotIR, gotSrc, err := expandFns(context.Background(), svc, []*ir.File{svc}, string(src), false)
	if err != nil {
		t.Fatal(err)
	}
	if gotIR != svc || gotSrc != string(src) {
		t.Fatal("single-file mode was modified — it has no corpus to resolve against")
	}
}

// TestExpandFnsNoExternalsIsNoOp pins that a corpus that never needed
// inlining is left alone, so wiring the pass in cannot perturb unrelated
// conversions.
func TestExpandFnsNoExternalsIsNoOp(t *testing.T) {
	files := loadStripped(t)
	var lib *ir.File
	for _, f := range files {
		if filepath.Base(f.Path) == "fn_min_lib.pc" {
			lib = f
		}
	}
	if lib == nil {
		t.Fatal("fn_min_lib.pc missing")
	}
	if len(lib.ExternalFns) != 0 {
		t.Fatalf("fixture drift: fn_min_lib has %d external fns", len(lib.ExternalFns))
	}
	src, err := os.ReadFile(lib.Path)
	if err != nil {
		t.Fatal(err)
	}
	gotIR, gotSrc, err := expandFns(context.Background(), lib, files, string(src), false)
	if err != nil {
		t.Fatal(err)
	}
	if gotIR != lib || gotSrc != string(src) {
		t.Fatal("a file with no external fns was modified")
	}
}

// TestExpandCorpusFnsReportsRefusals pins the never-silent rule at the CLI
// seam: a refused helper is present in the result's Skips, so the caller can
// log it. chk_session is the standing example.
func TestExpandCorpusFnsReportsRefusals(t *testing.T) {
	files := loadStripped(t)
	expanded, results, err := expandCorpusFns(context.Background(), files, false)
	if err != nil {
		t.Fatal(err)
	}
	svcPath := svcOf(t, files).Path
	res, ok := results[svcPath]
	if !ok {
		t.Fatal("no result recorded for the service — refusals would be invisible")
	}
	var sawChk bool
	for _, s := range res.Skips {
		if s.Fn == "chk_session" {
			sawChk = true
			if s.Detail == "" {
				t.Fatal("a refusal carries no detail — a code alone is not an answer")
			}
		}
	}
	if !sawChk {
		t.Fatalf("chk_session refusal not recorded: %+v", res.Skips)
	}
	if _, ok := expanded[svcPath]; !ok {
		t.Fatal("the service was not expanded despite calling a cross-file fn")
	}
	// The fn library itself is never expanded: nothing calls it.
	if _, ok := results[filepath.Join(strippedDir, "fn_min_lib.pc")]; ok {
		t.Fatal("a non-entry file was expanded — nothing calls it")
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
