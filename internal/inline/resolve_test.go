package inline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	"tux-to-any/internal/tsscan"
)

// fixtureRoot is the shared corpus fixture directory, matching the
// convention the ir and plan tests use.
const fixtureRoot = "../../testdata"

const (
	svcPath = "SVC_MIN_KITCHEN.pc"
	libPath = "fn_min_lib.pc"
)

// loadCorpus extracts the stripped demo corpus (one service, one fn library)
// with its source text, exactly as dir mode would.
func loadCorpus(t *testing.T) (*Corpus, *ir.File) {
	t.Helper()
	dir := filepath.Join(fixtureRoot, "stripped")
	files, err := ir.ExtractDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		sources[f.Path] = string(b)
	}
	c := NewCorpus(files, sources)
	for _, f := range files {
		if filepath.Base(f.Path) == svcPath {
			return c, f
		}
	}
	t.Fatal("service fixture missing")
	return nil, nil
}

// TestCorpusResolutionIsFirstWinsSorted pins the resolution rule: a helper
// name maps to exactly one defining file, chosen first-wins over sorted
// paths — the same rule ir.ExtractDirOpts applies to ExternalFn.DefinedIn.
// If the pass and the IR disagreed here, the pass would lift a definition
// from a different file than the one whose SQL units the plan attributed to
// the helper.
func TestCorpusResolutionIsFirstWinsSorted(t *testing.T) {
	c, svc := loadCorpus(t)
	if len(c.Paths()) < 2 {
		t.Fatalf("corpus paths = %v", c.Paths())
	}
	paths := c.Paths()
	for i := 1; i < len(paths); i++ {
		if paths[i-1] > paths[i] {
			t.Fatalf("Paths not sorted: %v", paths)
		}
	}
	// fn_min_check is defined in fn_min_lib.pc and called by the service.
	owner, ok := c.defOf["fn_min_check"]
	if !ok {
		t.Fatal("fn_min_check does not resolve")
	}
	if filepath.Base(owner.Path) != libPath {
		t.Fatalf("fn_min_check defined in %s, want %s", owner.Path, libPath)
	}
	// The IR's own resolution must agree.
	var irOwner string
	for _, e := range svc.ExternalFns {
		if e.Name == "fn_min_check" {
			irOwner = filepath.Base(e.DefinedIn)
			if !e.Resolved {
				t.Fatal("fn_min_check unresolved in IR")
			}
		}
	}
	if irOwner != filepath.Base(owner.Path) {
		t.Fatalf("IR says %q, corpus index says %q — the two must agree", irOwner, filepath.Base(owner.Path))
	}
}

// readSource reads a corpus file's text off disk, for tests that need the
// caller's original bytes to compare against the expanded ones.
func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// defAt scans src and returns the definition of name — a helper so the
// table tests below read as source, not as synthesized scanner records.
func defAt(t *testing.T, src, name string) tsscan.FunctionDef {
	t.Helper()
	facts, err := tsscan.ScanBytes([]byte(src), "t.pc")
	if err != nil {
		t.Fatal(err)
	}
	def, ok := definitionOf(facts, name)
	if !ok {
		t.Fatalf("no definition of %s in:\n%s", name, src)
	}
	return def
}

// TestResolveCalleeSpans pins the definition span: the whole definition —
// declaration through closing brace — never less. The wrapped-return-type
// walk-back is pinned separately in TestDefinitionStartRules; this fixture
// declares the return type on the name's own line, so the span starts there.
func TestResolveCalleeSpans(t *testing.T) {
	c, _ := loadCorpus(t)
	cal, skip := c.resolveCallee("fn_min_check", 1, "")
	if skip != nil {
		t.Fatalf("unexpected skip: %+v", skip)
	}
	if cal.start != cal.def.StartLine {
		t.Fatalf("definitionStart = %d, want the name line %d (return type is inline in this fixture)", cal.start, cal.def.StartLine)
	}
	if cal.start > cal.def.BodyStartLine || cal.end != cal.def.BodyEndLine {
		t.Fatalf("span [%d,%d] does not bracket the body [%d,%d]", cal.start, cal.end, cal.def.BodyStartLine, cal.def.BodyEndLine)
	}
	lines := strings.Split(cal.sourceText, "\n")
	first := lines[cal.start-1]
	if !strings.Contains(first, "fn_min_check") || !strings.Contains(first, "int") {
		t.Fatalf("span starts at %q — expected the declaration, not a body line", first)
	}
	if got := strings.TrimSpace(lines[cal.end-1]); got != "}" {
		t.Fatalf("span ends at %q, want the closing brace", got)
	}
	// The declaration, signature included, must be wholly inside the span.
	decl := strings.Join(lines[cal.start-1:cal.def.BodyStartLine-1], "\n")
	if !strings.Contains(decl, "fn_min_check(") || !strings.Contains(decl, "char* c_out_flg") {
		t.Fatalf("declaration not fully inside the span:\n%s", decl)
	}
}

