package pred

import "testing"

func TestPresenceOf(t *testing.T) {
	cases := []struct {
		cond    string
		field   string
		present bool
		ok      bool
	}{
		{"Foccur32(fml_ibuffer, FML_FML_THING) > 0", "FML_FML_THING", true, true},
		{"foccur32(fml_ibuffer, FML_FML_THING) > 0", "FML_FML_THING", true, true},
		{"Foccur(buf, FML_X) != 0", "FML_X", true, true},
		{"Foccur32(buf, FML_X) >= 1", "FML_X", true, true},
		{"Foccur32(buf, FML_X) == 0", "FML_X", false, true},
		{"Foccur32(buf, FML_X) <= 0", "FML_X", false, true},
		{"Foccur32(buf, FML_X) < 1", "FML_X", false, true},
		{"Foccur32(buf, FML_X)", "FML_X", true, true},
		{"!Foccur32(buf, FML_X)", "FML_X", false, true},
		{"!(Foccur32(buf, FML_X) > 0)", "FML_X", false, true},
		{"0 < Foccur32(buf, FML_X)", "FML_X", true, true},
		{"0 == Foccur32(buf, FML_X)", "FML_X", false, true},
		// Exact-count arithmetic is not existence.
		{"Foccur32(buf, FML_X) == 1", "", false, false},
		{"Foccur32(buf, FML_X) > 1", "", false, false},
		{"Foccur32(buf) > 0", "", false, false},
		{"cnt > 0", "", false, false},
	}
	for _, tc := range cases {
		e := Parse(tc.cond)
		f, p, ok := PresenceOf(&e)
		if f != tc.field || p != tc.present || ok != tc.ok {
			t.Errorf("PresenceOf(%q) = (%q,%v,%v), want (%q,%v,%v)", tc.cond, f, p, ok, tc.field, tc.present, tc.ok)
		}
	}
}

func TestGoPresenceOf(t *testing.T) {
	a := ParseCode(`request.FmlThing != ""`)
	if id, p, ok := GoPresenceOf(&a); !ok || !p || id != "FmlThing" {
		t.Errorf("GoPresenceOf present = (%q,%v,%v)", id, p, ok)
	}
	b := ParseCode(`FmlThing == ""`)
	if id, p, ok := GoPresenceOf(&b); !ok || p || id != "FmlThing" {
		t.Errorf("GoPresenceOf absent = (%q,%v,%v)", id, p, ok)
	}
	c := ParseCode(`cnt > 0`)
	if _, _, ok := GoPresenceOf(&c); ok {
		t.Error("non-presence matched")
	}
}

func TestFoccurFields(t *testing.T) {
	e := Parse("Foccur32(a, FML_X) > 0 && Foccur(b, FML_Y) == 0")
	got := FoccurFields(&e)
	if len(got) != 2 || got[0] != "FML_X" || got[1] != "FML_Y" {
		t.Errorf("FoccurFields = %v", got)
	}
}
