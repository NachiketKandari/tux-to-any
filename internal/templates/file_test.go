package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeOverride(t *testing.T, dir, id, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".tmpl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write override %s: %v", id, err)
	}
}

func TestNewFileProviderMissingDir(t *testing.T) {
	if _, err := NewFileProvider(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing override dir must fail at construction")
	}
}

func TestNewFileProviderUnknownID(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, "not_a_template", "hello")
	_, err := NewFileProvider(dir)
	if err == nil || !strings.Contains(err.Error(), "unknown template id") {
		t.Fatalf("unknown override id must fail loudly, got %v", err)
	}
}

func TestNewFileProviderMalformed(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, string(ModelFile), "{{ .Package")
	if _, err := NewFileProvider(dir); err == nil {
		t.Fatal("malformed override template must fail at construction")
	}
}

func TestFileProviderOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, string(ModelFile), "// custom override\npackage {{ .Package }}\n")
	p, err := NewFileProvider(dir)
	if err != nil {
		t.Fatalf("NewFileProvider: %v", err)
	}

	out, err := p.Render(ModelFile, ModelFileData{Package: "models"})
	if err != nil {
		t.Fatalf("Render override: %v", err)
	}
	if !strings.HasPrefix(out, "// custom override\n") {
		t.Fatalf("override must win, got:\n%s", out)
	}

	// Every non-overridden ID falls back to the embedded set.
	fallback, err := p.Render(DBInterfaceFile, DBInterfaceData{
		Package: "db", StoreType: "store", IfaceName: "NavStore", CtorName: "NewNavStore",
	})
	if err != nil {
		t.Fatalf("Render embedded fallback: %v", err)
	}
	if !strings.Contains(fallback, "type NavStore interface") {
		t.Fatalf("embedded fallback did not render, got:\n%s", fallback)
	}

	// Non-.tmpl files are ignored, not errors.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileProvider(dir); err != nil {
		t.Fatalf("non-.tmpl files must be ignored: %v", err)
	}
}

func TestFileProviderRenderErrorNamesFile(t *testing.T) {
	dir := t.TempDir()
	writeOverride(t, dir, string(ModelFile), "{{ .NoSuchField }}")
	p, err := NewFileProvider(dir)
	if err != nil {
		t.Fatalf("NewFileProvider: %v", err)
	}
	_, rerr := p.Render(ModelFile, ModelFileData{Package: "models"})
	if rerr == nil {
		t.Fatal("data mismatch must fail at render")
	}
	if !strings.Contains(rerr.Error(), "model_file") || !strings.Contains(rerr.Error(), "override") {
		t.Fatalf("render error must name the template and file, got %v", rerr)
	}
}

func TestFileProviderUnknownRenderID(t *testing.T) {
	dir := t.TempDir()
	p, err := NewFileProvider(dir)
	if err != nil {
		t.Fatalf("NewFileProvider: %v", err)
	}
	if _, err := p.Render(ID("nope"), nil); err == nil {
		t.Fatal("unknown id must fail")
	}
}

func TestResolve(t *testing.T) {
	p, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve empty: %v", err)
	}
	if _, ok := p.(*EmbeddedProvider); !ok {
		t.Fatalf("empty dir must resolve to the embedded provider, got %T", p)
	}

	if _, err := Resolve(t.TempDir()); err != nil {
		t.Fatalf("Resolve dir: %v", err)
	}
}

func TestDump(t *testing.T) {
	dir := t.TempDir()
	written, skipped, err := Dump(dir, false)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if len(written) != len(AllIDs) || len(skipped) != 0 {
		t.Fatalf("Dump wrote %d skipped %d, want %d/%d", len(written), len(skipped), len(AllIDs), 0)
	}
	for _, id := range AllIDs {
		if _, err := os.Stat(filepath.Join(dir, string(id)+".tmpl")); err != nil {
			t.Fatalf("dumped set missing %s: %v", id, err)
		}
	}

	// A second dump never clobbers: everything is skipped.
	written, skipped, err = Dump(dir, false)
	if err != nil {
		t.Fatalf("Dump second: %v", err)
	}
	if len(written) != 0 || len(skipped) != len(AllIDs) {
		t.Fatalf("second Dump wrote %d skipped %d, want 0/%d", len(written), len(skipped), len(AllIDs))
	}

	// force rewrites.
	written, _, err = Dump(dir, true)
	if err != nil {
		t.Fatalf("Dump force: %v", err)
	}
	if len(written) != len(AllIDs) {
		t.Fatalf("force Dump wrote %d, want %d", len(written), len(AllIDs))
	}
}

func TestVerify(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Dump(dir, false); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	issues, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify clean dir: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("dumped set must verify clean, got %v", issues)
	}

	writeOverride(t, dir, "bogus", "x")
	writeOverride(t, dir, string(DBMethodMerge), "{{ .Query")
	writeOverride(t, dir, string(DBMethodUpdateTx), "   \n")
	issues, err = Verify(dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("want 3 issues (unknown, malformed, empty), got %d: %v", len(issues), issues)
	}
}

func TestList(t *testing.T) {
	infos := List(nil)
	if len(infos) != len(AllIDs) {
		t.Fatalf("List(nil) = %d entries, want %d", len(infos), len(AllIDs))
	}
	for _, info := range infos {
		if info.Overridden || info.Bytes == 0 {
			t.Fatalf("embedded info must be non-empty and not overridden: %+v", info)
		}
	}

	dir := t.TempDir()
	writeOverride(t, dir, string(ModelFile), "// small\n")
	p, err := NewFileProvider(dir)
	if err != nil {
		t.Fatalf("NewFileProvider: %v", err)
	}
	infos = List(p)
	overridden := 0
	for _, info := range infos {
		if info.Overridden {
			overridden++
			if info.ID != ModelFile || info.Bytes == 0 {
				t.Fatalf("unexpected override info: %+v", info)
			}
		}
	}
	if overridden != 1 {
		t.Fatalf("List overrides = %d, want 1", overridden)
	}
}
