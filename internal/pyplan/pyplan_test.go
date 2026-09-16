package pyplan

import (
	"testing"

	"tux-to-any/internal/common"
)

func TestBindIdxOrdering(t *testing.T) {
	got := bindIdx([]string{"b", "a"}, []string{"a", "b"})
	if len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Errorf("bindIdx = %v, want [1 0]", got)
	}
}

func TestCamel(t *testing.T) {
	if got := common.CamelPy("bat_mf_demo_rt"); got != "BatMfDemoRt" {
		t.Errorf("Camel = %q", got)
	}
	if got := common.CamelPy("demo@weird_name"); got != "DemoWeirdName" {
		t.Errorf("Camel sanitization = %q", got)
	}
}

func TestBindOrder(t *testing.T) {
	sql := "UPDATE T SET A = :x_a, B = :x_b WHERE C = : x_c AND D = :x_a"
	got := bindOrder(sql)
	if len(got) != 3 || got[0] != "x_a" || got[1] != "x_b" || got[2] != "x_c" {
		t.Errorf("bindOrder = %v, want [x_a x_b x_c] (dedup + `: name` collapse)", got)
	}
}

// stripInto/collapseBinds moved to internal/sqltext (shared with the Go
// emitter); their table tests live there.
