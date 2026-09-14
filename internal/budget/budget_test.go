package budget

import (
	"errors"
	"testing"
)

func TestCount(t *testing.T) {
	b := New(12000, 4000, 4)
	if got := b.Count(""); got != 0 {
		t.Errorf("Count(\"\") = %d, want 0", got)
	}
	if got := b.Count("abcd"); got != 1 {
		t.Errorf("Count(4 chars) = %d, want 1", got)
	}
	if got := b.Count("abcde"); got != 2 {
		t.Errorf("Count(5 chars) = %d, want 2 (rounded up)", got)
	}
	if got := New(12000, 4000, 0).CharsPerToken; got != 4 {
		t.Errorf("non-positive ratio must fall back to 4, got %d", got)
	}
}

func TestCheckCeilings(t *testing.T) {
	b := New(10, 5, 4)
	if err := b.CheckInput("abcd"); err != nil {
		t.Errorf("within prompt ceiling: %v", err)
	}
	var over *ErrOverBudget
	err := b.CheckInput("abcdefghijk") // 11 chars → 3 tokens > 10? no: 3 <= 10
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = b.CheckInput(makeStr(45)) // 45 chars → 12 tokens > 10
	if !errors.As(err, &over) || over.Section != "prompt" || over.Have != 12 || over.Limit != 10 {
		t.Errorf("prompt over-budget = %v, want prompt 12>10", err)
	}
	err = b.CheckOutput(makeStr(25)) // 25 chars → 7 tokens > 5
	if !errors.As(err, &over) || over.Section != "output" {
		t.Errorf("output over-budget = %v, want output section", err)
	}
}

func makeStr(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func TestDBCallLine(t *testing.T) {
	c := DBCall{Receiver: "store", Name: "GetNavHistory", CtxName: "c",
		Args: []string{"compCd", "schCd", "fromDate", "toDate"}}
	if got, want := c.Line(), "store.GetNavHistory(c, compCd, schCd, fromDate, toDate)"; got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
	bare := DBCall{Name: "GetDateDetails", CtxName: "c"}
	if got, want := bare.Line(), "GetDateDetails(c)"; got != want {
		t.Errorf("bare Line() = %q, want %q", got, want)
	}
}

// TestOutputCeiling pins the ceiling policy: static passes MaxOutputTokens
// through (0 = no ceiling); dynamic derives min(context − input − reserve,
// modelMaxOutput) with a floor of 1.
func TestOutputCeiling(t *testing.T) {
	st := New(12000, 4000, 4)
	if got := st.OutputCeiling(500); got != 4000 {
		t.Errorf("static ceiling = %d, want MaxOutputTokens 4000", got)
	}
	if got := New(0, 0, 4).OutputCeiling(500); got != 0 {
		t.Errorf("static unset ceiling = %d, want 0 (no ceiling)", got)
	}
	dyn := New(12000, 4000, 4)
	dyn.ModelContextTokens = 40960
	dyn.ModelMaxOutputTokens = 16384
	dyn.OutputReserveTokens = 768
	if !dyn.Dynamic() {
		t.Error("dynamic() must report true when the context is set")
	}
	if got := dyn.OutputCeiling(4726); got != 16384 {
		t.Errorf("dynamic room 35466 clamped = %d, want 16384", got)
	}
	if got := dyn.OutputCeiling(40200); got != 1 {
		t.Errorf("collapsed room floored = %d, want 1", got)
	}
	b2 := New(0, 0, 4)
	b2.ModelContextTokens = 1000
	b2.OutputReserveTokens = 100
	if got := b2.OutputCeiling(100); got != 800 {
		t.Errorf("unclamped room = %d, want 800", got)
	}
}

// TestCheckOutputCap pins the explicit-ceiling variant the dynamic seam
// consumes.
func TestCheckOutputCap(t *testing.T) {
	b := New(0, 0, 4)
	if err := b.CheckOutputCap(makeStr(20), 6); err != nil { // 5 tokens ≤ 6
		t.Errorf("within cap: %v", err)
	}
	var over *ErrOverBudget
	err := b.CheckOutputCap(makeStr(25), 6) // 7 tokens > 6
	if !errors.As(err, &over) || over.Have != 7 || over.Limit != 6 {
		t.Errorf("over-cap = %v, want output 7>6", err)
	}
}
