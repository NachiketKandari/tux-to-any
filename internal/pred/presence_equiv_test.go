package pred

import "testing"

func TestEquivalentFoccurPresence(t *testing.T) {
	eq := func(a, b string) bool {
		pa, pb := ParseCode(a), ParseCode(b)
		return Equivalent(&pa, &pb, func(x, y string) bool { return IdentKey(x) == IdentKey(y) })
	}
	// Same field, any buffer → equivalent; Go presence form matches.
	if !eq("Foccur32(a, FML_X) > 0", "Foccur32(b, FML_X) > 0") {
		t.Error("buffer spelling should not matter")
	}
	if !eq("Foccur32(a, FML_FOO) > 0", `Foo != ""`) {
		t.Error("legacy present vs Go present should match")
	}
	if !eq("Foccur32(a, FML_FOO) == 0", `Foo == ""`) {
		t.Error("legacy absent vs Go absent should match")
	}
	if eq("Foccur32(a, FML_FOO) > 0", `Foo == ""`) {
		t.Error("inverted polarity matched")
	}
	if eq("Foccur32(a, FML_FOO) > 0", `Bar != ""`) {
		t.Error("different field matched")
	}
	if eq("Foccur32(a, FML_FOO) > 0", "cnt > 0") {
		t.Error("presence vs numeric matched")
	}
}
