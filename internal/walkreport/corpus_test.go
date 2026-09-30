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
	// 334 after P2's first slice. Two movements, in opposite directions:
	//
	//   -5  P2's newline fix. FnSaveRiskProfile now renders its SQL read
	//       and its nested FnInsertIntoUra call, so it stops being a body
	//       that fails its gate. It never counted before — a dropped method
	//       is invisible to a census — so this is new population, not a
	//       drop: 5 new R-NESTED-HELPER-ARG-UNRESOLVED for the nested call's
	//       arguments, which were previously unemitted entirely.
	//
	// P1's slice is what took R-HELPER-ARG-UNRESOLVED from 345 to 250.
	total: 334,
	byCode: map[string]int{
		ReasonHelperArgUnresolved:       250,
		ReasonStoreArgUnresolved:        20,
		ReasonResponseFieldUnresolved:   58, // 39 response-role + 19 row-match
		ReasonNestedHelperArgUnresolved: 5,  // FnInsertIntoUra's args, seen from inside FnSaveRiskProfile
		ReasonNoStoreCalls:              1,  // FnFindRiskProfile: FML + tpcall, no SQL
		ReasonUnclassified:              0,
	},
	byMethod: map[string]int{
		// The endpoint gaps are spread across the 23 emitted endpoint
		// methods. GetPointTypeD is the heaviest at 24 (its helper args plus
		// its own store binds and shaping gaps); GetViewSavedRiskAnalizer
		// has the fewest at 14. Both fell by exactly 5 in P1's slice, which
		// is the uniform shape to expect from a fix that applies to every
		// endpoint equally — a lopsided drop would mean the fix only reached
		// some branches.
		"GetPointTypeD":            24,
		"GetViewSavedRiskAnalizer": 14,
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
	// fns.go carries exactly two codes, and the split between them is the
	// whole of P2's first slice: FnFindRiskProfile renders empty (FML +
	// tpcall, no SQL, so nothing to project) while FnSaveRiskProfile renders
	// its read and its nested call, whose arguments are the remaining gap.
	// A third code here, or a gap that is neither, means the split stopped
	// meaning what it says.
	inFns := map[string]int{}
	for _, g := range c.Gaps {
		if g.File == "fns.go" {
			inFns[g.Reason]++
		}
	}
	want := map[string]int{ReasonNoStoreCalls: 1, ReasonNestedHelperArgUnresolved: 5}
	if len(inFns) != len(want) {
		t.Errorf("fns.go carries %d distinct code(s) %v, want %d", len(inFns), inFns, len(want))
	}
	for code, n := range want {
		if inFns[code] != n {
			t.Errorf("fns.go %s = %d, want %d", code, inFns[code], n)
		}
	}
	for code := range inFns {
		if _, ok := want[code]; !ok {
			t.Errorf("fns.go carries an unexpected code %s", code)
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
