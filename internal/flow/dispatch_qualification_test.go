package flow

import (
	"testing"
)

// emptyProbeSrc pins the sub-threshold-candidate fall-through (regression:
// tirod.pc / SVC_MF_TIORD_BK). A `strcmp(x, "")` emptiness probe harvests
// exactly one value yet sits in two branch guards, so it clears the ≥2-guard
// qualification rule and used to WIN recognizer 2 — short-circuiting the
// cascade before recognizer 3 could return the entry's real char-compare
// spine. Qualification is per candidate (collectAxes), so the probe falls
// through and option_flg dispatches.
const emptyProbeSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char option_flg[2];
	char vc_from_date[13];
	if (option_flg == 'P') {
		list_forms();
	}
	if (option_flg == 'D') {
		form_details();
	}
	if (option_flg == 'R') {
		review();
	}
	if (strcmp(vc_from_date, "") == 0 || strcmp(vc_from_date, "") == 0) {
		errlog("S31310", "Invalid date selected.");
	}
	if (strcmp(vc_from_date, "") == 0) {
		errlog("S31345", "Invalid date selected.");
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisEmptyProbeDoesNotHijackCascade(t *testing.T) {
	a := dispatchAxisOf(t, emptyProbeSrc)
	if a == nil {
		t.Fatal("axis: nil — the emptiness probe short-circuited the cascade past option_flg")
	}
	if a.Key() != "option_flg" {
		t.Errorf("axis key = %q, want %q (recognizer 2's 1-value probe must not win)", a.Key(), "option_flg")
	}
	if len(a.Domain) != 3 {
		t.Errorf("domain = %v, want 3 values (P/D/R)", a.Domain)
	}
}

// oneValueStrcmpSpineAlone pins that a lone 1-value strcmp candidate is still
// no axis at all — the fall-through must not manufacture a dispatch where the
// probe is the only evidence.
const oneValueStrcmpSpineAlone = `void SVC_DEMO(TPSVCINFO *rqst) {
	char vc_from_date[13];
	if (strcmp(vc_from_date, "") == 0) {
		errlog("S31310", "Invalid date selected.");
	}
	if (strcmp(vc_from_date, "") == 0) {
		errlog("S31345", "Invalid date selected.");
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisOneValueProbeIsNotAnAxis(t *testing.T) {
	if a := dispatchAxisOf(t, oneValueStrcmpSpineAlone); a != nil {
		t.Errorf("axis = %v, want nil (a 1-value domain is not a dispatch)", a)
	}
}

// nestedHeavierSrc pins API-level precedence (regression). outer_flg is the
// entry's top-level spine (2 guards); inner_flg is a NESTED chain with more
// values and more compares (4 each). Weight alone used to hand rank 0 to
// inner_flg, so scenarios were sliced on per-arm detail instead of the API
// surface — and classifyAxisKinds, judging depth relative to rank 0, then
// labelled BOTH primary.
const nestedHeavierSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char outer_flg;
	char inner_flg;
	if (outer_flg == 'A') {
		stuff_a();
	}
	if (outer_flg == 'B') {
		stuff_b();
	}
	if (li_sssn_id != 0) {
		if (inner_flg == 'P') {
			work_p();
		}
		if (inner_flg == 'R') {
			work_r();
		}
		if (inner_flg == 'A') {
			work_a();
		}
		if (inner_flg == 'W') {
			work_w();
		}
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisPrefersTopLevelOverHeavierNested(t *testing.T) {
	a := dispatchAxisOf(t, nestedHeavierSrc)
	if a == nil {
		t.Fatal("axis: nil")
	}
	if a.Key() != "outer_flg" {
		t.Errorf("axis key = %q (domain %v, sites %d), want %q — a nested chain is per-arm detail, not the entry's spine",
			a.Key(), a.Domain, a.Sites, "outer_flg")
	}
}

func TestAxesForNestedChainIsSecondaryNotPrimary(t *testing.T) {
	tree := axesTree(t, nestedHeavierSrc, nil)
	axes := tree.AxesFor([]byte(nestedHeavierSrc))
	if len(axes) != 2 {
		t.Fatalf("axes = %d, want 2: %v", len(axes), axes)
	}
	if axes[0].Key() != "outer_flg" || axes[0].Kind != AxisPrimary {
		t.Errorf("rank 0 = %s/%s, want outer_flg/primary", axes[0].Key(), axes[0].Kind)
	}
	if axes[1].Key() != "inner_flg" || axes[1].Kind != AxisSecondary {
		t.Errorf("rank 1 = %s/%s, want inner_flg/secondary", axes[1].Key(), axes[1].Kind)
	}
}

// topLevelHeavierSrc is the ordinary shape — the top-level spine also wins on
// weight. Depth must not change the outcome here.
const topLevelHeavierSrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char outer_flg;
	char inner_flg;
	if (outer_flg == 'A') {
		stuff_a();
	}
	if (outer_flg == 'B') {
		stuff_b();
	}
	if (outer_flg == 'C') {
		stuff_c();
	}
	if (outer_flg == 'D') {
		stuff_d();
	}
	if (li_sssn_id != 0) {
		if (inner_flg == 'P') {
			work_p();
		}
		if (inner_flg == 'R') {
			work_r();
		}
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisTopLevelStillWinsWhenAlsoHeavier(t *testing.T) {
	a := dispatchAxisOf(t, topLevelHeavierSrc)
	if a == nil {
		t.Fatal("axis: nil")
	}
	if a.Key() != "outer_flg" {
		t.Errorf("axis key = %q (domain %v), want outer_flg", a.Key(), a.Domain)
	}
	if len(a.Domain) != 4 {
		t.Errorf("domain = %v, want 4 values", a.Domain)
	}
}

// nestedOnlySrc: every qualifying candidate is nested. Depth preference must
// not manufacture a nil here — the best nested chain is still the entry's
// only spine, and reporting none would be a silent no-op.
const nestedOnlySrc = `void SVC_DEMO(TPSVCINFO *rqst) {
	char inner_flg;
	if (li_sssn_id != 0) {
		if (inner_flg == 'P') {
			work_p();
		}
		if (inner_flg == 'R') {
			work_r();
		}
	}
	tpreturn(TPSUCCESS, 0, (char *)ptr_fml_Obuffer, 0L, 0);
}
`

func TestDispatchAxisNestedOnlyStillDetected(t *testing.T) {
	a := dispatchAxisOf(t, nestedOnlySrc)
	if a == nil {
		t.Fatal("axis: nil — a nested chain is the only spine here; reporting none would be a silent no-op")
	}
	if a.Key() != "inner_flg" {
		t.Errorf("axis key = %q, want inner_flg", a.Key())
	}
}
