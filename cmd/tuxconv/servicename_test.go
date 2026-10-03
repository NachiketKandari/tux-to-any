package main

// Service-name uniqueness: a draft's `service:` field keys the generated
// package/directory (plan's base = <root>/<service>, ledger + plan
// filenames), so discover must never hand two drafts the same service name
// — across the entry files of one run and across the drafts already on disk
// (never-clobber accumulates them in mappings/).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/config"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
)

// TestServiceNameFromStem pins the derivation: lower snake, every
// non-identifier byte folded to '_', leading digit escaped — always a
// valid Go identifier, whatever the file was called.
func TestServiceNameFromStem(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"plain stem", "SVC_DEMO.pc", "svc_demo"},
		{"dash folds to underscore", "SVC-DEMO.pcf", "svc_demo"},
		{"dot folds to underscore", "Foo.Bar.pc", "foo_bar"},
		{"leading digit escaped", "1ST_AMT.pc", "X1st_amt"},
		{"nested path uses base", filepath.Join("a", "b", "nav.pc"), "nav"},
		{"already snake", "svc_cust_get_dtl.pc", "svc_cust_get_dtl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serviceName(&ir.File{Path: tt.path}); got != tt.want {
				t.Errorf("serviceName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestUniqueServiceName pins the collision ladder: base when free, else the
// first free numeric suffix — including the digit-suffixed bases the naive
// "append and re-check" loops get wrong.
func TestUniqueServiceName(t *testing.T) {
	tests := []struct {
		name string
		base string
		used []string
		want string
	}{
		{"free base keeps the name", "nav", nil, "nav"},
		{"collision gains 2", "nav", []string{"nav"}, "nav2"},
		{"gaps are reused", "nav", []string{"nav", "nav2", "nav3"}, "nav4"},
		{"digit base appends a digit", "svc2", []string{"svc2"}, "svc22"},
		{"digit base continues the ladder", "svc2", []string{"svc2", "svc22"}, "svc23"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			used := map[string]bool{}
			for _, n := range tt.used {
				used[n] = true
			}
			if got := uniqueServiceName(tt.base, used); got != tt.want {
				t.Errorf("uniqueServiceName(%q, %v) = %q, want %q", tt.base, tt.used, got, tt.want)
			}
		})
	}
}

// TestUsedServiceNamesSeedsFromDrafts pins what counts as an existing
// occupant: Go drafts only (cs drafts and unrelated files never poison the
// space), and a malformed draft is skipped, never fatal.
func TestUsedServiceNamesSeedsFromDrafts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nav.mapping.yaml", "service: nav\nendpoints:\n- {condition: 1, name: GetX, route: /x}\n")
	write("svc.mapping.yaml", "service: svc\nendpoints:\n- {condition: 1, name: GetY, route: /y}\n")
	write("foreign.cs.mapping.yaml", "source: X.pc\ncomponent: X\n")
	write("broken.mapping.yaml", "service: [unclosed\n")
	write("no-service.mapping.yaml", "endpoints:\n- {condition: 1, name: GetZ, route: /z}\n")
	write("notes.txt", "service: ignored\n")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "deep.mapping.yaml"), []byte("service: deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	used := usedServiceNames(dir)
	if len(used) != 2 || !used["nav"] || !used["svc"] {
		t.Errorf("usedServiceNames = %v, want exactly {nav svc}", used)
	}
	if got := usedServiceNames(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Errorf("usedServiceNames(missing dir) = %v, want empty", got)
	}
}

// loadDraftServices loads every Go draft in dir and returns its service
// name. LoadMapping also proves each draft is loader-legal (unique endpoint
// names included) and fails the test otherwise.
func loadDraftServices(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read drafts: %v", err)
	}
	services := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".mapping.yaml") || strings.HasSuffix(name, ".cs.mapping.yaml") {
			continue
		}
		m, err := plan.LoadMapping(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("draft %s must load: %v", name, err)
		}
		if services[m.Service] {
			t.Errorf("service %q repeated in %s", m.Service, name)
		}
		services[m.Service] = true
	}
	return services
}

func copyNavFixture(t *testing.T, dst string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "nav", "SVC_DEMO_LIST.pc"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiscoverServiceNamesUniqueAcrossRun is the end-to-end regression: two
// entry files with the same stem in different directories (dir mode) would
// both derive service svc_demo_list. The first keeps it, the second gets
// svc_demo_list2, and both drafts stay loader-legal.
func TestDiscoverServiceNamesUniqueAcrossRun(t *testing.T) {
	root := t.TempDir()
	copyNavFixture(t, filepath.Join(root, "alpha", "SVC_DEMO_LIST.pc"))
	copyNavFixture(t, filepath.Join(root, "beta", "SVC_DEMO_LIST.pc"))
	out := filepath.Join(root, "mappings")

	if _, err := discoverCore(context.Background(), root, out, false, config.Default(), nil, budget.Budget{}); err != nil {
		t.Fatalf("discoverCore: %v", err)
	}
	services := loadDraftServices(t, out)
	if len(services) != 2 {
		t.Fatalf("service names = %v, want 2 distinct (one per same-stem entry)", services)
	}
	for _, want := range []string{"svc_demo_list", "svc_demo_list2"} {
		if !services[want] {
			t.Errorf("missing service %q in %v", want, services)
		}
	}
}

// TestDiscoverAvoidsExistingServiceName pins the cross-run case: an
// unrelated draft already on disk claims the stem-derived name, so the
// fresh draft must pick the next free one even though its file name is free.
func TestDiscoverAvoidsExistingServiceName(t *testing.T) {
	root := t.TempDir()
	copyNavFixture(t, filepath.Join(root, "SVC_DEMO_LIST.pc"))
	out := filepath.Join(root, "mappings")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "service: svc_demo_list\nendpoints:\n- {condition: 1, name: GetX, route: /x}\n"
	if err := os.WriteFile(filepath.Join(out, "existing.mapping.yaml"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := discoverCore(context.Background(), filepath.Join(root, "SVC_DEMO_LIST.pc"), out, false, config.Default(), nil, budget.Budget{}); err != nil {
		t.Fatalf("discoverCore: %v", err)
	}
	services := loadDraftServices(t, out)
	if !services["svc_demo_list"] || !services["svc_demo_list2"] {
		t.Errorf("services = %v, want the existing svc_demo_list + fresh svc_demo_list2", services)
	}
}
