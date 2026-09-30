package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tux-to-any/internal/budget"
	"tux-to-any/internal/flow"
	"tux-to-any/internal/ir"
	"tux-to-any/internal/plan"
)

// fnCfSource is a C service whose helper exercises every branch shape the
// control-flow accounting has to reach a verdict on:
//
//   - a debug-logging if, elidable by a named flow rule
//   - a SQL statement with the SQLCODE check after it, covered by the store
//     call the emitter renders
//   - a real conditional that guards a helper call, which is neither
//   - an index loop with no hint at all
//   - a do-while, whose hint describes a rendering rather than a
//     disappearance
const fnCfSource = `
int svc_thing(void)
{
  EXEC SQL
    SELECT TBL_A
    INTO   :c_val
    FROM   TBL_A
    WHERE  TBL_A_ID = :c_id;
  return 0;
}

int fn_do_work(char* c_ServiceName, char* c_id, char* c_flag)
{
  char c_val[9];
  int i_loop;

  if(DEBUG_MSG_LVL_3)
  {
    userlog("val = %s", c_val);
  }

  EXEC SQL
    UPDATE TBL_A
    SET    TBL_A_VAL = :c_val
    WHERE  TBL_A_ID = :c_id;
  if(SQLCODE != 0)
  {
    errlog(c_ServiceName,"E1",SQLMSG, DEF_USR, DEF_SSSN,c_val);
    return (-1);
  }

  if(c_flag == 'A')
  {
    i_loop = fn_do_work(c_ServiceName, c_id, c_flag);
  }

  for(i_loop = 0; i_loop < 4; i_loop++)
  {
    userlog("i = %d", i_loop);
  }

  do
  {
    i_loop++;
  } while (i_loop < 2);

  return 0;
}
`

