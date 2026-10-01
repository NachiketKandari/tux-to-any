package walk

import (
	"reflect"
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

// TestIndexAssignsEachHostToOneRead is the core property: a host belongs to
// exactly one read, and asking through a different read's slot says so.
func TestIndexAssignsEachHostToOneRead(t *testing.T) {
	// Two reads that both carry a "rpqm_qstn_id" column — the real corpus
	// shape, where q4 and cur_get_tblc_dtls share a column list.
	s := Index([]Read{
		{
			QueryID: "q4", Capture: "getTblcDtls", RowType: "GetTblcDtls",
			Shape:  "SELECT_SINGLE",
			Hosts:  []string{"sql_rp_prof", "sql_rp_eq_grwth"},
			Fields: []string{"RpProf", "RpEqGrwth"},
		},
		{
			QueryID: "cur_rps_risk_prof_scrn", Capture: "rpsRiskProfScrn",
			RowType: "RpsRiskProfScrn", Shape: "SELECT_MULTI",
			Hosts:  []string{"sql_rps_c_table", "sql_rps_a_text"},
			Fields: []string{"RpsCTable", "RpsAText"},
		},
	})

	if got, want := s.Len(), 2; got != want {
		t.Fatalf("scope has %d reads, want %d", got, want)
	}

	// sql_rps_c_table belongs to the rps read (index 1), not the tblc read.
	if s.ReadOwns(0, "sql_rps_c_table") {
		t.Error("read 0 claims sql_rps_c_table, which only read 1 produces")
	}
	if !s.ReadOwns(1, "sql_rps_c_table") {
		t.Error("read 1 does not claim sql_rps_c_table, which it produces")
	}

	// The owner names the read and the field the value lands in.
	o, ok := s.OwnerFor("sql_rps_c_table")
	if !ok {
		t.Fatal("sql_rps_c_table has no owner")
	}
	if o.Read != 1 || o.Field != "RpsCTable" {
		t.Errorf("owner = {Read:%d Field:%q}, want {Read:1 Field:RpsCTable}", o.Read, o.Field)
	}
}

// TestIndexFirstReadWins is the determinism rule. When two reads carry the
// same host, the earlier read in walk order owns it — a stable answer, not a
// map-iteration accident.
func TestIndexFirstReadWins(t *testing.T) {
	s := Index([]Read{
		{
			QueryID: "q4", Capture: "first", RowType: "First",
			Hosts: []string{"sql_shared_col"}, Fields: []string{"SharedCol"},
		},
		{
			QueryID: "q5", Capture: "second", RowType: "Second",
			Hosts: []string{"sql_shared_col"}, Fields: []string{"SharedCol"},
		},
	})
	o, ok := s.OwnerFor("sql_shared_col")
	if !ok {
		t.Fatal("shared host has no owner")
	}
	if o.Read != 0 {
		t.Errorf("shared host owned by read %d, want the first read (0)", o.Read)
	}
}

// TestIndexSkipsReadsWithNoShape guards the slot stability the renderer
// relies on: a read with no row shape keeps its index so every other read's
// index still matches the walk order.
func TestIndexSkipsReadsWithNoShape(t *testing.T) {
	s := Index([]Read{
		{QueryID: "q1", Capture: "dml"},
		{
			QueryID: "q2", Capture: "rows", RowType: "Rows",
			Hosts: []string{"sql_a"}, Fields: []string{"A"},
		},
	})
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (the shapeless read keeps its slot)", s.Len())
	}
	if !s.ReadOwns(1, "sql_a") {
		t.Error("read 1 lost its owner — a shapeless read shifted the indices")
	}
	if s.ReadOwns(0, "sql_a") {
		t.Error("read 0 (no shape) claims a host it cannot produce")
	}
}

// TestIndexHandlesRaggedShape covers a partly-known row shape: hosts and
// fields of different lengths must not panic or pair the wrong entries.
func TestIndexHandlesRaggedShape(t *testing.T) {
	s := Index([]Read{{
		QueryID: "q1", Capture: "rows", RowType: "Rows",
		Hosts:  []string{"sql_a", "sql_b", "sql_c"},
		Fields: []string{"A"},
	}})
	if !s.ReadOwns(0, "sql_a") {
		t.Error("sql_a should pair with A")
	}
	for _, h := range []string{"sql_b", "sql_c"} {
		if _, ok := s.OwnerFor(h); ok {
			t.Errorf("%s has a field to land in — it must not be indexed", h)
		}
	}
}

// TestUnresolvedIsEndpointLevel is the P3A contract the renderer reports from:
// a field no read produces is unresolved ONCE for the endpoint, not once per
// read that failed to produce it.
func TestUnresolvedIsEndpointLevel(t *testing.T) {
	s := Index([]Read{
		{Hosts: []string{"sql_a"}, Fields: []string{"A"}},
		{Hosts: []string{"sql_b"}, Fields: []string{"B"}},
	})
	got := s.Unresolved([]string{"sql_a", "sql_b", "sql_missing", "sql_missing", "sql_also_missing"})
	want := []string{"sql_missing", "sql_also_missing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unresolved = %v, want %v (deduped, in order)", got, want)
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
	if s.ReadOwns(0, "sql_a") {
		t.Error("nil scope should own nothing")
	}
	if _, ok := s.ReadFor("x"); ok {
		t.Error("nil scope should have no captures")
	}
	if got := s.Unresolved([]string{"sql_a"}); !reflect.DeepEqual(got, []string{"sql_a"}) {
		t.Errorf("nil scope should report everything unresolved, got %v", got)
	}
}
