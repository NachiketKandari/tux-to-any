package csgen

import (
	"strings"
	"testing"

	"tux-to-any/internal/csplan"
	"tux-to-any/internal/pred"
)

// csGuards is the S7 twin fixture: the merged I/F guards with the legacy and
// request-field spellings bound.
func csGuards() []csplan.Guard {
	bind := map[string]string{
		pred.IdentKey("cFlag"):       "c_flag",
		pred.IdentKey("CFlag"):       "c_flag",
		pred.IdentKey("MfGrowthFlg"): "c_flag",
	}
	return []csplan.Guard{
		{Line: 352, Cond: "c_flag == 'F'", Bind: bind},
		{Line: 581, Cond: "c_flag == 'I'", Bind: bind},
	}
}

// TestGuardErrsCSRetention pins the accept shapes: both guards with camel,
// Pascal, or request-field spellings, and && reordering.
func TestGuardErrsCSRetention(t *testing.T) {
	guards := csGuards()
	cases := []struct {
		name string
		body string
	}{
		{"camel", "if (cFlag == \"F\")\n{\n    x = 1;\n}\nif (cFlag == \"I\")\n{\n    x = 2;\n}\n"},
		{"pascal", "if (CFlag == 'F')\n{\n    x = 1;\n}\nif (CFlag == 'I')\n{\n    x = 2;\n}\n"},
		{"request field", "if (request.MfGrowthFlg == \"F\")\n{\n    x = 1;\n}\nif (request.MfGrowthFlg == \"I\")\n{\n    x = 2;\n}\n"},
		{"else if chain", "if (cFlag == \"F\")\n{\n    x = 1;\n}\nelse if (cFlag == \"I\")\n{\n    x = 2;\n}\n"},
	}
	for _, tc := range cases {
		if errs := guardErrs(guards, tc.body); len(errs) != 0 {
			t.Errorf("%s: rejected a retaining block: %v", tc.name, errs)
		}
	}
}

// TestGuardErrsCSLost is the S7 dual in unit form: the block keeps the I
// guard and runs the F arm unconditionally — a hard gate error, not a
// silent behavior change.
func TestGuardErrsCSLost(t *testing.T) {
	errs := guardErrs(csGuards(), "if (cFlag == \"I\")\n{\n    x = 2;\n}\n")
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly the lost F guard", errs)
	}
	got := errs[0]
	for _, want := range []string{"runtime dispatch guard lost", "line 352", "c_flag == 'F'"} {
		if !strings.Contains(got, want) {
			t.Errorf("guard error missing %q: %s", want, got)
		}
	}
}

// TestGuardErrsCSIdentity pins the mutation rejects: unrelated variables,
// inversion, widening, narrowing, and comment-only guards never pass.
func TestGuardErrsCSIdentity(t *testing.T) {
	guards := csGuards()
	cases := []struct {
		name string
		body string
	}{
		{"unrelated variable", "if (other == \"F\")\n{\n    x = 1;\n}\nif (cFlag == \"I\")\n{\n    x = 2;\n}\n"},
		{"inverted", "if (!(cFlag == \"F\"))\n{\n    x = 1;\n}\nif (!(cFlag == \"I\"))\n{\n    x = 2;\n}\n"},
		{"weakened or", "if (cFlag == \"F\" || cFlag == \"X\")\n{\n    x = 1;\n}\nif (cFlag == \"I\")\n{\n    x = 2;\n}\n"},
		{"narrowed and", "if (cFlag == \"F\" && cFlag == \"I\")\n{\n    x = 1;\n}\n"},
		{"commented out", "// if (cFlag == \"F\") { x = 1; }\n/* if (cFlag == \"I\") { x = 2; } */\n"},
	}
	for _, tc := range cases {
		if errs := guardErrs(guards, tc.body); len(errs) == 0 {
			t.Errorf("%s: accepted — want a reject", tc.name)
		}
	}
}

// TestGuardErrsCSDegrades pins the no-op shape: no guards never errors.
func TestGuardErrsCSDegrades(t *testing.T) {
	if errs := guardErrs(nil, "if (x == 1)\n{\n}\n"); errs != nil {
		t.Errorf("no guards must be a no-op, got %v", errs)
	}
}

// TestSeamPromptCarriesGuards pins the prompt twin: a guarded arm's seam
// prompt carries the runtime-dispatch contract, an unguarded arm stays
// byte-clean of it.
func TestSeamPromptCarriesGuards(t *testing.T) {
	ep := EpData{
		Name: "HistOrFull", Route: "hist-full", DTOName: "CustF", RetType: "CustFDto",
		ReturnExpr: "response", Span: "40-120",
		Scenario: "c_flag in {F,I}", Filter: "c_flag == 'F' || c_flag == 'I'",
		Guards:   csGuards(),
		LineSpan: [2]int{40, 120},
	}
	p := &csplan.Plan{Component: "CustComponent", Service: "CustService"}
	prompt := userPrompt(p, ep, fileData{RepoField: "Repo", Service: "CustService"}, "src", nil)
	want := "Runtime dispatch guards — keep every one live as an if condition in the residual block: L352: c_flag == 'F'; L581: c_flag == 'I'"
	if !strings.Contains(prompt, want) {
		t.Errorf("seam prompt missing %q\ngot:\n%s", want, prompt)
	}
	ep.Guards = nil
	prompt = userPrompt(p, ep, fileData{RepoField: "Repo", Service: "CustService"}, "src", nil)
	if strings.Contains(prompt, "Runtime dispatch guards") {
		t.Errorf("unguarded arm must not carry the guard contract, got:\n%s", prompt)
	}
}
