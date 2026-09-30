package ir

import (
	"path/filepath"
	"sort"
	"testing"

	"tux-to-any/internal/tsscan"
)

// Guards for a failure mode this package has produced twice: a fact that is
// DECLARED, READ by a downstream emitter as a guard, and never POPULATED —
// so the guard is dead code that reads like a decision.
//
//   - FmlOp.Error is read by gen/deterministic.go:391,420 and never set here.
//   - An `if` nested inside another `if` is recorded by tsscan and never
//     reaches File.Conditions, because chainsOf admits only Depth == 1.
//
// Neither crashes anything, and neither is visible in the output, so neither
// shows up as a failing test. These two tests are the floor under that: they
// pass today, and they FAIL if the debt grows or a new dead flag appears.
//
// What they deliberately do NOT do is pretend the debt is zero. Both are
// ratchets over a measured baseline, and the baselines are spelled out below
// so the shortfall is visible in the source instead of inferable only by
// reading the code.

// corpusDirs are the tracked fixture directories, scanned the way the tool
// scans a client corpus. testdata/svccall is included: it is the only fixture
// with an in-corpus tpcall target, so it is the only one that exercises the
// callee side of the cross-call join.
var corpusDirs = []string{"fixtures/stripped", "fixtures/adversarial", "fixtures/pf", "fixtures/nav", "fixtures/merge", "svccall"}

func corpusFiles(t *testing.T) []*File {
	t.Helper()
	var out []*File
	for _, d := range corpusDirs {
		files, err := ExtractDir(filepath.Join("../../testdata", d))
		if err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		out = append(out, files...)
	}
	if len(out) == 0 {
		t.Fatal("no corpus files extracted — the guard would pass vacuously")
	}
	return out
}

// TestNoDeadFactFlags is the general guard: every boolean FML fact that a
// consumer reads as a guard must be reachable from the corpus, or be listed
// below with a reason it cannot be.
//
// The allowlist is the point. An empty allowlist would be a test that fails
// for one known reason; a populated one forces a human decision per dead
// flag, and records the reason where the next person will find it. It also
// fails when an entry goes STALE, so fixing a flag cannot leave the debt
// sitting in the list forever.
func TestNoDeadFactFlags(t *testing.T) {
	type flag struct {
		name string
		set  func(FmlOp) bool
		setT func(TPField) bool
	}
	flags := []flag{
		{"FmlOp.Optional", func(o FmlOp) bool { return o.Optional }, nil},
		{"FmlOp.Dropped", func(o FmlOp) bool { return o.Dropped }, nil},
		{"FmlOp.Error", func(o FmlOp) bool { return o.Error }, nil},
		{"FmlOp.Composite", func(o FmlOp) bool { return o.Composite }, nil},
		{"TPField.Optional", nil, func(f TPField) bool { return f.Optional }},
		{"TPField.Unchecked", nil, func(f TPField) bool { return f.Unchecked }},
		{"TPField.Composite", nil, func(f TPField) bool { return f.Composite }},
	}

	// unpopulatedFactFlags: a declared guard no corpus op currently sets.
	// Each entry is a KNOWN GAP, with the reason it is still empty and what
	// would close it. Adding a field here is a decision to accept the debt.
	unpopulatedFactFlags := map[string]string{
		"TPField.Optional": "srwindow.go copies Optional from the op, so this can only be set " +
			"once FmlOp.Optional is. That needs an FNOTPRES-guarded read, and " +
			"fnotpresGuarded (fml.go:174) fires only when the guard's own body mentions the " +
			"FNOTPRES macro — the idiom `if(Fget32(...)==FNOTPRES)`. The corpus writes the " +
			"literal `== -1` and never mentions FNOTPRES, so no op qualifies. THIRD instance " +
			"of the same shape and the most misleading of them: SVC_MIN_KITCHEN line 42 reads " +
			"`if(Fget32(ptr_fml_Ibuffer,FML_USER_ID,0,c_user_id,0) == -1)` and returns TPFAIL in " +
			"the body, which is an FNOTPRES guard to any human reader — and the IR records " +
			"Optional=false for it. Closed by recognising the `== -1` spelling, which is what " +
			"every fixture in the corpus actually uses.",
		"FmlOp.Error": "ir never sets it; FmlOpOf has no classifier for the value side. " +
			"The shared rule spelled out in flow and contract is " +
			"IsErrField(field) || (kind==FmlAdd && IsErrValue(target)), and only the field " +
			"half is implemented. An earlier attempt to add it put error:true on 47 corpus " +
			"ops and was reverted because the emission delta could not be established. " +
			"Closed by implementing the classifier behind a test that pins detAdds output.",
	}

	populated := map[string]int{}
	tally := func(name string, n int) { populated[name] += n }
	for _, f := range corpusFiles(t) {
		ops := append([]FmlOp{}, f.FmlOps...)
		for i := range f.Conditions {
			ops = append(ops, f.Conditions[i].FmlOps...)
		}
		for _, op := range ops {
			for _, fl := range flags {
				if fl.set != nil && fl.set(op) {
					tally(fl.name, 1)
				}
			}
		}
		tps := [][]TPField{}
		for i := range f.TPCalls {
			tps = append(tps, f.TPCalls[i].SendFields, f.TPCalls[i].RecvFields)
			if f.TPCalls[i].Callee != nil {
				tps = append(tps, f.TPCalls[i].Callee.Expects, f.TPCalls[i].Callee.Produces)
			}
		}
		for _, list := range tps {
			for _, tf := range list {
				for _, fl := range flags {
					if fl.setT != nil && fl.setT(tf) {
						tally(fl.name, 1)
					}
				}
			}
		}
	}

	for _, fl := range flags {
		n, reached := populated[fl.name]
		_, allowed := unpopulatedFactFlags[fl.name]
		switch {
		case !reached && allowed:
			// Known and accepted. Say so, so the debt is visible in test output.
			t.Logf("known gap: %s is never set by the corpus (accepted)", fl.name)
		case !reached && !allowed:
			t.Errorf("%s is a guard no corpus op ever sets — dead code that reads like a "+
				"decision. Either populate it, or add it to unpopulatedFactFlags with the "+
				"reason it is empty and what would close it.", fl.name)
		case reached && allowed:
			t.Errorf("%s is allowlisted as never-set but the corpus now sets it %d times — "+
				"stale entry. Remove it from unpopulatedFactFlags: the gap is closed.", fl.name, n)
		}
	}
	// An allowlist key that is not a real flag is a typo, and a typo would
	// silently disable the guard for that field forever.
	for name := range unpopulatedFactFlags {
		known := false
		for _, fl := range flags {
			if fl.name == name {
				known = true
			}
		}
		if !known {
			t.Errorf("unpopulatedFactFlags names %q, which is not a flag this test checks; "+
				"it is a typo and disables the guard for that field", name)
		}
	}
}

