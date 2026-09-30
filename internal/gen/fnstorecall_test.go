package gen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
)

// storeCallStub builds the budget.DBCall detEmitFnStoreCall renders, matching
// the shape a rows-returning read gets: a receiver-qualified method and a
// context argument.
func storeCallStub(name, args string) budget.DBCall {
	return budget.DBCall{
		Receiver: "s.store",
		Name:     name,
		CtxName:  args,
		Args:     nil,
	}
}

// parseBody parses a whole emitted fragment inside a function body. The
// defect being guarded against is a malformed FINAL line — a statement fused
// onto the previous one — so checking the fragment as a unit is both the right
// check and the only one that works: a bare `if err != nil {` or `}` is
// structurally fine and does not parse in isolation.
func parseBody(t *testing.T, what, body string) {
	t.Helper()
	src := "package p\nfunc f() {\n" + body + "\n}\n"
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.AllErrors); err != nil {
		t.Errorf("%s: emitted fragment does not parse: %v\n%s", what, err, body)
	}
}

// TestFnHelperStoreCallTerminatesItsBlankCapture is the regression test for
// the defect P2 opened on.
//
// detEmitFnStoreCall ended its blank-identifier write with "\n_ = <capture>"
// — a LEADING newline and no trailing one. Every other statement in the
// emitter ended with a newline; this one did not. So any statement emitted
// after a rows-shaped store call was concatenated onto it:
//
//	_ = getUacUsrAccntsq63fnInsertIntoUraSt := s.FnInsertIntoUra(...)
//
// which does not parse.
//
// It survived because a helper whose store call happened to be LAST was
// unaffected — the missing newline only ever showed up as a malformed final
// line, which the surrounding trim swallowed. FnSaveRiskProfile has a store
// call followed by a nested helper call, so it fused; the method failed
// convert's gate, was dropped from fns.go, and tux.go kept calling a method
// that did not exist. The emitted package did not compile and nothing in the
// tree said why.
func TestFnHelperStoreCallTerminatesItsBlankCapture(t *testing.T) {
	for _, shape := range []string{"rows", "single", "scalar"} {
		dc := &detCall{
			line:    100,
			shape:   shape,
			capture: "getUacUsrAccntsq63",
			call:    storeCallStub("GetUacUsrAccntsq63", "c"),
		}
		var sb strings.Builder
		detEmitFnStoreCall(&sb, dc, nil, false, false, new(bool))
		got := sb.String()

		if !strings.HasSuffix(got, "\n") {
			t.Errorf("shape %s: emitter output does not end in a newline: %q", shape, got)
		}
		if !strings.Contains(got, "_ = getUacUsrAccntsq63\n") {
			t.Errorf("shape %s: blank capture is not newline-terminated:\n%q", shape, got)
		}
		parseBody(t, "shape "+shape, got)
	}
}

// TestFnHelperStoreCallFollowedByAnotherStatementIsTheRealCase reproduces the
// corpus shape directly: the emitter, then the next statement, into one
// buffer — which is how detEmitFnEvents composes them.
func TestFnHelperStoreCallFollowedByAnotherStatementIsTheRealCase(t *testing.T) {
	dc := &detCall{
		line:    100,
		shape:   "rows",
		capture: "getUacUsrAccntsq63",
		call:    storeCallStub("GetUacUsrAccntsq63", "c"),
	}
	var sb strings.Builder
	errDecl := false
	detEmitFnStoreCall(&sb, dc, nil, false, false, &errDecl)
	detEmitFnHelper(&sb, &detHelper{
		goName: "FnInsertIntoUra",
		cap:    "fnInsertIntoUraSt",
		args:   []string{`""`, "sql_ura_uniq_nmbr", `""`, `""`, `""`, "0"},
		retInt: true,
	}, nil, false, false)

	body := sb.String()
	if strings.Contains(body, "getUacUsrAccntsq63fnInsertIntoUraSt") {
		t.Fatalf("the two statements fused:\n%s", body)
	}
	parseBody(t, "store call then helper", body)

	// And the fused body as a whole must parse, since that is what convert's
	// gate checks.
	src := "package p\nfunc f() {\n" + body + "\n}\n"
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.AllErrors); err != nil {
		t.Errorf("fused body does not parse: %v\n%s", err, body)
	}
}

// TestFnHelperErrorShapedCallStillEndsCleanly is the paired check on the
// branch that does not emit a capture, so the newline contract is not
// satisfied by luck on one path and broken on the other.
func TestFnHelperErrorShapedCallStillEndsCleanly(t *testing.T) {
	dc := &detCall{
		line:  100,
		shape: "error",
		call:  storeCallStub("Exec", "c"),
	}
	var sb strings.Builder
	detEmitFnStoreCall(&sb, dc, nil, false, false, new(bool))
	got := sb.String()
	if !strings.HasSuffix(got, "\n}") && !strings.HasSuffix(got, "\n") {
		t.Errorf("error-shaped output does not end cleanly: %q", got)
	}
	parseBody(t, "error shape", got)
}
