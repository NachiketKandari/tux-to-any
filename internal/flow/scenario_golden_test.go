package flow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// The SCEN-3/4 corpus goldens: every scenario's flattened .pc plus the
// shared-report twin, byte-pinned under testdata/scen/ (itself local-only).
// The corpus is gitignored local material; its root is taken
// from the TUX_SCEN_CORPUS environment variable, so the test skips when
// the variable, the sources, or the goldens are absent on a fresh clone.
// Regenerate deliberately with SCEN_UPDATE_GOLDENS=1 (only after a rubric
// or renderer change).
const scenUpdateGoldens = "SCEN_UPDATE_GOLDENS"

func TestScenarioGoldens(t *testing.T) {
	corpus := os.Getenv("TUX_SCEN_CORPUS")
	if strings.TrimSpace(corpus) == "" {
		t.Skip("TUX_SCEN_CORPUS unset — no local corpus for scenario goldens")
	}
	const goldenDir = "../../../testdata/scen"
	entries, err := os.ReadDir(corpus)
	if err != nil {
		t.Skipf("corpus absent (local-only): %v", err)
	}
	update := os.Getenv(scenUpdateGoldens) == "1"
	if !update {
		if _, err := os.Stat(goldenDir); err != nil {
			t.Skipf("goldens absent (local-only): %v", err)
		}
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".pc") {
			continue
		}
		path := filepath.Join(corpus, name)
		f, err := ir.ExtractFileOpts(path, ir.Options{})
		if err != nil || f.Entry == "" {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		facts, err := scanner.ScanBytes(src, f.Path)
		if err != nil {
			t.Fatal(err)
		}
		tree := Build(src, facts, f.Entry, f)
		axis := tree.DispatchAxisFor(src)
		if axis == nil {
			continue
		}
		scens := Scenarios(tree, axis)
		entry := f.Entry
		t.Run(entry, func(t *testing.T) {
			for _, sc := range scens {
				got := RenderScenario(sc, entry, src, f)
				golden := filepath.Join(goldenDir, entry+"."+sc.Var+"_"+scenarioFileValue(sc.Value)+".pc")
				scenCompareFile(t, golden, got, update)
			}
			diff := DiffScenarios(entry, scens)
			scenCompareFile(t, filepath.Join(goldenDir, entry+".shared.md"), RenderSharedMD(diff), update)
			scenCompareJSON(t, filepath.Join(goldenDir, entry+".shared.json"), diff, update)
		})
	}
}

// scenCompareFile compares (or regenerates) one text golden.
func scenCompareFile(t *testing.T, golden, got string, update bool) {
	t.Helper()
	if update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden read %s: %v (regenerate with %s=1)", golden, err, scenUpdateGoldens)
	}
	if got != string(want) {
		t.Errorf("%s drifted from the golden (regenerate with %s=1 after a deliberate renderer/rubric change)", golden, scenUpdateGoldens)
	}
}

// scenCompareJSON compares (or regenerates) one JSON golden (canonical
// MarshalIndent, trailing newline).
func scenCompareJSON(t *testing.T, golden string, diff *ScenarioDiff, update bool) {
	t.Helper()
	data, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden read %s: %v (regenerate with %s=1)", golden, err, scenUpdateGoldens)
	}
	if string(data) != strings.TrimRight(string(want), "\n") {
		t.Errorf("%s drifted from the golden (regenerate with %s=1 after a deliberate rubric change)", golden, scenUpdateGoldens)
	}
}

// scenarioFileValue is the filename-safe axis value (the renderer's rule,
// mirrored here so the golden names stay stable).
func scenarioFileValue(v string) string {
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