// droppedNestedIfs is the measured baseline for TestNoUnacknowledgedNestedIfs.
//
// Each entry is a corpus file whose entry function contains `if` statements
// nested two or more levels deep. chainsOf admits only Depth == 1, so every
// one of them is recorded by tsscan and absent from File.Conditions — which
// means plan and gen never emit its control flow. 88 branches in total, 65 of
// them in SVC_DEMO_LIST alone.
//
// This is a MEASUREMENT, not an endorsement. The lines are the honest current
// state of a gap that silently discards error handling. The
// `if(tpcall(...) == -1) { userlog(...); }` guard in SVC_MIN_KITCHEN is one of
// the five counted under SVC_MIN_KITCHEN.pc, and its userlog does not appear
// anywhere in the generated Go. SVC_SVC_CALLER.pc — the cross-call join fixture
// — carries the same shape: its tpcall guard is nested inside `if(c_flag=='D')`,
// so it is dropped as well, and the join records the call's data contract
// without recording that the caller guards its failure at all.
//
// Closing this means modelling nesting, which changes emitted control flow for
// every consumer of File.Conditions.
var droppedNestedIfs = map[string]int{
	"ADV_DEEP_NEST.pc":    8,
	"ADV_TPCALL_NOFML.pc": 1,
	"SVC_DEMO_LIST.pc":    65,
	"SVC_DEMO_MERGE.pc":   2,
	"SVC_MIN_CURSOR.pc":   2,
	"SVC_MIN_DML.pc":      3,
	"SVC_MIN_KITCHEN.pc":  5,
	"SVC_SVC_CALLER.pc":   1,
	"SVC_TP_DEMO.pc":      1,
}

