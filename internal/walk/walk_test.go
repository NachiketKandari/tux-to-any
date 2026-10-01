package walk

import (
	"testing"
)

// TestNormalizeHostIsTheOneKey pins the index's spelling of a host. These are
// the real spellings the corpus uses: bare hosts, ".arr" members, the sql_
// prefix, and the row-shape forms.
//
// Note what the rule does NOT do: it strips "sql_" but never the "c_" of a
// char host, and a dotted member yields the MEMBER — "sql_x.arr" is "arr".
// Both look surprising and both are load-bearing, because gen.normHost is the
// key the renderer's own maps are built with.
func TestNormalizeHostIsTheOneKey(t *testing.T) {
	cases := map[string]string{
		"c_user_id":          "c_user_id",
		"sql_rpam_answer_id": "rpam_answer_id",
		"sql_rps_a_text.arr": "arr",
		"sql_rpqm_qstn_id":   "rpqm_qstn_id",
		"  sql_rp_prof  ":    "rp_prof",
		"sql_grc[0]":         "grc",
	}
	for in, want := range cases {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOwnerForAssignsEachHostToOneRead is the core property the renderer
// depends on: a host belongs to exactly one read, and the owner names both the
// read and the row field the value lands in. This is what stops every read
// being asked about every field.
func TestOwnerForAssignsEachHostToOneRead(t *testing.T) {
	s := Index([]Read{
		{
			QueryID: "q4", Capture: "getTblcDtls", RowType: "GetTblcDtls",
			Hosts:  []string{"sql_rp_prof", "sql_rp_eq_grwth"},
			Fields: []string{"RpProf", "RpEqGrwth"},
		},
		{
			QueryID: "cur_rps_risk_prof_scrn", Capture: "rpsRiskProfScrn",
			RowType: "RpsRiskProfScrn",
			Hosts:   []string{"sql_rps_c_table", "sql_rps_a_text"},
			Fields:  []string{"RpsCTable", "RpsAText"},
		},
	})

	if got, want := s.Len(), 2; got != want {
		t.Fatalf("scope has %d reads, want %d", got, want)
	}

	o, ok := s.OwnerFor("sql_rps_c_table")
	if !ok {
		t.Fatal("sql_rps_c_table has no owner")
	}
	if o.Read != 1 || o.Field != "RpsCTable" {
		t.Errorf("owner = {Read:%d Field:%q}, want {Read:1 Field:RpsCTable}", o.Read, o.Field)
	}

	if o, ok := s.OwnerFor("sql_rp_prof"); !ok || o.Read != 0 || o.Field != "RpProf" {
		t.Errorf("sql_rp_prof owner = %+v (ok=%v), want {Read:0 Field:RpProf}", o, ok)
	}
	if _, ok := s.OwnerFor("sql_not_present"); ok {
		t.Error("a host no read produces must have no owner")
	}
}

// TestIndexFirstReadWins is the determinism rule. When two reads carry the
// same host, the earlier read in walk order owns it — a stable answer, not a
// map-iteration accident.
func TestIndexFirstReadWins(t *testing.T) {
	s := Index([]Read{
		{Capture: "first", Hosts: []string{"sql_shared_col"}, Fields: []string{"SharedCol"}},
		{Capture: "second", Hosts: []string{"sql_shared_col"}, Fields: []string{"SharedCol"}},
	})
	o, ok := s.OwnerFor("sql_shared_col")
	if !ok {
		t.Fatal("shared host has no owner")
	}
	if o.Read != 0 {
		t.Errorf("shared host owned by read %d, want the first read (0)", o.Read)
	}
}

// TestReadForResolvesCaptures pins the lookup the shaping path uses to ask
// "which index am I?" — captures are unique per endpoint, so it is a lookup.
func TestReadForResolvesCaptures(t *testing.T) {
	s := Index([]Read{
		{Capture: "getDual"},
		{Capture: "getRpsRiskProfScrn"},
	})
	if i, ok := s.ReadFor("getRpsRiskProfScrn"); !ok || i != 1 {
		t.Errorf("ReadFor = (%d, %v), want (1, true)", i, ok)
	}
	if _, ok := s.ReadFor("nope"); ok {
		t.Error("ReadFor found a capture that is not in scope")
	}
}

// TestIndexSkipsReadsWithNoShape guards the slot stability the renderer relies
// on: a read with no row shape keeps its index, so every other read's index
// still matches the walk order.
func TestIndexSkipsReadsWithNoShape(t *testing.T) {
	s := Index([]Read{
		{QueryID: "q1", Capture: "dml"},
		{QueryID: "q2", Capture: "rows", RowType: "Rows",
			Hosts: []string{"sql_a"}, Fields: []string{"A"}},
	})
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (the shapeless read keeps its slot)", s.Len())
	}
	if o, ok := s.OwnerFor("sql_a"); !ok || o.Read != 1 {
		t.Errorf("sql_a owner = %+v (ok=%v), want Read 1 — a shapeless read shifted the indices", o, ok)
	}
}

// TestIndexHandlesRaggedShape covers a partly-known row shape: hosts and
// fields of different lengths must not panic or pair the wrong entries.
func TestIndexHandlesRaggedShape(t *testing.T) {
	s := Index([]Read{{
		Capture: "rows", RowType: "Rows",
		Hosts:  []string{"sql_a", "sql_b", "sql_c"},
		Fields: []string{"A"},
	}})
	if o, ok := s.OwnerFor("sql_a"); !ok || o.Field != "A" {
		t.Errorf("sql_a owner = %+v (ok=%v), want Field A", o, ok)
	}
	for _, h := range []string{"sql_b", "sql_c"} {
		if _, ok := s.OwnerFor(h); ok {
			t.Errorf("%s has no field to land in — it must not be indexed", h)
		}
	}
}

// TestNilScopeIsUsable lets callers build a scope unconditionally rather than
// nil-checking at every call site.
func TestNilScopeIsUsable(t *testing.T) {
	var s *Scope
	if s.Len() != 0 {
		t.Error("nil scope should have no reads")
	}
	if _, ok := s.OwnerFor("sql_a"); ok {
		t.Error("nil scope should own nothing")
	}
	if _, ok := s.ReadFor("x"); ok {
		t.Error("nil scope should have no captures")
	}
}
