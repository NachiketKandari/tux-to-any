package main

// convertcs — the Pro*C → .NET Core (C#) target. scan → IR → flow →
// csplan → csgen → cschk gates → the seven-file component tree
// (Controller / DTO / NamedQueries / Repository / Service). The service
// body renders deterministically (repo calls, row mapping, logging);
// the arm's residual logic stays a tuxgo:TODO seam the LLM fills on an
// LLM-enabled run.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tux-to-any/internal/cschk"
	"tux-to-any/internal/csgen"
	"tux-to-any/internal/csplan"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/telemetry"
)

func runConvertcs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("convertcs", flag.ContinueOnError)
	outDir := fs.String("out", "", "Output root for the generated component tree (default: conversion_logs/_staged)")
	mappingFlag := fs.String("mapping", "", "convertcs mapping YAML (namespace/component/endpoints — required)")
	noLLM := fs.Bool("no-llm", false, "deterministic-only run (service bodies keep tuxgo:TODO seams)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tuxconv convertcs <file|dir> -mapping <yaml> [-no-llm] [-out dir]")
		fs.PrintDefaults()
	}
	flagArgs, positional := reorderArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	rest := positional
	if len(rest) == 0 {
		return fmt.Errorf("convertcs: provide a .pc/.pcf file or directory")
	}
	if strings.TrimSpace(*mappingFlag) == "" {
		return fmt.Errorf("convertcs: -mapping <yaml> is required — the tool never invents endpoints")
	}

	target := rest[0]
	fi, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("convertcs: cannot access %s: %w", target, err)
	}
	path := target
	if fi.IsDir() {
		path, err = resolveDirSource(target, *mappingFlag)
		if err != nil {
			return err
		}
	}

	log := telemetry.Log(ctx)
	log.Info("convertcs started", "source", path, "mapping", *mappingFlag, "no_llm", *noLLM)

	mapping, err := csplan.LoadMapping(*mappingFlag)
	if err != nil {
		return err
	}
	irf, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		return fmt.Errorf("convertcs: extract %s: %w", path, err)
	}
	if irf.Entry == "" {
		return fmt.Errorf("convertcs: %s has no Tuxedo entry — fn libraries convert via convertgo (fn-lib mode)", filepath.Base(path))
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("convertcs: read %s: %w", path, err)
	}

	plan, err := csplan.Build(csplan.Options{Main: irf, Source: string(src), Mapping: mapping})
	if err != nil {
		return err
	}
	res, err := csgen.Generate(ctx, csgen.Options{Plan: plan, NoLLM: *noLLM})
	if err != nil {
		return err
	}

	// Gates: structural per file + SQL fidelity + Oracle param counts.
	var issues []cschk.Issue
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
		issues = append(issues, cschk.Check(rel, res.Files[rel], typeNames[rel])...)
	}
	issues = append(issues, cschk.SQLFidelity(plan, res.Files)...)
	issues = append(issues, cschk.OracleParams(plan, res.Files)...)

	// Write the tree under the output root, rooted at the component.
	base := *outDir
	if base == "" {
		base = "conversion_logs/_staged"
	}
	root := filepath.Join(base, plan.Component)
	for _, rel := range res.Order {
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("convertcs: mkdir %s: %w", filepath.Dir(dst), err)
		}
		if err := os.WriteFile(dst, []byte(res.Files[rel]), 0o644); err != nil {
			return fmt.Errorf("convertcs: write %s: %w", dst, err)
		}
		log.Info("convertcs file written", "file", dst)
	}

	deviations := 0
	structural := 0
	for _, is := range issues {
		if is.Kind == "sql-deviation" {
			deviations++
		} else {
			structural++
		}
		log.Error("convertcs gate issue", "kind", is.Kind, "file", is.File, "detail", is.Detail)
	}

	log.Info("convertcs completed", "component", plan.Component, "files", len(res.Order),
		"endpoints", len(plan.Endpoints), "queries", len(plan.Queries),
		"sql_deviations", deviations, "structural_issues", structural)
	fmt.Printf("convertcs: %d file(s) written under %s — %d endpoint(s), %d query unit(s), %d sql deviations, %d structural issues\n",
		len(res.Order), root, len(plan.Endpoints), len(plan.Queries), deviations, structural)
	if deviations+structural > 0 {
		return fmt.Errorf("convertcs: %d gate issue(s) — review the log above", deviations+structural)
	}
	return nil
}

// resolveDirSource picks the mapping's source entry from a directory
// target (the same contract as the Go pipeline's dir-mode mapping).
func resolveDirSource(dir, mappingPath string) (string, error) {
	// read only the source: field (lenient) to find the entry file
	m, err := csplan.LoadMapping(mappingPath)
	if err != nil {
		return "", err
	}
	if m.Source == "" {
		return "", fmt.Errorf("convertcs: %s is a directory — the mapping must set source: <entry .pc file>", dir)
	}
	cand := filepath.Join(dir, m.Source)
	if _, err := os.Stat(cand); err != nil {
		return "", fmt.Errorf("convertcs: mapping source %s not found under %s", m.Source, dir)
	}
	return cand, nil
}
