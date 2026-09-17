package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

// renderSrc is a tiny dispatch entry pinning the flattened rendering
// deterministically: preamble, satisfied fold comment, contradicted drop,
// UNFOLDED residue, and the /*L<n>*/ provenance prefixes.
const renderSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char trn_cd;
	if (Fget32(ptr_fml_Ibuffer, FML_TRANS_CD, 0, (char *)sql_trn_cd.arr, 0) == -1) {
		Fadd32(ptr_fml_Ibuffer, FML_ERR_MSG, c_errmsg, 0);
		tpreturn(TPFAIL, 0L, (char *)ptr_fml_Ibuffer, 0L, 0);
	}
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (trn_cd == 'A') {
		work_a();
	}
	if (trn_cd == 'P' && flag == 1) {
		work_p();
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func renderScenarios(t *testing.T) ([]*Scenario, *Tree, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_DEMO.pc")
	if err := os.WriteFile(path, []byte(renderSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := scanner.ScanBytes([]byte(renderSrc), "demo.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(renderSrc), facts, "SVC_DEMO", f)
	axis := tree.DispatchAxisFor([]byte(renderSrc))
	if axis == nil {
		t.Fatal("no axis")
	}
	return Scenarios(tree, axis), tree, renderSrc
}

func TestRenderScenarioMarkers(t *testing.T) {
	scens, tree, src := renderScenarios(t)
	var a *Scenario
	for _, sc := range scens {
		if sc.Value == "A" {
			a = sc
		}
	}
	if a == nil {
		t.Fatal("no A scenario")
	}
	out := RenderScenario(a, tree, "SVC_DEMO", []byte(src), nil)
	// Header stats + provenance prefixes + fold markers.
	for _, want := range []string{
		"/* scenario: trn_cd == 'A'",
		"/* ---- preamble (shared init) ---- */",
		"/*L9*/",
		"/*L11*/",
		"/*L12*/",
		"/* folded: !(strcmp(sql_trn_cd.arr, \"A\")) (holds) */",
		"work_a();",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered scenario missing %q:\n%s", want, out)
		}
	}
	// The P branch is contradicted under A — its header and its unbraced
	// body (`trn_cd = 'P';`) never render; the tree nests the single
	// statement, so the contradicting write cannot leak into the slice.
	for _, gone := range []string{"/*L7*/", "/*L8*/", "work_p();", "trn_cd = 'P';"} {
		if strings.Contains(out, gone) {
			t.Errorf("dropped P-branch line %q leaked into the A rendering", gone)
		}
	}
	if !strings.Contains(out, "/* ---- body under trn_cd == 'A' ---- */") {
		t.Error("body banner missing")
	}
}

func TestDiffScenarioSetAlgebra(t *testing.T) {
	scens, _, _ := renderScenarios(t)
	if len(scens) != 2 {
		t.Fatalf("scenarios = %d, want P and A", len(scens))
	}
	d := DiffScenarios("SVC_DEMO", scens)
	if len(d.Keys) != 2 {
		t.Fatalf("keys = %v", d.Keys)
	}
	if len(d.Shared) == 0 {
		t.Fatal("no shared blocks — the read-guard preamble must be shared")
	}
	// The shared region is the read-guard preamble (decl + the Fget32 error
	// check, lines 2-6); the normalization chain folds per value, so it is
	// NOT shared — each scenario keeps only its own write.
	first := d.Shared[0]
	if first.Start != 2 || first.End != 6 || len(first.Scenarios) != 2 {
		t.Errorf("first shared block = %+v, want lines 2-6 × 2 scenarios", first)
	}
	// The divergence starts at the first normalization if (line 7) — the
	// controller's dispatch delta.
	if d.Divergence != 7 {
		t.Errorf("divergence = %d, want 7", d.Divergence)
	}
	// Per-scenario unique blocks exist (each normalization write + body).
	if len(d.Unique["trn_cd=A"]) == 0 || len(d.Unique["trn_cd=P"]) == 0 {
		t.Errorf("unique blocks missing: %v", d.Unique)
	}
	// Membership: both scenarios share no queries here — none recorded.
	if len(d.Queries) != 0 {
		t.Errorf("queries = %v, want none (fixture has no SQL)", d.Queries)
	}
	// The report renders deterministically.
	md := RenderSharedMD(d)
	if !strings.Contains(md, "## Shared blocks") || !strings.Contains(md, "## First divergence") {
		t.Errorf("report sections missing:\n%s", md)
	}
}
