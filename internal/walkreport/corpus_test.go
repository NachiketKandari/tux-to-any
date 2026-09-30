package walkreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corpusTree is the real emitted controller for riskPipelineTest, relative to
// this package. It is gitignored pipeline output rather than a committed
// fixture, so every test that uses it skips when it is absent.
const corpusTree = "../../conversion_logs/_staged/tux/controller"

// corpusShape is the census the corpus produces, measured 2026-09-30 by
// running the instrument against a `-no-llm convertgo` of riskPipelineTest.
//
// It is the P0 baseline: P1's acceptance is a drop in
// R-HELPER-ARG-UNRESOLVED, and P2's is R-NO-STORE-CALLS reaching zero. Both
// claims are only checkable against a pinned starting point.
//
// These numbers are a contract with the emitter. If a later change moves a
// gap from one code to another — which is what P1, P2 and P3 are supposed to
// do — this test is where the change gets stated out loud. It is expected to
// fail during those phases and be updated in the same commit as the change,
// never after.
type corpusShape struct {
	total    int
	byCode   map[string]int
	byMethod map[string]int
}

var corpusBaseline = corpusShape{
	// 424, not the 423 the plan measured: the extra one is the new
	// R-NO-STORE-CALLS marker on FnFindRiskProfile. Before that marker
	// existed the helper rendered a bare `return 0` and the census could
	// not see it — the count went up by one because a silent gap became a
	// loud one, which is the only direction this number should ever move
	// during P0.
	total: 424,
	byCode: map[string]int{
		ReasonHelperArgUnresolved:     345,
		ReasonStoreArgUnresolved:      20,
		ReasonResponseFieldUnresolved: 58, // 39 response-role + 19 row-match
		ReasonNoStoreCalls:            1,  // FnFindRiskProfile's empty body
		ReasonUnclassified:            0,
	},
	byMethod: map[string]int{
		// The endpoint gaps are spread across the 23 emitted endpoint
		// methods, each carrying its 15 helper params. GetPointTypeD is the
		// heaviest at 29 (15 helper args plus its own store binds and
		// shaping gaps); GetViewSavedRiskAnalizer has the fewest at 19.
		// Spot-checking one high and one low method catches a regression in
		// attribution without freezing all 23 numbers, which would make
		// this test fail for unrelated emitter changes.
		"GetPointTypeD":            29,
		"GetViewSavedRiskAnalizer": 19,
	},
}

// TestCorpusCensusMatchesTheBaseline runs the instrument against real
// generator output and pins the result. It is the only test that can catch a
// rewording in internal/gen: the shape fixtures use hand-copied strings,
// which would keep passing if the emitter changed its wording and only the
// copy went stale.
func TestCorpusCensusMatchesTheBaseline(t *testing.T) {
	if _, err := os.Stat(corpusTree); err != nil {
		t.Skipf("no emitted controller tree at %s — run `tuxconv convertgo` on riskPipelineTest first", corpusTree)
	}
	c, err := CensusDir(corpusTree)
	if err != nil {
		t.Fatal(err)
	}

	if c.Total != corpusBaseline.total {
		t.Errorf("total = %d, want %d", c.Total, corpusBaseline.total)
	}
	for _, code := range AllReasons {
		if got, want := c.ByCode[code], corpusBaseline.byCode[code]; got != want {
			t.Errorf("code %s = %d, want %d", code, got, want)
		}
	}
	for method, want := range corpusBaseline.byMethod {
		if got := c.ByMethod[method]; got != want {
			t.Errorf("method %s = %d gaps, want %d", method, got, want)
		}
	}
	// Exactly one gap belongs in fns.go, and it must be the empty-helper
	// marker. This is the file-level half of the R-NO-STORE-CALLS contract:
	// FnFindRiskProfile's C body is FML plus tpcall with no SQL, so it is the
	// one helper the deterministic path cannot project. A second gap here
	// would mean another helper started rendering empty, and a gap of any
	// other code would mean the marker was misattributed.
	for _, g := range c.Gaps {
		if g.File == "fns.go" && g.Reason != ReasonNoStoreCalls {
			t.Errorf("fns.go carries %s at line %d: %s", g.Reason, g.Line, g.Message)
		}
	}

	// Every gap must be attributed to a method. A gap with an empty method
	// is a gap whose owner cannot be identified, which would make the
	// per-method cut — the granularity P1 diffs against — silently wrong.
	for _, g := range c.Gaps {
		if g.Method == "" {
			t.Errorf("%s:%d has no enclosing method: %s", g.File, g.Line, g.Message)
			break
		}
	}
}

