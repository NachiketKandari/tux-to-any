package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/config"
)

// The engine-wiring audit (docs/engine-wiring-audit.md Tier-1 #4) fixed the
// buffer-roles wiring: discover/discovercs extracted every file with an
// empty ir.Options — no built-in roles at all — so every FML buffer came
// out unknown-role, the error-add idiom never fired, and the response
// census inflated. This pins extractFlowIR to the same config seam
// extract/plan/convertgo use.
func TestExtractFlowIRThreadsBufferRoles(t *testing.T) {
	const src = `/* synthetic: FML buffer carrying the Ibuffer role suffix */
#include <atmi.h>
#include <fml32.h>
#include <sqlca.h>

char c_ServiceName[33];

void SVC_DEMO_BUFS(TPSVCINFO* rqst)
{
    FBFR32 *ptr_fml_Ibuffer;
    char c_flag;

    ptr_fml_Ibuffer = (FBFR32*)rqst->data;
    strcpy(c_ServiceName,rqst->name);
    Fget32(ptr_fml_Ibuffer,FML_MODE_FLG,0,(char*)&c_flag,0);

    tpreturn(TPSUCCESS,0L,0L,0L,0);
}
`
	path := filepath.Join(t.TempDir(), "svc_demo_bufs.pc")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	files, err := extractFlowIR(path, config.Default())
	if err != nil {
		t.Fatalf("extractFlowIR: %v", err)
	}
	if len(files) != 1 || len(files[0].Buffers) == 0 {
		t.Fatalf("no buffers extracted: %+v", files)
	}
	var role string
	for _, b := range files[0].Buffers {
		if strings.HasSuffix(strings.ToLower(b.Name), "ibuffer") {
			role = string(b.Role)
		}
	}
	if role != "input" {
		t.Errorf("buffer role = %q, want %q — the config registry must reach extraction", role, "input")
	}
}
