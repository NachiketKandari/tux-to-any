package csdraft

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/cschk"
	"tux-to-any/internal/csgen"
	"tux-to-any/internal/csplan"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
)

// TestRealCorpusCsSmoke exercises the deterministic convertcs pipeline
// end to end against an optional local-only corpus (gitignored .pc files
// listed in the TUX_CS_CORPUS environment variable as a path-list,
// os.PathListSeparator). For every corpus file the smoke runs the full
// structural path — IR → flow tree → cs draft → strict mapping load →
// csplan → csgen (-no-llm) → cschk gates — proving the pipeline holds on
// the files the local reference conversions cover. Absent variable or
// files skip — CI and fresh clones never depend on it. The corpus itself
// is never named in tracked content (the corpusguard keeps it that way).
func TestRealCorpusCsSmoke(t *testing.T) {
	spec := os.Getenv("TUX_CS_CORPUS")
	if strings.TrimSpace(spec) == "" {
		t.Skip("TUX_CS_CORPUS unset — no local corpus to smoke")
	}
	for _, path := range filepath.SplitList(spec) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			t.Skipf("corpus file not present: %s", path)
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			smokeOne(t, path)
		})
	}
}

// smokeOne runs the deterministic pipeline over one corpus file. Corpus
// files vary: honest shapes skip (no entry, no qualifying arms, arms
// without SQL); panics and unexpected pipeline errors fail loudly.
func smokeOne(t *testing.T, path string) {
	t.Helper()
	irf, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if irf.Entry == "" {
		t.Skipf("no entry function (fn library) — nothing to smoke")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	facts, err := flow.ScanForIR(string(src), irf)
	if err != nil {
		t.Fatalf("flow scan: %v", err)
	}
	tree := flow.Build(src, facts, irf.Entry, irf)

	// The draft's namespace/area placeholders filled (the config-default
	// path) — the draft must load as-is, zero hand-written yaml.
	draft := Render(irf, tree, src, Options{Namespace: "SmokeCorpus", Area: "Smoke.Area"})
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "smoke.cs.mapping.yaml")
	if err := os.WriteFile(draftPath, []byte(draft), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	mapping, err := csplan.LoadMapping(draftPath)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "at least one endpoint must be mapped") {
			t.Skipf("no qualifying scenario slices — nothing to smoke")
		}
		t.Fatalf("draft does not load: %v", err)
	}

	plan, err := csplan.Build(csplan.Options{Main: irf, Source: string(src), Mapping: mapping})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "owns no SQL") {
			t.Skipf("mapped arms carry no SQL — nothing to smoke")
		}
		t.Fatalf("plan: %v", err)
	}

	res, err := csgen.Generate(t.Context(), csgen.Options{Plan: plan, NoLLM: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	typeNames := map[string]string{
		"Controller/" + plan.Controller + ".cs":   plan.Controller,
		"DTO/" + plan.DTOCls + ".cs":              plan.DTOCls,
		"NamedQueries/" + plan.QueriesCls + ".cs": plan.QueriesCls,
		"Repository/I" + plan.Repo + ".cs":        "I" + plan.Repo,
		"Repository/" + plan.Repo + ".cs":         plan.Repo,
		"Service/I" + plan.Service + ".cs":        "I" + plan.Service,
		"Service/" + plan.Service + ".cs":         plan.Service,
	}
	for _, rel := range res.Order {
		for _, is := range cschk.Check(rel, res.Files[rel], typeNames[rel]) {
			t.Errorf("gate: %s", is.Error())
		}
	}
	for _, is := range cschk.SQLFidelity(plan, res.Files) {
		t.Errorf("gate: %s", is.Error())
	}
	for _, is := range cschk.OracleParams(plan, res.Files) {
		t.Errorf("gate: %s", is.Error())
	}
	t.Logf("%s: %d file(s), %d endpoint(s), %d query unit(s), %d coverage warning(s)",
		filepath.Base(path), len(res.Order), len(plan.Endpoints), len(plan.Queries), len(plan.Warnings))
	if testing.Verbose() && len(plan.Warnings) > 0 {
		for _, w := range plan.Warnings {
			fmt.Println("  coverage:", w)
		}
	}
}
