// GT-7 -out staging: with an explicit output root, gentest stages the
// scanned service's non-test Go sources (and the module's go.mod) into the
// out tree so the directory becomes a complete, compatible snapshot. The
// target tree is never modified by staging, never overwritten either: a
// destination name that already exists gets the `_convertgo` suffix (the
// plan's collision rule, e.g. interface.go → interface_convertgo.go).
package testgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tux-to-any/internal/testscan"
)

// stageSources copies the scanned services' non-test sources into the run's
// BaseDir. It is a no-op unless Options.Stage is set (explicit -out/-base)
// and the output root differs from the module root (an in-place run has
// nothing to snapshot).
func stageSources(res *Result, opts Options, svcs []*serviceCtx) {
	if !opts.Stage || opts.BaseDir == "" || len(svcs) == 0 {
		return
	}
	absBase, err := filepath.Abs(opts.BaseDir)
	if err != nil {
		absBase = opts.BaseDir
	}
	for _, sc := range svcs {
		if sc.moduleRoot != "" {
			absRoot, rerr := filepath.Abs(sc.moduleRoot)
			if rerr == nil && absRoot == absBase {
				continue // same tree — never copy onto itself
			}
		}
		for _, sub := range []string{"db", "controller", "handler", "models", "model"} {
			dir := filepath.Join(sc.dir, sub)
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
					continue
				}
				stageOne(res, opts.BaseDir, sc, dir, name)
			}
		}
		if sc.moduleRoot != "" {
			stageOne(res, opts.BaseDir, sc, sc.moduleRoot, "go.mod")
		}
	}
}

// stageOne writes one source file into the out tree, renaming on collision.
func stageOne(res *Result, baseDir string, sc *serviceCtx, dir, name string) {
	dst, err := outPathFor(baseDir, sc, dir, name)
	if err != nil {
		return
	}
	dst = collisionName(dst)
	src := filepath.Join(dir, name)
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return
	}
	res.Staged = append(res.Staged, dst)
}

// hasGoFiles reports whether dir holds at least one .go file (a staged
// go.mod sits at the module root, where vet/compile gates must not run).
func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
			return true
		}
	}
	return false
}

// collisionName returns the plan's collision-safe destination: the natural
// name when free, else `<stem>_convertgo<ext>` (numbered deterministically
// when repeated).
func collisionName(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	cand := stem + "_convertgo" + ext
	for n := 2; ; n++ {
		if _, err := os.Stat(cand); err != nil {
			return cand
		}
		cand = fmt.Sprintf("%s_convertgo%d%s", stem, n, ext)
	}
}

// checklistFile is the structural checklist against the human reference
// suites (riskPipelineTest/rpdbtest.txt, rptestcontroller.txt,
// handler_test.txt): the template-shape markers a generated file must carry
// for its layer. Findings are advisory only — the plan asks for guidelines,
// not byte comparison — and never fail a run.
func checklistFile(layer testscan.Layer, content string) []string {
	var markers []string
	switch layer {
	case testscan.LayerDB:
		markers = []string{"testCases :=", "for _, testCase := range", "assert.ErrorContains(", "assert.NoError("}
		if !strings.Contains(content, "ExpectQuery(") && !strings.Contains(content, "ExpectExec(") {
			return []string{"checklist db: missing ExpectQuery/ExpectExec mock expectation"}
		}
	case testscan.LayerController:
		markers = []string{"testCases :=", "EXPECT()", "gomock.Any()", "assert.ErrorContains(", "assert.NoError("}
	case testscan.LayerHandler:
		markers = []string{"CreateTestGinContext", "assert.Equal("}
	}
	var missing []string
	for _, m := range markers {
		if !strings.Contains(content, m) {
			missing = append(missing, m)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("checklist %s: missing %s", layer, strings.Join(missing, ", "))}
}
