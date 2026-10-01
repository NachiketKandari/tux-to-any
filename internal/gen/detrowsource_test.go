package gen

import (
	"reflect"
	"testing"

	"tux-to-any/internal/ir"
	"tux-to-any/internal/templates"
	"tux-to-any/internal/walk"
)

// respField is a minimal FieldSpec for a response model field.
func respField(name string) templates.FieldSpec { return templates.FieldSpec{Name: name} }

// p3aScope is the corpus shape in miniature: two reads, each owning a
// different host. It is the situation the old per-read question could not
// express — a field belongs to ONE of them.
func p3aScope() *walk.Scope {
	return walk.Index([]walk.Read{
		{
			QueryID: "q4", Capture: "getTblcDtls", RowType: "GetTblcDtls",
			Hosts:  []string{"sql_rp_prof", "sql_rp_eq_grwth"},
			Fields: []string{"RpProf", "RpEqGrwth"},
		},
		{
			QueryID: "cur_rps", Capture: "rpsRiskProfScrn", RowType: "RpsRiskProfScrn",
			Hosts:  []string{"sql_rps_c_table"},
			Fields: []string{"RpsCTable"},
		},
	})
}

// TestDetRowSourcesAttributesEachFieldToOneRead is P3A's core claim: a
// response field is attributed to the read that produces its host, not to
// every read that fails to.
func TestDetRowSourcesAttributesEachFieldToOneRead(t *testing.T) {
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
		{Kind: ir.FmlAdd, Field: "FML_USR_ADDRSS2_LN1", Target: "sql_rp_prof"},
	}
	resp := []templates.FieldSpec{respField("PointType"), respField("UsrAddrss2Ln1")}

	owned := detRowSources(adds, resp, p3aScope())

	if got, want := owned["PointType"], 1; got != want {
		t.Errorf("PointType owned by read %d, want %d (the rps read)", got, want)
	}
	if got, want := owned["UsrAddrss2Ln1"], 0; got != want {
		t.Errorf("UsrAddrss2Ln1 owned by read %d, want %d (the tblc read)", got, want)
	}
}

// TestDetUnsourcedIsReportedOncePerEndpoint is the P3A reporting contract. A
// field whose host no read produces is one gap for the endpoint — not one per
// read that failed to produce it, which is what produced the same field list
// repeated under every read.
func TestDetUnsourcedIsReportedOncePerEndpoint(t *testing.T) {
	adds := []ir.FmlOp{
		// A host no read yields: the C writes it from a computed local.
		{Kind: ir.FmlAdd, Field: "FML_ANSWR_FLAG", Target: "c_risk_prof_set_flg"},
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
	}
	resp := []templates.FieldSpec{
		respField("PointType"), respField("AnswrFlag"), respField("UsrUsrNm"),
	}

	owned := detRowSources(adds, resp, p3aScope())
	got := detUnsourced(owned, resp)
	want := []string{"AnswrFlag", "UsrUsrNm"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("detUnsourced = %v, want %v (response order, PointType excluded)", got, want)
	}
}

// TestDetRowPairsEmitsOnlyItsOwnFields is the emission half: the tblc read
// renders the field it owns and abstains on the one the rps read owns, instead
// of claiming it through a substring guess.
func TestDetRowPairsEmitsOnlyItsOwnFields(t *testing.T) {
	scope := p3aScope()
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
		{Kind: ir.FmlAdd, Field: "FML_USR_ADDRSS2_LN1", Target: "sql_rp_prof"},
	}
	resp := []templates.FieldSpec{respField("PointType"), respField("UsrAddrss2Ln1")}
	owned := detRowSources(adds, resp, scope)

	dc := &detCall{
		capture:   "getTblcDtls",
		rowName:   "GetTblcDtls",
		rowByHost: map[string]string{"rp_prof": "RpProf", "rp_eq_grwth": "RpEqGrwth"},
		rowFields: []templates.FieldSpec{respField("RpProf"), respField("RpEqGrwth")},
	}

	pairs := detRowPairs(dc, adds, resp, scope, owned)
	if len(pairs) != 1 {
		t.Fatalf("tblc read emitted %d pair(s), want 1 (only the field it owns); got %+v", len(pairs), pairs)
	}
	if pairs[0].Resp != "UsrAddrss2Ln1" || pairs[0].Row != "RpProf" {
		t.Errorf("pair = %+v, want {UsrAddrss2Ln1 RpProf}", pairs[0])
	}

	// And the owning read does render it.
	rpsDC := &detCall{
		capture:   "rpsRiskProfScrn",
		rowName:   "RpsRiskProfScrn",
		rowByHost: map[string]string{"rps_c_table": "RpsCTable"},
		rowFields: []templates.FieldSpec{respField("RpsCTable")},
	}
	rpsPairs := detRowPairs(rpsDC, adds, resp, scope, owned)
	if len(rpsPairs) != 1 || rpsPairs[0].Resp != "PointType" || rpsPairs[0].Row != "RpsCTable" {
		t.Errorf("rps read pairs = %+v, want [{PointType RpsCTable}]", rpsPairs)
	}
}

