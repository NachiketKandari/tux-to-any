package ir

import (
	"path/filepath"
	"testing"
)

// testdata/svccall is the corpus the rest of testdata does not have: a
// service whose tpcall target is DEFINED in the same scanned directory.
//
// Every other fixture that issues a tpcall names a callee that is referenced
// but never defined (SVC_MIN_DETAIL, SVC_DEMO_DETAIL, SVC_ADV_TARGET). That
// is the right shape for pinning the placeholder path and the wrong shape for
// pinning a cross-call join: with no in-corpus callee anywhere, both halves of
// the contract never appear together and the positive path goes untested.
//
// This file pins the FIXTURE, not the join. It asserts only surface that
// extraction already produced before any join existed — the resolved callee
// file, the caller's folded buffer state at the call line, and the callee's
// own input/output traffic — so it keeps holding whatever consumes those
// facts. What the join concludes from them is the join's own business.

// svccallFixture is the fixture directory, relative to this package.
const svccallFixture = "../../testdata/svccall"

func svccallFiles(t *testing.T) (caller, callee *File) {
	t.Helper()
	files, err := ExtractDir(svccallFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		switch filepath.Base(f.Path) {
		case "SVC_SVC_CALLER.pc":
			caller = f
		case "SVC_SVC_CALLEE.pc":
			callee = f
		}
	}
	if caller == nil || callee == nil {
		t.Fatalf("svccall fixture incomplete: caller=%v callee=%v", caller != nil, callee != nil)
	}
	return caller, callee
}

// TestSvcCallFixtureResolvesItsCallee is the premise the fixture exists to
// establish, and the one that separates this corpus from every other: the
// tpcall target resolves to a file in the corpus, so a consumer has a callee
// contract to compare against instead of only a name.
func TestSvcCallFixtureResolvesItsCallee(t *testing.T) {
	caller, _ := svccallFiles(t)
	if len(caller.TPCalls) != 1 {
		t.Fatalf("caller has %d tpcalls, want 1", len(caller.TPCalls))
	}
	tp := caller.TPCalls[0]
	if tp.Service != "SVC_SVC_CALLEE" {
		t.Errorf("service = %q, want SVC_SVC_CALLEE", tp.Service)
	}
	if filepath.Base(tp.ServiceFile) != "SVC_SVC_CALLEE.pc" {
		t.Errorf("ServiceFile = %q, want the in-corpus callee", tp.ServiceFile)
	}
	// A single match, not a comma-joined list: the fixture must not double
	// as the ambiguity case.
	if want := filepath.Join(svccallFixture, "SVC_SVC_CALLEE.pc"); tp.ServiceFile != want {
		t.Errorf("ServiceFile = %q, want exactly %q", tp.ServiceFile, want)
	}
	if tp.Ambiguous {
		t.Error("the fixture's call is marked ambiguous — it is resolvable")
	}
}

// TestSvcCallFixtureHasBothHalvesOfTheContract pins that the corpus really
// carries a two-sided contract to join, and pins the fields on each side by
// name so a rename in either fixture shows up here rather than silently
// weakening the join's test coverage.
func TestSvcCallFixtureHasBothHalvesOfTheContract(t *testing.T) {
	caller, callee := svccallFiles(t)
	tp := caller.TPCalls[0]

	if got, want := fieldNames(tp.SendFields), []string{"FML_AMT", "FML_MODE_FLG", "FML_USER_ID"}; !equalStrings(got, want) {
		t.Errorf("caller send fields = %v, want %v", got, want)
	}
	if got, want := fieldNames(tp.RecvFields), []string{"FML_ACC_ID", "FML_LST_UPD"}; !equalStrings(got, want) {
		t.Errorf("caller recv fields = %v, want %v", got, want)
	}

	if callee.Entry != "SVC_SVC_CALLEE" {
		t.Fatalf("callee entry = %q, want SVC_SVC_CALLEE", callee.Entry)
	}
	in, out := map[string]bool{}, map[string]bool{}
	for _, b := range callee.Buffers {
		switch b.Role {
		case BufferInput:
			in[b.Name] = true
		case BufferOutput:
			out[b.Name] = true
		}
	}
	if len(in) == 0 || len(out) == 0 {
		t.Fatalf("callee buffers carry no input/output role: %+v", callee.Buffers)
	}
	var expects, produces []string
	record := func(ops []FmlOp) {
		for _, op := range ops {
			switch op.Kind {
			case FmlGet:
				if in[op.Buffer] {
					expects = append(expects, op.Field)
				}
			case FmlAdd:
				if out[op.Buffer] {
					produces = append(produces, op.Field)
				}
			}
		}
	}
	record(callee.FmlOps)
	for i := range callee.Conditions {
		record(callee.Conditions[i].FmlOps)
	}
	// FML_ERR_MSG is a dropped op — session/error plumbing the conversion
	// discards from a service's OWN contract. It is still in this list on
	// purpose: it genuinely crosses the call boundary, and a consumer that
	// filtered it out would hide a field the callee really does write.
	if got, want := expects, []string{"FML_USER_ID", "FML_AMT"}; !equalStrings(got, want) {
		t.Errorf("callee expects = %v, want %v", got, want)
	}
	if got, want := produces, []string{"FML_ACC_ID", "FML_ERR_MSG"}; !equalStrings(got, want) {
		t.Errorf("callee produces = %v, want %v", got, want)
	}
}

