package flow

import (
	"testing"

	"tux-to-any/internal/pred"
	scanner "tux-to-any/internal/tsscan"
)

const censusSrc = `void SVC_CENSUS(TPSVCINFO *rqst) {
	int cnt_d2u;
	int i_cnt_d2us;
	char c_enable_d2u_flg;
	char c_d2u_active_flg;
	cnt_d2u = 0;
	i_cnt_d2us = 0;
	c_enable_d2u_flg = 'N';
	if (c_d2u_active_flg == 'Y') {
		i_cnt_d2us = 1;
	}
	if (cnt_d2u > 0 || i_cnt_d2us > 0) {
		c_enable_d2u_flg = 'Y';
	} else {
		c_enable_d2u_flg = 'N';
	}
	if (DEBUG_MSG_LVL_3) {
		userlog("cnt %d", cnt_d2u);
	}
	if (SQLCODE != 0) {
		tpreturn(TPFAIL, 0L, (char *)rqst->data, 0L, 0);
	}
}
`

func censusTree(t *testing.T) *Tree {
	t.Helper()
	facts, err := scanner.ScanBytes([]byte(censusSrc), "census.pc")
	if err != nil {
		t.Fatal(err)
	}
	return Build([]byte(censusSrc), facts, "SVC_CENSUS", nil)
}

// TestConditionCensusOrArm pins the lost-OR-arm class: the merged condition
// and its effect survive into the census with a rename-robust skeleton.
func TestConditionCensusOrArm(t *testing.T) {
	tree := censusTree(t)
	conds := ConditionCensus(tree, 0, 1<<30)
	var orArm *CensusCond
	for i := range conds {
		if len(conds[i].Idents) == 2 {
			orArm = &conds[i]
		}
	}
	if orArm == nil {
		t.Fatalf("OR-arm condition missing from census: %+v", conds)
	}
	if orArm.Skeleton != "# > 0 || # > 0" {
		t.Errorf("skeleton = %q, want %q", orArm.Skeleton, "# > 0 || # > 0")
	}
	found := false
	for _, e := range orArm.Effects {
		if e == "c_enable_d2u_flg" {
			found = true
		}
	}
	if !found {
		t.Errorf("effects = %v, want c_enable_d2u_flg", orArm.Effects)
	}
	if !orArm.HasElse {
		t.Error("OR arm has an else sibling — HasElse must be true")
	}
	// The flag condition carries its own effect.
	var flag *CensusCond
	for i := range conds {
		for _, id := range conds[i].Idents {
			if id == "c_d2u_active_flg" {
				flag = &conds[i]
			}
		}
	}
	if flag == nil {
		t.Fatalf("flag condition missing: %+v", conds)
	}
	found = false
	for _, e := range flag.Effects {
		if e == "i_cnt_d2us" {
			found = true
		}
	}
	if !found {
		t.Errorf("flag effects = %v, want i_cnt_d2us", flag.Effects)
	}
}

// TestConditionCensusExclusions pins the elision mirror: debug-only and
// SQLCODE plumbing never enter the census.
func TestConditionCensusExclusions(t *testing.T) {
	tree := censusTree(t)
	conds := ConditionCensus(tree, 0, 1<<30)
	for _, c := range conds {
		for _, id := range c.Idents {
			if id == "SQLCODE" || id == "DEBUG_MSG_LVL_3" {
				t.Errorf("plumbing condition leaked into census: %+v", c)
			}
		}
	}
	if len(conds) != 2 {
		t.Errorf("census = %d conditions, want 2 (flag + OR arm)", len(conds))
	}
}

// TestSkeletonRenameRobust pins the canonical form: renames collapse,
// operators and literals survive.
func TestSkeletonRenameRobust(t *testing.T) {
	cases := []struct {
		cond string
		want string
	}{
		{"cnt_d2u > 0 || i_cnt_d2us > 0", "# > 0 || # > 0"},
		{"c_d2u_active_flg == 'Y'", `# == "Y"`},
		{"a && b || c", "# && # || #"},
	}
	for _, c := range cases {
		e := pred.Parse(c.cond)
		if got := Skeleton(&e); got != c.want {
			t.Errorf("Skeleton(%q) = %q, want %q", c.cond, got, c.want)
		}
	}
}
