// Package corpusguard carries one regression test: the local-only .pc
// corpus (gitignored) stays out of tracked content — file names, example
// directories, and distinctive tokens never appear in a tracked file. The
// .gitignore entries (the enforcement itself) and this file (the token
// list) are the only permitted mentions.
package corpusguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// banned are the corpus-specific substrings: local file names, the
// gitignored example directories, and identifiers that only exist in the
// local sources (never in the synthetic testdata fixtures).
var banned = []string{
	// local-only directories and files
	"moreExamples", "tuxExamples", "batchExamples",
	"mainTux", "batchTux", "con_trn", "sub_trn",
	"orignial_dotnet", "original_dotnet", "dotNetConverted",
	"mbm_rt_test", "ti_rjct_test", "pythonEqTux",
	// distinctive identifiers from the local sources
	"fn_d2u_mf", "fn_is_d2u_active", "DCM_D2U_CLNT_MSTR", "UAC_USR_ACCNTS",
	"bat_mf_mbm_rt", "SVC_OLN_GET_DTL",
}

// exempt lists tracked files allowed to carry banned substrings: the
// gitignore (which must name the ignored dirs) and this guard itself.
var exempt = map[string]bool{
	".gitignore": true,
	filepath.Join("internal", "corpusguard", "guard_test.go"): true,
}

func TestNoCorpusReferencesInTrackedFiles(t *testing.T) {
	root := strings.TrimSpace(run(t, "git", "rev-parse", "--show-toplevel"))
	out := run(t, "git", "-C", root, "ls-files")
	var hits []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path := strings.TrimSpace(line)
		if path == "" || exempt[path] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read tracked file %s: %v", path, err)
		}
		lower := strings.ToLower(string(data))
		for _, tok := range banned {
			if strings.Contains(lower, strings.ToLower(tok)) {
				hits = append(hits, path+": "+tok)
			}
		}
	}
	sort.Strings(hits)
	for _, h := range hits {
		t.Errorf("local-corpus reference in tracked content: %s", h)
	}
	if len(hits) > 0 {
		t.Logf("the corpus is local-only (gitignored); remove the tokens above from tracked files")
	}
}

// run executes a git command at the repo root and returns trimmed stdout.
func run(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	return strings.TrimSpace(string(out))
}
