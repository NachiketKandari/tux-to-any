package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
)

// scenarioValueFile renders the filename-safe axis value (SCEN-D6 file
// naming: <entry>.<var>_<val>.pc).
func scenarioValueFile(v string) string {
	var sb strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	if sb.Len() == 0 {
		return "_"
	}
	return sb.String()
}

// scenarioFileBase is one scenario's flattened-file base name.
func scenarioFileBase(entry string, sc *flow.Scenario) string {
	return entry + "." + sc.Var + "_" + scenarioValueFile(sc.Value) + ".pc"
}

// scenarioArtifacts writes the SCEN-3/4 emission set for one entry: the
// flattened .pc per scenario plus the shared-report twin. Files are the
// human-validation artifacts — written verbatim, no ledger, no validation.
// File names are collision-safe: scenarioValueFile's charset collapse can
// map two distinct values onto one name ("a.b" and "a_b"), so a repeated
// base gains a numbered suffix instead of silently clobbering the first
// artifact (deterministic in scenario order).
func scenarioArtifacts(log *slog.Logger, dir, entry string, src []byte, scens []*flow.Scenario, irFile *ir.File, tree *flow.Tree) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	seen := map[string]bool{}
	for _, sc := range scens {
		base := scenarioFileBase(entry, sc)
		for n := 2; seen[base]; n++ {
			base = strings.TrimSuffix(scenarioFileBase(entry, sc), ".pc") + "_" + strconv.Itoa(n) + ".pc"
		}
		seen[base] = true
		path := filepath.Join(dir, base)
		if err := os.WriteFile(path, []byte(flow.RenderScenario(sc, tree, entry, src, irFile)), 0o644); err != nil {
			return written, fmt.Errorf("scenarios: write %s: %w", path, err)
		}
		written = append(written, path)
	}
	if len(scens) > 0 {
		diff := flow.DiffScenarios(entry, scens)
		md, jsonPath := filepath.Join(dir, entry+".shared.md"), filepath.Join(dir, entry+".shared.json")
		if err := os.WriteFile(md, []byte(flow.RenderSharedMD(diff)), 0o644); err != nil {
			return written, fmt.Errorf("scenarios: write %s: %w", md, err)
		}
		data, err := json.MarshalIndent(diff, "", "  ")
		if err != nil {
			return written, err
		}
		if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
			return written, fmt.Errorf("scenarios: write %s: %w", jsonPath, err)
		}
		written = append(written, md, jsonPath)
	}
	if log != nil {
		log.Info("scenario artifacts written", "dir", dir, "entry", entry, "files", len(written))
	}
	return written, nil
}
