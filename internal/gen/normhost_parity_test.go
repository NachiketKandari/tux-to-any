package gen

import (
	"testing"

	"tux-to-any/internal/walk"
)

// TestNormalizeHostAgreesWithGenNormHost is the anti-drift pin for P3A.
//
// internal/walk keys its provenance index with walk.NormalizeHost; this
// package keys every renderer map with normHost. If the two disagree on any
// spelling, the index silently starts answering a different question than the
// renderer asks — a field would be reported owned while the renderer looks it
// up under another key, or vice versa, and nothing else in the suite would
// notice.
//
// The spellings below are the real ones from riskPipelineTest/tux.pc and the
// generated row shapes, plus the awkward forms the rule actually turns on.
func TestNormalizeHostAgreesWithGenNormHost(t *testing.T) {
	hosts := []string{
		// bare char/varchar hosts
		"c_user_id", "c_match_accnt", "c_rqst_typ", "l_sssn_id",
		// the sql_ prefix the rule strips
		"sql_rpam_answer_id", "sql_rpqm_qstn_id", "sql_rp_prof",
		"sql_rps_a_text", "sql_ura_uniq_nmbr", "sql_urf_risk_prof",
		// .arr members — the rule takes the MEMBER, not the base
		"sql_rps_a_text.arr", "sql_rpqm_qstn_section.arr", "sql_rpam_answer_text.arr",
		// indexed forms
		"sql_grc[0]", "sql_grc[i]", "c_user_id[0]",
		// buffer pointers and FML plumbing
		"ptr_fml_Obuffer", "ptr_fml_Ibuffer", "ptr_fml_Rbuf", "ptr_fml_Sbuf",
		// whitespace and the trailing-comment shape
		"  sql_rp_prof  ", "sql_rp_prof /* Ver 1.3 */",
		// already-normalized and empty
		"rpam_answer_id", "",
	}
	for _, h := range hosts {
		got, want := walk.NormalizeHost(h), normHost(h)
		if got != want {
			t.Errorf("walk.NormalizeHost(%q) = %q but gen.normHost(%q) = %q — "+
				"the provenance index and the renderer now key differently",
				h, got, h, want)
		}
	}
}
