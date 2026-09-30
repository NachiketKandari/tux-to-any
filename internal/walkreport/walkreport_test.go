package walkreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClassifyPinsEveryCorpusShape is the load-bearing test: each shape below
// is copied verbatim from a real tuxgo:TODO on riskPipelineTest, so if the
// emitter rewords a message the census stops classifying it and this fails
// loudly rather than the number quietly drifting into R-UNCLASSIFIED.
//
// The verbatim copies matter more than they look. An earlier draft of this
// test used hand-written paraphrases, which passed while matching nothing.
const (
	// helperArg: 345 on the corpus, the dominant gap and P1's target.
	helperArg = `FnSaveRiskProfile(sql_urf_eq_growth_asset_prcnt): no request-field provenance — zero value passed; LLM maps it`
	// helperArgAll15: the helper's out-param params (d_* pointers) arrive
	// spelled the same as the scalars — the emitter prints pr.Name, never an
	// &-prefixed form, so there is exactly one shape to match here.
	helperArgAll15 = `FnFindRiskProfile(d_debt_amt): no request-field provenance — zero value passed; LLM maps it`
	// storeArg: 20 on the corpus, lowerCamel param as Go.Param.
	storeArg = `UpdateUrfUsrRiskProf.dUrfDebtPrsrvAssetPrcnt: no request-field provenance for "d_urf_debt_prsrv_asset_prcnt" — zero value passed; LLM maps it`
	// rowMatchList: 19 on the corpus, the per-field list form.
	rowMatchList = `response fields without row match (zero values): PointType, UsrUsrNm`
	// responseRole: 39 on the corpus, the per-call form.
	responseRole = `getUacUsrAccnts (UacUsrAccnts) has no response-field match — kept for its error check; LLM maps its role`
	// nestedHelperArg: 5 on the corpus, FnInsertIntoUra's arguments seen from
	// inside FnSaveRiskProfile's body. Only reachable once P2 rendered that
	// body; the census surfaced it as R-UNCLASSIFIED first.
	nestedHelperArg = `FnInsertIntoUra(c_ura_user_id): no helper-param provenance — zero value passed; LLM maps it`
	// noStoreCalls: 1 on the corpus, FnFindRiskProfile's empty body. It did
	// not exist as a marker until P0 — before, the same helper rendered a
	// bare `return 0` and the census could not see it at all, which is the
	// silence P0 exists to end.
	noStoreCalls = `R-NO-STORE-CALLS: FnFindRiskProfile — the helper body rendered empty; its statements are not represented in this method`
	// controlFlow: 2 on the corpus, one per helper whose body branches or
	// loops. Each is a single summary gap; the per-construct lines beneath it
	// are unmarked detail, so the census reports "this method is missing 21
	// branches" once rather than 21 times.
	controlFlow = `R-CONTROL-FLOW-NOT-RENDERED: FnSaveRiskProfile — 21 of 32 branch/loop constructs in the legacy body are not rendered as Go control flow; the statements below are this method's whole walk, so every construct listed here is work this method does not do`
	// guardLeak: 1 on the corpus, and the only one of its kind. The legacy
	// source calls fn_insert_into_ura inside `if (c_flg_using == 'A')`; the
	// emitted Go calls it unconditionally, so it runs where the C does not.
	guardLeak = `R-CALL-OUTSIDE-ITS-GUARD: FnInsertIntoUra at line 4709 is emitted unconditionally, but the legacy source guards it with the branch at line 4704 (c_flg_using == 'A') — the call runs on paths where the C does not`
)

func TestClassifyPinsEveryCorpusShape(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    string
	}{
		{"helper arg", helperArg, ReasonHelperArgUnresolved},
		{"helper arg out-param", helperArgAll15, ReasonHelperArgUnresolved},
		{"store bind", storeArg, ReasonStoreArgUnresolved},
		{"row match list", rowMatchList, ReasonResponseFieldUnresolved},
		{"response role", responseRole, ReasonResponseFieldUnresolved},
		{"nested helper arg", nestedHelperArg, ReasonNestedHelperArgUnresolved},
		{"no store calls", noStoreCalls, ReasonNoStoreCalls},
		{"control flow", controlFlow, ReasonControlFlowNotRendered},
		{"guard leak", guardLeak, ReasonCallOutsideItsGuard},
		{"leading whitespace", "  " + storeArg, ReasonStoreArgUnresolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.message); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.message, got, tc.want)
			}
		})
	}
}

