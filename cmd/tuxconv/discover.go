package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/config"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/llm"
	"tux-to-any/internal/telemetry"
)

// runDiscover implements `tuxgo discover <file|dir>` — the PRD-2026-09-10
// endpoint-discovery scan-then-tag mode: the flow IR finds the API
// candidates (conditions/blocks enclosing Fget32 reads and non-error
// Fadd32 writes) and emits a mapping draft the user tags with names/routes.
// The tool never invents endpoints (§4.2.8). Naming: one AI call per
// candidate when the client is available (run.llm, no -no-llm), deterministic
// names otherwise — every proposal is an advisory draft default.
func runDiscover(ctx context.Context, args []string) error {
	log := telemetry.Log(ctx)
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	outDir := fs.String("out", "", "Directory for the draft yamls (default: mappings/)")
	stdout := fs.Bool("stdout", false, "Print the draft(s) instead of writing files")
	noLLM := fs.Bool("no-llm", false, "Deterministic-only run: skip AI naming (overrides run.llm)")
	configPath := fs.String("config", "", "Path to .tuxgo.yaml (default: ./.tuxgo.yaml when present, else defaults)")

	flagArgs, positional := reorderArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	// The config always routes (defaults when the file is absent) — the
	// target falls back to convert.input so a bare `tuxgo discover` works.
	cfg, cfgSource, err := loadRunConfig(*configPath)
	if err != nil {
		return err
	}
	logConfigRouting(ctx, cfg, cfgSource)

	var target string
	if len(positional) > 0 {
		target = positional[0]
	} else {
		target = cfg.Convert.Input
	}
	if target == "" {
		return fmt.Errorf("must provide a .pc/.pcf file or directory, or set convert.input in .tuxgo.yaml")
	}

	client := resolveLLMClient(ctx, cfg, *noLLM, "endpoint naming")
	if client != nil {
		if mdl, rerr := cfg.Route(""); rerr == nil {
			log.Info("AI naming enabled — one call per candidate endpoint, deterministic fallback per failure", "model", mdl.Model)
		}
	}
	bd := newWiring(ctx, cfg).budget

	_, err = discoverCore(ctx, target, discoverOutDir(*outDir), *stdout, client, bd)
	return err
}

