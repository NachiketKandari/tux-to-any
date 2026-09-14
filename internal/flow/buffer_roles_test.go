package flow

import (
	"testing"

	"tux-to-any/internal/ir"
)

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #4) pinned
// the buffer-role exclusion: adds into input/send-role buffers are request
// plumbing, never response-mapping candidates — but only when the roles
// actually reach the flow node (the wiring bug: discover extracted with an
// empty registry, so every buffer was unknown-role and the exclusion never
// fired, inflating the response census).
func TestBufferRoleExcludesRequestAddsFromResponseMapping(t *testing.T) {
	// BufRoles keys are the raw extracted buffer names (flow.Build maps
	// irFile.Buffers verbatim); the role VALUE is what the audit's wiring
	// bug zeroed out ("unknown-role" for every buffer).
	withRole := &Node{BufRoles: map[string]string{"ptr_fml_Ibuffer": "input"}}
	inputAdd := ir.FmlOp{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_MODE_FLG"}
	if !isErrorAdd(inputAdd, withRole) {
		t.Error("add into an input-role buffer must classify as request plumbing, not a response write")
	}
	if isErrorAdd(inputAdd, &Node{}) {
		t.Error("unknown-role buffer stays conservative — a plain add is a response write until proven otherwise")
	}
	if isErrorAdd(ir.FmlOp{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_X"}, &Node{BufRoles: map[string]string{"ptr_fml_Ibuffer": "unknown-role"}}) {
		t.Error("an unknown-ROLE buffer (the audit's extraction output) must stay conservative")
	}

	fanout := fanoutAdds(&Node{
		FmlOps: []ir.FmlOp{
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_A"},
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_B"},
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Obuffer", Field: "FML_C"},
		},
		BufRoles: map[string]string{"ptr_fml_Ibuffer": "input", "ptr_fml_Obuffer": "output"},
	})
	if len(fanout) != 0 {
		t.Errorf("input-role adds must not surface as response-mapping fanout, got %v", fanout)
	}
}
