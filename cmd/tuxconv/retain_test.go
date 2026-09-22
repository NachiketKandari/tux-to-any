package main

import (
	"os"
	"path/filepath"
	"testing"
)

func touchDir(t *testing.T, dir, name string) {
	t.Helper()
	// A marker file inside forces a distinct, ordered mtime per dir.
	p := filepath.Join(dir, name, "marker.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func touchRun(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Names sort in creation order (a<b<c<d<e), which doubles as the
	// mtime tiebreak in listAges — newest/oldest stays deterministic
	// even on coarse-timestamp filesystems.
}

func TestPruneOldRunsKeepsNewest(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	audit := filepath.Join(root, "audit")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(audit, 0o755); err != nil {
		t.Fatal(err)
	}
	// 5 log files + 5 audit dirs, created in order (mtime ascending).
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		touchRun(t, logs, "run-"+n+".jsonl")
		touchDir(t, audit, n)
	}

	if got := pruneOldRuns(logs, audit, 2); got != 6 {
		t.Fatalf("pruned %d, want 6 (3 logs + 3 audit dirs)", got)
	}
	for _, n := range []string{"d", "e"} {
		if _, err := os.Stat(filepath.Join(logs, "run-"+n+".jsonl")); err != nil {
			t.Errorf("newest log run-%s.jsonl must survive: %v", n, err)
		}
		if _, err := os.Stat(filepath.Join(audit, n)); err != nil {
			t.Errorf("newest audit dir %s must survive: %v", n, err)
		}
	}
	for _, n := range []string{"a", "b", "c"} {
		if _, err := os.Stat(filepath.Join(logs, "run-"+n+".jsonl")); !os.IsNotExist(err) {
			t.Errorf("oldest log run-%s.jsonl must be pruned", n)
		}
		if _, err := os.Stat(filepath.Join(audit, n)); !os.IsNotExist(err) {
			t.Errorf("oldest audit dir %s must be pruned", n)
		}
	}
}

func TestPruneOldRunsNothingToDo(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	touchRun(t, logs, "run-x.jsonl")
	// Under the keep: nothing removed.
	if got := pruneOldRuns(logs, filepath.Join(root, "audit"), 50); got != 0 {
		t.Fatalf("pruned %d, want 0", got)
	}
	// Missing dirs: nothing removed, no error.
	if got := pruneOldRuns(filepath.Join(root, "nope"), filepath.Join(root, "alsono"), 50); got != 0 {
		t.Fatalf("pruned %d on missing dirs, want 0", got)
	}
	// A stray file inside the audit dir is never a prune target (the dir
	// pass only removes directories): with keep=0 the one log file goes,
	// loose.txt survives.
	touchRun(t, filepath.Join(root, "audit"), "loose.txt")
	if got := pruneOldRuns(logs, filepath.Join(root, "audit"), 0); got != 1 {
		t.Fatalf("pruned %d, want 1 (only the log file)", got)
	}
	if _, err := os.Stat(filepath.Join(root, "audit", "loose.txt")); err != nil {
		t.Errorf("stray audit file must survive pruning: %v", err)
	}
}