// discoverCore is the scan-then-tag engine shared by `tuxgo discover` and
// convert's no-mapping fallback: for every entry file it finds the API
// candidates and emits an editable draft into out (or prints with stdout).
// AI naming runs per candidate when client is non-nil; the deterministic
// picker fills any gaps. Drafts never clobber — existing ones stay and the
// fresh draft lands alongside as a numbered sibling.
func discoverCore(ctx context.Context, target, out string, stdout bool, client llm.Client, bd budget.Budget) (int, error) {
	log := telemetry.Log(ctx)

	fi, err := os.Stat(target)
	if err != nil {
		return 0, fmt.Errorf("cannot access target path %s: %w", target, err)
	}
	dirMode := fi.IsDir()

	irFiles, err := extractFlowIR(target)
	if err != nil {
		return 0, err
	}
	if len(irFiles) == 0 {
		return 0, fmt.Errorf("no .pc or .pcf files found in %s", target)
	}

	rec := auditRecorder(ctx)

	written, existing := 0, 0
	for _, f := range irFiles {
		if f.Entry == "" {
			fmt.Printf("- %s: no entry function (fn library) — skipped\n", filepath.Base(f.Path))
			continue
		}
		src, err := os.ReadFile(f.Path)
		if err != nil {
			return written, fmt.Errorf("discover: read %s: %w", f.Path, err)
		}
		facts, err := flow.ScanForIR(string(src), f)
		if err != nil {
			return written, fmt.Errorf("discover: scan %s: %w", f.Path, err)
		}
		tree := flow.Build(src, facts, f.Entry, f)
		var draft string
		if axis := tree.DispatchAxisFor(src); axis != nil {
			// SCEN-5: the dispatch-axis entry folds into scenario slices —
			// the scenario draft + the SCEN-3/4 artifacts replace the block
			// heuristic on the endpoint path (condition/conditionRef drafts
			// stay loadable for the non-axis legacy files). The artifacts
			// are the human-validation evidence and write regardless; the
			// draft needs at least one scenario passing the census rubric
			// (request reads AND non-error response writes).
			scens := flow.Scenarios(tree, axis)
			diff := flow.DiffScenarios(f.Entry, scens)
			qualifying := 0
			for _, sc := range scens {
				if len(sc.Gets) > 0 && len(sc.Adds) > 0 {
					qualifying++
				}
			}
			printScenarioSummary(f, axis, scens)
			if qualifying == 0 {
				fmt.Printf("- %s: axis %s — no scenario carries reads+writes (the census rubric); map manually if you know better\n", f.Entry, axis)
			} else {
				aiNames := aiNameScenarios(ctx, log, client, bd, f, scens, diff, src, rec)
				draft = renderScenarioDraft(f, axis, scens, diff, aiNames, dirMode)
			}
			if !stdout {
				scenDir := filepath.Join(filepath.Dir(out), config.DefaultScenDir)
				paths, aerr := scenarioArtifacts(log, scenDir, f.Entry, src, scens, f)
				if aerr != nil {
					return written, aerr
				}
				for _, p := range paths {
					fmt.Printf("  artifact: %s\n", p)
				}
			}
			if draft == "" {
				continue
			}
		} else {
			candidates := flow.Discover(tree, f.Conditions)
			if len(candidates) == 0 {
				fmt.Printf("- %s: no API candidates found (%d conditions inspected, no dispatch axis)\n", f.Entry, len(f.Conditions))
				continue
			}
			aiNames := aiNameEndpoints(ctx, log, client, bd, f, tree, candidates, src, rec)
			draft = renderDraft(f, candidates, dirMode, aiNames)
			printDiscoverSummary(f, tree, candidates)
		}
		if stdout {
			fmt.Println()
			fmt.Println(draft)
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
		path := filepath.Join(out, base+".mapping.yaml")
		if _, err := os.Stat(path); err == nil {
			// A tagged draft is user work — never clobber it. The fresh
			// draft lands alongside as "<base> (1).mapping.yaml", first
			// free number.
			var alt string
			for i := 1; ; i++ {
				cand := filepath.Join(out, fmt.Sprintf("%s (%d).mapping.yaml", base, i))
				if _, err := os.Stat(cand); os.IsNotExist(err) {
					alt = cand
					break
				}
			}
			fmt.Printf("  note: %s kept — writing a fresh draft alongside\n", path)
			existing++
			path = alt
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(path, []byte(draft), 0o644); err != nil {
			return written, fmt.Errorf("discover: write %s: %w", path, err)
		}
		written++
		log.Info("draft written", "path", path)
		fmt.Printf("  draft: %s\n", path)
	}
	if stdout {
		return written, nil
	}
	fmt.Printf("\n%d draft(s) written to %s (%d existing kept) — review name/route, fill module/readDBs if you generate into an existing repo, then: tuxgo convert %s\n",
		written, out, existing, target)
	return written, nil
}

// discoverOutDir resolves the draft directory: the -out override, else the
// mappings/ convention next to the working directory.
func discoverOutDir(flagOut string) string {
	if flagOut != "" {
		return flagOut
	}
	return config.DefaultMappingsDir
}

// renderDraft emits the mapping draft (DIS-D5): only strict-decodable keys,
// metadata in comments, pre-filled name/route (AI- or deterministic-suggested,
// always editable), census comments, dbMethods pins when the AI proposed
// them, and the non-candidates kept (commented) for user control. Module and
// readDBs stay commented hints — module defaults to the service name at
// load, so a draft is loadable as-is once names exist.
func renderDraft(f *ir.File, candidates []flow.Candidate, dirMode bool, aiNames map[string]aiSuggestion) string {
	entry := filepath.Base(f.Path)
	svc := strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(entry, filepath.Ext(entry)), "-", "_"))
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — generated by tuxgo (endpoint discovery).\n", entry)
	sb.WriteString("#\n")
	sb.WriteString("# HOW TO USE: names/routes below are suggested defaults — review them\n")
	sb.WriteString("# (or delete entries you do not want), optionally set module/readDBs,\n")
	sb.WriteString("# then convertgo. A condition you omit stays logic-only. `condition:` is\n")
	sb.WriteString("# the top-level inventory index; `conditionRef:` is a nested block.\n")
	sb.WriteString("\n")
	if dirMode {
		fmt.Fprintf(&sb, "source: %s\n", entry)
	}
	fmt.Fprintf(&sb, "service: %s\n", svc)
	fmt.Fprintf(&sb, "# module: %s   # defaults to the service name; set your real import path for an existing repo\n", svc)
	sb.WriteString("# readDBs:                     # logical read-DB names for handler wiring\n")
	sb.WriteString("#   - EXAMPLE\n")
	sb.WriteString("\nendpoints:\n")
	mapped := map[int]string{} // condition index → candidate key ("" when only non-candidate)
	for _, c := range candidates {
		n := strings.SplitN(strings.TrimPrefix(c.Key, "c"), ".", 2)[0]
		var idx int
		fmt.Sscanf(n, "%d", &idx)
		if _, ok := mapped[idx]; !ok {
			mapped[idx] = c.Key
		}
	}
	for _, c := range candidates {
		if strings.Contains(c.Key, ".") {
			fmt.Fprintf(&sb, "  - conditionRef: %-12s # nested block, lines %d-%d | reads: %s | writes: %s",
				c.Key, c.StartLine, c.EndLine,
				fieldsOrDash(c.Gets), fieldsOrDash(c.Adds))
			if len(c.QueryIDs) > 0 {
				fmt.Fprintf(&sb, " | queries: %s", strings.Join(c.QueryIDs, ","))
			}
			if len(c.Codes) > 0 {
				fmt.Fprintf(&sb, " | error codes: %s", strings.Join(c.Codes, ","))
			}
			if c.Redundant {
				sb.WriteString(" | NOTE: subset of its parent — consider tagging the parent")
			}
			sb.WriteString("\n")
		} else {
			fmt.Fprintf(&sb, "  - condition: %-15s # lines %d-%d | reads: %s | writes: %s",
				strings.TrimPrefix(c.Key, "c"), c.StartLine, c.EndLine,
				fieldsOrDash(c.Gets), fieldsOrDash(c.Adds))
			if len(c.QueryIDs) > 0 {
				fmt.Fprintf(&sb, " | queries: %s", strings.Join(c.QueryIDs, ","))
			}
			if len(c.Codes) > 0 {
				fmt.Fprintf(&sb, " | error codes: %s", strings.Join(c.Codes, ","))
			}
			sb.WriteString("\n")
		}
		sug := aiNames[c.Key]
		origin := "# TODO tag the Go method name"
		switch {
		case sug.Name == "":
		case sug.Deterministic:
			origin = "# deterministic — edit freely"
		default:
			origin = "# ai-suggested — edit freely"
		}
		fmt.Fprintf(&sb, "    name: %-16s %s\n", strconv.Quote(sug.Name), origin)
		switch {
		case sug.Route == "":
			sb.WriteString("    route: \"\"                 # TODO tag the route path (starts with /)\n")
		case sug.Deterministic:
			fmt.Fprintf(&sb, "    route: %-15s # deterministic — edit freely\n", strconv.Quote(sug.Route))
		default:
			fmt.Fprintf(&sb, "    route: %-15s # ai-suggested — edit freely\n", strconv.Quote(sug.Route))
		}
	}
	// Non-candidates, commented, for control.
	var rest []int
	for _, cond := range f.Conditions {
		if _, ok := mapped[cond.Index]; !ok {
			rest = append(rest, cond.Index)
		}
	}
	if len(rest) > 0 {
		sb.WriteString("# not API candidates by the discovery rubric — map manually if you know better:\n")
		for _, idx := range rest {
			cond := &f.Conditions[idx-1]
			gets, adds, _ := flow.Census(cond)
			fmt.Fprintf(&sb, "# - condition: %-13d # lines %d-%d | reads: %s | writes: %s\n",
				idx, cond.StartLine, cond.EndLine, fieldsOrDash(gets), fieldsOrDash(adds))
		}
	}
	// dbMethods: AI-proposed pins emit as real (editable) yaml; without
	// them the commented skeleton lists the query IDs for manual pins.
	pins := map[string]methodPinSuggestion{}
	for _, sug := range aiNames {
		for id, m := range sug.Methods {
			if _, dup := pins[id]; !dup {
				pins[id] = m
			}
		}
	}
	if len(pins) > 0 {
		sb.WriteString("\ndbMethods:                     # ai-suggested — edit freely; params stay\n")
		sb.WriteString("                               # deterministic (derived from the query binds)\n")
		ids := make([]string, 0, len(pins))
		for id := range pins {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			p := pins[id]
			if p.Row != "" {
				fmt.Fprintf(&sb, "  %s: {name: %s, row: %s}\n", id, p.Name, p.Row)
			} else {
				fmt.Fprintf(&sb, "  %s: {name: %s}\n", id, p.Name)
			}
		}
		sb.WriteString("# `fn_<name>:<qid>` keys pin queries inside an external fn library file.\n")
		return sb.String()
	}
	var qids []string
	for _, q := range f.Queries {
		qids = append(qids, q.ID)
	}
	if len(qids) > 0 {
		sb.WriteString("\n# dbMethods:                   # OPTIONAL per-query method pins (uncomment\n")
		for _, id := range qids {
			fmt.Fprintf(&sb, "#   %-24s  # name: GetThing / params: [b:string] / row: ThingRow\n", id+":")
		}
		sb.WriteString("# `fn_<name>:<qid>` keys pin queries inside an external fn library file.\n")
	}
	return sb.String()
}

