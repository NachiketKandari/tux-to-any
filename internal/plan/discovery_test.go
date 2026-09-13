package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	scanner "tux-to-any/internal/tsscan"
)

const nestedSrc = `void SVC_D(TPSVCINFO *rqst) {
	if (flag == 'A') {
		if (Fget32(ibuf, FML_COMP_CD, 0, (char *)&comp, 0) == -1) {
			Fadd32(ibuf, FML_ERR_MSG, msg, 0);
			tpreturn(TPFAIL, 0L, ibuf, 0L, 0);
		}
		EXEC SQL INSERT INTO T VALUES (:comp);
		Fadd32(obuf, FML_A, (char *)&a, 0);
		if (cnt > 0) {
			if (Fget32(ibuf, FML_B, 0, (char *)&b, 0) == -1) {
				Fadd32(ibuf, FML_ERR_MSG, msg, 0);
				tpreturn(TPFAIL, 0L, ibuf, 0L, 0);
			}
			Fadd32(obuf, FML_B, (char *)&b, 0);
			Fadd32(obuf, FML_C, (char *)&c, 0);
		}
		Fadd32(obuf, FML_D, (char *)&d, 0);
	} else {
		tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
	}
}
`

func discoverFixture(t *testing.T) (*ir.File, string, *flow.Tree, []flow.Candidate) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_D.pc")
	if err := os.WriteFile(path, []byte(nestedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := scanner.ScanBytes([]byte(nestedSrc), path)
	if err != nil {
		t.Fatal(err)
	}
	tree := flow.Build([]byte(nestedSrc), facts, f.Entry, f)
	return f, nestedSrc, tree, flow.Discover(tree, f.Conditions)
}

func TestDiscoverNestedKeys(t *testing.T) {
	f, _, tree, candidates := discoverFixture(t)
	_ = f
	var keys []string
	for _, c := range candidates {
		keys = append(keys, c.Key)
	}
	if len(keys) != 2 || keys[0] != "c1" || keys[1] != "c1.1" {
		t.Fatalf("candidate keys = %v, want [c1 c1.1]", keys)
	}
	if !candidates[1].Redundant {
		t.Errorf("c1.1 must be marked redundant (subset of c1): %+v", candidates[1])
	}
	if candidates[0].Redundant {
		t.Errorf("c1 has no qualifying parent: %+v", candidates[0])
	}
	// Round trip: the reference resolves to the inner branch's span.
	synth, err := flow.ConditionFor(tree, f.Conditions, "c1.1")
	if err != nil {
		t.Fatal(err)
	}
	if synth.Index != 0 {
		t.Errorf("synthesized Index = %d, want 0", synth.Index)
	}
	hasErrFlag := false
	for _, op := range synth.FmlOps {
		if op.Error {
			hasErrFlag = true
		}
	}
	if !hasErrFlag {
		t.Error("synthesized condition carries no Error-flagged ops (the inner guard's err add)")
	}
}

func TestPlanBuildWithConditionRef(t *testing.T) {
	f, src, _, _ := discoverFixture(t)
	m := &Mapping{
		Service: "svcd",
		Module:  "app/pkg/services/svcd",
		Endpoints: []Endpoint{
			{ConditionRef: "c1.1", Name: "Nested", Route: "/nested"},
			{Condition: 2, Name: "Default", Route: "/default"},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	var ctrl, dbUnits int
	names := map[string]bool{}
	for _, u := range p.Units {
		switch u.Kind {
		case KindControllerMethod:
			ctrl++
			names[u.Name] = true
		case KindDBMethod:
			dbUnits++
		}
	}
	if ctrl != 2 || !names["Nested"] || !names["Default"] {
		t.Errorf("controller units = %d %v, want 2 with Nested present", ctrl, names)
	}
	if dbUnits != 0 {
		t.Errorf("db units = %d, want 0 (the INSERT belongs to c1, not the nested c1.1)", dbUnits)
	}
	// The INSERT (q1) belongs only to the unmapped c1 — a visible skip.
	found := false
	for _, s := range p.Skipped {
		if s.QueryID == "q1" {
			found = true
		}
	}
	if !found {
		t.Errorf("q1 not recorded as skipped: %+v", p.Skipped)
	}
}

func TestValidateConditionRefRules(t *testing.T) {
	base := func(e Endpoint) *Mapping {
		return &Mapping{
			Service:   "svcd",
			Module:    "app/pkg/services/svcd",
			Endpoints: []Endpoint{e},
		}
	}
	if err := base(Endpoint{Condition: 1, ConditionRef: "c1", Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("both condition and conditionRef must be rejected")
	}
	if err := base(Endpoint{Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("neither condition nor conditionRef must be rejected")
	}
	if err := (&Mapping{
		Service: "svcd", Module: "app/pkg/services/svcd",
		Endpoints: []Endpoint{
			{ConditionRef: "c1.1", Name: "X", Route: "/x"},
			{ConditionRef: "c1.1", Name: "Y", Route: "/y"},
		},
	}).Validate(); err == nil {
		t.Error("duplicate conditionRef must be rejected")
	}
	if err := base(Endpoint{ConditionRef: "c1", Name: "X", Route: "/x"}).Validate(); err != nil {
		t.Errorf("valid ref rejected: %v", err)
	}
}

func TestPlanBuildUnknownRefFails(t *testing.T) {
	f, src, _, _ := discoverFixture(t)
	m := &Mapping{
		Service: "svcd", Module: "app/pkg/services/svcd",
		Endpoints: []Endpoint{{ConditionRef: "c9", Name: "X", Route: "/x"}},
	}
	_, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err == nil || !strings.Contains(err.Error(), "inventory has") {
		t.Errorf("unknown ref must fail loudly, got: %v", err)
	}
}

// scenarioSrc is a normalize-chain dispatch entry for the scenarioRef
// schema/resolution tests (SCEN-5): alias trn_cd, domain P/A.
const scenarioSrc = `void SVC_S(TPSVCINFO *rqst) {
	char trn_cd;
	if (Fget32(ibuf, FML_TRANS_CD, 0, (char *)sql_trn_cd.arr, 0) == -1) {
		Fadd32(ibuf, FML_ERR_MSG, msg, 0);
		tpreturn(TPFAIL, 0L, ibuf, 0L, 0);
	}
	if (!(strcmp(sql_trn_cd.arr, "P")))
		trn_cd = 'P';
	if (!(strcmp(sql_trn_cd.arr, "A")))
		trn_cd = 'A';
	if (trn_cd == 'P') {
		Fadd32(obuf, FML_P_OUT, (char *)&p, 0);
	}
	if (trn_cd == 'A') {
		EXEC SQL INSERT INTO T VALUES (:a);
		Fadd32(obuf, FML_A_OUT, (char *)&a, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`

func scenarioFixture(t *testing.T) (*ir.File, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "SVC_S.pc")
	if err := os.WriteFile(path, []byte(scenarioSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f, scenarioSrc
}

// defaultArmSrc is a braced dispatch ladder with an else — the fixture for
// the default-arm scenarioRef round-trip and the arm-coverage advisory.
const defaultArmSrc = `void SVC_DARM(TPSVCINFO *rqst) {
	char c_flag;
	if (c_flag == 'H') {
		EXEC SQL INSERT INTO TH VALUES (:a);
		Fadd32(obuf, FML_H_OUT, (char *)&a, 0);
	} else if (c_flag == 'F') {
		Fadd32(obuf, FML_F_OUT, (char *)&b, 0);
	} else {
		EXEC SQL INSERT INTO TD VALUES (:c);
		Fadd32(obuf, FML_D_OUT, (char *)&c, 0);
	}
	tpreturn(TPSUCCESS, 0L, obuf, 0L, 0);
}
`

func defaultArmFixture(t *testing.T) (*ir.File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_DARM.pc")
	if err := os.WriteFile(path, []byte(defaultArmSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f, defaultArmSrc
}

// TestPlanDefaultScenarioRefAccepted pins the default arm's round trip:
// the else arm's slice is a legal scenarioRef value (plan accepts what the
// discovery draft emits), and the covered arm never warns.
func TestPlanDefaultScenarioRefAccepted(t *testing.T) {
	f, src := defaultArmFixture(t)
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{{ScenarioRef: "c_flag=default", Name: "DefaultList", Route: "/default"}},
	}
	p, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatalf("default scenarioRef rejected: %v", err)
	}
	for _, w := range p.Warnings {
		if strings.Contains(w, "else arm") {
			t.Errorf("the mapped else arm must not warn: %s", w)
		}
	}
	// The unmapped enumerated arms stay advisory-loud, with the default
	// slice as the suggested mapping shape.
	hinted := 0
	for _, w := range p.Warnings {
		if strings.Contains(w, "scenarioRef: c_flag=default") {
			hinted++
		}
	}
	if hinted == 0 {
		t.Errorf("unmapped arms must carry the default-slice hint, warnings = %v", p.Warnings)
	}
	// The default endpoint's controller unit resolves — one controller.
	ctrls := 0
	for _, u := range p.Units {
		if u.Kind == KindControllerMethod {
			ctrls++
		}
	}
	if ctrls != 1 {
		t.Errorf("controller units = %d, want 1 (the default slice)", ctrls)
	}
}

// TestPlanArmCoverageWarnsForUnmappedDefault pins the advisory that would
// have caught the silent else-arm omission: mapping only one arm leaves
// the else arm loud, naming the exact scenarioRef that maps it.
func TestPlanArmCoverageWarnsForUnmappedDefault(t *testing.T) {
	f, src := defaultArmFixture(t)
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{{ScenarioRef: "c_flag=H", Name: "Hist", Route: "/h"}},
	}
	p, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range p.Warnings {
		if strings.Contains(w, "else arm 3") && strings.Contains(w, "scenarioRef: c_flag=default") {
			found = true
		}
	}
	if !found {
		t.Errorf("unmapped else arm must warn with the mapping hint, warnings = %v", p.Warnings)
	}
}

// TestPlanDefaultRefRejectedWithoutElse pins the guard against inventing a
// default: an entry whose dispatch has no else arm accepts no default ref.
func TestPlanDefaultRefRejectedWithoutElse(t *testing.T) {
	f, src := scenarioFixture(t)
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{{ScenarioRef: "trn_cd=default", Name: "X", Route: "/x"}},
	}
	_, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err == nil || !strings.Contains(err.Error(), "detected trn_cd domain") {
		t.Errorf("default ref without an else arm must fail loudly, got: %v", err)
	}
}

func TestParseScenarioRef(t *testing.T) {
	k, v, err := ParseScenarioRef("trn_cd=A")
	if err != nil || k != "trn_cd" || v != "A" {
		t.Errorf("ParseScenarioRef = %q/%q/%v, want trn_cd/A/nil", k, v, err)
	}
	for _, bad := range []string{"", "=A", "trn_cd=", "trn_cd", "trn cd=A"} {
		if _, _, err := ParseScenarioRef(bad); err == nil {
			t.Errorf("ParseScenarioRef(%q) must fail", bad)
		}
	}
}

func TestMappingScenarioRefValidation(t *testing.T) {
	base := func(e Endpoint) *Mapping {
		return &Mapping{Service: "svcs", Module: "app/pkg/services/svcs", Endpoints: []Endpoint{e}}
	}
	if err := base(Endpoint{ScenarioRef: "trn_cd=A", Name: "X", Route: "/x"}).Validate(); err != nil {
		t.Errorf("valid scenarioRef rejected: %v", err)
	}
	if err := base(Endpoint{ScenarioRef: "bogus", Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("malformed scenarioRef must be rejected")
	}
	if err := base(Endpoint{ScenarioRef: "trn_cd=A", Condition: 1, Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("scenarioRef + condition must be rejected")
	}
	if err := base(Endpoint{ScenarioRef: "trn_cd=A", ConditionRef: "c1", Name: "X", Route: "/x"}).Validate(); err == nil {
		t.Error("scenarioRef + conditionRef must be rejected")
	}
	if err := (&Mapping{Service: "svcs", Endpoints: []Endpoint{
		{ScenarioRef: "trn_cd=A", Name: "X", Route: "/x"},
		{ScenarioRef: "trn_cd=A", Name: "Y", Route: "/y"},
	}}).Validate(); err == nil {
		t.Error("duplicate scenarioRef must be rejected")
	}
}

func TestPlanBuildScenarioRef(t *testing.T) {
	f, src := scenarioFixture(t)
	_ = f
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{
			{ScenarioRef: "trn_cd=A", Name: "TransA", Route: "/trans-a"},
			{ScenarioRef: "trn_cd=P", Name: "TransP", Route: "/trans-p"},
			{ScenarioRef: "trn_cd=X", Name: "TransX", Route: "/trans-x"}, // outside the domain
		},
	}
	_, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err == nil || !strings.Contains(err.Error(), "detected trn_cd domain") {
		t.Errorf("out-of-domain scenario value must fail loudly, got: %v", err)
	}

	m.Endpoints = m.Endpoints[:2]
	p, err := Build(Options{Main: f, Source: src, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	// TransA carries the INSERT; TransP has no queries — the skip ledger
	// holds nothing of TransA's, and TransA's db unit is tx (the insert is
	// not inside a begin→commit span here — wait, no span exists, so the
	// vote is false and the unit renders plain).
	var aQueries []string
	for _, u := range p.Units {
		if u.Kind == KindDBMethod {
			aQueries = append(aQueries, u.QueryIDs...)
		}
	}
	if len(aQueries) == 0 || aQueries[0] == "" {
		t.Errorf("TransA's insert query missing: %v", aQueries)
	}
	found := false
	for _, u := range p.Units {
		if u.Kind == KindDBMethod && u.Tx {
			found = true
		}
	}
	if found {
		t.Error("no begin→commit span exists — every DML unit must render plain (tx=false)")
	}
	// Controller units: one per scenario, source lines = the body extent.
	ctrls := 0
	for _, u := range p.Units {
		if u.Kind == KindControllerMethod {
			ctrls++
		}
	}
	if ctrls != 2 {
		t.Errorf("controller units = %d, want one per scenario", ctrls)
	}
}

func TestPlanScenarioTxVote(t *testing.T) {
	// A top-level begin→commit span wrapping a top-level DML insert rides
	// every scenario slice — both scenarios vote tx, the unit renders the
	// tx variant.
	srcTx := strings.Replace(scenarioSrc, "	if (trn_cd == 'P') {",
		"	i_ch_val = tpbegin(60, 0);\n	EXEC SQL INSERT INTO T VALUES (:b);\n	tpcommit(0);\n	if (trn_cd == 'P') {", 1)
	path := filepath.Join(t.TempDir(), "SVC_S.pc")
	if err := os.WriteFile(path, []byte(srcTx), 0o644); err != nil {
		t.Fatal(err)
	}
	ftx, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := &Mapping{
		Service: "svcs", Module: "app/pkg/services/svcs",
		Endpoints: []Endpoint{
			{ScenarioRef: "trn_cd=P", Name: "TransP", Route: "/trans-p"},
			{ScenarioRef: "trn_cd=A", Name: "TransA", Route: "/trans-a"},
		},
	}
	p, err := Build(Options{Main: ftx, Source: srcTx, Mapping: m, Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	txUnits := 0
	for _, u := range p.Units {
		if u.Kind == KindDBMethod && u.Tx {
			txUnits++
		}
	}
	if txUnits != 1 {
		t.Errorf("tx DML units = %d, want 1 (the P scenario's insert rides the tp span)", txUnits)
	}
}