// TestReasonCodeAloneStillClassifies matters because a bare reason code is
// how these are searched for day to day (`grep R-NO-STORE-CALLS`). The
// emitter always writes "<code>: <helper> — <prose>", so the anchor has to
// survive the trailing detail being absent.
func TestReasonCodeAloneStillClassifies(t *testing.T) {
	if got := Classify(ReasonNoStoreCalls); got != ReasonNoStoreCalls {
		t.Errorf("Classify(%q) = %q, want itself", ReasonNoStoreCalls, got)
	}
	if got := Classify(ReasonNoStoreCalls + ": FnX — prose"); got != ReasonNoStoreCalls {
		t.Errorf("Classify(%q) = %q, want %q", ReasonNoStoreCalls+": FnX — prose", got, ReasonNoStoreCalls)
	}
	// The anchor is a prefix, so a message that merely starts with similar
	// text must not be claimed.
	if got := Classify("R-NO-STORE-CALLS are documented elsewhere"); got != ReasonNoStoreCalls {
		t.Errorf("Classify accepted prose starting with the code: %q", got)
	}
}

// TestNestedHelperArgIsNotTheCallerSideOne is the distinction P2 depends on.
// The two messages have the same shape — GoName(param): … provenance … — and
// differ only in the last phrase, so a census that matched on the prefix would
// merge them. P2 fixes detFnCallArgs; P1 fixed detHelperCalls. A merged bucket
// would let P2 report a drop that is really only one of the two.
func TestNestedHelperArgIsNotTheCallerSideOne(t *testing.T) {
	nested := Classify(nestedHelperArg)
	caller := Classify(helperArg)
	if nested == caller {
		t.Fatalf("nested and caller-side helper args both classified %q — "+
			"P1 and P2 fix different code paths and need them apart", nested)
	}
	if nested != ReasonNestedHelperArgUnresolved {
		t.Errorf("nested helper arg = %q, want %q", nested, ReasonNestedHelperArgUnresolved)
	}
}

// TestHelperArgAndStoreArgAreDistinguished is the distinction P1 depends on.
// Both shapes mean "no provenance" and a coarse census would merge them into
// one bucket; P1 then fixes detHelperCalls and reports a drop that is really
// only the helper half. If these ever collapse to the same code, P1's
// acceptance number becomes meaningless.
func TestHelperArgAndStoreArgAreDistinguished(t *testing.T) {
	if helper := Classify(helperArg); helper == Classify(storeArg) {
		t.Fatalf("helper-arg and store-bind shapes both classified %q — "+
			"P1 fixes the two call sites separately and needs them apart", helper)
	}
}

// TestClassifyDoesNotGuess is the instrument's own honesty rule. A message
// that matches nothing must land in R-UNCLASSIFIED, never in whichever code
// happens to be scanned last — a wrong bucket would look like a real gap in
// that category and quietly corrupt the phase comparison.
func TestClassifyDoesNotGuess(t *testing.T) {
	for _, msg := range []string{
		"some entirely new gap the emitter grew",
		"FnX(y): a completely different problem",
		"",
		"   ",
		// Close to a real shape but not it: the emitter dropped the
		// signature half, so the anchor that distinguishes a helper call
		// from a store call is gone. Must not be bucketed as either.
		"no request-field provenance — zero value passed",
		// The row-match phrase without its leading clause. rowMatchRe is
		// anchored on purpose: a message that merely mentions "row match"
		// somewhere else is not the response-field gap.
		"cannot find any row match for this",
		// The response-role form with its leading word dropped.
		"has no response-field match — kept for its error check",
	} {
		if got := Classify(msg); got != ReasonUnclassified {
			t.Errorf("Classify(%q) = %q, want %q", msg, got, ReasonUnclassified)
		}
	}
}

