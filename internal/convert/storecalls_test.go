package convert

import (
	"strings"
	"testing"
)

// TestAssignStoreCalls pins the captured-call view rendering: error-only
// calls assign the named err, row calls declare a deterministic capture and
// err, duplicate methods gain a suffix, and non-call lines stay untouched.
func TestAssignStoreCalls(t *testing.T) {
	view := "s.store.GetUserInfo(c, request.MatchAccnt, request.UsrId)\n" +
		"s.store.FetchAnalyzerMstr(c)\n" +
		"s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, request.UsrAddrss2Stte)\n" +
		"s.store.FetchAnalyzerMstr(c)\n" +
		"data = append(data, &models.X{})\n" +
		"err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {\n"
	shape := map[string]string{
		"GetUserInfo":       "single",
		"FetchAnalyzerMstr": "rows",
		"UpdateRiskProfile": "error",
	}
	got, n := assignStoreCalls(view, "s.store.", shape)
	if n != 4 {
		t.Fatalf("rewritten = %d, want 4:\n%s", n, got)
	}
	for _, want := range []string{
		"getUserInfo, err := s.store.GetUserInfo(c, request.MatchAccnt, request.UsrId)",
		"fetchAnalyzerMstrRows, err := s.store.FetchAnalyzerMstr(c)",
		"err = s.store.UpdateRiskProfile(c, tx, request.MatchAccnt, request.UsrAddrss2Stte)",
		"fetchAnalyzerMstrRows2, err := s.store.FetchAnalyzerMstr(c)",
		"data = append(data, &models.X{})",
		"err = utils.ExecTransaction(c, s.store.GetDB(), func(tx *sqlx.Tx) error {",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// Methods without a shape entry stay bare.
	bare := "s.store.UnknownCall(c)\nreturn data, nil\n"
	if got, n := assignStoreCalls(bare, "s.store.", shape); n != 0 || got != bare {
		t.Errorf("unclassified call altered (n=%d):\n%s", n, got)
	}
}