// TestSvcCallFixtureStraddlesTheContract pins the reason the fixture's two
// services do not agree: one field is sent that the callee never reads, and
// one is read back that the callee never writes. The second is the direction
// that can produce wrong behaviour rather than only wasted work, so a
// consumer that only checks one direction has not covered the hazard.
//
// A consumer that wants to assert this for real should ask the IR for its
// own conclusion rather than recomputing it here — this is the fixture's
// design intent, pinned at the level of the facts the conclusion is built on.
func TestSvcCallFixtureStraddlesTheContract(t *testing.T) {
	caller, callee := svccallFiles(t)
	sent := map[string]bool{}
	for _, f := range caller.TPCalls[0].SendFields {
		sent[f.Field] = true
	}
	read := map[string]bool{}
	for _, f := range caller.TPCalls[0].RecvFields {
		read[f.Field] = true
	}
	in, out := map[string]bool{}, map[string]bool{}
	for _, b := range callee.Buffers {
		switch b.Role {
		case BufferInput:
			in[b.Name] = true
		case BufferOutput:
			out[b.Name] = true
		}
	}
	wants, makes := map[string]bool{}, map[string]bool{}
	record := func(ops []FmlOp) {
		for _, op := range ops {
			switch op.Kind {
			case FmlGet:
				if in[op.Buffer] {
					wants[op.Field] = true
				}
			case FmlAdd:
				if out[op.Buffer] {
					makes[op.Field] = true
				}
			}
		}
	}
	record(callee.FmlOps)
	for i := range callee.Conditions {
		record(callee.Conditions[i].FmlOps)
	}

	// Sent but never read.
	if sent["FML_MODE_FLG"] {
		if wants["FML_MODE_FLG"] {
			t.Error("FML_MODE_FLG is now read by the callee — update the fixture")
		}
	} else {
		t.Error("the fixture no longer sends a field the callee ignores")
	}
	// Read back but never written: the hazard direction.
	if read["FML_LST_UPD"] {
		if makes["FML_LST_UPD"] {
			t.Error("FML_LST_UPD is now written by the callee — update the fixture")
		}
	} else {
		t.Error("the fixture no longer reads back a field the callee never writes")
	}
	// And the fields that DO line up, so the fixture keeps covering the
	// bound case and not only the gaps.
	if !sent["FML_AMT"] || !wants["FML_AMT"] {
		t.Error("FML_AMT no longer binds caller-to-callee")
	}
	if !read["FML_ACC_ID"] || !makes["FML_ACC_ID"] {
		t.Error("FML_ACC_ID no longer binds callee-to-caller")
	}
}

// TestOtherFixturesHaveNoInCorpusCallee is the other half of why svccall
// exists, and it is the assertion that keeps this fixture honest: if a
// fixture elsewhere grows an in-corpus callee, the "no resolvable callee
// anywhere" assumption behind the placeholder-path goldens no longer holds,
// and it should be said out loud rather than discovered later.
func TestOtherFixturesHaveNoInCorpusCallee(t *testing.T) {
	for _, dir := range []string{"stripped", "adversarial", "pf"} {
		files, err := ExtractDir(filepath.Join("../../testdata", dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			for i := range f.TPCalls {
				if f.TPCalls[i].ServiceFile != "" {
					t.Errorf("%s/%s resolves callee %q in-corpus — the placeholder-path "+
						"goldens for %s assume no tpcall target is ever defined in the corpus",
						dir, filepath.Base(f.Path), f.TPCalls[i].ServiceFile, dir)
				}
			}
		}
	}
}
