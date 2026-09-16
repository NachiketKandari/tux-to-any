package sqltext

import "testing"

func TestStripInto(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"simple select", "select a, b into :h1, :h2 from t where x = :x", "select a, b from t where x = :x"},
		{"spaced binds", "SELECT A, B INTO : h_a, : h_b FROM DEMO_T WHERE A = : bind_a", "SELECT A, B FROM DEMO_T WHERE A = : bind_a"},
		{"multi-line", "select date('01-02-2020','dd-mm-yyyy'),sysdate\n into :c_from,:c_to\n from dual", "select date('01-02-2020','dd-mm-yyyy'),sysdate\n from dual"},
		{"insert into kept", "insert into t (a) values (:a)", "insert into t (a) values (:a)"},
		{"into literal not a list", "select 'into x' from t", "select 'into x' from t"},
		{"literal from inside into list", "select a into :h1 from t where b = 'from z'", "select a from t where b = 'from z'"},
		{"paren from inside list", "select f(a, (select x from y)) into :h from t", "select f(a, (select x from y)) from t"},
		{"no into passthrough", "select a from t", "select a from t"},
		{"count into", "SELECT COUNT(*) INTO :cnt FROM DEMO", "SELECT COUNT(*) FROM DEMO"},
		{"identifier into not a keyword", "select point_into_a from t", "select point_into_a from t"},
	}
	for _, c := range cases {
		if got := StripInto(c.in); got != c.want {
			t.Errorf("%s: StripInto(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestCollapseBinds(t *testing.T) {
	got := CollapseBinds("UPDATE T SET A = : x_a WHERE B =:x_b AND C = 'x: y'")
	want := "UPDATE T SET A = :x_a WHERE B =:x_b AND C = 'x: y'"
	if got != want {
		t.Errorf("CollapseBinds = %q, want %q", got, want)
	}
}
