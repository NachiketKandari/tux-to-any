package tsscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAdversarialNoPanic runs every hostile fixture: the scanner must never
// panic and never drop its unbalanced facts silently.
func TestAdversarialNoPanic(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(fixtureRoot, "adversarial"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(fixtureRoot, "adversarial", name))
			if err != nil {
				t.Fatal(err)
			}
			facts, err := ScanBytes(src, name)
			if err != nil {
				t.Fatalf("scan error: %v", err)
			}
			// loud, never silent: comment-debris and broken-define fixtures
			// self-heal, but anything still unbalanced must be recorded
			for _, u := range facts.Unbalanced {
				if u.Kind == "" || u.StartLine <= 0 {
					t.Fatalf("malformed unbalanced record: %+v", u)
				}
			}
			// deterministic across runs
			again, _ := ScanBytes(src, name)
			if len(again.Branches) != len(facts.Branches) || len(again.AllSQL) != len(facts.AllSQL) {
				t.Fatal("nondeterministic scan")
			}
		})
	}
}

// TestRealCorpusSmoke exercises an optional local-only corpus (gitignored
// .pc files listed in the TUX_CORPUS environment
// variable as a path-list (os.PathListSeparator). Absent variable or files
// skip — CI and fresh clones never depend on it.
func TestRealCorpusSmoke(t *testing.T) {
	spec := os.Getenv("TUX_CORPUS")
	if strings.TrimSpace(spec) == "" {
		t.Skip("TUX_CORPUS unset — no local corpus to smoke")
	}
	for _, path := range filepath.SplitList(spec) {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			t.Skipf("corpus file not present: %s", path)
		}
		start := time.Now()
		facts, err := ScanFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		dt := time.Since(start)
		if facts.NumLines == 0 {
			t.Errorf("%s: empty facts", path)
		}
		// a real production file must parse clean end to end
		if len(facts.ParseErrors) != 0 {
			t.Errorf("%s parse errors: %+v", path, facts.ParseErrors[:min(3, len(facts.ParseErrors))])
		}
		if len(facts.Branches) == 0 {
			t.Errorf("%s branches = 0, expected full recovery", path)
		}
		t.Logf("%s scanned in %v", path, dt)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
