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
