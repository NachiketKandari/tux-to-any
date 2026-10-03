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