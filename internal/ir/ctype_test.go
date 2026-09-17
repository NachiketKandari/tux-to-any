package ir

import "testing"

func TestCanonicalCType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"char", "char"},
		{"VARCHAR", "varchar"},
		{"varchar2", "varchar"},
		{"unsigned int", "int"},
		{"integer", "int"},
		{"long", "long"},
		{"long int", "long"},
		{"short int", "short"},
		{"double", "double"},
		{"", ""},
		{"mytime_t", "mytime_t"},
	}
	for _, c := range cases {
		if got := CanonicalCType(c.in); got != c.want {
			t.Errorf("CanonicalCType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeprecatedShimsStillWork(t *testing.T) {
	// The deprecated IR shims must keep working while backends migrate:
	// TemplateID delegates to the same ids gen.TemplateFor uses for tx=true.
	if got := QuerySelectSingle.TemplateID(); got != TemplateSelectSingle {
		t.Errorf("TemplateID() = %q, want %q", got, TemplateSelectSingle)
	}
}