// renderScenarioDraft emits the mapping draft for an axis entry (SCEN-5):
// one scenarioRef endpoint per qualifying scenario (the census rubric —
// request reads AND non-error response writes), census comments carrying
// the tx evidence, pre-filled advisory names, and the non-qualifying
// scenarios kept (commented) for user control. Module and readDBs stay
// commented hints — module defaults to the service name at load.
func renderScenarioDraft(f *ir.File, axis *flow.DispatchAxis, scens []*flow.Scenario, diff *flow.ScenarioDiff, aiNames map[string]aiSuggestion, dirMode bool) string {
	entry := filepath.Base(f.Path)
	svc := strings.ToLower(strings.ReplaceAll(strings.TrimSuffix(entry, filepath.Ext(entry)), "-", "_"))
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — generated by tuxgo (dispatch-axis scenarios).\n", entry)
	sb.WriteString("#\n")
	sb.WriteString("# HOW TO USE: names/routes below are suggested defaults — review them\n")
	sb.WriteString("# (or delete entries you do not want), optionally set module/readDBs,\n")
	sb.WriteString("# then convertgo. The entry folds into one scenario slice per value of its\n")
	sb.WriteString("# dispatch axis, plus the default arm when the dispatch chain ends in an\n")
	sb.WriteString("# else (`<var>=default`); every reachable arm gets exactly one slice.\n")
	sb.WriteString("# `scenarioRef:` names one slice (`<var>=<value>`). A scenario you omit\n")
	sb.WriteString("# stays logic-only. Shared/unique-block evidence:\n")
	sb.WriteString("# scenarios/<entry>.shared.md.\n")
	sb.WriteString("\n")
	if dirMode {
		fmt.Fprintf(&sb, "source: %s\n", entry)
	}
	fmt.Fprintf(&sb, "service: %s\n", svc)
	fmt.Fprintf(&sb, "# module: %s   # defaults to the service name; set your real import path for an existing repo\n", svc)
	sb.WriteString("# readDBs:                     # logical read-DB names for handler wiring\n")
	sb.WriteString("#   - EXAMPLE\n")
	sb.WriteString("\nendpoints:\n")
	var logicOnly []*flow.Scenario
	for _, sc := range scens {
		if len(sc.Gets) == 0 || len(sc.Adds) == 0 {
			logicOnly = append(logicOnly, sc)
			continue
		}
		ext := sc.BodyExtent()
		fmt.Fprintf(&sb, "  - scenarioRef: %-16s # kept lines %d-%d | reads: %s | writes: %s",
			scenarioRefValue(sc), ext[0], ext[1], censusList(sc.Gets, 12), censusList(sc.Adds, 12))
		if qids := scenarioQueryIDs(sc); len(qids) > 0 {
			fmt.Fprintf(&sb, " | queries: %d (%s)", len(qids), censusList(qids, 20))
		}
		if txIDs := scenarioTxIDs(sc); len(txIDs) > 0 {
			fmt.Fprintf(&sb, " | tx: %s", strings.Join(txIDs, ","))
		}
		if len(sc.Codes) > 0 {
			fmt.Fprintf(&sb, " | error codes: %s", censusList(sc.Codes, 12))
		}
		sb.WriteString("\n")
		writeSuggestion(&sb, aiNames[sc.Key])
	}
	if len(logicOnly) > 0 {
		sb.WriteString("# scenarios without reads+writes (the census rubric) — map manually if you know better:\n")
		for _, sc := range logicOnly {
			fmt.Fprintf(&sb, "# - scenarioRef: %-14s # reads: %s | writes: %s | queries: %d\n",
				scenarioRefValue(sc), fieldsOrDash(sc.Gets), fieldsOrDash(sc.Adds), len(scenarioQueryIDs(sc)))
		}
	}
	writeDBMethodSkeleton(&sb, f)
	return sb.String()
}

