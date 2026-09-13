package ir

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenRoot holds the vendored synthetic fixtures and the pinned IR JSON
// goldens for them (testdata/goldens), exercised byte-for-byte.
const (
	goldenRoot  = "../../testdata/goldens"
	fixtureRoot = "../../testdata/fixtures"
)

// normalizePaths blanks path-shaped fields so absolute-vs-relative roots
// never mask semantic equality.
func normalizePaths(f *File) *File {
	f.Path = filepath.Base(f.Path)
	for i := range f.ExternalFns {
		f.ExternalFns[i].DefinedIn = filepath.Base(f.ExternalFns[i].DefinedIn)
	}
	for i := range f.TPCalls {
		if f.TPCalls[i].ServiceFile != "" {
			parts := strings.Split(f.TPCalls[i].ServiceFile, ",")
			for j := range parts {
				parts[j] = filepath.Base(parts[j])
			}
			f.TPCalls[i].ServiceFile = strings.Join(parts, ",")
		}
	}
	return f
}

func goldenJSON(t *testing.T, rel string) ([]byte, *File) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenRoot, rel))
	if err != nil {
		t.Fatalf("golden missing: %v", err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("golden unmarshal: %v", err)
	}
	return raw, &f
}

func mineJSON(t *testing.T, f *File) []byte {
	t.Helper()
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestGoldenFileMode pins file-mode extraction against the goldens in
// testdata/goldens for the synthetic fixtures — byte-equal JSON after path
// normalization.
func TestGoldenFileMode(t *testing.T) {
	cases := []struct {
		fixture string
		golden  string
	}{
		{"nav/SVC_DEMO_LIST.pc", "nav_filemode.ir.json"},
		{"nav/fn_demo_lib.pc", "fn_demo_lib_filemode.ir.json"},
		{"merge/SVC_DEMO_MERGE.pc", "merge.ir.json"},
		{"pf/SVC_TP_DEMO.pc", "SVC_TP_DEMO.ir.json"},
		{"pf/comment_traps.pc", "comment_traps.ir.json"},
		{"pf/unbalanced.pc", "unbalanced.ir.json"},
		{"pf/fragment_nav_slice.txt", "fragment_nav_slice.ir.json"},
		{"stripped/MIN_FRAGMENT.pc", "MIN_FRAGMENT_filemode.ir.json"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			_, want := goldenJSON(t, tc.golden)
			got, err := ExtractFile(filepath.Join(fixtureRoot, tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			got = normalizePaths(got)
			want = normalizePaths(want)
			gj, wj := mineJSON(t, got), mineJSON(t, want)
			var gf, wf map[string]any
			if err := json.Unmarshal(gj, &gf); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(wj, &wf); err != nil {
				t.Fatal(err)
			}
			diffs := diffMaps("", gf, wf)
			if len(diffs) > 0 {
				t.Fatalf("IR differs from golden (%d paths):\n  %s", len(diffs), strings.Join(diffs, "\n  "))
			}
		})
	}
}

// TestGoldenDirMode pins corpus-mode extraction: fragment detection bypassed,
// external fns resolved across the directory, tpcall services resolved.
func TestGoldenDirMode(t *testing.T) {
	cases := []struct {
		dir     string
		goldens string
	}{
		{"stripped", "stripped"},
		{"adversarial", "adversarial"},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			files, err := ExtractDir(filepath.Join(fixtureRoot, tc.dir))
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]*File{}
			for _, f := range files {
				name := strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
				byName[name] = normalizePaths(f)
			}
			entries, err := os.ReadDir(filepath.Join(goldenRoot, tc.goldens))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				name := strings.TrimSuffix(e.Name(), ".ir.json")
				goldenPath := filepath.Join(goldenRoot, tc.goldens, e.Name())
				raw, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatal(err)
				}
				var want File
				if err := json.Unmarshal(raw, &want); err != nil {
					t.Fatal(err)
				}
				normalizedWant := normalizePaths(&want)
				got, ok := byName[name]
				if !ok {
					t.Errorf("file %s missing from dir extraction", name)
					continue
				}
				var gf, wf map[string]any
				if err := json.Unmarshal(mineJSON(t, got), &gf); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(mineJSON(t, normalizedWant), &wf); err != nil {
					t.Fatal(err)
				}
				diffs := diffMaps("", gf, wf)
				if len(diffs) > 0 {
					t.Errorf("%s differs from golden (%d paths):\n  %s", name, len(diffs), strings.Join(diffs, "\n  "))
				}
			}
		})
	}
}

// diffMaps compares two decoded JSON values, returning dotted paths of the
// differences (lists compared by length + element equality; empty lists
// equal null, matching the goldens' marshaling).
func diffMaps(prefix string, a, b any) []string {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return []string{prefix + ": type mismatch"}
		}
		var out []string
		for k, v := range av {
			bv2, inb := bv[k]
			if !inb {
				if isEmptyJSON(v) && bv[k] == nil {
					continue
				}
				out = append(out, prefix+"/"+k+" (theirs only)")
				continue
			}
			if isEmptyJSON(v) && isEmptyJSON(bv2) {
				continue
			}
			out = append(out, diffMaps(prefix+"/"+k, v, bv2)...)
		}
		for k, v := range bv {
			if _, ina := av[k]; !ina {
				if isEmptyJSON(v) {
					continue
				}
				out = append(out, prefix+"/"+k+" (golden only)")
			}
		}
		return out
	case []any:
		bv, ok := b.([]any)
		if !ok {
			if b == nil && len(av) == 0 {
				return nil
			}
			return []string{prefix + ": list vs scalar"}
		}
		if len(av) != len(bv) {
			return []string{fmt.Sprintf("%s: length %d vs %d", prefix, len(av), len(bv))}
		}
		var out []string
		for i := range av {
			out = append(out, diffMaps(fmt.Sprintf("%s[%d]", prefix, i), av[i], bv[i])...)
		}
		return out
	default:
		if jsonEqual(a, b) {
			return nil
		}
		return []string{fmt.Sprintf("%s: %v vs %v", prefix, a, b)}
	}
}

func isEmptyJSON(v any) bool {
	if v == nil {
		return true
	}
	if l, ok := v.([]any); ok && len(l) == 0 {
		return true
	}
	return false
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
