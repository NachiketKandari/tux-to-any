package flow

import "testing"

func TestArmViewNeutral(t *testing.T) {
	tree := &Tree{Function: "fn", StartLine: 1, EndLine: 10, Root: []*Node{
		{Kind: KindSQL, Sub: "SELECT", Line: 2, EndLine: 2, QueryIDs: []string{"q1"}},
		{Kind: KindStmt, Line: 3, EndLine: 3, FmlOps: nil, Calls: []string{"tpreturn"}, Text: "tpreturn(TPSUCCESS, 0, d, 0, 0);"},
		{Kind: KindDecl, Line: 4, EndLine: 4},
	}}
	lines := ArmView(tree, 1, 10)
	if len(lines) == 0 {
		t.Fatalf("ArmView empty")
	}
	for _, l := range lines {
		switch l.Kind {
		case ViewQuery, ViewFMLOp, ViewTPCall, ViewReturn, ViewDropped, ViewCode, ViewPlaceholder:
		default:
			t.Errorf("unknown view kind %q", l.Kind)
		}
	}
	// No Go syntax may leak into the neutral view.
	for _, l := range lines {
		for _, banned := range []string{"s.store.", "return data, nil", "rows, err :="} {
			if len(l.Text) >= len(banned) && containsSubstr(l.Text, banned) {
				t.Errorf("Go leak in ArmView %q", l.Text)
			}
		}
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