// fnCfService builds a Service and its Plan over fnCfSource.
func fnCfService(t *testing.T) (*Service, *plan.Plan) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SVC_THING.pc")
	if err := os.WriteFile(path, []byte(fnCfSource), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ir.ExtractFileOpts(path, ir.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := &plan.Mapping{Service: "thing", Module: "app/thing"}
	p, err := plan.Build(plan.Options{Main: f, Source: fnCfSource, Mapping: m,
		Budget: budget.New(12000, 4000, 4)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(Options{Plan: p, Main: f, Source: fnCfSource})
	if err != nil {
		t.Fatal(err)
	}
	return svc, p
}

// helperNamed finds the plan's fn-helper record by C name.
func helperNamed(t *testing.T, p *plan.Plan, name string) *plan.FnHelper {
	t.Helper()
	for i := range p.FnHelpers {
		if p.FnHelpers[i].Name == name {
			return &p.FnHelpers[i]
		}
	}
	t.Fatalf("no fn helper %q in the plan", name)
	return nil
}

// TestElidableHintsExcludeRenderingsIsTheLoadBearingOne is why elidableHints
// is a hand-listed set rather than "every hint flow.Match returns".
//
// Two hints describe work that still has to happen: a fetch loop becomes a
// range over rows, and a do-while becomes a for with a trailing break. If
// either counted as elidable, an emitter that has not implemented either
// would silently pass — the hint would license exactly the gap this code
// exists to report. The test fails if someone widens the set casually.
func TestElidableHintsExcludeRenderings(t *testing.T) {
	for _, k := range []flow.HintKind{flow.HintFetchIterate, flow.HintDoWhile} {
		if elidableHints[k] {
			t.Errorf("%s describes a rendering, not a disappearance — treating it "+
				"as elidable would license a gap nothing fills", k)
		}
	}
	for _, k := range []flow.HintKind{
		flow.HintDebugIf, flow.HintErrOpLoop, flow.HintOccDecode, flow.HintRequestGuard,
	} {
		if !elidableHints[k] {
			t.Errorf("%s collapses in Go and should be elidable, but is not in the set", k)
		}
	}
}

// TestHelperFlowTreeFindsTheHelperNotJustTheEntry is the seam this work added.
// flow.Build always accepted a function name; nothing called it with one for
// a helper, so the entry's tree was the only tree in the system.
func TestHelperFlowTreeFindsTheHelperNotJustTheEntry(t *testing.T) {
	svc, _ := fnCfService(t)
	tree := svc.helperFlowTree("fn_do_work")
	if tree == nil {
		t.Fatal("no flow tree for fn_do_work")
	}
	if tree.Function != "fn_do_work" {
		t.Errorf("tree is for %q, want fn_do_work", tree.Function)
	}
	if len(tree.Root) == 0 {
		t.Error("helper tree has no root nodes — it resolved to an empty body")
	}
	// An unknown name is a miss, not a guess at some other function.
	if got := svc.helperFlowTree("fn_not_here"); got != nil && len(got.Root) > 0 {
		t.Errorf("an unknown helper name produced a populated tree: %d root nodes", len(got.Root))
	}
}

// TestControlFlowAccountedForEveryConstruct is the core claim: the walk visits
// every branch and loop in the helper's body and reaches a verdict on each.
// The fixture has five of them, and the verdicts must differ — a test that
// only checked the count would pass if everything fell into one bucket.
func TestControlFlowAccountedForEveryConstruct(t *testing.T) {
	svc, p := fnCfService(t)
	h := helperNamed(t, p, "fn_do_work")
	rep := svc.detFnControlFlow(h, []*detCall{{line: sqlGuardProbeLine(t, svc), method: "UpdateTblA"}}, nil)

	if rep.total == 0 {
		t.Fatal("no control flow found in a helper that has five constructs")
	}
	verdicts := map[string]int{}
	for _, it := range rep.items {
		if it.verdict == "" {
			t.Errorf("construct at line %d has no verdict", it.line)
		}
		verdicts[it.verdict]++
	}
	if rep.rendered+rep.elided+rep.missing != rep.total {
		t.Errorf("tallies do not sum: %d rendered + %d elided + %d missing != %d total",
			rep.rendered, rep.elided, rep.missing, rep.total)
	}
	// The SQLCODE check is covered by the store call's own error check.
	if rep.rendered == 0 {
		t.Errorf("nothing marked rendered; the SQLCODE guard should be: %v", verdicts)
	}
	// The debug if is elided by a named rule.
	if rep.elided == 0 {
		t.Errorf("nothing marked elided; the debug branch should be: %v", verdicts)
	}
	// The real conditional and the plain loop are neither.
	if rep.missing == 0 {
		t.Errorf("nothing marked missing; the c_flag branch and the loop should be: %v", verdicts)
	}
}

// TestSQLCodeGuardNeedsAStoreCallToBeCovered is the precision that keeps the
// SQLCODE exception honest. Covered means "the emitter's error check already
// is this branch" — which is only true when a store call actually rendered
// for the SQL statement in front of it. With no store call in the accounting,
// a SQLCODE branch has no Go error check behind it and must stay a gap.
func TestSQLCodeGuardNeedsAStoreCallToBeCovered(t *testing.T) {
	svc, p := fnCfService(t)
	h := helperNamed(t, p, "fn_do_work")

	without := svc.detFnControlFlow(h, nil, nil)
	if without.rendered != 0 {
		t.Errorf("%d constructs claimed rendered with no store calls at all", without.rendered)
	}

	// A store call rendered on the UPDATE's own line turns the SQLCODE guard
	// that follows it into the covered case.
	dc := &detCall{line: sqlGuardProbeLine(t, svc), method: "UpdateTblA"}
	with := svc.detFnControlFlow(h, []*detCall{dc}, nil)
	if with.rendered == 0 {
		t.Errorf("a SQLCODE guard directly after a rendered store call is still a gap: %v",
			verdictCounts(with))
	}
}

// sqlGuardProbeLine returns the line of the UPDATE statement in the fixture,
// which is the node the SQLCODE guard immediately follows.
func sqlGuardProbeLine(t *testing.T, svc *Service) int {
	t.Helper()
	tree := svc.helperFlowTree("fn_do_work")
	for i, n := range tree.Root {
		if n.Kind == flow.KindSQL && i+1 < len(tree.Root) {
			if g := tree.Root[i+1]; g.Kind == flow.KindBranch && isSQLCodeGuard(g) {
				return n.Line
			}
		}
	}
	t.Fatal("fixture no longer has a SQL node followed by a SQLCODE guard")
	return 0
}

func verdictCounts(rep fnControlFlowReport) map[string]int {
	out := map[string]int{}
	for _, it := range rep.items {
		out[it.verdict]++
	}
	return out
}

// TestGuardLeakNamesTheInnermostGuard is the severity case. Spans nest, so
// pairing a call with the outermost enclosing branch would name a condition
// the call does not actually depend on — in the fixture the c_flag test is
// inside a loop, and it is the c_flag test that decides whether the call runs.
func TestGuardLeakNamesTheInnermostGuard(t *testing.T) {
	items := []fnControlFlow{
		{line: 10, endLine: 99, kind: "loop", cond: "i=0;i<4;i++", verdict: "not rendered"},
		{line: 20, endLine: 40, kind: "branch", cond: "c_flag == 'A'", verdict: "not rendered"},
		{line: 50, endLine: 60, kind: "branch", cond: "else", verdict: "not rendered"},
	}
	leaks := fnFindGuardLeaks(items, nil, []detHelper{{line: 30, goName: "FnDoWork"}})
	if len(leaks) != 1 {
		t.Fatalf("want 1 leak, got %d: %+v", len(leaks), leaks)
	}
	if leaks[0].guardLine != 20 {
		t.Errorf("paired with the guard at line %d, want the innermost (20)", leaks[0].guardLine)
	}
	if leaks[0].what != "FnDoWork" {
		t.Errorf("leak names %q, want FnDoWork", leaks[0].what)
	}

	// A call past every unrendered span is not a leak. Note the line: 70
	// would be INSIDE the loop's 10-99 span, so 120 is the first line that
	// is genuinely outside all three.
	if got := fnFindGuardLeaks(items, nil, []detHelper{{line: 120, goName: "FnOther"}}); len(got) != 0 {
		t.Errorf("a call outside every guard reported as a leak: %+v", got)
	}
	// Neither is a call inside a span that IS accounted for.
	rendered := []fnControlFlow{{line: 10, endLine: 99, verdict: "covered by the store call's error check"}}
	if got := fnFindGuardLeaks(rendered, nil, []detHelper{{line: 30, goName: "FnX"}}); len(got) != 0 {
		t.Errorf("a call inside a rendered guard reported as a leak: %+v", got)
	}
}

// TestTODOsCarryTheCodeAndNotTheDetailLines guards the census. If the
// per-construct lines carried tuxgo:TODO they would each count as their own
// gap, and a method missing 21 branches would report 22 — the summary plus
// the list — which is the kind of inflation that hides a real count.
func TestTODOsCarryTheCodeAndNotTheDetailLines(t *testing.T) {
	rep := fnControlFlowReport{
		items: []fnControlFlow{
			{line: 10, kind: "branch", cond: "a == 1", verdict: "not rendered"},
			{line: 20, kind: "loop", cond: "i=0;i<4;i++", verdict: "not rendered"},
			{line: 30, kind: "branch", cond: "SQLCODE != 0", verdict: "covered by the store call's error check"},
		},
		total: 3, missing: 2, rendered: 1,
		leaks: []fnGuardLeak{{what: "FnDoWork", line: 25, guardLine: 20, guardCond: "c_flag == 'A'"}},
	}
	lines := rep.TODOs("FnDoWork")
	if len(lines) == 0 {
		t.Fatal("no TODOs for a report with gaps")
	}
	summaries, details := 0, 0
	for _, ln := range lines {
		if strings.Contains(ln, "tuxgo:TODO") {
			summaries++
		} else {
			details++
		}
	}
	if summaries != 2 {
		t.Errorf("want 2 marked gaps (one control-flow, one guard leak), got %d:\n%s",
			summaries, strings.Join(lines, "\n"))
	}
	if details != 2 {
		t.Errorf("want 2 unmarked detail lines, got %d:\n%s", details, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "2 of 3") {
		t.Errorf("summary does not carry the counts:\n%s", lines[0])
	}
	if !strings.Contains(strings.Join(lines, "\n"), "FnDoWork at line 25") {
		t.Errorf("guard leak does not name the call and its line:\n%s", strings.Join(lines, "\n"))
	}
}

// TestTODOsAreSilentWhenEverythingIsAccounted is what lets a future P2 slice
// claim a helper renders completely: with no gaps, there is nothing to say.
func TestTODOsAreSilentWhenEverythingIsAccounted(t *testing.T) {
	rep := fnControlFlowReport{
		items: []fnControlFlow{
			{line: 10, verdict: "covered by the store call's error check", rendered: true},
			{line: 20, verdict: "elided: debug-only-if collapses in Go"},
		},
		total: 2, rendered: 1, elided: 1,
	}
	if got := rep.TODOs("FnDoWork"); len(got) != 0 {
		t.Errorf("a fully accounted helper still emitted TODOs:\n%s", strings.Join(got, "\n"))
	}
}

// TestNoTreeIsNotAClaimOfCompleteness: an absent tree is a parse failure, and
// returning a zero report for one is fine only because the caller does not
// read zero as "complete". This pins that the report stays empty rather than
// inventing a verdict.
func TestNoTreeIsNotAClaimOfCompleteness(t *testing.T) {
	svc := &Service{}
	rep := svc.detFnControlFlow(&plan.FnHelper{Name: "fn_absent"}, nil, nil)
	if rep.total != 0 || rep.missing != 0 || len(rep.items) != 0 {
		t.Errorf("a helper with no tree produced verdicts: %+v", rep)
	}
}

// TestCondTextIsOneLine keeps a legacy condition from breaking the comment it
// sits in, and keeps a 200-character Fget32 call from dominating the line.
func TestCondTextIsOneLine(t *testing.T) {
	for _, in := range []string{
		"a\n== 1",
		"a\r\n== 1",
		"i=0;\n i<4;\n i++",
	} {
		got := detCondText(in)
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("detCondText(%q) = %q, still has a line break", in, got)
		}
	}
	if got := detCondText("   "); got != "(no condition — an else arm)" {
		t.Errorf("a blank condition = %q, want the else-arm phrasing", got)
	}
	long := strings.Repeat("x", 400)
	got := detCondText(long)
	if len([]rune(got)) > 91 {
		t.Errorf("a 400-char condition was not truncated: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncation is not marked: %q", got)
	}
}
