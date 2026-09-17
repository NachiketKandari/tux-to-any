package sqltext

import (
	"reflect"
	"testing"
)

func TestCanonicalSQL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"select into stripped", "select a, b into :h1, :h2 from t where x = :x", "select a, b from t where x = :x"},
		{"spaced binds collapse", "SELECT A INTO : h FROM T WHERE A = : bind_a;", "SELECT A FROM T WHERE A = :bind_a"},
		{"insert into kept", "insert into t (a) values (:a);", "insert into t (a) values (:a)"},
		{"semicolon trimmed", "select a from t;", "select a from t"},
		{"idempotent", "select a from t where x = :x", "select a from t where x = :x"},
	}
	for _, c := range cases {
		if got := CanonicalSQL(c.in); got != c.want {
			t.Errorf("%s: CanonicalSQL(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
		if again := CanonicalSQL(CanonicalSQL(c.in)); again != CanonicalSQL(c.in) {
			t.Errorf("%s: CanonicalSQL not idempotent: %q", c.name, again)
		}
	}
}

func TestExecutableBinds(t *testing.T) {
	got := ExecutableBinds("select a from t where x = :x_a and y = : x_b and z = 'p: q' and w = :1")
	want := []string{"x_a", "x_b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExecutableBinds = %v, want %v", got, want)
	}
	if got := ExecutableBinds("select 1 from dual"); len(got) != 0 {
		t.Errorf("ExecutableBinds(no binds) = %v, want empty", got)
	}
}