// TestNoUnacknowledgedNestedIfs is the branch-shaped guard: every nested `if`
// in an entry function is either represented in File.Conditions or counted in
// the table above.
//
// It fails in both directions, on purpose. A file whose count grew has a new
// silently-dropped branch and the table needs a decision. A file whose count
// shrank means someone modelled nesting, which is good news, and the table
// should be updated so it stops claiming a gap that no longer exists.
func TestNoUnacknowledgedNestedIfs(t *testing.T) {
	const wantTotal = 88

	got := map[string]int{}
	total := 0
	for _, f := range corpusFiles(t) {
		if f.Entry == "" {
			continue // not a service: its branches are not service conditions by design
		}
		facts, err := tsscan.ScanFile(f.Path)
		if err != nil {
			t.Fatalf("%s: %v", f.Path, err)
		}
		for _, b := range facts.Branches {
			if b.Function == f.Entry && b.Kind == tsscan.BranchIf && b.Depth >= 2 {
				got[filepath.Base(f.Path)]++
			}
		}
	}

	names := map[string]bool{}
	for n := range got {
		names[n] = true
	}
	for n := range droppedNestedIfs {
		names[n] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	for _, name := range ordered {
		g := got[name]
		w, expected := droppedNestedIfs[name]
		switch {
		case !expected && g > 0:
			t.Errorf("%s has %d nested if(s) that File.Conditions does not represent, and "+
				"the file is not in droppedNestedIfs — a silently dropped branch. Add it to the "+
				"table, or model the nesting.", name, g)
		case expected && g == 0:
			t.Errorf("%s is in droppedNestedIfs for %d nested if(s) but now has none — the "+
				"gap was closed. Remove it from the table so it stops claiming a debt that "+
				"does not exist.", name, w)
		case expected && g != w:
			t.Errorf("%s nested if count moved %d -> %d. If branches are now represented, "+
				"lower the table entry; if new ones are being dropped, decide whether to "+
				"accept them.", name, w, g)
		}
		total += g
	}

	if total != wantTotal {
		t.Errorf("corpus-wide dropped nested ifs = %d, baseline says %d. Update "+
			"droppedNestedIfs and its comment with the new measurement and the reason.",
			total, wantTotal)
	}
}

// TestTpcallErrorGuardsAreDroppedNotMisreported pins the concrete consequence
// that motivated this file, so the general guards above cannot be satisfied by
// quietly changing what "a condition" means.
//
// SVC_MIN_KITCHEN guards its tpcall with `== -1` and logs on failure. That
// guard is a nested if, so it is not a Condition, so the userlog never reaches
// the generated Go. This asserts the CURRENT behaviour — including that the
// guard is absent — because a test that fails on today's output would only
// record that the gap exists without saying what it costs. When nesting is
// modelled this test is expected to FAIL, and that failure is the signal.
func TestTpcallErrorGuardsAreDroppedNotMisreported(t *testing.T) {
	files, err := ExtractDir("../../testdata/fixtures/stripped")
	if err != nil {
		t.Fatal(err)
	}
	var kitchen *File
	for _, f := range files {
		if f.Entry == "SVC_MIN_KITCHEN" {
			kitchen = f
		}
	}
	if kitchen == nil {
		t.Fatal("SVC_MIN_KITCHEN not extracted")
	}
	if len(kitchen.TPCalls) != 1 {
		t.Fatalf("want 1 tpcall, got %d", len(kitchen.TPCalls))
	}

	// The tpcall itself is recorded, with its buffers — that half works.
	tp := kitchen.TPCalls[0]
	if tp.Service != "SVC_MIN_DETAIL" {
		t.Errorf("service = %q", tp.Service)
	}
	// But nothing on TPCall says whether the return value is checked, and no
	// Condition covers the guard. This is the gap: an inliner cannot tell a
	// caller that logs from one that propagates from one that ignores.
	for _, c := range kitchen.Conditions {
		if containsToken(c.Expr, "tpcall") {
			t.Errorf("condition at line %d now covers the tpcall guard (%q) — if this test "+
				"fails, the guard is represented, which is the fix this file is waiting for. "+
				"Then add the return-check fact to TPCall and update this test.", c.StartLine, c.Expr)
		}
	}
}

// containsToken reports whether an identifier-shaped token appears in text.
func containsToken(text, token string) bool {
	for i := 0; i+len(token) <= len(text); i++ {
		if text[i:i+len(token)] != token {
			continue
		}
		if i > 0 && isIdentByteFor(text[i-1]) {
			continue
		}
		if i+len(token) < len(text) && isIdentByteFor(text[i+len(token)]) {
			continue
		}
		return true
	}
	return false
}
