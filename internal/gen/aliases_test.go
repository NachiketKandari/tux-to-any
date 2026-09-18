package gen

import (
	"strings"
	"testing"

	"tux-to-any/internal/ir"
	"tux-to-any/internal/sqltext"
)

// TestColumnAliasesComputed pins the A4/A5 contract end to end on a
// corpus-style query: computed items gain a deterministic alias in the SQL
// and the same name in the row db tag, while the Go field name stays
// host-var-derived and bare columns are untouched.
func TestColumnAliasesComputed(t *testing.T) {
	s, p, _ := genNavFixture(t)
	var histUnit *struct {
		id string
	}
	_ = histUnit
	_ = p
	q := s.Query("cur_demo_hist")
	if q == nil {
		t.Fatal("cur_demo_hist missing")
	}
	aliases, warn := s.columnAliases("cur_demo_hist", q)
	if warn != "" {
		t.Fatalf("unexpected alignment warning: %s", warn)
	}
	if len(aliases) == 0 {
		t.Fatal("no aliases computed for a query with TO_CHAR")
	}
	items := sqltext.SelectItems(sqltext.CanonicalSQL(q.SQL))
	for pos := range aliases {
		if !sqltext.IsComputedItem(items[pos]) && !isOracleBare(items[pos]) {
			t.Errorf("position %d (%q) aliased but not computed", pos, items[pos])
		}
	}
	fields, err := s.rowFields("cur_demo_hist", q)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != len(q.RowShape) {
		t.Fatalf("fields = %d, want %d", len(fields), len(q.RowShape))
	}
	for pos, alias := range aliases {
		if fields[pos].DBTag != alias {
			t.Errorf("pos %d DBTag = %q, want alias %q", pos, fields[pos].DBTag, alias)
		}
	}
	// Go field names stay host-var-derived (StrDesc-style stability).
	for i, hv := range q.RowShape {
		base := hv
		if j := strings.LastIndex(base, "."); j >= 0 {
			base = base[j+1:]
		}
		if j := strings.IndexAny(base, " \t"); j > 0 {
			base = base[:j]
		}
		base = strings.TrimPrefix(base, "sql_")
		if !strings.EqualFold(fields[i].Name, exportOf(base)) {
			t.Errorf("pos %d field name = %q, want host-var-derived %q", i, fields[i].Name, base)
		}
	}
	// The emitted SQL carries the same aliases.
	var unit *struct{ ID string }
	_ = unit
	for i := range p.Units {
		u := &p.Units[i]
		if len(u.QueryIDs) > 0 && u.QueryIDs[0] == "cur_demo_hist" {
			body, _, _, err := s.DBMethod(*u)
			if err != nil {
				t.Fatal(err)
			}
			for _, alias := range aliases {
				if !strings.Contains(body, "AS "+alias) {
					t.Errorf("db body missing AS %s\n%s", alias, body)
				}
			}
		}
	}
}

func isOracleBare(item string) bool {
	t := strings.TrimSpace(item)
	if j := strings.LastIndex(t, "."); j >= 0 {
		t = t[j+1:]
	}
	return oracleBareFuncs[strings.ToUpper(t)]
}

func exportOf(snake string) string {
	parts := strings.Split(snake, "_")
	var sb strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return sb.String()
}

// TestColumnAliasesDedupShared pins the single-alias-set rule: a duplicate
// query resolves to its canonical unit's aliases.
func TestColumnAliasesDedupShared(t *testing.T) {
	s, _, _ := genNavFixture(t)
	q := s.Query("cur_demo_hist")
	if q == nil {
		t.Fatal("cur_demo_hist missing")
	}
	dup := *q
	dup.ID = "q5"
	dup.DuplicateOf = q.ID
	a1, _ := s.columnAliases("cur_demo_hist", q)
	a2, _ := s.columnAliases("q5", &dup)
	if len(a1) == 0 || len(a2) == 0 {
		t.Fatal("expected aliases on both sides of the dedup")
	}
	for pos, alias := range a1 {
		if a2[pos] != alias {
			t.Errorf("pos %d canonical %q vs duplicate %q", pos, alias, a2[pos])
		}
	}
}

// TestColumnAliasesFnNamespaced pins sanitization of fn-namespaced query
// ids (`fn_gene_otp:q2` carries a colon).
func TestColumnAliasesFnNamespaced(t *testing.T) {
	s, _, _ := genNavFixture(t)
	q := &ir.Query{
		ID:       "q2",
		Type:     ir.QuerySelectMulti,
		SQL:      "SELECT NVL(A, 0) INTO :h FROM T",
		RowShape: []string{"h"},
	}
	aliases, warn := s.columnAliases("fn_gene_otp:q2", q)
	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	if len(aliases) != 1 {
		t.Fatalf("aliases = %v, want one", aliases)
	}
	for _, a := range aliases {
		if strings.ContainsAny(a, ":") || len(a) > 30 {
			t.Errorf("alias %q not sanitized/capped", a)
		}
	}
}

// TestColumnAliasesTruncation pins the 30-byte Oracle cap with a
// deterministic hash suffix.
func TestColumnAliasesTruncation(t *testing.T) {
	s, _, _ := genNavFixture(t)
	q := &ir.Query{
		ID:       "averylongunitidentifier",
		Type:     ir.QuerySelectMulti,
		SQL:      "SELECT NVL(A, 0) INTO :h FROM T",
		RowShape: []string{"h"},
	}
	s.Mapping.Service = "averylongservicename"
	aliases, _ := s.columnAliases("averylongunitidentifier", q)
	for _, a := range aliases {
		if len(a) > 30 {
			t.Errorf("alias %q exceeds 30 bytes", a)
		}
	}
	again, _ := s.columnAliases("averylongunitidentifier", q)
	for pos, a := range aliases {
		if again[pos] != a {
			t.Errorf("alias not deterministic: %q vs %q", a, again[pos])
		}
	}
}

// TestColumnAliasesAlignmentGuard pins the loud skip: a select/INTO count
// mismatch aliases nothing and warns instead of silently misaligning.
func TestColumnAliasesAlignmentGuard(t *testing.T) {
	s, _, _ := genNavFixture(t)
	q := &ir.Query{
		ID:       "qx",
		Type:     ir.QuerySelectMulti,
		SQL:      "SELECT A, B FROM T",
		RowShape: []string{"only_one"},
	}
	aliases, warn := s.columnAliases("qx", q)
	if len(aliases) != 0 {
		t.Errorf("misaligned query aliased: %v", aliases)
	}
	if warn == "" {
		t.Error("misaligned query skipped silently — want a loud warning")
	}
}