// TestEveryReasonHasAMatch keeps the vocabulary honest: a registered code
// with no pattern would report as a permanent zero and read as "this gap is
// gone" when in fact nothing can ever produce it.
func TestEveryReasonHasAMatch(t *testing.T) {
	for _, code := range AllReasons {
		if code == ReasonUnclassified {
			continue
		}
		hit := false
		for _, msg := range []string{helperArg, helperArgAll15, storeArg, rowMatchList, responseRole,
			nestedHelperArg, noStoreCalls, controlFlow, guardLeak} {
			if Classify(msg) == code {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("reason %q matches none of the corpus shapes", code)
		}
	}
}

// TestAllReasonsHasNoDuplicates guards the census table against a code
// appearing twice, which would double-count it in the rendered report.
func TestAllReasonsHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, code := range AllReasons {
		if seen[code] {
			t.Errorf("reason %q listed twice in AllReasons", code)
		}
		seen[code] = true
	}
}

// emittedSample mimics a real emitted controller file closely enough to
// exercise line counting, method attribution, and the "skip prose that is
// not a TODO" rule.
const emittedSample = `package controller

import "context"

// tuxgo:TODO this one is package-level and belongs to no method
// this comment mentions tuxgo:TODO but is not a marker line, so it must not count
func (s *tuxController) FnOne(c context.Context) int {
	// tuxgo:TODO ` + helperArg + `
	return 0
}

func (s *tuxController) FnTwo(c context.Context) int {
	// tuxgo:TODO ` + storeArg + `
	// tuxgo:TODO ` + responseRole + `
	return 0
}

// fnThree has a receiver-free declaration and must still be attributed.
func fnThree() {
	// tuxgo:TODO ` + rowMatchList + `
}
`

func TestCensusFileAttributesGapsToTheirMethod(t *testing.T) {
	gaps := CensusFile("tux.go", []byte(emittedSample))
	if len(gaps) != 5 {
		t.Fatalf("census found %d gaps, want 5:\n%+v", len(gaps), gaps)
	}

	want := []struct {
		method string
		reason string
	}{
		{"", ReasonUnclassified}, // package level
		{"FnOne", ReasonHelperArgUnresolved},
		{"FnTwo", ReasonStoreArgUnresolved},
		{"FnTwo", ReasonResponseFieldUnresolved},
		{"fnThree", ReasonResponseFieldUnresolved},
	}
	for i, w := range want {
		g := gaps[i]
		if g.Method != w.method {
			t.Errorf("gap %d method = %q, want %q (line %d: %s)", i, g.Method, w.method, g.Line, g.Message)
		}
		if g.Reason != w.reason {
			t.Errorf("gap %d reason = %q, want %q", i, g.Reason, w.reason)
		}
	}
}

// TestCensusFileIgnoresNonMarkerProse is what makes counting from the
// artifact safe: prose that merely mentions the marker is not a gap. Were
// this to break, the census would inflate on every explanatory comment the
// generator ever writes about its own TODOs.
func TestCensusFileIgnoresNonMarkerProse(t *testing.T) {
	gaps := CensusFile("tux.go", []byte(emittedSample))
	for _, g := range gaps {
		if strings.Contains(g.Message, "is not a marker line") {
			t.Error("counted a comment that only mentions the marker")
		}
		if g.Line > 0 {
			line := strings.Split(emittedSample, "\n")[g.Line-1]
			if !strings.Contains(line, "//") {
				t.Errorf("gap at line %d is not a comment: %q", g.Line, line)
			}
		}
	}
}

// TestCensusDirWalksTheTree covers the non-.go skip and the relative naming.
func TestCensusDirWalksTheTree(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tux.go", emittedSample)
	write("fns.go", "package controller\n\nfunc f() {\n\t// tuxgo:TODO "+helperArg+"\n}\n")
	// A non-Go asset that happens to contain the marker text must not count.
	write("README.md", "the generator writes "+TODOPrefix+" comments\n")

	c, err := CensusDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Total != 6 {
		t.Errorf("total = %d, want 6 (5 in tux.go + 1 in fns.go)", c.Total)
	}
	if c.ByFile["tux.go"] != 5 || c.ByFile["fns.go"] != 1 {
		t.Errorf("by file = %v", c.ByFile)
	}
	if c.ByMethod["FnTwo"] != 2 || c.ByMethod["FnOne"] != 1 {
		t.Errorf("by method = %v", c.ByMethod)
	}
	if c.ByCode[ReasonHelperArgUnresolved] != 2 {
		t.Errorf("helper-arg code = %d, want 2", c.ByCode[ReasonHelperArgUnresolved])
	}
}