// TestResolveCalleeRefusals pins that every refusal is loud and classified.
// An external fn the pass declines to inline keeps its existing plan-layer
// behavior — but the user must be able to see that it was declined and why.
func TestResolveCalleeRefusals(t *testing.T) {
	c, _ := loadCorpus(t)
	cases := []struct {
		name string
		want SkipCode
	}{
		{"chk_session", SkipSessionPlumbing},
		{"fn_begin_tran", SkipTransactionPlumbing},
		{"fn_not_a_real_helper", SkipNotInCorpus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cal, skip := c.resolveCallee(tc.name, 1, "")
			if cal != nil {
				t.Fatalf("resolved %s, want a refusal", tc.name)
			}
			if skip == nil {
				t.Fatal("no skip recorded — a silent refusal is the failure mode this guards")
			}
			if skip.Code != tc.want {
				t.Fatalf("code = %q, want %q (detail: %s)", skip.Code, tc.want, skip.Detail)
			}
			if skip.Detail == "" {
				t.Fatal("skip carries no detail — a code alone is not an answer")
			}
		})
	}
}

// TestResolveCalleeRejectsFmlTraffic pins the one hazard that makes lifting a
// body unsafe: a helper that touches the service's FML buffers. The buffers
// are the caller's scope, so the helper's traffic would be attributed to the
// caller's request/response contract. Refused, loudly.
func TestResolveCalleeRejectsFmlTraffic(t *testing.T) {
	libSrc := `#include <fml32.h>

void SVC_TMP(TPSVCINFO *rqst)
{
    FBFR32 *ibuf;
    char c_flg;
    ibuf = (FBFR32 *)rqst->data;
    Fget32(ibuf, FML_X, 0, &c_flg, 0);
}

int fn_touches_buffers(FBFR32 *pbuf, char *c_out)
{
    char c_flg;
    Fget32(pbuf, FML_X, 0, &c_flg, 0);
    *c_out = c_flg;
    return 1;
}
`
	libIR, err := ir.ExtractSourceOpts([]byte(libSrc), "lib.pc", ir.DefaultOptions().CorpusMode())
	if err != nil {
		t.Fatal(err)
	}
	c := NewCorpus([]*ir.File{libIR}, map[string]string{"lib.pc": libSrc})
	cal, skip := c.resolveCallee("fn_touches_buffers", 1, "")
	if cal != nil {
		t.Fatal("a helper doing FML traffic must not be lifted")
	}
	if skip == nil || skip.Code != SkipFmlTraffic {
		t.Fatalf("skip = %+v, want %s", skip, SkipFmlTraffic)
	}
	if !strings.Contains(skip.Detail, "Fget32") {
		t.Fatalf("detail does not name the offending call: %s", skip.Detail)
	}
}

// TestDefinitionStartRules pins the conservative walk-back: it lifts a
// wrapped return type and stops at every boundary, because splicing a line
// of unrelated code would be worse than lifting one line short.
func TestDefinitionStartRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "return type on the name line",
			src:  "static int fn_a(void)\n{\n}\n",
			want: 1,
		},
		{
			name: "return type wrapped above",
			src:  "int\nfn_a(void)\n{\n}\n",
			want: 1,
		},
		{
			name: "two-word return type wrapped above",
			src:  "static unsigned long\nfn_a(void)\n{\n}\n",
			want: 1,
		},
		{
			name: "stops at a previous statement",
			src:  "int g_last = 1;\nint\nfn_a(void)\n{\n}\n",
			want: 2,
		},
		{
			name: "stops at a directive",
			src:  "#define X 1\nint\nfn_a(void)\n{\n}\n",
			want: 2,
		},
		{
			name: "stops at a blank line",
			src:  "char c_x;\n\nint\nfn_a(void)\n{\n}\n",
			want: 3,
		},
		{
			name: "stops at a comment",
			src:  "/* note */\nint\nfn_a(void)\n{\n}\n",
			want: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The name sits on line 2 in every case except the first.
			def := defAt(t, tc.src, "fn_a")
			if got := definitionStart(tc.src, def); got != tc.want {
				t.Fatalf("definitionStart = %d, want %d", got, tc.want)
			}
		})
	}
}