// TestCorpusCensusIsFullyAccounted is the arithmetic guard: the codes must
// sum to the total. It catches the failure where a new shape starts landing
// in R-UNCLASSIFIED while the specific codes still report their old counts —
// the situation where each individual number looks plausible and the sum does
// not.
func TestCorpusCensusIsFullyAccounted(t *testing.T) {
	if _, err := os.Stat(corpusTree); err != nil {
		t.Skipf("no emitted controller tree at %s", corpusTree)
	}
	c, err := CensusDir(corpusTree)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, code := range AllReasons {
		sum += c.ByCode[code]
	}
	if sum != c.Total {
		t.Errorf("codes sum to %d but total is %d — a TODO is escaping classification", sum, c.Total)
	}
}

// TestShapeFixtureClassifiesEveryShape is the always-on counterpart to the
// corpus test: it runs on a committed fixture, so the classifier is covered
// even in a checkout with no pipeline output.
func TestShapeFixtureClassifiesEveryShape(t *testing.T) {
	c, err := CensusDir("../../testdata/walkreport/controller")
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 29 {
		t.Fatalf("fixture total = %d, want 29", c.Total)
	}
	want := map[string]int{
		ReasonHelperArgUnresolved:     15,
		ReasonStoreArgUnresolved:      6,
		ReasonResponseFieldUnresolved: 8,
		ReasonUnclassified:            0,
	}
	for _, code := range AllReasons {
		if got := c.ByCode[code]; got != want[code] {
			t.Errorf("code %s = %d, want %d", code, got, want[code])
		}
	}
	wantMethod := map[string]int{
		"MANAGE_RISK_PROFILE_VIEW": 25,
		"VIEW_RISK_PROFILE":        2,
		"UPDATE_RISK_PROFILE":      2,
	}
	if len(c.ByMethod) != len(wantMethod) {
		t.Errorf("by method = %v, want %d methods", c.ByMethod, len(wantMethod))
	}
	for method, n := range wantMethod {
		if got := c.ByMethod[method]; got != n {
			t.Errorf("method %s = %d, want %d", method, got, n)
		}
	}
	// Tidy must be silent: the fixture exists to be a healthy census.
	r := &WalkReport{Target: "fixture", Controller: "testdata/walkreport/controller", Census: &c}
	if f := r.Tidy(); len(f) != 0 {
		t.Errorf("fixture census reported findings: %v", f)
	}
}

// TestShapeFixtureSurvivesRegeneration pins the fixture's own hygiene: it must
// stay parseable so method attribution keeps working, and its gaps must stay
// inside methods. A fixture that stopped parsing would test the unparseable
// path forever and silently stop testing attribution.
func TestShapeFixtureSurvivesRegeneration(t *testing.T) {
	const fixture = "../../testdata/walkreport/controller/tux.go"
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if spans := methodSpans(string(data)); len(spans) != 3 {
		t.Errorf("fixture parses to %d methods, want 3 — it no longer parses cleanly", len(spans))
	}
	for _, g := range CensusFile("tux.go", data) {
		if g.Method == "" {
			t.Errorf("line %d is unattributed: %s", g.Line, g.Message)
		}
		if !strings.HasSuffix(g.Reason, "UNRESOLVED") {
			t.Errorf("line %d classified %q", g.Line, g.Reason)
		}
	}
}

// TestReportRoundTripsThroughJSON checks the -json path keeps every field,
// since P5 will diff two phases through it. A census that renders well but
// drops Gaps on marshal would still let a human read the summary while
// defeating the machine comparison.
func TestReportRoundTripsThroughJSON(t *testing.T) {
	c, err := CensusDir("../../testdata/walkreport/controller")
	if err != nil {
		t.Fatal(err)
	}
	r := &WalkReport{
		Target:     "fixture",
		Controller: "testdata/walkreport/controller",
		Coverage: []CoverageReport{{
			Path:      "tux.pc",
			Functions: []Coverage{NewCoverage("SVC_RISK_PRFL", 2713, 2723, 10, []int{856, 857})},
		}},
		Census: &c,
	}
	data, err := MarshalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"total": 29`) {
		t.Errorf("JSON lost the total:\n%s", data)
	}
	if !strings.Contains(string(data), ReasonHelperArgUnresolved) {
		t.Errorf("JSON lost the reason codes:\n%s", data)
	}
	// Gaps must be present, not omitted, or the diff-by-machine claim fails.
	if !strings.Contains(string(data), `"gaps"`) {
		t.Errorf("JSON omitted the per-gap list:\n%s", data)
	}
	if !strings.Contains(string(data), `"method": "MANAGE_RISK_PROFILE_VIEW"`) {
		t.Errorf("JSON lost method attribution:\n%s", data)
	}
}

func TestCensusDirReportsAMissingTree(t *testing.T) {
	if _, err := CensusDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error for a missing tree")
	}
}