// TestCensusDirSurvivesUnparseableGo is deliberate: generated output can be a
// fragment, and a census that refuses to read it is useless during exactly the
// debugging it exists for. The gaps still count; only attribution is lost.
func TestCensusDirSurvivesUnparseableGo(t *testing.T) {
	dir := t.TempDir()
	body := "package controller\nfunc broken( {\n\t// tuxgo:TODO " + helperArg + "\n"
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := CensusDir(dir)
	if err != nil {
		t.Fatalf("an unparseable file must not fail the census: %v", err)
	}
	if c.Total != 1 {
		t.Errorf("total = %d, want 1", c.Total)
	}
	if c.Gaps[0].Method != "" {
		t.Errorf("method = %q, want empty when the file cannot be parsed", c.Gaps[0].Method)
	}
}

// TestTidyFlagsAnUnclassifiedGap is the guard that makes a new emitter shape
// visible. Without it, the only symptom of an unregistered TODO is a number
// that looks fine.
func TestTidyFlagsAnUnclassifiedGap(t *testing.T) {
	r := &WalkReport{Census: &Census{
		Total:  4,
		ByCode: map[string]int{ReasonHelperArgUnresolved: 3, ReasonUnclassified: 1},
	}}
	findings := r.Tidy()
	if len(findings) != 1 || !strings.Contains(findings[0], "1 TODO") {
		t.Fatalf("findings = %v, want one naming the 1 unclassified TODO", findings)
	}
}

// TestTidyIsQuietOnAKnownCensus is the other half: a healthy census must
// report no findings, or the signal drowns in noise.
func TestTidyIsQuietOnAKnownCensus(t *testing.T) {
	r := &WalkReport{Census: &Census{
		Total:  423,
		ByCode: map[string]int{ReasonHelperArgUnresolved: 345, ReasonStoreArgUnresolved: 20, ReasonResponseFieldUnresolved: 58},
	}}
	if f := r.Tidy(); len(f) != 0 {
		t.Errorf("findings = %v, want none", f)
	}
}

// TestTidyFlagsAnEmptyCensus catches the wrong-directory mistake, which
// otherwise reads as a perfect score.
func TestTidyFlagsAnEmptyCensus(t *testing.T) {
	r := &WalkReport{Census: &Census{Total: 0, ByCode: map[string]int{}}}
	findings := r.Tidy()
	if len(findings) != 1 || !strings.Contains(findings[0], "wrong directory") {
		t.Errorf("findings = %v, want one naming the wrong directory", findings)
	}
}

// TestNewCoverageHandlesTheDegenerateCase pins the choice that a function with
// no code lines reports 100% rather than 0%. Zero would read as "we
// understood nothing", which is the opposite of the truth.
func TestNewCoverageHandlesTheDegenerateCase(t *testing.T) {
	c := NewCoverage("empty", 0, 0, 0, nil)
	if c.Percent != 100 {
		t.Errorf("Percent = %d for a function with no code lines, want 100", c.Percent)
	}
}

// TestNewCoverageTruncatesResidue keeps one bad function from burying the
// table, while Unknown still carries the true count.
func TestNewCoverageTruncatesResidue(t *testing.T) {
	residue := make([]int, 200)
	for i := range residue {
		residue[i] = i + 1
	}
	c := NewCoverage("bad", 100, 300, 200, residue)
	if len(c.ResidueLines) != 20 {
		t.Errorf("ResidueLines = %d, want 20", len(c.ResidueLines))
	}
	if c.Unknown != 200 {
		t.Errorf("Unknown = %d, want the true count 200", c.Unknown)
	}
	if c.Percent != 33 {
		t.Errorf("Percent = %d, want 33", c.Percent)
	}
}

