package flow

import (
	"strings"
	"testing"

	scanner "tux-to-any/internal/tsscan"
)

// assignSrc exercises the accumulator shape the corpus actually uses: a total
// introduced at the top of the body, then updated inside a nested branch. The
// C is
//
//	double d_tot_amt = 0;
//	...
//	if(strcmp(code,"CSH")==0) { d_tot_amt = d_tot_amt + val; }
//
// so the second assignment to d_tot_amt lands in a NESTED Go scope once the
// branch renders. That is the case that silently shadowed.
const assignSrc = `
int svc_t(void)
{
  EXEC SQL
    SELECT TBL_A
    INTO   :c_val
    FROM   TBL_A
    WHERE  TBL_A_ID = :c_id;

  d_tot_amt = 0;
  d_tot_csh_lqd = 0;

  if(strcmp(c_cd,"CSH")==0)
  {
    d_tot_csh_lqd = d_tot_csh_lqd + c_val;
    d_tot_amt = d_tot_amt + c_val;
  }

  return 0;
}
`

func renderAssignBody(t *testing.T) string {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(assignSrc), "assign.pc")
	if err != nil {
		t.Fatal(err)
	}
	tree := Build([]byte(assignSrc), facts, "svc_t", nil)
	return RenderSpan(tree, nil, tree.StartLine, tree.EndLine, 1).Body
}

// TestAssignmentIntroducesOnceThenUsesEquals is the core invariant: exactly
// one `:=` per name, every later assignment `=`.
//
// The renderer elides the C declaration and lets the first assignment
// introduce the variable, so `:=` is right once. Emitting it every time was
// the bug.
func TestAssignmentIntroducesOnceThenUsesEquals(t *testing.T) {
	body := renderAssignBody(t)
	if !strings.Contains(body, "d_tot_amt := 0") {
		t.Errorf("the first assignment to d_tot_amt does not introduce it:\n%s", body)
	}
	// Inside the branch — the nested-scope case.
	if strings.Contains(body, "d_tot_amt := d_tot_amt") {
		t.Errorf("d_tot_amt is re-declared inside the branch; Go would shadow "+
			"the outer total and the accumulation would be silently discarded:\n%s", body)
	}
	if !strings.Contains(body, "d_tot_amt = d_tot_amt + c_val") {
		t.Errorf("the nested assignment to d_tot_amt is not a plain `=`:\n%s", body)
	}
	if !strings.Contains(body, "d_tot_csh_lqd = d_tot_csh_lqd + c_val") {
		t.Errorf("the nested assignment to d_tot_csh_lqd is not a plain `=`:\n%s", body)
	}
	// And the invariant stated as a count: no name may be introduced twice.
	introduced := map[string]int{}
	for _, ln := range strings.Split(body, "\n") {
		if i := strings.Index(ln, " := "); i > 0 {
			name := strings.TrimSpace(ln[:i])
			if isGoIdent(name) {
				introduced[name]++
			}
		}
	}
	for name, n := range introduced {
		if n > 1 {
			t.Errorf("%s is introduced %d times; every one after the first is a "+
				"redeclaration or a shadow:\n%s", name, n, body)
		}
	}
}

// TestAssignOpIsAlwaysEqualsForANonIdentifier: Go cannot declare through a
// deref, a field, or an index. `*out := 0` does not compile and `row.F := x`
// does not either, so those lhs forms must be assignments from the first time.
func TestAssignOpIsAlwaysEqualsForANonIdentifier(t *testing.T) {
	r := &renderer{introduced: map[string]bool{}}
	for _, lhs := range []string{
		"*sql_urf_debt_prsrv_asset_prcnt",
		"row.Field",
		"a[i]",
		"a->f",
		"(x)",
	} {
		if got := r.assignOp(lhs); got != "=" {
			t.Errorf("assignOp(%q) = %q, want \"=\" — Go cannot declare through it", lhs, got)
		}
		// Repeatedly: a deref assignment is never promoted to a declaration
		// no matter how many times it appears.
		if got := r.assignOp(lhs); got != "=" {
			t.Errorf("assignOp(%q) on the second call = %q, want \"=\"", lhs, got)
		}
	}
}

// TestAssignOpIntroducesEachNameExactlyOnce is the unit behind the invariant,
// and it checks the state actually mutates rather than trusting the caller.
func TestAssignOpIntroducesEachNameExactlyOnce(t *testing.T) {
	r := &renderer{introduced: map[string]bool{}}
	want := []string{":=", "=", "="}
	for i, w := range want {
		if got := r.assignOp("d_tot_amt"); got != w {
			t.Errorf("call %d: assignOp = %q, want %q", i+1, got, w)
		}
	}
	if !r.introduced["d_tot_amt"] {
		t.Error("assignOp did not record the name it introduced")
	}
	// A different name gets its own introduction.
	if got := r.assignOp("d_tot_debt"); got != ":=" {
		t.Errorf("a fresh name = %q, want \":=\"", got)
	}
	// Whitespace is tolerated, since splitAssign already trims but the
	// renderer should not depend on that.
	if got := r.assignOp("  spaced  "); got != ":=" {
		t.Errorf("untrimmed name = %q, want \":=\"", got)
	}
	if got := r.assignOp("  spaced  "); got != "=" {
		t.Errorf("untrimmed repeat = %q, want \"=\"", got)
	}
}

// TestFlatTrackingCannotReintroduceAcrossSiblings is why the set is flat
// rather than per-scope. Two sibling branches each assigning the same C
// variable must both be `=`; a per-scope set would have introduced it twice.
func TestFlatTrackingCannotReintroduceAcrossSiblings(t *testing.T) {
	r := &renderer{introduced: map[string]bool{}}
	r.assignOp("d_tot_amt") // first branch
	if got := r.assignOp("d_tot_amt"); got != "=" {
		t.Errorf("a sibling branch re-introduced the name: %q", got)
	}
	// The conservative direction: if two C scopes genuinely shadowed, the
	// flat set under-introduces and Go reports a compile error. That is the
	// acceptable failure. Silently shadowing is not.
}

// TestAssignOpOnAnEmptyLhsDoesNotPanic: splitAssign can return an empty lhs
// for a malformed statement, and the operator picker must not introduce "".
func TestAssignOpOnAnEmptyLhsDoesNotPanic(t *testing.T) {
	r := &renderer{introduced: map[string]bool{}}
	for _, lhs := range []string{"", "   ", "*", "."} {
		if got := r.assignOp(lhs); got != "=" {
			t.Errorf("assignOp(%q) = %q, want \"=\"", lhs, got)
		}
	}
	if r.introduced[""] {
		t.Error("an empty name was recorded as introduced")
	}
}
