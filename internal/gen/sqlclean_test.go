package gen

import (
	"strings"
	"testing"
)

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
