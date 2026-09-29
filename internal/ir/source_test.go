package ir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractSourceMatchesFile pins the one property the inline pass depends
// on: folding source text already in memory is the same fold as reading the
// file off disk. The inline pass re-folds its expanded source through
// ExtractSourceOpts, so if the two ever diverged an inlined helper would
// extract differently from a native one and the equivalence the feature
// rests on would be false.
func TestExtractSourceMatchesFile(t *testing.T) {
	cases := []string{
		"nav/SVC_DEMO_LIST.pc", // service: entry, conditions, queries
		"nav/fn_demo_lib.pc",   // helper library: fn definitions, SQL
	}
	for _, rel := range cases {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(fixtureRoot, rel)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fromDisk, err := ExtractFileOpts(path, DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			fromMem, err := ExtractSourceOpts(src, path, DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			a := mustJSON(t, fromDisk)
			b := mustJSON(t, fromMem)
			if a != b {
				t.Fatalf("in-memory fold diverged from on-disk fold\n disk: %s\n  mem: %s", a, b)
			}
		})
	}
}

// TestExtractSourceCorpusModeSkipsFragment pins CorpusMode: a caller holding
// a corpus (the inline pass) must not let a file that happens to look
// fragment-shaped be re-fragmented, exactly as ExtractDirOpts does for a
// whole directory.
func TestExtractSourceCorpusModeSkipsFragment(t *testing.T) {
	// A body-less file trips the fragment rubric in file mode.
	src := []byte("int loose_symbol;\n")
	if _, err := ExtractSourceOpts(src, "loose.pc", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	plain, err := ExtractSourceOpts(src, "loose.pc", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !plain.Fragment {
		t.Fatal("file mode: a file with no function definitions is a fragment")
	}
	corpus, err := ExtractSourceOpts(src, "loose.pc", DefaultOptions().CorpusMode())
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Fragment {
		t.Fatal("corpus mode: fragment detection must be bypassed")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
