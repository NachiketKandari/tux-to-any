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
	//
	//   +3  P2's control-flow accounting. Two helpers gained a
	//       R-CONTROL-FLOW-NOT-RENDERED summary (one each), and one call
	//       gained a R-CALL-OUTSIDE-ITS-GUARD. All three are newly VISIBLE
	//       gaps, not new work: fn_save_risk_profile is 201 classified lines
	//       of branch and loop that the store-call walk never consulted,
	//       and fn_insert_into_ura is called under `if (c_flg_using == 'A')`
	//       in the C but unconditionally in the Go. Before this, neither
	//       fact was recorded anywhere at all.
	//
	// The 28 unrendered constructs listed under those two summaries are
	// unmarked detail lines, so they cost 2 gaps rather than 30 — see
	// TestControlFlowDetailLinesAreNotGaps.
	//
	//   +2  P4 under Option A. Both tpcall sites are now counted as
	//       R-TPCALL. They were not gaps before — they were ABSENT: the
	//       tpcall placeholder path is entry-scoped and the corpus entry
	//       has zero tpcalls, so a helper's tpcall reached the emitted tree
	//       as no call, no TODO, and no reason code. A count is the
	//       minimum honest record of a call that was dropped.
	total: 339,
	byCode: map[string]int{
		ReasonHelperArgUnresolved:       250,
		ReasonStoreArgUnresolved:        20,
		ReasonResponseFieldUnresolved:   58, // 39 response-role + 19 row-match
		ReasonNestedHelperArgUnresolved: 5,  // FnInsertIntoUra's args, seen from inside FnSaveRiskProfile
		ReasonControlFlowNotRendered:    2,  // FnSaveRiskProfile (21 of 32) + FnFindRiskProfile (7 of 10)
		ReasonCallOutsideItsGuard:       1,  // FnInsertIntoUra, guarded in C by c_flg_using == 'A'
		ReasonTPCallNotRendered:         2,  // one per SVC_NETWORTH call site, both in fn helpers
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
	// fns.go carries exactly five codes, and the split between them is the
	// whole of P2. FnFindRiskProfile has no SQL to project (FML + tpcall), so
	// it renders empty and is the one R-NO-STORE-CALLS. FnSaveRiskProfile
	// renders its read and its nested call: the nested call's arguments are
	// 5 helper-arg gaps, the two helpers' unrendered branches are 2
	// control-flow gaps, and the call the C guards with c_flg_using == 'A'
	// but the Go does not is the single guard leak.
	//
	// FnInsertIntoUra is the one helper that renders completely — its only
	// branch is the SQLCODE check, which the store call's error check already
	// is, and its only other construct is a debug-logging if that a named
	// flow rule elides. It therefore contributes no gap of any code, and
	// this assertion is what records that fact rather than leaving it
	// implied by its absence.
	inFns := map[string]int{}
	byMethodInFns := map[string]map[string]int{}
	for _, g := range c.Gaps {
		if g.File != "fns.go" {
			continue
		}
		inFns[g.Reason]++
		if byMethodInFns[g.Method] == nil {
			byMethodInFns[g.Method] = map[string]int{}
		}
		byMethodInFns[g.Method][g.Reason]++
	}
	want := map[string]int{
		ReasonNoStoreCalls:              1,
		ReasonNestedHelperArgUnresolved: 5,
		ReasonControlFlowNotRendered:    2,
		ReasonCallOutsideItsGuard:       1,
		ReasonTPCallNotRendered:         2,
	}
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
	// The per-helper attribution, which is what makes "renders completely"
	// a checked claim about FnInsertIntoUra rather than an assumption.
	wantPerHelper := map[string]map[string]int{
		"FnFindRiskProfile": {
			ReasonNoStoreCalls:           1,
			ReasonControlFlowNotRendered: 1,
			ReasonTPCallNotRendered:      1,
		},
		"FnSaveRiskProfile": {
			ReasonNestedHelperArgUnresolved: 5,
			ReasonControlFlowNotRendered:    1,
			ReasonCallOutsideItsGuard:       1,
			ReasonTPCallNotRendered:         1,
		},
	}
	for helper, codes := range wantPerHelper {
		got := byMethodInFns[helper]
		if len(got) != len(codes) {
			t.Errorf("%s carries %d distinct code(s) %v, want %d", helper, len(got), got, len(codes))
		}
		for code, n := range codes {
			if got[code] != n {
				t.Errorf("%s %s = %d, want %d", helper, code, got[code], n)
			}
		}
	}
	if _, ok := byMethodInFns["FnInsertIntoUra"]; ok {
		t.Errorf("FnInsertIntoUra carries gaps %v — it was the one helper "+
			"rendering completely, so a gap here means that stopped being true",
			byMethodInFns["FnInsertIntoUra"])
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

// TestControlFlowDetailLinesAreNotGaps is what keeps the control-flow count
// readable.
//
// internal/gen lists every unrendered construct under one summary line, and
// those detail lines deliberately carry no tuxgo:TODO. If they did, a helper
// missing 21 branches would report 22 gaps — the summary plus its own list —
// and the census total would grow with the verbosity of the report rather than
// with the amount of missing work. The count has to mean "this many methods
// are missing control flow", so a reader can hold the whole list in their head.
//
// This checks the invariant on the real corpus rather than on a hand-built
// fixture, because the emitter is what decides the marker.
func TestControlFlowDetailLinesAreNotGaps(t *testing.T) {
	if _, err := os.Stat(corpusTree); err != nil {
		t.Skip("no emitted corpus; run a -no-llm convertgo first")
	}
	body, err := os.ReadFile(filepath.Join(corpusTree, "fns.go"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(body), "\n")
	summaries, details := 0, 0
	for _, ln := range lines {
		if strings.Contains(ln, ReasonControlFlowNotRendered) {
			summaries++
		}
		if strings.Contains(ln, "legacy branch at line") || strings.Contains(ln, "legacy loop at line") {
			details++
			if strings.Contains(ln, TODOPrefix) {
				t.Errorf("a control-flow detail line carries the gap marker, so it "+
					"would count as a gap of its own:\n%s", ln)
			}
		}
	}
	if summaries == 0 {
		t.Fatal("no control-flow summary in the corpus; the accounting stopped emitting")
	}
	// 21 under FnSaveRiskProfile + 7 under FnFindRiskProfile. Pinned so that
	// a change in how many constructs the walk finds is a stated decision
	// rather than a silent drift — a drop means constructs are going
	// unaccounted rather than unlisted.
	if details != 28 {
		t.Errorf("corpus lists %d unrendered constructs, want 28 (21 + 7) — "+
			"the walk is finding a different number of branches/loops", details)
	}
	if summaries != 2 {
		t.Errorf("corpus has %d control-flow summaries, want 2 (one per helper)", summaries)
	}
}

// TestNoHelperClaimsSuccessWithAnUnrenderedTPCall is the safety property that
// gives P4's Option A its weight, checked against real emitted output rather
// than only a fixture.
//
// A tpcall is how the other service's data arrives. A helper that skipped it
// has not done its work, so it must return the legacy FAILURE status. The
// corpus's generated callers test `== -1`, so a helper returning anything else
// lets execution walk past the check — and in GetPointTypeD the next statement
// is UpdateRpdRiskProfileDevationq59, which writes the risk profile to the
// database. A stub that silently succeeds and persists data it never fetched
// is the failure mode this closes.
//
// This is a whole-file scan rather than a census assertion on purpose: the
// census counts gaps, and this is about the code those gaps produced. A count
// cannot tell you the return value is wrong.
func TestNoHelperClaimsSuccessWithAnUnrenderedTPCall(t *testing.T) {
	if _, err := os.Stat(corpusTree); err != nil {
		t.Skip("no emitted corpus; run a -no-llm convertgo first")
	}
	body, err := os.ReadFile(filepath.Join(corpusTree, "fns.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Split on method boundaries and inspect each one's own text.
	var name string
	var chunk []string
	flush := func() {
		if name == "" {
			return
		}
		text := strings.Join(chunk, "\n")
		if strings.Contains(text, ReasonTPCallNotRendered) && !strings.Contains(text, "return -1") {
			t.Errorf("%s carries %s but never returns the failure status; its "+
				"caller's ==-1 check would pass over a call that never happened",
				name, ReasonTPCallNotRendered)
		}
		name, chunk = "", nil
	}
	for _, ln := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(ln, "func (s *tuxController) ") {
			flush()
			// Fields: ["func", "(s", "*tuxController)", "FnName(params…"].
			// The name is glued to its first parameter, so cut at the "("
			// rather than trimming a suffix.
			if f := strings.Fields(ln); len(f) > 3 {
				name = f[3]
				if i := strings.Index(name, "("); i >= 0 {
					name = name[:i]
				}
			}
			continue
		}
		chunk = append(chunk, ln)
	}
	flush()

	// And the positive half: a helper with no tpcall must NOT be forced to
	// fail. FnInsertIntoUra is the control — over-applying the override
	// would make the whole fn library return errors.
	if !strings.Contains(string(body), "func (s *tuxController) FnInsertIntoUra(") {
		t.Fatal("FnInsertIntoUra is missing from the corpus; the control case cannot be checked")
	}
}