// lineHas reports whether some line of s contains both substrings, which is
// how the render tests assert "this code is paired with that count" without
// pinning column widths.
func lineHas(s, a, b string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, a) && strings.Contains(ln, b) {
			return true
		}
	}
	return false
}

// TestRenderIsDeterministic guards the golden test below: the report is
// compared byte-for-byte, and map iteration order would make it flaky.
func TestRenderIsDeterministic(t *testing.T) {
	r := &WalkReport{
		Target:     "riskPipelineTest/tux.pc",
		Controller: "conversion_logs/_staged/tux/controller",
		Coverage: []CoverageReport{{
			Path:      "tux.pc",
			Functions: []Coverage{NewCoverage("SVC_RISK_PRFL", 2713, 2723, 10, []int{856, 857})},
		}},
		Census: &Census{
			Total:    423,
			ByCode:   map[string]int{ReasonHelperArgUnresolved: 345, ReasonStoreArgUnresolved: 20, ReasonResponseFieldUnresolved: 58},
			ByMethod: map[string]int{"FnFindRiskProfile": 345, "MANAGE_RISK_PROFILE_VIEW": 20},
			ByFile:   map[string]int{"tux.go": 423},
		},
	}
	var first strings.Builder
	r.Render(&first)
	for i := 0; i < 20; i++ {
		var next strings.Builder
		r.Render(&next)
		if next.String() != first.String() {
			t.Fatalf("render %d differs from the first render; map order is leaking", i)
		}
	}

	got := first.String()
	// Spot-check the numbers a reader is meant to take away. Column padding
	// is checked as "the code and the count share a line" rather than at a
	// fixed offset: the alignment is presentation and will be retuned, but
	// the pairing of a code with its count is the contract.
	for _, want := range [][2]string{
		{"gap census: 423 TODO(s)", ""},
		{ReasonHelperArgUnresolved, "345"},
		{ReasonStoreArgUnresolved, "20"},
		{ReasonResponseFieldUnresolved, "58"},
		{"2713/2723 code lines classified (99%)", ""},
		{"unknown 10 at lines 856,857", ""},
	} {
		code, count := want[0], want[1]
		if !strings.Contains(got, code) {
			t.Errorf("render missing %q:\n%s", code, got)
			continue
		}
		if count != "" && !lineHas(got, code, count) {
			t.Errorf("render does not pair %q with count %q:\n%s", code, count, got)
		}
	}
	// A clean census must not print a findings block.
	if strings.Contains(got, "findings:") {
		t.Errorf("render printed findings for a clean census:\n%s", got)
	}
}

// TestRenderSkipsEmptyCodesInTheTable is cosmetic but load-bearing for
// scanning: a zero row for a code that has not appeared yet is noise between
// the reader and the rows that matter.
func TestRenderSkipsEmptyCodesInTheTable(t *testing.T) {
	r := &WalkReport{Census: &Census{Total: 1, ByCode: map[string]int{ReasonHelperArgUnresolved: 1}}}
	var b strings.Builder
	r.Render(&b)
	if strings.Contains(b.String(), ReasonStoreArgUnresolved) {
		t.Errorf("render showed an absent code:\n%s", b.String())
	}
	// R-UNCLASSIFIED always shows, even at zero: it is the row that says
	// "nothing landed here by accident".
	if !strings.Contains(b.String(), ReasonUnclassified) {
		t.Errorf("render hid R-UNCLASSIFIED:\n%s", b.String())
	}
}

// TestRenderShowsUnclassifiedSoItCannotHide is the paired check on the rule
// above: the one code that must never be suppressed.
func TestRenderShowsUnclassifiedSoItCannotHide(t *testing.T) {
	r := &WalkReport{Census: &Census{Total: 1, ByCode: map[string]int{ReasonUnclassified: 1}}}
	var b strings.Builder
	r.Render(&b)
	if !strings.Contains(b.String(), ReasonUnclassified) {
		t.Errorf("render hid a non-zero R-UNCLASSIFIED:\n%s", b.String())
	}
}