// scenarioRefValue renders the draft's scenarioRef value ("trn_cd=A").
func scenarioRefValue(sc *flow.Scenario) string {
	return sc.Var + "=" + sc.Value
}

// scenarioTxIDs lists the scenario's tx-flagged DML query ids.
func scenarioTxIDs(sc *flow.Scenario) []string {
	var out []string
	for _, q := range sc.Queries {
		if q.DML && q.Tx {
			out = append(out, q.ID)
		}
	}
	return out
}

// writeSuggestion renders one endpoint's name/route lines with the
// advisory-origin comment.
func writeSuggestion(sb *strings.Builder, sug aiSuggestion) {
	origin := "# TODO tag the Go method name"
	switch {
	case sug.Name == "":
	case sug.Deterministic:
		origin = "# deterministic — edit freely"
	default:
		origin = "# ai-suggested — edit freely"
	}
	fmt.Fprintf(sb, "    name: %-16s %s\n", strconv.Quote(sug.Name), origin)
	switch {
	case sug.Route == "":
		sb.WriteString("    route: \"\"                 # TODO tag the route path (starts with /)\n")
	case sug.Deterministic:
		fmt.Fprintf(sb, "    route: %-15s # deterministic — edit freely\n", strconv.Quote(sug.Route))
	default:
		fmt.Fprintf(sb, "    route: %-15s # ai-suggested — edit freely\n", strconv.Quote(sug.Route))
	}
}

