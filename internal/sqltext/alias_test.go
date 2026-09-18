package sqltext

import (
	"strings"
	"testing"
)

func TestSelectItemsSplit(t *testing.T) {
	sql := `SELECT MF_NAV_COMP_CD, NVL(MF_NAV_NAV, 0), TO_CHAR(MF_NAV_DATE, 'dd-mm-yyyy'), DECODE(NVL(MF_SCH_FREED_TYPE,''), 'I', 'INCOME, GENERATOR', '-') FROM MF_NAVS WHERE X = :x`
	items := SelectItems(sql)
	if len(items) != 4 {
		t.Fatalf("SelectItems = %q, want 4 items", items)
	}
	if items[0] != "MF_NAV_COMP_CD" {
		t.Errorf("item0 = %q", items[0])
	}
	if !strings.HasPrefix(items[1], "NVL(") {
		t.Errorf("item1 = %q", items[1])
	}
	if !strings.Contains(items[3], "'INCOME, GENERATOR'") {
		t.Errorf("comma inside string literal split the list: %q", items[3])
	}
}

func TestSelectItemsParensAndLiterals(t *testing.T) {
	sql := `select date('01-' || to_char(sysdate - 90, 'MM') || '-' || to_char(sysdate - 90, 'YYYY'), 'dd-mm-yyyy'), sysdate from dual`
	items := SelectItems(sql)
	if len(items) != 2 {
		t.Fatalf("SelectItems = %q, want 2 items", items)
	}
}

func TestIsComputedItem(t *testing.T) {
	cases := []struct {
		item string
		want bool
	}{
		{"MF_NAV_COMP_CD", false},
		{"t.col", false},
		{"*", false},
		{"t.*", false},
		{"NVL(MF_NAV_NAV, 0)", true},
		{"TO_CHAR(MF_NAV_DATE, 'dd-mm-yyyy')", true},
		{"DECODE(NVL(MF_SCH_FREED_TYPE,''), 'I', 'X', '-')", true},
		{"CASE WHEN A THEN 1 ELSE 0 END", true},
		{"'Y'", true},
		{"0", true},
		{"COUNT(*)", true},
		{"a || b", true},
		{"NVL(x, 0) AS FOO", false},
		{"NVL(x, 0) FOO", false},
		{"MF_NAV_COMP_CD AS FOO", false},
	}
	for _, c := range cases {
		if got := IsComputedItem(c.item); got != c.want {
			t.Errorf("IsComputedItem(%q) = %v, want %v", c.item, got, c.want)
		}
	}
}

func TestAliasForSanitizeAndCap(t *testing.T) {
	got := AliasFor("maintux", "q3", 4)
	if got != "TUXC_MAINTUX_Q3_4" {
		t.Errorf("AliasFor = %q", got)
	}
	fn := AliasFor("svc", "fn_gene_otp:q2", 1)
	if strings.ContainsAny(fn, ":") {
		t.Errorf("fn-namespaced alias not sanitized: %q", fn)
	}
	long := AliasFor("averylongservicename", "averylongunitidentifier", 12)
	if len(long) > 30 {
		t.Errorf("alias %q exceeds the 30-byte Oracle cap (%d)", long, len(long))
	}
	if again := AliasFor("averylongservicename", "averylongunitidentifier", 12); again != long {
		t.Errorf("AliasFor not deterministic: %q vs %q", long, again)
	}
}

func TestInjectAliasesIdempotent(t *testing.T) {
	sql := `SELECT MF_NAV_COMP_CD, NVL(MF_NAV_NAV, 0), TO_CHAR(MF_NAV_DATE, 'dd-mm-yyyy') FROM MF_NAVS WHERE X = :x`
	once := InjectAliases(sql, map[int]string{1: "TUXC_S_Q_2", 2: "TUXC_S_Q_3"})
	if !strings.Contains(once, "NVL(MF_NAV_NAV, 0) AS TUXC_S_Q_2") {
		t.Fatalf("alias not injected:\n%s", once)
	}
	if strings.Contains(once, "MF_NAV_COMP_CD AS") {
		t.Fatalf("bare column must not be aliased:\n%s", once)
	}
	twice := InjectAliases(once, map[int]string{1: "TUXC_S_Q_2", 2: "TUXC_S_Q_3"})
	if twice != once {
		t.Fatalf("injection not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestInjectAliasesNoopOnAliased(t *testing.T) {
	sql := `SELECT NVL(x, 0) AS ALREADY FROM T`
	got := InjectAliases(sql, map[int]string{0: "TUXC_S_Q_1"})
	if got != sql {
		t.Errorf("already-aliased item rewritten:\n%s", got)
	}
}
