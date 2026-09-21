package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Merge-gap coverage ported from convert-tux-to-go's cmd/tuxgo tests: the
// behaviors all survived the rename (batchTargets scoping, writeDraft's
// never-clobber contract, mappingForEntry's lenient convention-dir pick),
// but the cmd-level regression tests did not. These pin them on the new
// stack.
func TestBatchTargetsFileFilterScoping(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"svc_alpha.pc", "svc_beta.pcf", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	ctx := context.Background()
	all, err := batchTargets(ctx, dir, "")
	if err != nil {
		t.Fatalf("unfiltered: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unfiltered = %v, want the 2 .pc/.pcf files", all)
	}
	kept, err := batchTargets(ctx, dir, "ALPHA")
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if len(kept) != 1 || !strings.Contains(kept[0], "svc_alpha.pc") {
		t.Fatalf("filtered = %v, want only svc_alpha.pc (case-insensitive)", kept)
	}
	if _, err := batchTargets(ctx, dir, "zzz-no-match"); err == nil {
		t.Fatalf("expected an error when the filter matches nothing")
	}
	// An explicitly passed file always converts, filter or not.
	only, err := batchTargets(ctx, filepath.Join(dir, "svc_beta.pcf"), "alpha")
	if err != nil || len(only) != 1 {
		t.Fatalf("explicit file = %v, %v — must bypass the filter", only, err)
	}
}

func TestWriteDraftNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	first, kept, err := writeDraft(dir, "svc.mapping.yaml", "draft: one\n")
	if err != nil || kept {
		t.Fatalf("first write = %s, kept=%v, %v", first, kept, err)
	}
	second, kept, err := writeDraft(dir, "svc.mapping.yaml", "draft: two\n")
	if err != nil || !kept {
		t.Fatalf("second write = %s, kept=%v, %v — must keep the first draft", second, kept, err)
	}
	if second == first {
		t.Fatalf("second draft must land alongside, not on %s", first)
	}
	orig, _ := os.ReadFile(first)
	if string(orig) != "draft: one\n" {
		t.Fatalf("original draft clobbered: %q", orig)
	}
}

func TestMappingForEntryPicks(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		body := "service: svc\nmodule: svc\n"
		if src != "" {
			body = "source: " + src + "\n" + body
		}
		body += "endpoints:\n- {condition: 1, name: GetX, route: /x}\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
	}
	write("other.mapping.yaml", "OTHER.pc")
	write("svc.mapping.yaml", "SVC_MIN.pc")
	got, err := mappingForEntry(dir, "SVC_MIN.pc")
	if err != nil || !strings.HasSuffix(got, "svc.mapping.yaml") {
		t.Fatalf("mappingForEntry = %s, %v — source: match must win", got, err)
	}
	// Foreign drafts never poison the run: unknown entry → "" (drafts-and-stops).
	got, err = mappingForEntry(dir, "UNKNOWN.pc")
	if err != nil || got != "" {
		t.Fatalf("mappingForEntry unknown = %s, %v — want empty", got, err)
	}
}
