package cschk

import "testing"

// TestCheckIdentifierGate pins the identifier gate: a digit-leading
// declared name compiles nowhere — the gate catches it loudly at
// generation time (the real-corpus row shape sql_17dim_val produced
// `public string 17DIM_VAL` once).
func TestCheckIdentifierGate(t *testing.T) {
	src := `namespace Demo
{
    public class DemoDTO
    {
        public string _17DIM_VAL { get; set; } = string.Empty;
        public string MAR_FORM_NO { get; set; } = string.Empty;
    }
}`
	if issues := Check("DTO/DemoDTO.cs", src, "DemoDTO"); len(issues) != 0 {
		t.Errorf("valid identifiers flagged: %v", issues)
	}

	bad := `namespace Demo
{
    public class DemoDTO
    {
        public string 17DIM_VAL { get; set; } = string.Empty;
    }
}`
	issues := Check("DTO/DemoDTO.cs", bad, "DemoDTO")
	if len(issues) != 1 || issues[0].Kind != "type" || issues[0].Detail == "" {
		t.Errorf("digit-leading identifier not caught: %v", issues)
	}
}