// writeDBMethodSkeleton emits the draft's dbMethods tail: AI-proposed pins
// as real (editable) yaml, else the commented skeleton listing the query
// IDs for manual pins.
func writeDBMethodSkeleton(sb *strings.Builder, f *ir.File) {
	var qids []string
	for _, q := range f.Queries {
		qids = append(qids, q.ID)
	}
	if len(qids) > 0 {
		sb.WriteString("\n# dbMethods:                   # OPTIONAL per-query method pins (uncomment\n")
		for _, id := range qids {
			fmt.Fprintf(sb, "#   %-24s  # name: GetThing / params: [b:string] / row: ThingRow\n", id+":")
		}
		sb.WriteString("# `fn_<name>:<qid>` keys pin queries inside an external fn library file.\n")
	}
}

func printDiscoverSummary(f *ir.File, tree *flow.Tree, candidates []flow.Candidate) {
	top, nested := 0, 0
	for _, c := range candidates {
		if strings.Contains(c.Key, ".") {
			nested++
		} else {
			top++
		}
	}
	fmt.Printf("\n%s: %d candidate(s) (%d top-level, %d nested) from %d condition(s), coverage %d/%d lines\n",
		f.Entry, len(candidates), top, nested, len(f.Conditions),
		tree.Coverage.Classified, tree.Coverage.CodeLines)
	for _, c := range candidates {
		note := ""
		if c.Redundant {
			note = "  (redundant with parent)"
		}
		fmt.Printf("  %-8s lines %d-%d  reads %-2d writes %-2d%s\n",
			c.Key, c.StartLine, c.EndLine, len(c.Gets), len(c.Adds), note)
	}
}

// printScenarioSummary is the axis path's per-entry scan report (SCEN-5):
// the detected axis, every scenario's census shape, and which qualify.
func printScenarioSummary(f *ir.File, axis *flow.DispatchAxis, scens []*flow.Scenario) {
	fmt.Printf("\n%s: %d scenario(s) on %s\n", f.Entry, len(scens), axis)
	for _, sc := range scens {
		note := ""
		if len(sc.Gets) == 0 || len(sc.Adds) == 0 {
			note = "  (logic-only — reads+writes census)"
		}
		fmt.Printf("  %-12s kept %3d dropped %3d unfolded %d  reads %-2d writes %-2d queries %-3d%s\n",
			scenarioRefValue(sc), sc.Counts.Kept, sc.Counts.Dropped, sc.Counts.Unfolded,
			len(sc.Gets), len(sc.Adds), len(scenarioQueryIDs(sc)), note)
	}
}

func fieldsOrDash(fields []string) string {
	if len(fields) == 0 {
		return "none"
	}
	sorted := append([]string(nil), fields...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// censusList renders a draft-comment census list compactly: the first cap
// entries, then "+N more" (the full census lives in the scenario artifacts
// and the shared report).
func censusList(fields []string, cap int) string {
	if len(fields) == 0 {
		return "none"
	}
	if len(fields) <= cap {
		return strings.Join(fields, ",")
	}
	return strings.Join(fields[:cap], ",") + fmt.Sprintf(" +%d more", len(fields)-cap)
}
