package flow

import (
	"strings"
	"testing"

	"tux-to-any/internal/pred"
	scanner "tux-to-any/internal/tsscan"
)

const foccurSrc = `void SVC_FOCCUR(TPSVCINFO *rqst) {
	char c_flag;
	if (Foccur32(fml_ibuffer, FML_FML_THING) > 0) {
		c_flag = 'Y';
	} else {
		c_flag = 'N';
	}
	if (Foccur32(fml_ibuffer, FML_OTHER) == 0) {
		c_flag = 'Z';
	}
}
`

func foccurTree(t *testing.T) *Tree {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(foccurSrc), "foccur.pc")
	if err != nil {
		t.Fatal(err)
	}
	return Build([]byte(foccurSrc), facts, "SVC_FOCCUR", nil)
}

// TestFoccurCensusKeepsPresence pins the logical-part contract: foccur32
// existence checks are business logic — both arms enter the census with
// presence-normalized skeletons, never filtered as plumbing.
func TestFoccurCensusKeepsPresence(t *testing.T) {
	tree := foccurTree(t)
	conds := ConditionCensus(tree, 0, 1<<30)
	if len(conds) != 2 {
		t.Fatalf("census = %+v, want the two foccur presence checks", conds)
	}
	if conds[0].Skeleton != `# != ""` {
		t.Errorf("present skeleton = %q, want %q", conds[0].Skeleton, `# != ""`)
	}
	if conds[1].Skeleton != `# == ""` {
		t.Errorf("absent skeleton = %q, want %q", conds[1].Skeleton, `# == ""`)
	}
	found := false
	for _, id := range conds[0].Idents {
		if id == "FML_FML_THING" {
			found = true
		}
	}
	if !found {
		t.Errorf("present idents = %v, want FML_FML_THING", conds[0].Idents)
	}
}

// TestFoccurRenderPresence pins the draft transliteration: the foccur
// existence check renders as a Go presence check on the request field,
// not as a TODO.
func TestFoccurRenderPresence(t *testing.T) {
	tree := foccurTree(t)
	r := RenderSpan(tree, nil, 0, 1<<30, 1)
	if !strings.Contains(r.Body, `FmlThing != ""`) {
		t.Errorf("draft missing present transliteration:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, `Other == ""`) {
		t.Errorf("draft missing absent transliteration:\n%s", r.Body)
	}
	for _, td := range r.TODOs {
		if strings.Contains(td, "Foccur") || strings.Contains(td, "foccur") {
			t.Errorf("foccur leaked into TODO residue: %q", td)
		}
	}
}

// TestFoccurSkeletonDistinct pins that presence never collides with a
// generic numeric check: `Foccur32(...) > 0` is `# != ""`, `x > 0` stays
// `# > 0`.
func TestFoccurSkeletonDistinct(t *testing.T) {
	a := pred.Parse("Foccur32(buf, FML_X) > 0")
	b := pred.Parse("x > 0")
	if Skeleton(&a) == Skeleton(&b) {
		t.Errorf("presence skeleton %q collides with numeric %q", Skeleton(&a), Skeleton(&b))
	}
}

// TestFoccurIsNotNoise pins that the return-anchored experiment treats
// foccur presence as business dispatch, never plumbing noise.
func TestFoccurIsNotNoise(t *testing.T) {
	if IsNoiseCond("Foccur32(fml_ibuffer, FML_FML_THING) > 0") {
		t.Error("foccur presence flagged as noise — it is business logic")
	}
	if IsNoiseCond("foccur32(fml_ibuffer, FML_X) == 0") {
		t.Error("lowercase foccur flagged as noise")
	}
}
