package flow

import (
	"testing"

	"tux-to-any/internal/ir"
)

// The error-emission rule is field+value based, never buffer based: the
// reply is frequently the request buffer reused in place, so a role-based
// rule (adds into input/send buffers = errors) swallowed genuine response
// writes (FML_VLME into the reused input buffer, risk.pc). Error-ness is
// the FML_ERR field or the error-message value written (c_errmsg); every
// other add is a response write — something is returned either way.
func TestErrorAddClassificationIsFieldAndValueBased(t *testing.T) {
	errField := ir.FmlOp{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_ERR_MSG", Target: "c_errmsg"}
	if !isErrorAdd(errField) {
		t.Error("an ERR-field add must classify as an error emission")
	}
	errValue := ir.FmlOp{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_STATLIN", Target: "c_errmsg"}
	if !isErrorAdd(errValue) {
		t.Error("a c_errmsg-valued add must classify as an error emission even on a non-ERR field")
	}
	valueCase := ir.FmlOp{Kind: ir.FmlAdd, Buffer: "ptr_fml_Sbuffer", Field: "FML_ERR_TXT", Target: "C_Err_Msg"}
	if !isErrorAdd(valueCase) {
		t.Error("the value rule matches case-insensitively, underscores flattened")
	}
	response := ir.FmlOp{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_VLME", Target: "&i_return_val"}
	if isErrorAdd(response) {
		t.Error("a plain add into the reused input buffer is a response write, not an error emission")
	}
	get := ir.FmlOp{Kind: ir.FmlGet, Field: "FML_ERR_CODE", Target: "c_err"}
	if isErrorAdd(get) {
		t.Error("gets never qualify as error emissions")
	}
}

func TestFanoutKeepsResponseAddsRegardlessOfBufferRole(t *testing.T) {
	fanout := fanoutAdds(&Node{
		FmlOps: []ir.FmlOp{
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_A", Target: "c_a"},
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_B", Target: "c_b"},
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Ibuffer", Field: "FML_ERR_MSG", Target: "c_errmsg"},
			{Kind: ir.FmlAdd, Buffer: "ptr_fml_Sbuffer", Field: "FML_C", Target: "c_msg"},
		},
		BufRoles: map[string]string{"ptr_fml_Ibuffer": "input", "ptr_fml_Sbuffer": "send"},
	})
	if len(fanout) != 1 {
		t.Fatalf("fanout buffers = %v, want only ptr_fml_Ibuffer", fanout)
	}
	fields := fanout["ptr_fml_Ibuffer"]
	if len(fields) != 2 || fields[0] != "FML_A" || fields[1] != "FML_B" {
		t.Errorf("fanout fields = %v, want [FML_A FML_B] — the error emission excluded, role ignored", fields)
	}
}