// TestDetReadFeedsResponseIsAboutAttribution pins the distinction that made
// the single-read note honest: a read whose only apparent pairs came from
// another read's fields feeds nothing, and must be reported as such rather
// than kept alive by a cross-read guess.
func TestDetReadFeedsResponseIsAboutAttribution(t *testing.T) {
	scope := p3aScope()
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
	}
	resp := []templates.FieldSpec{respField("PointType")}
	owned := detRowSources(adds, resp, scope)

	tblc := &detCall{capture: "getTblcDtls", rowName: "GetTblcDtls"}
	rps := &detCall{capture: "rpsRiskProfScrn", rowName: "RpsRiskProfScrn"}

	if detReadFeedsResponse(tblc, owned, scope) {
		t.Error("tblc read reported as feeding a response, but the only field is owned by the rps read")
	}
	if !detReadFeedsResponse(rps, owned, scope) {
		t.Error("rps read does not feed the response, but it owns PointType")
	}
	if detReadFeedsResponse(nil, owned, scope) {
		t.Error("nil read must not feed a response")
	}
}

// TestDetReadCouldSourceUnsourcedIsTheP3CSplit pins the distinction P3C
// exists to draw. Two reads both feed no response field:
//
//   - one whose row shape carries the host an unsourced response write names.
//     The value exists and shaping failed to place it — a real gap.
//   - one whose row shape does not. The read is kept for its own sake —
//     finished work, and no LLM budget belongs on it.
//
// Reporting them with one message is what let 21 real gaps hide inside 45.
func TestDetReadCouldSourceUnsourcedIsTheP3CSplit(t *testing.T) {
	// The endpoint could not place PointType, whose write names
	// sql_rps_c_table.
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
	}
	unsourced := []string{"PointType"}

	// This read's row DOES carry sql_rps_c_table — it could have sourced it.
	carrier := &detCall{
		capture: "rpsRiskProfScrn", rowName: "RpsRiskProfScrn",
		query: &ir.Query{RowShape: []string{"sql_rps_c_table", "sql_rps_a_text"}},
	}
	if !detReadCouldSourceUnsourced(carrier, adds, unsourced) {
		t.Error("read carrying sql_rps_c_table reported as unable to source PointType — this is a real gap")
	}

	// This read's row cannot: the host is not in its shape.
	other := &detCall{
		capture: "getDual", rowName: "Dual",
		query: &ir.Query{RowShape: []string{"sql_ura_uniq_nmbr"}},
	}
	if detReadCouldSourceUnsourced(other, adds, unsourced) {
		t.Error("read without sql_rps_c_table reported as a gap — it is finished work")
	}

	// No unsourced fields at all: nothing to place, so nothing can be a gap.
	if detReadCouldSourceUnsourced(carrier, adds, nil) {
		t.Error("a read cannot be a gap when no field is unsourced")
	}
	// A read with no IR query has no shape to check against.
	if detReadCouldSourceUnsourced(&detCall{capture: "x"}, adds, unsourced) {
		t.Error("a read with no query must not be reported as a gap")
	}
	if detReadCouldSourceUnsourced(nil, adds, unsourced) {
		t.Error("nil read must not be reported as a gap")
	}
}

// --- Guards that this corpus does not reach -------------------------------
//
// The tests above exercise the helpers directly. These exercise the WIRING:
// the two branches in detEmitShaping/detRowPairs that only fire when a field's
// provenance and its fuzzy match disagree. On riskPipelineTest they never fire
// — toggling either one off produces a byte-identical emitted tree — so
// without these cases the guards would be untested code that reads as tested.

