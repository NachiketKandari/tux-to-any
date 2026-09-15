package gen

import (
	"strings"
	"testing"
)

func TestStripInto(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"simple select", "select a, b into :h1, :h2 from t where x = :x", "select a, b from t where x = :x"},
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
		if got := stripInto(c.in); got != c.want {
			t.Errorf("%s: stripInto(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestDBMethodsSQLDropsInto pins BP-8 at the gen render site: the INTO
// host-var list must never reach the emitted Go query string (the driver
// scans by column name; the IR goldens still pin INTO inside q.SQL).
func TestDBMethodsSQLDropsInto(t *testing.T) {
	s, p, _ := genNavFixture(t)
	file, err := s.DBMethodsFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"into :", "INTO :"} {
		if strings.Contains(file, banned) {
			t.Errorf("db methods file emits %q:\n%.400s", banned, file)
		}
	}
	// The stripped statement keeps its FROM clause and the scan target.
	if !strings.Contains(file, "from dual") {
		t.Error("db methods file lost the FROM clause while stripping INTO")
	}
	if !strings.Contains(file, "GetContext(c, &dateInfo, query)") {
		t.Error("db methods file lost the GetContext scan call")
	}
}
