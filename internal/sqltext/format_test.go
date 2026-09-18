package sqltext

import "testing"

func TestFormat(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"select list and conditions expand",
			"select a, b from t where x = :x and y = 'p, q' order by a desc",
			"select\n    a,\n    b\nfrom t\nwhere x = :x\n    and y = 'p, q'\norder by a desc",
		},
		{
			"distinct rides the head line",
			"select distinct a, b from t",
			"select distinct\n    a,\n    b\nfrom t",
		},
		{
			"insert values",
			"insert into t (a, b) values (:a, :b)",
			"insert into t (a, b)\nvalues (:a, :b)",
		},
		{
			"update set stays with update",
			"UPDATE T SET A = :x_a, B = :x_b WHERE C = :x_c AND D = :x_a",
			"UPDATE T\nSET A = :x_a, B = :x_b\nWHERE C = :x_c\n    AND D = :x_a",
		},
		{
			"delete from stays together",
			"delete from t where a = 1",
			"delete from t\nwhere a = 1",
		},
		{
			"between and never splits",
			"select a from t where a between :lo and :hi and b = 1",
			"select\n    a\nfrom t\nwhere a between :lo and :hi\n    and b = 1",
		},
		{
			"union starts a fresh select",
			"select a from t union all select b from u",
			"select\n    a\nfrom t\nunion all\nselect\n    b\nfrom u",
		},
		{
			"merge clauses",
			"MERGE INTO A a USING (SELECT :id AS \"ID\" FROM DUAL) s ON (a.ID = s.ID) WHEN MATCHED THEN UPDATE SET a.B = :b WHEN NOT MATCHED THEN INSERT (ID, B) VALUES (:id, :b)",
			"MERGE INTO A a\nUSING (SELECT :id AS \"ID\" FROM DUAL) s\nON (a.ID = s.ID)\nWHEN MATCHED THEN\nUPDATE SET a.B = :b\nWHEN NOT MATCHED THEN\nINSERT (ID, B)\nVALUES (:id, :b)",
		},
		{
			"case when stays inline",
			"SELECT CASE WHEN A = 1 THEN 'x' ELSE 'y' END AS C FROM T WHERE B = :b",
			"SELECT\n    CASE WHEN A = 1 THEN 'x' ELSE 'y' END AS C\nFROM T\nWHERE B = :b",
		},
		{
			"nested select stays inline",
			"SELECT A FROM T WHERE X IN (SELECT Y FROM U WHERE Z = 1)",
			"SELECT\n    A\nFROM T\nWHERE X IN (SELECT Y FROM U WHERE Z = 1)",
		},
		{
			"literals and quoted identifiers survive",
			"select 'a,b' as x, \"weird,col\" from t",
			"select\n    'a,b' as x,\n    \"weird,col\"\nfrom t",
		},
		{
			"hint comment preserved",
			"SELECT /*+ INDEX(T IDX) */ A FROM T",
			"SELECT\n    /*+ INDEX(T IDX) */ A\nFROM T",
		},
		{
			"line comment forces newline",
			"select a from t -- note",
			"select\n    a\nfrom t -- note",
		},
		{
			"value adjacency kept, whitespace collapsed",
			"select  a = 1   from   t",
			"select\n    a = 1\nfrom t",
		},
		{"empty", "", ""},
		{"blank", "   \n\t", ""},
	}
	for _, c := range cases {
		got := Format(c.in)
		if got != c.want {
			t.Errorf("%s: Format(%q) =\n%s\nwant:\n%s", c.name, c.in, got, c.want)
		}
		if again := Format(got); again != got {
			t.Errorf("%s: Format not idempotent:\n%s", c.name, again)
		}
	}
}