// TestOwnershipGuardBeatsAFuzzyMatch is the case the P3A guard exists for.
//
// PointType is owned by the rps read, but its NAME fuzzy-matches the tblc
// read's RpProf ("pointtype" contains... nothing, but the reverse direction
// does) — detFuzzyRowField matches on substring in either direction, so a read
// whose row fields happen to contain the response field's name would claim it
// even though another read owns it. The guard must win over the guess.
func TestOwnershipGuardBeatsAFuzzyMatch(t *testing.T) {
	scope := walk.Index([]walk.Read{
		{
			QueryID: "q4", Capture: "tblc", RowType: "GetTblcDtls",
			Hosts: []string{"sql_rp_prof"}, Fields: []string{"PointTypeProf"},
		},
		{
			QueryID: "cur_rps", Capture: "rps", RowType: "RpsRiskProfScrn",
			Hosts: []string{"sql_rps_c_table"}, Fields: []string{"RpsCTable"},
		},
	})
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
	}
	resp := []templates.FieldSpec{respField("PointType")}
	owned := detRowSources(adds, resp, scope)

	// Sanity: the fuzzy matcher WOULD have matched tblc's row field.
	tblc := &detCall{
		capture: "tblc", rowName: "GetTblcDtls",
		rowByHost: map[string]string{"rp_prof": "PointTypeProf"},
		rowFields: []templates.FieldSpec{respField("PointTypeProf")},
	}
	if detFuzzyRowField(tblc.rowFields, "PointType") == "" {
		t.Fatal("fixture is not exercising the guard: the fuzzy matcher found no match")
	}

	// The guard must refuse it anyway: another read owns PointType.
	if pairs := detRowPairs(tblc, adds, resp, scope, owned); len(pairs) != 0 {
		t.Errorf("tblc emitted %+v — it claimed a field the rps read owns, via a fuzzy match", pairs)
	}
}

// TestReadFeedsResponseGuardIsAttributionNotPairs is the P3C wiring case: a
// read whose ONLY apparent pairs come from another read's fields must report
// that it feeds nothing, not that it shaped something.
func TestReadFeedsResponseGuardIsAttributionNotPairs(t *testing.T) {
	scope := walk.Index([]walk.Read{
		{
			QueryID: "q4", Capture: "tblc", RowType: "GetTblcDtls",
			Hosts: []string{"sql_rp_prof"}, Fields: []string{"PointTypeProf"},
		},
		{
			QueryID: "cur_rps", Capture: "rps", RowType: "RpsRiskProfScrn",
			Hosts: []string{"sql_rps_c_table"}, Fields: []string{"RpsCTable"},
		},
	})
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
	}
	resp := []templates.FieldSpec{respField("PointType")}
	owned := detRowSources(adds, resp, scope)

	tblc := &detCall{
		capture: "tblc", rowName: "GetTblcDtls",
		rowByHost: map[string]string{"rp_prof": "PointTypeProf"},
		rowFields: []templates.FieldSpec{respField("PointTypeProf")},
	}
	// It looks like it shapes something (the fuzzy match), but it does not:
	// attribution is the question, not pairs.
	if detReadFeedsResponse(tblc, owned, scope) {
		t.Error("tblc reported as feeding a response on the strength of a fuzzy match " +
			"for a field the rps read owns")
	}
}

// TestDetReadCouldSourceUnsourcedGuardIsReachable covers the P3C branch that
// the corpus never takes: a read that feeds no response field BUT whose row
// carries the host an unsourced write names. That read is a real gap, not
// finished work, and the message must say so.
func TestDetReadCouldSourceUnsourcedGuardIsReachable(t *testing.T) {
	adds := []ir.FmlOp{
		{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_rps_c_table"},
	}
	unsourced := []string{"PointType"}

	// Owns nothing, but carries the host: a gap.
	carrier := &detCall{
		capture: "rps", rowName: "RpsRiskProfScrn",
		query: &ir.Query{RowShape: []string{"sql_rps_c_table"}},
	}
	if !detReadCouldSourceUnsourced(carrier, adds, unsourced) {
		t.Error("a read carrying the unsourced host was not reported as a gap")
	}

	// Owns nothing and cannot carry it: finished work.
	kept := &detCall{
		capture: "dual", rowName: "Dual",
		query: &ir.Query{RowShape: []string{"sql_ura_uniq_nmbr"}},
	}
	if detReadCouldSourceUnsourced(kept, adds, unsourced) {
		t.Error("a read that cannot carry the unsourced host was reported as a gap")
	}
}
