package gen

import (
	"testing"

	"tux-to-any/internal/ir"
)

// detAdds is the shaping path's view of "which writes are response values".
// It must admit output-buffer writes only: an Fadd32 against the input buffer
// writes the *request* buffer — a default or a guard — and is never the
// response value.
//
// This is the same rule flow.resolveResponses draws for SCEN-D9, and the two
// paths disagreeing is exactly what left the response field unmapped: the
// shared preamble's Ibuffer `Fadd32(…, FML_POINT_TYPE, &sql_urf_mm_opt_stts_2)`
// was admitted alongside the per-branch Obuffer write and, being earlier in
// source order, won the first-wins target lookup.
func TestDetAddsAdmitsOutputBufferWritesOnly(t *testing.T) {
	cond := &ir.Condition{
		FmlOps: []ir.FmlOp{
			// The preamble default: writes the request buffer.
			{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_default_val", Buffer: "ptr_fml_Ibuffer", Line: 5},
			// The endpoint's real response write.
			{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_answer_id", Buffer: "ptr_fml_Obuffer", Line: 20},
			// Other buffers and kinds must not slip in either.
			{Kind: ir.FmlAdd, Field: "FML_OPN_RT", Target: "d_debt_amt", Buffer: "ptr_fml_Rbuf", Line: 30},
			{Kind: ir.FmlGet, Field: "FML_USR_ID", Target: "c_user_id", Buffer: "ptr_fml_Ibuffer", Line: 2},
			// Dropped (session/error plumbing) and error adds stay out
			// regardless of buffer.
			{Kind: ir.FmlAdd, Field: "FML_ERR_MSG", Target: "c_errmsg", Buffer: "ptr_fml_Obuffer", Line: 8, Dropped: true},
			{Kind: ir.FmlAdd, Field: "FML_ERR_MSG", Target: "c_err_msg", Buffer: "ptr_fml_Obuffer", Line: 9, Error: true},
		},
	}

	got := detAdds(cond)
	if len(got) != 1 {
		t.Fatalf("detAdds returned %d op(s), want 1 (the Obuffer write); got %+v", len(got), got)
	}
	if got[0].Target != "sql_answer_id" {
		t.Errorf("detAdds kept target %q, want sql_answer_id", got[0].Target)
	}
	if got[0].Buffer != "ptr_fml_Obuffer" {
		t.Errorf("detAdds kept buffer %q, want ptr_fml_Obuffer", got[0].Buffer)
	}
}

// TestDetAddsKeepsEveryDistinctOutputWrite is the companion: widening the
// flow-side dedup key means the condition can now carry several writes of one
// field, and detAdds must pass all of them through rather than reducing them
// to one. Which of them is the response value is detRowPairs' decision; a
// filter that dropped them here would re-hide what the flow fix just
// surfaced.
func TestDetAddsKeepsEveryDistinctOutputWrite(t *testing.T) {
	cond := &ir.Condition{
		FmlOps: []ir.FmlOp{
			{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_arm_a", Buffer: "ptr_fml_Obuffer", Line: 20},
			{Kind: ir.FmlAdd, Field: "FML_POINT_TYPE", Target: "sql_arm_b", Buffer: "ptr_fml_Obuffer", Line: 40},
		},
	}
	if got := detAdds(cond); len(got) != 2 {
		t.Errorf("detAdds returned %d op(s), want 2 — every distinct output write is a fact; got %+v", len(got), got)
	}
}
