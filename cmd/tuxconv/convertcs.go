package main

// convertcs — the Pro*C → .NET Core (C#) target. scan → IR → flow →
// csplan → csgen → cschk gates → the seven-file component tree
// (Controller / DTO / NamedQueries / Repository / Service). The service
// body renders deterministically (repo calls, row mapping, logging);
// the arm's residual logic fills through the LLM seam on an LLM-enabled
// run (-no-llm keeps the tuxgo:TODO placeholder for a resume). Filled
// bodies persist in the run's ledger and are never re-generated.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tux-to-any/internal/config"
	"tux-to-any/internal/cschk"
	"tux-to-any/internal/csgen"
	"tux-to-any/internal/csplan"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/telemetry"
)

func runConvertcs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("convertcs", flag.ContinueOnError)
	outDir := fs.String("out", "", "Output root for the generated component tree (default: convertcs.out from config, else conversion_logs/_staged)")
	mappingFlag := fs.String("mapping", "", "convertcs mapping YAML (namespace/component/endpoints — required)")
	noLLM := fs.Bool("no-llm", false, "deterministic-only run (service bodies keep tuxgo:TODO seams; overrides run.llm)")
	configPath := fs.String("config", "", "Path to .tuxgo.yaml (default: ./.tuxgo.yaml when present, else defaults)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tuxconv convertcs <file|dir> -mapping <yaml> [-no-llm] [-out dir] [-config path]")
		fs.PrintDefaults()
	}
	flagArgs, positional := reorderArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	log := telemetry.Log(ctx)
	cfg, cfgSource, err := loadRunConfig(*configPath)
	if err != nil {
		return err
	}
	logConfigRouting(ctx, cfg, cfgSource)

	// Flag > config > default precedence (C1).
	target, err := resolveConvertcsInput(positional, cfg)
	if err != nil {
		return err
	}
	mappingPath := *mappingFlag
	if mappingPath == "" {
		mappingPath = cfg.Convertcs.Mapping
	}
	if strings.TrimSpace(mappingPath) == "" {
		return fmt.Errorf("convertcs: -mapping <yaml> is required — the tool never invents endpoints")
	}
	noLLMEnabled := *noLLM || cfg.Convertcs.NoLLM

	fi, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("convertcs: cannot access %s: %w", target, err)
	}
	path := target
	if fi.IsDir() {
		path, err = resolveDirSource(target, mappingPath)
		if err != nil {
			return err
		}
	}

	log.Info("convertcs started", "source", path, "mapping", mappingPath, "no_llm", noLLMEnabled)

	mapping, err := csplan.LoadMapping(mappingPath)
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

	client := resolveLLMClient(ctx, cfg, noLLMEnabled, "convertcs service bodies")
	wiring := newWiring(ctx, cfg)

	// Ledger resume (A4): filled bodies are never re-generated — a re-run
	// re-gates and reuses them; TODO seams retry the seam.
	ledgerPath := convertcsLedgerPath(cfg, plan.Component)
	resumed := loadConvertcsLedger(ledgerPath, plan.Source)

	res, err := csgen.Generate(ctx, csgen.Options{
		Plan: plan, Source: string(src), NoLLM: noLLMEnabled || client == nil,
		Client: client, Budget: wiring.budget, MaxRetries: cfg.ValidateCfg.MaxRetries,
		Audit: wiring.audit, Resumed: resumed,
	})
	if err != nil {
		return err
	}
	saveConvertcsLedger(ctx, ledgerPath, plan, res)

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
		base = cfg.Convertcs.Out
	}
	if base == "" {
		base = config.DefaultStagedDir
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

	for _, w := range plan.Warnings {
		log.Warn("convertcs arm coverage", "detail", w)
		fmt.Println("  coverage:", w)
	}
	for _, n := range res.Notes {
		fmt.Println("  note:", n)
	}

	log.Info("convertcs completed", "component", plan.Component, "files", len(res.Order),
		"endpoints", len(plan.Endpoints), "queries", len(plan.Queries),
		"sql_deviations", deviations, "structural_issues", structural,
		"llm_calls", res.LLMCalls, "filled_bodies", len(res.Filled))
	fmt.Printf("convertcs: %d file(s) written under %s — %d endpoint(s), %d query unit(s), %d sql deviations, %d structural issues, %d llm calls\n",
		len(res.Order), root, len(plan.Endpoints), len(plan.Queries), deviations, structural, res.LLMCalls)
	if deviations+structural > 0 {
		return fmt.Errorf("convertcs: %d gate issue(s) — review the log above", deviations+structural)
	}
	return nil
}

// resolveConvertcsInput picks the conversion target: the CLI positional
// wins, else convertcs.input from the yaml, else an error naming both
// sources (C1 precedence).
func resolveConvertcsInput(positional []string, cfg *config.Config) (string, error) {
	if len(positional) > 0 {
		return positional[0], nil
	}
	if cfg.Convertcs.Input != "" {
		return cfg.Convertcs.Input, nil
	}
	return "", fmt.Errorf("no input target: pass a .pc/.pcf file or directory, or set convertcs.input in .tuxgo.yaml")
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

// csBodyLedger is the convertcs resume record (A4): per-endpoint body
// state under the run's ledger dir. Filled bodies persist so a re-run
// never re-generates them; TODO seams record filled=false so a later
// LLM-enabled run retries the seam.
type csBodyLedger struct {
	Source   string            `json:"source"`
	Endpoint map[string]csBody `json:"endpoints"`
}

type csBody struct {
	Span   string `json:"span"`
	Filled bool   `json:"filled"`
	Body   string `json:"body,omitempty"`
}

// convertcsLedgerPath is the component's resume file under paths.ledger.
func convertcsLedgerPath(cfg *config.Config, component string) string {
	return filepath.Join(cfg.Paths.Ledger, "convertcs-"+component+".json")
}

// loadConvertcsLedger returns the component's filled bodies keyed by
// endpoint name (only entries recorded against the same source file).
// Missing or corrupt files resume nothing — the run regenerates honestly.
func loadConvertcsLedger(path, source string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var led csBodyLedger
	if err := json.Unmarshal(data, &led); err != nil || led.Source != source {
		return nil
	}
	resumed := map[string]string{}
	for name, b := range led.Endpoint {
		if b.Filled && b.Body != "" {
			resumed[name] = b.Body
		}
	}
	if len(resumed) == 0 {
		return nil
	}
	return resumed
}

// saveConvertcsLedger persists the run's per-endpoint body state
// (best-effort: a ledger write failure is a WARN, never a run failure).
func saveConvertcsLedger(ctx context.Context, path string, plan *csplan.Plan, res csgen.Result) {
	led := csBodyLedger{Source: plan.Source, Endpoint: map[string]csBody{}}
	for _, ep := range plan.Endpoints {
		b := csBody{Span: ep.SourceSpan}
		if body, ok := res.Bodies[ep.Name]; ok {
			b.Filled = true
			b.Body = body
		}
		led.Endpoint[ep.Name] = b
	}
	data, err := json.MarshalIndent(led, "", "  ")
	if err != nil {
		telemetry.Log(ctx).Warn("convertcs ledger write failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		telemetry.Log(ctx).Warn("convertcs ledger write failed", "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		telemetry.Log(ctx).Warn("convertcs ledger write failed", "error", err)
	}
}
