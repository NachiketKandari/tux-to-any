package flow

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"tux-to-any/internal/ir"
	"tux-to-any/internal/pred"
	scanner "tux-to-any/internal/tsscan"
)

// DispatchAxis is the detected dispatch spine of an entry function
// (PRD-2026-09-12 SCEN-1): the request buffer ref the transaction code is
// read into, the scalar alias a normalization chain writes ("" for the
// direct-strcmp idiom), the value domain, and the predicate-site count.
// RefName is the scenario-key identifier — the alias when present, else the
// ref's base ident before the first '.'.
type DispatchAxis struct {
	Ref        string
	RefName    string
	Alias      string
	Domain     []string
	Sites      int
	Normalized bool
	// HasDefault marks a dispatch chain that terminates in an else — the
	// entry dispatches a default arm no domain value names. Detected from
	// the flow tree (unbraced chain arms never join the condition
	// inventory, so the tree is the truth here); the default arm's slice
	// keys `<var>=DefaultKey()`.
	HasDefault bool
}

// DefaultKey returns the scenario value naming the default (else) arm:
// "default" unless a domain value already claims it, then the first free
// "default_" variant — the key stays a parseable scenarioRef value and
// never collides with an enumerated slice.
func (a *DispatchAxis) DefaultKey() string {
	for tok := "default"; ; tok += "_" {
		if !slices.Contains(a.Domain, tok) {
			return tok
		}
	}
}

// Key renders the scenario-key identifier (SCEN-D7): "trn_cd" for the
// normalize-chain idiom, "sql_mf_trn_cd" for the direct-strcmp one.
func (a *DispatchAxis) Key() string {
	if a == nil {
		return ""
	}
	return a.RefName
}

var (
	strcmpSiteRe   = regexp.MustCompile(`\bstrn?cmp\s*\(\s*([A-Za-z_][\w.]*)\s*,\s*"([^"]*)"`)
	charAssignRe   = regexp.MustCompile(`(?:^|[^\w=!<>+\-*/&|.'"])([A-Za-z_]\w*)\s*=\s*'([^'])'\s*;`)
	charCompareRe  = regexp.MustCompile(`(?:^|[^\w=!<>+\-*/&|.'"])([A-Za-z_]\w*)\s*(?:==|!=)\s*'([^'])'`)
	charAssignSemi = regexp.MustCompile(`([A-Za-z_]\w*)\s*=\s*'([^'])'\s*;`)
)

// axisStats accumulates the harvest for one candidate ref/ident.
type axisStats struct {
	ref      string
	vals     map[string]bool // strcmp values
	sites    int             // strcmp predicate lines
	compares int             // char-compare predicate lines (alias-scoped)
	cvals    map[string]bool // char-compare values (alias-scoped)
	normals  map[string]int  // normalization links: alias → count
}

func newAxisStats(ref string) *axisStats {
	return &axisStats{ref: ref, vals: map[string]bool{}, cvals: map[string]bool{}, normals: map[string]int{}}
}

// DispatchAxisFor detects the entry's dispatch spine (SCEN-1). Recognizers, in
// priority order (SCEN-D3 — the list a new idiom joins):
//  1. normalize-chain: `strcmp(<ref>, "<v>") == 0` (or `!strcmp`) guarding a
//     `<alias> = '<v>'` char assignment — ref + alias + domain from the
//     linked values (e.g. sql_trn_cd.arr → trn_cd, P/R/S/A/W/I).
//  2. direct-strcmp: predicates strcmp the ref directly, domain from the
//     distinct literals (e.g. sql_trn_cd.arr, no alias).
//  3. char-compare: predicates compare a scalar against char literals,
//     domain from the distinct compares.
//
// An axis needs ≥2 distinct values (a 1-value domain is not a dispatch);
// no qualifying spine → nil, and the caller falls back to one scenario over
// the whole function (never a silent no-op). Harvest is line-based over the
// entry's body span with comment masking (facts.InComment) so commented-out
// predicates never pollute the domain.
func DispatchAxisFor(src []byte, facts *scanner.SourceFacts, entry string) *DispatchAxis {
	span, ok := entrySpan(facts, entry)
	if !ok {
		return nil
	}
	lines := bytes.Split(src, []byte("\n"))
	from, to := span[0], span[1]
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from > to {
		return nil
	}

	refs := map[string]*axisStats{}
	idents := map[string]*axisStats{}
	links := map[string]map[string]int{} // ref → alias → linked count

	// Pass 1: strcmp + normalize + char-compare harvest (comment-masked).
	for i := from; i <= to; i++ {
		text := string(lines[i-1])
		if lineCommentMasked(facts, i, text) {
			continue
		}
		for _, m := range strcmpSiteRe.FindAllStringSubmatch(text, -1) {
			ref, val := m[1], m[2]
			st, ok := refs[ref]
			if !ok {
				st = newAxisStats(ref)
				refs[ref] = st
			}
			st.vals[val] = true
			st.sites++
		}
		for _, m := range charAssignRe.FindAllStringSubmatch(text, -1) {
			ident, ch := m[1], m[2]
			st, ok := idents[ident]
			if !ok {
				st = newAxisStats(ident)
				idents[ident] = st
			}
			st.cvals[ch] = true
			// Normalization link: the nearest preceding strcmp guarding
			// this assignment (unbraced if-bodies keep them adjacent).
			linkNormal(links, refs, i, ident, ch, lines, from)
		}
		for _, m := range charCompareRe.FindAllStringSubmatch(text, -1) {
			ident, ch := m[1], m[2]
			st, ok := idents[ident]
			if !ok {
				st = newAxisStats(ident)
				idents[ident] = st
			}
			st.compares++
			st.cvals[ch] = true
		}
	}

	// Recognizer 1: the ref with the most normalization links (its alias
	// is the most-linked ident); domain = linked values ∪ strcmp values.
	best := pickAxis(refs, links, func(st *axisStats) (alias string, weight int) {
		maxAlias, maxN := "", 0
		for al, n := range links[st.ref] {
			if n > maxN {
				maxAlias, maxN = al, n
			}
		}
		if maxAlias == "" {
			return "", 0
		}
		return maxAlias, maxN
	})

	// Recognizer 2: direct-strcmp — ref with the most distinct values.
	if best == nil {
		best = pickAxis(refs, links, func(st *axisStats) (alias string, weight int) {
			return "", len(st.vals)
		})
	}

	// Recognizer 3: char-compare scalar (no strcmp anywhere).
	if best == nil {
		best = pickAxis(idents, links, func(st *axisStats) (alias string, weight int) {
			return st.ref, st.compares
		})
	}
	if best == nil || len(best.Domain) < 2 {
		return nil
	}
	return best
}

// pickAxis selects the qualifying stats with the recognizer's weight,
// breaking ties by site count then identifier, and composes the axis.
func pickAxis(stats map[string]*axisStats, links map[string]map[string]int, weightOf func(*axisStats) (alias string, weight int)) *DispatchAxis {
	var best *axisStats
	var bestAlias string
	var bestWeight int
	refs := make([]string, 0, len(stats))
	for r := range stats {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	for _, r := range refs {
		st := stats[r]
		alias, weight := weightOf(st)
		if weight <= 0 {
			continue
		}
		if weight > bestWeight || (weight == bestWeight && best != nil && (st.sites+st.compares) > (best.sites+best.compares)) {
			best, bestAlias, bestWeight = st, alias, weight
		}
	}
	if best == nil {
		return nil
	}
	domain := map[string]bool{}
	for v := range best.vals {
		domain[v] = true
	}
	for v := range best.cvals {
		domain[v] = true
	}
	var vals []string
	for v := range domain {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	name := bestAlias
	if name == "" {
		name = best.ref
	}
	if dot := strings.Index(name, "."); dot > 0 {
		name = name[:dot]
	}
	normalized := bestAlias != "" && len(links[best.ref]) > 0
	return &DispatchAxis{
		Ref:        best.ref,
		RefName:    name,
		Alias:      bestAlias,
		Domain:     vals,
		Sites:      best.sites + best.compares,
		Normalized: normalized,
	}
}

// linkNormal links an assignment `<alias> = '<ch>'` to the nearest
// preceding strcmp of a ref whose value matches ch (within a small window —
// unbraced if-bodies keep guard and assignment adjacent). Only exact
// value matches count: `strcmp(x,"A")` guarding `alias = 'A'`.
func linkNormal(links map[string]map[string]int, refs map[string]*axisStats, line int, alias, ch string, lines [][]byte, from int) {
	window := 4
	for j := line - 1; j >= from && j >= line-window; j-- {
		text := string(lines[j-1])
		for _, m := range strcmpSiteRe.FindAllStringSubmatch(text, -1) {
			ref, val := m[1], m[2]
			if val != ch {
				continue
			}
			if _, ok := refs[ref]; !ok {
				refs[ref] = newAxisStats(ref)
			}
			if links[ref] == nil {
				links[ref] = map[string]int{}
			}
			links[ref][alias]++
			return
		}
	}
}

// entrySpan resolves the entry function's body line span.
func entrySpan(facts *scanner.SourceFacts, entry string) ([2]int, bool) {
	for _, fn := range facts.Functions {
		if fn.Name == entry && fn.BodyStartLine > 0 && fn.BodyEndLine > fn.BodyStartLine {
			return [2]int{fn.BodyStartLine, fn.BodyEndLine}, true
		}
	}
	return [2]int{}, false
}

// lineCommentMasked reports whether the line is comment text (fully inside
// a multi-line span, or a single-line comment starting at the first
// non-whitespace column) — the mask that keeps commented-out predicates
// out of the harvest.
func lineCommentMasked(facts *scanner.SourceFacts, line int, text string) bool {
	col := 1
	for _, r := range text {
		if r == ' ' || r == '\t' {
			col++
			continue
		}
		break
	}
	return facts.InComment(line, col)
}

// chainExtent returns the index just past the if/elseif/else chain whose
// head sits at nodes[i]: consecutive sibling branch nodes — an `if`, then
// any `elseif` arms, then an optional `else` — form one chain (the tree
// keeps chain members adjacent; nothing can sit between them). Any other
// node's extent is that single node.
func chainExtent(nodes []*Node, i int) int {
	if nodes[i].Kind != KindBranch || nodes[i].Sub != string(scanner.BranchIf) {
		return i + 1
	}
	j := i + 1
	for j < len(nodes) && nodes[j].Kind == KindBranch && nodes[j].Sub == string(scanner.BranchElseIf) {
		j++
	}
	if j < len(nodes) && nodes[j].Kind == KindBranch && nodes[j].Sub == string(scanner.BranchElse) {
		j++
	}
	return j
}

// hasAxisDefault reports whether an axis-touching top-level if/elseif
// chain terminates in an else — the entry dispatches a default arm no
// domain value names. The tree is the truth here, not the condition
// inventory: unbraced chain arms never join the inventory, but the tree
// keeps them.
func (t *Tree) hasAxisDefault(ref, alias string) bool {
	touches := func(n *Node) bool {
		return n.Predicate != nil && exprTouchesAxis(n.Predicate, ref, alias)
	}
	for i := 0; i < len(t.Root); {
		end := chainExtent(t.Root, i)
		if t.Root[i].Kind == KindBranch && t.Root[i].Sub == string(scanner.BranchIf) {
			dispatch := false
			for _, m := range t.Root[i:end] {
				if touches(m) {
					dispatch = true
					break
				}
			}
			if dispatch && t.Root[end-1].Sub == string(scanner.BranchElse) {
				return true
			}
		}
		i = end
	}
	return false
}

// String renders the axis for logs and PRD evidence.
func (a *DispatchAxis) String() string {
	if a == nil {
		return "axis: none"
	}
	id := a.Ref
	if a.Alias != "" {
		id = fmt.Sprintf("%s (alias %s)", a.Alias, a.Ref)
	}
	suffix := ""
	if a.HasDefault {
		suffix = " +default"
	}
	return fmt.Sprintf("axis %s domain [%s] sites %d%s", id, strconv.Quote(strings.Join(a.Domain, ",")), a.Sites, suffix)
}

// DispatchAxisFor derives the tree's own function's dispatch axis using the
// scanner facts the tree was built from — the entry point plan/gen/convert
// use after flow.TreeFor (SCEN-D1: detection stays a flow home; consumers
// never re-derive facts). A tree built without facts (hand-built in tests)
// yields nil — honest none. The default-arm mark reads the tree itself.
func (t *Tree) DispatchAxisFor(src []byte) *DispatchAxis {
	axis := DispatchAxisFor(src, t.facts, t.Function)
	if axis == nil {
		return nil
	}
	axis.HasDefault = t.hasAxisDefault(axis.Ref, axis.Alias)
	return axis
}

// FoldKind classifies what the fold did to a branch predicate under the
// scenario's axis assumption (SCEN-D2).
type FoldKind string

const (
	// FoldSatisfied: the predicate is provably true under the assumption —
	// emitted as a folded comment, children kept.
	FoldSatisfied FoldKind = "satisfied"
	// FoldContradicted: provably false — the subtree is dropped and counted.
	FoldContradicted FoldKind = "contradicted"
	// FoldMixed: the assumption participates in a compound predicate — the
	// residual predicate (axis terms removed) stays live on the node.
	FoldMixed FoldKind = "mixed"
	// FoldKept: the predicate never touches the axis — verbatim.
	FoldKept FoldKind = "kept"
	// FoldUnrecognized: axis-touching text the fold could not decide — the
	// whole subtree stays and the Residue lists it (SCEN-D4, loud).
	FoldUnrecognized FoldKind = "unrecognized"
)

// SliceNode is one node of a scenario body: the original tree node's span
// plus what the fold did. Branch predicates carry the folded predicate
// text ("" when satisfied — it became a comment) and Children keeps the
// surviving nesting verbatim.
type SliceNode struct {
	Kind       Kind         `json:"kind"`
	Sub        string       `json:"sub,omitempty"`
	Line       int          `json:"line"`
	EndLine    int          `json:"end_line,omitempty"`
	Cond       string       `json:"cond,omitempty"`        // original predicate text (verbatim audit trail)
	Fold       FoldKind     `json:"fold"`                  // satisfied/contradicted/mixed/kept/unrecognized
	FoldedCond string       `json:"folded_cond,omitempty"` // residual predicate for mixed ("" otherwise)
	Children   []*SliceNode `json:"children,omitempty"`
}

// ScenarioQuery records one query reachable in the scenario: its IR id,
// SQL kind, and whether DML rides a transaction inside the scenario body
// (a COMMIT/ROLLBACK node reachable in the same slice).
type ScenarioQuery struct {
	ID string `json:"id"`
	// DML is true for INSERT/UPDATE/DELETE/MERGE queries.
	DML bool `json:"dml,omitempty"`
	// Tx is the per-scenario decision (G-SCEN6): a DML query whose scenario
	// body contains commit/rollback facts — the controller calls the tx
	// template variant for it; DML without commit/rollback facts renders
	// the non-tx (autocommit) variant. Non-DML queries never set Tx.
	Tx bool `json:"tx,omitempty"`
}

// ScenarioResponse is one non-error response write (Fadd32) resolved per
// SCEN-D9: the field's source variable and the last surviving write's value
// before the add line. Stable=false marks a genuine same-path rewrite after
// the add (loud — never a silent guess).
type ScenarioResponse struct {
	Field  string `json:"field"`
	Buffer string `json:"buffer,omitempty"`
	Var    string `json:"var,omitempty"`
	Value  string `json:"value,omitempty"`
	Line   int    `json:"line"`
	Stable bool   `json:"stable"`
}

// Scenario is the SCEN-2 slice for one axis value: preamble spans shared by
// every scenario, the folded body tree (nesting preserved, contradicted
// branches recorded by span), the folded FML census, reachable queries with
// the tx decision, loud residue, and the reconcile counts.
type Scenario struct {
	Key   string `json:"key"`   // "trn_cd=A"
	Var   string `json:"var"`   // key identifier (alias or ref base)
	Value string `json:"value"` // the axis value verbatim
	// Default marks the dispatch chain's else arm (the `<var>=default`
	// slice): it runs when the axis value matches no enumerated slice.
	Default   bool            `json:"default,omitempty"`
	Preamble  []int           `json:"preamble,omitempty"` // line spans [start,end] pairs, flattened
	Body      []*SliceNode    `json:"body,omitempty"`
	Gets      []string        `json:"gets,omitempty"`
	Adds      []string        `json:"adds,omitempty"`
	ErrorAdds []string        `json:"error_adds,omitempty"`
	Codes     []string        `json:"codes,omitempty"`
	Queries   []ScenarioQuery `json:"queries,omitempty"`
	// TxSpans lists the live begin→commit pairs in the slice (SCEN-D8) —
	// the evidence behind the per-query Tx flags, visible for review.
	TxSpans []txSpan `json:"tx_spans,omitempty"`
	// Responses resolves the scenario's response writes (SCEN-D9).
	Responses []ScenarioResponse `json:"responses,omitempty"`
	Residue   []string           `json:"residue,omitempty"`
	Counts    ScenarioCounts     `json:"counts"`
}

// ScenarioCounts reconcile the slice against the tree: Kept+Dropped must
// equal the classified lines inside the body span (coverage cross-check).
type ScenarioCounts struct {
	Kept         int   `json:"kept"`
	Dropped      int   `json:"dropped"`
	Unfolded     int   `json:"unfolded"`
	DroppedLines []int `json:"dropped_lines,omitempty"`
}

// ScenarioFor slices the tree for one axis value (SCEN-2): the assumption
// `axisVar == value` folds every branch predicate that touches the axis —
// satisfied folds to a comment (children kept), contradicted drops the
// subtree (recorded), mixed keeps the residual predicate, unrecognized
// axis-touching predicates keep the whole subtree and add a Residue entry.
// Folding is chain-aware (SCEN-D2): sibling if/elseif/else arms are
// mutually exclusive, so a provably-true arm makes every later sibling
// unreachable (dropped, counted), and an else arm is reachable only when
// no earlier arm provably matched — the else arm of a dispatch chain never
// leaks into an enumerated value's slice, and the `=default` slice's body
// is exactly the else arm. Preamble = top-level nodes before the first
// axis-touching chain (chain granularity — the split never cuts inside a
// chain); those nodes are shared and appear in every scenario's span list.
// A nil axis is the honest fallback shape (G-SCEN1): nothing folds, and
// the whole function is the body (no preamble split) — FallbackScenario
// wraps it.
func ScenarioFor(tree *Tree, axis *DispatchAxis, value string) *Scenario {
	sc := &Scenario{Key: axis.Key() + "=" + value, Var: axis.Key(), Value: value}
	ref, alias := "", ""
	if axis != nil {
		ref, alias = axis.Ref, axis.Alias
		sc.Default = axis.HasDefault && value == axis.DefaultKey()
	}
	// Aliases fold too: predicates may use either the ref (strcmp) or the
	// alias (char compare) — the assumption covers both spellings.
	inAxis := func(e *pred.Expr) bool {
		return exprTouchesAxis(e, ref, alias)
	}
	assume := func(e *pred.Expr) (pred.Expr, tribool, bool) {
		return foldExpr(e, ref, alias, value)
	}
	var walk func(nodes []*Node) []*SliceNode
	// foldArm folds one node under the scenario assumption — the chain
	// walk's per-member logic. The second result reports a provably-true
	// arm (the chain-exclusivity signal). A dropped arm returns nil:
	// dropped subtrees are counted, never emitted.
	foldArm := func(n *Node) (*SliceNode, bool) {
		sn := &SliceNode{Kind: n.Kind, Sub: n.Sub, Line: n.Line, EndLine: n.EndLine, Cond: n.Cond}
		switch {
		case n.Kind != KindBranch || n.Predicate == nil:
			sn.Fold = FoldKept
			sn.Children = walk(n.Children)
			sc.Counts.Kept++
			return sn, false
		case !inAxis(n.Predicate):
			// Never touches the axis — verbatim, never residue.
			sn.Fold = FoldKept
			sn.Children = walk(n.Children)
			sc.Counts.Kept++
			return sn, false
		default:
			residual, truth, ok := assume(n.Predicate)
			switch {
			case ok && truth == triFalse:
				sc.Counts.Dropped++
				sc.Counts.DroppedLines = append(sc.Counts.DroppedLines, spanLines(n.Line, n.EndLine)...)
				return nil, false // subtree dropped, counted
			case ok && truth == triTrue:
				sn.Fold = FoldSatisfied
				sn.Children = walk(n.Children)
				sc.Counts.Kept++
				return sn, true
			case ok && truth == triMixed:
				sn.Fold = FoldMixed
				sn.FoldedCond = residual.String()
				sn.Children = walk(n.Children)
				sc.Counts.Kept++
				return sn, false
			default:
				sn.Fold = FoldUnrecognized
				sc.Counts.Unfolded++
				sc.Residue = append(sc.Residue, fmt.Sprintf("L%d: %s", n.Line, oneLineC(n.Cond)))
				sn.Children = walk(n.Children)
				sc.Counts.Kept++
				return sn, false
			}
		}
	}
	walk = func(nodes []*Node) []*SliceNode {
		var out []*SliceNode
		for i := 0; i < len(nodes); {
			end := chainExtent(nodes, i)
			// Dispatch chain: an if-headed run of sibling arms with at
			// least one axis-touching predicate. Chain semantics apply —
			// arms are mutually exclusive, so once an arm provably matches
			// every later sibling is unreachable (dropped, counted), and
			// the else arm is reachable only when no earlier arm matched.
			dispatch := false
			if nodes[i].Kind == KindBranch && nodes[i].Sub == string(scanner.BranchIf) {
				for _, m := range nodes[i:end] {
					if m.Predicate != nil && inAxis(m.Predicate) {
						dispatch = true
						break
					}
				}
			}
			taken := false
			for _, m := range nodes[i:end] {
				if dispatch && m.Sub == string(scanner.BranchElse) {
					if taken {
						sc.Counts.Dropped++
						sc.Counts.DroppedLines = append(sc.Counts.DroppedLines, spanLines(m.Line, m.EndLine)...)
						continue // the matched arm excludes the default
					}
					// The default arm is reached: keep verbatim (no
					// predicate to fold — its body is this slice's body).
					sn := &SliceNode{Kind: m.Kind, Sub: m.Sub, Line: m.Line, EndLine: m.EndLine, Cond: m.Cond, Fold: FoldKept}
					sn.Children = walk(m.Children)
					sc.Counts.Kept++
					out = append(out, sn)
					continue
				}
				sn, isTrue := foldArm(m)
				if sn != nil {
					out = append(out, sn)
				}
				taken = taken || isTrue
			}
			i = end
		}
		return out
	}

	// Preamble: top-level nodes strictly before the first axis-touching
	// chain (the split scans whole chains, so it never cuts a chain into
	// preamble and body); everything after (including that chain) is the
	// folded body. The nil-axis fallback has no preamble — the whole
	// function is body.
	split := len(tree.Root)
	if axis == nil {
		split = 0
	} else {
		for i := 0; i < len(tree.Root) && split == len(tree.Root); {
			end := chainExtent(tree.Root, i)
			for _, m := range tree.Root[i:end] {
				if m.Kind == KindBranch && m.Predicate != nil && inAxis(m.Predicate) {
					split = i
					break
				}
			}
			i = end
		}
	}
	for _, n := range tree.Root[:split] {
		sc.Preamble = append(sc.Preamble, n.Line, n.EndLine)
	}
	sc.Body = walk(tree.Root[split:])

	// Census + queries + tx over the surviving body (own-line census, no
	// descendant double-count: parent branches carry no FML of their own
	// beyond their header line, which the census below re-derives from
	// slice leaves only).
	censusOf(sc, tree)
	return sc
}

// Scenarios slices the tree for every domain value (SCEN-2): one Scenario
// per axis value, in the domain's deterministic order — plus the default
// arm's slice (`<var>=default`) when the dispatch chain terminates in an
// else. Every reachable arm gets exactly one slice; the default arm is
// never silently absorbed into the enumerated ones.
func Scenarios(tree *Tree, axis *DispatchAxis) []*Scenario {
	out := make([]*Scenario, 0, len(axis.Domain)+1)
	for _, v := range axis.Domain {
		out = append(out, ScenarioFor(tree, axis, v))
	}
	if axis.HasDefault {
		out = append(out, ScenarioFor(tree, axis, axis.DefaultKey()))
	}
	return out
}

// FallbackScenario is the honest axis:none shape (G-SCEN1): one scenario
// covering the whole function — nothing folds, nothing is silent. Keyed
// axis=none so the diff/report consumers treat it as a single-body scenario.
func FallbackScenario(tree *Tree) *Scenario {
	sc := ScenarioFor(tree, nil, "none")
	sc.Key, sc.Var, sc.Value = "axis=none", "axis", "none"
	return sc
}

// tribool is the fold's three-valued truth.
type tribool int

const (
	triUnknown tribool = iota
	triTrue
	triFalse
	triMixed
)

// exprTouchesAxis reports whether any leaf of e references the axis ref or
// alias in a COMPARISON sense: strcmp/strncmp calls whose compared argument
// is the ref (the dispatch idiom), ident leaves, or raw text mentioning the
// axis. Write-target mentions (Fget32/Fadd32 buffer arguments) never count
// — the read guard is not a predicate on the axis value.
func exprTouchesAxis(e *pred.Expr, ref, alias string) bool {
	if e == nil {
		return false
	}
	touch := func(text string) bool {
		return text != "" && (text == ref || text == alias)
	}
	switch e.Kind {
	case pred.KindOr, pred.KindAnd:
		for i := range e.Items {
			if exprTouchesAxis(&e.Items[i], ref, alias) {
				return true
			}
		}
	case pred.KindNot:
		return exprTouchesAxis(e.Inner, ref, alias)
	case pred.KindCmp:
		return exprTouchesAxis(e.L, ref, alias) || exprTouchesAxis(e.R, ref, alias)
	case pred.KindCall:
		if isStrcmpName(e.Name) {
			if len(e.Args) > 0 && (touch(strings.TrimSpace(e.Args[0])) || len(e.Args) > 1 && touch(strings.TrimSpace(e.Args[1]))) {
				return true
			}
		}
		return false
	case pred.KindIdent:
		return touch(e.Name)
	case pred.KindLit:
		return false
	case pred.KindRaw:
		return ref != "" && strings.Contains(e.Text, ref) || alias != "" && strings.Contains(e.Text, alias)
	}
	return false
}

// isStrcmpName reports the comparison-call names the fold recognizes.
func isStrcmpName(name string) bool {
	return name == "strcmp" || name == "strncmp"
}

// foldExpr evaluates e under the assumption `ref/alias == value`: axis
// literals match the value; `x == v` (axis side) → true, `x != v` → false;
// strcmp-forms: strcmp(ref,v)==0 → v==value; !strcmp / strcmp!=0 invert;
// and/or/not compose; any non-axis leaf or any Raw degrades to mixed when
// it shares a node with axis terms, unknown otherwise. Returns the residual
// expression (axis terms removed), the folded truth, and whether the fold
// was conclusive.
func foldExpr(e *pred.Expr, ref, alias, value string) (pred.Expr, tribool, bool) {
	if e == nil {
		return pred.Expr{}, triUnknown, false
	}
	switch e.Kind {
	case pred.KindOr:
		var resid []pred.Expr
		anyTrue := false
		raw := false
		for i := range e.Items {
			r, t, ok := foldExpr(&e.Items[i], ref, alias, value)
			if !ok {
				raw = true
				resid = append(resid, r)
				continue
			}
			switch t {
			case triTrue:
				anyTrue = true
			case triFalse:
			case triMixed:
				resid = append(resid, r)
			default:
				raw = true
				resid = append(resid, r)
			}
		}
		switch {
		case anyTrue:
			return pred.Expr{}, triTrue, true
		case raw:
			return orOf(resid), triMixed, true // or with undecidables: keep residual, runtime decides
		case len(resid) > 0:
			return orOf(resid), triMixed, true
		default:
			return pred.Expr{}, triFalse, true
		}
	case pred.KindAnd:
		var resid []pred.Expr
		anyFalse := false
		raw := false
		for i := range e.Items {
			r, t, ok := foldExpr(&e.Items[i], ref, alias, value)
			if !ok {
				raw = true
				resid = append(resid, r)
				continue
			}
			switch t {
			case triFalse:
				anyFalse = true
			case triTrue:
			case triMixed:
				resid = append(resid, r)
			default:
				raw = true
				resid = append(resid, r)
			}
		}
		switch {
		case anyFalse:
			return pred.Expr{}, triFalse, true
		case raw && len(resid) > 0:
			return andOf(resid), triMixed, true
		case len(resid) > 0:
			return andOf(resid), triMixed, true
		case raw:
			return pred.Expr{}, triMixed, true
		default:
			return pred.Expr{}, triTrue, true
		}
	case pred.KindNot:
		r, t, ok := foldExpr(e.Inner, ref, alias, value)
		if !ok {
			return pred.Expr{Kind: pred.KindNot, Inner: &r}, triUnknown, false
		}
		switch t {
		case triTrue:
			return pred.Expr{}, triFalse, true
		case triFalse:
			return pred.Expr{}, triTrue, true
		case triMixed:
			return notOf(r), triMixed, true
		default:
			return pred.Expr{Kind: pred.KindNot, Inner: &r}, triUnknown, false
		}
	case pred.KindCmp:
		return foldCmp(e, ref, alias, value)
	case pred.KindCall:
		return foldCall(e, ref, alias, value)
	case pred.KindIdent:
		if isAxisLeaf(e.Name, ref, alias) {
			// A bare axis ident in a boolean context: `if (strcmp(...))`
			// shape is a Call; a bare ident equals its value only if the
			// value is nonzero — undecidable textually → mixed keeps it.
			return pred.Ident(e.Name), triMixed, true
		}
		return *e, triUnknown, false
	default:
		return *e, triUnknown, false
	}
}

// foldCmp folds one comparison. The corpus shapes: `trn_cd == 'A'`,
// `trn_cd != 'P'`, `strcmp(x,"A") == 0`, `strcmp(x,"A") != 0`, and
// `(trn_cd=='A') && (sql_mf_sch_mul_trn_alwd=='Y')`. C semantics: strcmp
// returns 0 when EQUAL, so `strcmp(x,v) == 0` is the equals-test.
func foldCmp(e *pred.Expr, ref, alias, value string) (pred.Expr, tribool, bool) {
	lText, rText := leafText(e.L), leafText(e.R)
	lAxis := isAxisLeaf(lText, ref, alias)
	rAxis := isAxisLeaf(rText, ref, alias)
	// strcmp(x, "v") == 0 → equals-test; != 0 → not-equals. Either operand
	// may carry the call (both C spellings occur).
	if v, axis := strcmpAxisOf(e.L, ref, alias); axis && isNumericZero(rText) {
		return equalsFold(v, value, e.Op)
	}
	if v, axis := strcmpAxisOf(e.R, ref, alias); axis && isNumericZero(lText) {
		return equalsFold(v, value, e.Op)
	}
	if lAxis && rAxis {
		// ref vs alias comparisons are internal to the axis.
		return pred.Expr{}, triTrue, true
	}
	if lAxis || rAxis {
		other := rText
		if rAxis {
			other = lText
		}
		if pred.IsLitText(other) {
			lit := unquote(other)
			return equalsFold(lit, value, e.Op)
		}
		// Axis compared against a non-literal (trn_cd == c_x): runtime —
		// mixed keeps the comparison with the axis side substituted.
		if lAxis {
			return pred.Expr{Kind: pred.KindCmp, L: predLitPtr(value), Op: e.Op, R: e.R}, triMixed, true
		}
		return pred.Expr{Kind: pred.KindCmp, L: e.L, Op: e.Op, R: predLitPtr(value)}, triMixed, true
	}
	// Neither side touches the axis (e.g. `extra == 1`, `Fget32(...) == -1`):
	// runtime truth — the caller keeps it (never residue: not axis text).
	return *e, triMixed, true
}

// strcmpAxisOf reports (value, true) when e is a strcmp/strncmp call whose
// buffer argument is the axis ref/alias and whose other argument is a
// string literal; ("", false) otherwise.
func strcmpAxisOf(e *pred.Expr, ref, alias string) (string, bool) {
	if e == nil || e.Kind != pred.KindCall || !isStrcmpName(e.Name) || len(e.Args) != 2 {
		return "", false
	}
	a0, a1 := strings.TrimSpace(e.Args[0]), strings.TrimSpace(e.Args[1])
	if isAxisLeaf(a0, ref, alias) {
		if v, ok := litString(a1); ok {
			return v, true
		}
	}
	if isAxisLeaf(a1, ref, alias) {
		if v, ok := litString(a0); ok {
			return v, true
		}
	}
	return "", false
}

// isNumericZero reports the `== 0` comparison operand (plain 0 / 0L).
func isNumericZero(text string) bool {
	t := strings.TrimSpace(text)
	return t == "0" || t == "0L" || t == "0l"
}

// equalsFold applies the equals-test under op (== or !=).
func equalsFold(v, value, op string) (pred.Expr, tribool, bool) {
	eq := v == value
	switch op {
	case "==":
		if eq {
			return pred.Expr{}, triTrue, true
		}
		return pred.Expr{}, triFalse, true
	case "!=":
		if eq {
			return pred.Expr{}, triFalse, true
		}
		return pred.Expr{}, triTrue, true
	}
	return pred.Expr{}, triUnknown, false
}

// foldCall folds a Call leaf. strcmp truth is C's: nonzero (= different)
// is true; `!strcmp` inverts to the equals-test.
func foldCall(e *pred.Expr, ref, alias, value string) (pred.Expr, tribool, bool) {
	if v, axis := strcmpAxisOf(e, ref, alias); axis {
		if v == value {
			return pred.Expr{}, triFalse, true
		}
		return pred.Expr{}, triTrue, true
	}
	// Any other call is runtime truth → mixed (kept, not residue).
	return *e, triMixed, true
}

// isAxisLeaf reports whether text names the axis ref or alias exactly.
func isAxisLeaf(text, ref, alias string) bool {
	t := strings.TrimSpace(text)
	return t != "" && (t == ref || t == alias)
}

// litString extracts the C string literal content ("A") from raw arg text.
func litString(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// unquote strips matching outer quotes from literal text ('A' → A).
func unquote(text string) string {
	t := strings.TrimSpace(text)
	if len(t) >= 2 && (t[0] == '\'' || t[0] == '"') && t[len(t)-1] == t[0] {
		return t[1 : len(t)-1]
	}
	return t
}

// leafText renders a leaf's comparable text: ident name or literal text.
func leafText(e *pred.Expr) string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case pred.KindIdent:
		return e.Name
	case pred.KindLit:
		return e.Text
	case pred.KindRaw:
		return e.Text
	default:
		return ""
	}
}

// orOf/andOf/notOf rebuild compound residuals from surviving items.
func orOf(items []pred.Expr) pred.Expr {
	items = nonEmpty(items)
	if len(items) == 1 {
		return items[0]
	}
	return pred.Expr{Kind: pred.KindOr, Items: items}
}

func andOf(items []pred.Expr) pred.Expr {
	items = nonEmpty(items)
	if len(items) == 1 {
		return items[0]
	}
	return pred.Expr{Kind: pred.KindAnd, Items: items}
}

func notOf(inner pred.Expr) pred.Expr {
	if inner.Kind == pred.KindNot && inner.Inner != nil {
		return *inner.Inner
	}
	return pred.Expr{Kind: pred.KindNot, Inner: &inner}
}

func nonEmpty(items []pred.Expr) []pred.Expr {
	out := items[:0]
	for _, it := range items {
		if it.Kind != pred.KindLit || it.Text != "" {
			out = append(out, it)
		}
	}
	return out
}

// predLitPtr builds a pointer to a literal expr.
func predLitPtr(text string) *pred.Expr {
	e := pred.Expr{Kind: pred.KindLit, Text: "'" + text + "'"}
	return &e
}

// spanLines lists the inclusive line span (bounded — a pathological span
// would blow the dropped-lines ledger).
func spanLines(from, to int) []int {
	const maxSpan = 4096
	if to < from {
		return nil
	}
	if to-from+1 > maxSpan {
		to = from + maxSpan - 1
	}
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// oneLineC collapses whitespace (the residue's single-line form).
func oneLineC(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// txSpan is one live begin→commit pair discovered in a scenario slice.
// Begin/commit may be ATMI calls (tpbegin/tpcommit) or the helper trio
// (fn_equ_begintran/committran/aborttran); Kind records which.
type txSpan struct {
	BeginLine  int
	CommitLine int
	Kind       string // "tp" | "helper"
	// Conditional notes a commit reachable only behind runtime guards
	// (e.g. success-flag gates) — the tx decision stays but the
	// residue notes the condition (SCEN-D8).
	Conditional bool
}

// txCallKind classifies a call name as a tx-begin, tx-commit, or neither.
// Aborts never pair (SCEN-D8: error-path-only, never open or close a span).
func txCallKind(name string) (kind string, role string) {
	switch name {
	case "tpbegin":
		return "tp", "begin"
	case "tpcommit":
		return "tp", "commit"
	}
	if strings.HasPrefix(name, "fn_") {
		l := strings.ToLower(name)
		switch {
		case strings.Contains(l, "begintran") || strings.Contains(l, "begin_tran"):
			return "helper", "begin"
		case strings.Contains(l, "committran") || strings.Contains(l, "commit_tran"):
			return "helper", "commit"
		}
	}
	return "", ""
}

// txSpansOf pairs live begin→commit call sites over the scenario's surviving
// slice (SCEN-D8). Sites come from the scanner's exact-line call facts
// (Node.Calls cannot serve: branch nodes carry their whole span's calls);
// a commit pairs the nearest unpaired begin of the same kind (C stack
// discipline), a commit without a begin is ignored (a mid-branch commit
// rides the enclosing entry-level span), and a begin without a
// surviving commit closes nothing.
func txSpansOf(sc *Scenario, tree *Tree) []txSpan {
	if tree.facts == nil {
		return nil
	}
	kept := keptNodeLines(sc)
	type site struct {
		line int
		kind string
		role string
	}
	var sites []site
	for i := range tree.facts.Calls {
		call := &tree.facts.Calls[i]
		if call.Func != tree.Function {
			continue
		}
		kind, role := txCallKind(call.Name)
		if kind == "" || !kept[call.Line] {
			continue
		}
		sites = append(sites, site{line: call.Line, kind: kind, role: role})
	}
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].line < sites[j].line })
	spans := []txSpan{}
	type openBegin struct {
		kind string
		line int
	}
	var open []openBegin
	for _, s := range sites {
		switch s.role {
		case "begin":
			open = append(open, openBegin{kind: s.kind, line: s.line})
		case "commit":
			if len(open) > 0 {
				b := open[len(open)-1]
				open = open[:len(open)-1]
				spans = append(spans, txSpan{BeginLine: b.line, CommitLine: s.line, Kind: b.kind})
			}
		}
	}
	return spans
}

// keptNodeLines maps every line of each surviving slice node's span → true
// (a call site can sit mid-span, e.g. the second line of a merged stmt run).
func keptNodeLines(sc *Scenario) map[int]bool {
	kept := map[int]bool{}
	var walk func(nodes []*SliceNode)
	walk = func(nodes []*SliceNode) {
		for _, sn := range nodes {
			for l := sn.Line; l <= sn.EndLine; l++ {
				kept[l] = true
			}
			walk(sn.Children)
		}
	}
	walk(sc.Body)
	return kept
}

// censusOf folds the FML census and reachable queries from the surviving
// slice: it walks slice leaves only (their own FmlOps on the original
// nodes' header lines) — the dropped subtrees contribute nothing.
func censusOf(sc *Scenario, tree *Tree) {
	// The census re-walks the original tree limited to surviving spans —
	// body slice nodes plus the preamble (shared reads ride every census).
	kept := map[int]bool{}
	var walk func(nodes []*SliceNode)
	walk = func(nodes []*SliceNode) {
		for _, sn := range nodes {
			kept[sn.Line] = true
			walk(sn.Children)
		}
	}
	walk(sc.Body)
	for i := 0; i+1 < len(sc.Preamble); i += 2 {
		for l := sc.Preamble[i]; l <= sc.Preamble[i+1]; l++ {
			kept[l] = true
		}
	}
	// Kept node records for the response-value pass (SCEN-D9): line-ordered
	// originals carrying text/query ids.
	var recs []nodeRec

	gets := map[string]bool{}
	adds := map[string]bool{}
	errs := map[string]bool{}
	codes := map[string]bool{}
	dml := map[string]bool{}
	qids := map[string]int{} // id → first line

	var visit func(n *Node)
	visit = func(n *Node) {
		if !kept[n.Line] {
			return // dropped subtree: no census contribution
		}
		if n.Text != "" {
			q := ""
			if len(n.QueryIDs) > 0 {
				q = n.QueryIDs[0]
			}
			// Merged stmt runs span consecutive lines — split into per-line
			// records so a write between two adds resolves at its own line.
			for i, tl := range strings.Split(n.Text, "\n") {
				recs = append(recs, nodeRec{line: n.Line + i, kind: n.Kind, text: tl, sub: n.Sub, queryID: q})
			}
		}
		for _, op := range n.FmlOps {
			if op.Line > 0 && (op.Line < n.Line || op.Line > n.EndLine) {
				continue
			}
			switch {
			case op.Kind == ir.FmlGet:
				gets[op.Field] = true
			case op.Kind == ir.FmlAdd && isErrorAdd(op, n):
				errs[op.Field] = true
			case op.Kind == ir.FmlAdd:
				adds[op.Field] = true
			}
			if op.Code != "" {
				codes[op.Code] = true
			}
		}
		for _, q := range n.QueryIDs {
			if _, seen := qids[q]; !seen {
				qids[q] = n.Line
				if isDMLQuery(tree, q) {
					dml[q] = true
				}
			} else if n.Kind == KindSQL {
				// The SQL node's own line is the query's real position —
				// ancestor spans (a branch covering the whole loop) start
				// earlier and would misplace the span-enclosure check.
				qids[q] = n.Line
			}
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	for _, n := range tree.Root {
		visit(n)
	}
	sc.Gets = sortedKeys(gets)
	sc.Adds = sortedKeys(adds)
	sc.ErrorAdds = sortedKeys(errs)
	sc.Codes = sortedKeys(codes)
	// Queries: the scenario keeps queries whose surviving nodes reference
	// them — flat query id → order by first appearance.
	// The tx decision (G-SCEN6, SCEN-D8): a DML query is transactional when
	// a live begin→commit span encloses its first line in the slice.
	spans := txSpansOf(sc, tree)
	for _, q := range treeQueryOrder(tree) {
		line, ok := qids[q]
		if !ok {
			continue
		}
		qy := ScenarioQuery{ID: q, DML: dml[q]}
		if dml[q] {
			for _, s := range spans {
				if s.BeginLine < line && line < s.CommitLine {
					qy.Tx = true
					break
				}
			}
		}
		sc.Queries = append(sc.Queries, qy)
	}
	sc.TxSpans = spans
	sc.Responses = resolveResponses(sc, tree, recs)
}

// isDMLQuery reports the query's kind from the tree's SQL nodes.
func isDMLQuery(tree *Tree, id string) bool {
	for _, n := range tree.Root {
		if found := findDML(n, id); found {
			return true
		}
	}
	return false
}

func findDML(n *Node, id string) bool {
	if containsID(n.QueryIDs, id) && isDMLSub(n.Sub) {
		return true
	}
	for _, c := range n.Children {
		if findDML(c, id) {
			return true
		}
	}
	return false
}

func isDMLSub(sub string) bool {
	switch sub {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		return true
	}
	return false
}

func containsID(ids []string, id string) bool {
	for _, s := range ids {
		if s == id {
			return true
		}
	}
	return false
}

// sliceHasTx was retired by SCEN-D8: the corpus tx idiom is ATMI/helper
// begin→commit span pairs, never EXEC SQL COMMIT — txSpansOf + span
// enclosure replaced it.

// treeQueryOrder lists the file's query ids in the tree's appearance order.
func treeQueryOrder(tree *Tree) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(n *Node)
	visit = func(n *Node) {
		for _, q := range n.QueryIDs {
			if !seen[q] {
				seen[q] = true
				out = append(out, q)
			}
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	for _, n := range tree.Root {
		visit(n)
	}
	return out
}

// sortedKeys returns the sorted keys of a string set.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scenarioHeader is the flattened file's header block: the stats a reviewer
// cross-checks against the flow coverage before reading a line of code.
type scenarioHeader struct {
	Entry     string
	Var       string
	Value     string
	Kept      int
	Dropped   int
	Unfolded  int
	PreambleN int
	Queries   []ScenarioQuery
	Residue   []string
}

// RenderScenario emits the flattened .pc for one scenario (G-SCEN3): a
// deterministic header (stats + query/tx line), then the source with
// /*L<n>*/ provenance prefixes on every kept line — preamble first, body
// second (fold markers between), UNFOLDED markers inline at residue sites.
// The rendering is a consumer view of the Scenario — never a second source
// of truth (SCEN-D6).
func RenderScenario(sc *Scenario, entry string, src []byte, irFile *ir.File) string {
	lines := bytes.Split(src, []byte("\n"))
	get := func(l int) string {
		if l < 1 || l > len(lines) {
			return ""
		}
		return string(lines[l-1])
	}
	trim := func(s string) string { return strings.TrimRight(s, " \t\r") }

	h := scenarioHeader{Entry: entry, Var: sc.Var, Value: sc.Value, Kept: sc.Counts.Kept,
		Dropped: sc.Counts.Dropped, Unfolded: sc.Counts.Unfolded, Queries: sc.Queries, Residue: sc.Residue}
	for i := 0; i+1 < len(sc.Preamble); i += 2 {
		h.PreambleN += sc.Preamble[i+1] - sc.Preamble[i] + 1
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "/* scenario: %s == '%s'               */\n", sc.Var, sc.Value)
	fmt.Fprintf(&sb, "/* source:  %s  %s   */\n", entry, filepath.Base(pathOf(irFile, entry)))
	fmt.Fprintf(&sb, "/* kept:    %d nodes (preamble %d lines + body)        */\n", h.Kept, h.PreambleN)
	fmt.Fprintf(&sb, "/* dropped: %d nodes (contradicted branches)           */\n", h.Dropped)
	fmt.Fprintf(&sb, "/* unfolded: %d predicates — LOUD, verify manually     */\n", h.Unfolded)
	// Query/tx line: the scenario's db surface at a glance.
	if len(sc.Queries) > 0 {
		var ids, txIDs []string
		for _, q := range sc.Queries {
			ids = append(ids, q.ID)
			if q.Tx {
				txIDs = append(txIDs, q.ID)
			}
		}
		if len(txIDs) > 0 {
			fmt.Fprintf(&sb, "/* queries: %d (%s) — TX: %s */\n", len(ids), strings.Join(ids, " "), strings.Join(txIDs, " "))
		} else {
			fmt.Fprintf(&sb, "/* queries: %d (%s) — non-tx (wrapper/autocommit) */\n", len(ids), strings.Join(ids, " "))
		}
	} else {
		sb.WriteString("/* queries: none */\n")
	}
	for _, r := range sc.Residue {
		fmt.Fprintf(&sb, "/* UNFOLDED: %s */\n", r)
	}
	sb.WriteString("\n")

	// Provenance emission: preamble lines, boundary comment, body lines —
	// all in original order with /*L<n>*/ prefixes; fold annotations ride
	// the branch header lines. Chain siblings share boundary lines, so the
	// emitted set dedups (a line renders once).
	ann := map[int]string{}
	var annotate func(nodes []*SliceNode)
	annotate = func(nodes []*SliceNode) {
		for _, sn := range nodes {
			switch sn.Fold {
			case FoldSatisfied:
				ann[sn.Line] = " /* folded: " + oneLineC(sn.Cond) + " (holds) */"
			case FoldMixed:
				ann[sn.Line] = " /* folded: " + oneLineC(sn.Cond) + " → keep: " + sn.FoldedCond + " */"
			case FoldUnrecognized:
				ann[sn.Line] = " /* UNFOLDED — verify manually */"
			}
			annotate(sn.Children)
		}
	}
	annotate(sc.Body)
	emitted := map[int]bool{}
	emit := func(from, to int) {
		for l := from; l <= to; l++ {
			if l < 1 || l > len(lines) || emitted[l] {
				continue
			}
			emitted[l] = true
			fmt.Fprintf(&sb, "/*L%d*/%s\n", l, trim(get(l)))
		}
	}
	if len(sc.Preamble) > 0 {
		sb.WriteString("/* ---- preamble (shared init) ---- */\n")
		for i := 0; i+1 < len(sc.Preamble); i += 2 {
			emit(sc.Preamble[i], sc.Preamble[i+1])
		}
		sb.WriteString("\n/* ---- body under " + sc.Var + " == '" + sc.Value + "' ---- */\n")
	}
	for _, l := range bodyLines(sc) {
		if emitted[l] {
			continue
		}
		emitted[l] = true
		fmt.Fprintf(&sb, "/*L%d*/%s%s\n", l, trim(get(l)), ann[l])
	}
	return sb.String()
}

// bodyLines lists the scenario body's kept original line numbers in
// ascending order (slice spans minus the dropped subtrees' lines — a
// satisfied parent's span encloses its dropped children).
func bodyLines(sc *Scenario) []int {
	dropped := map[int]bool{}
	for _, l := range sc.Counts.DroppedLines {
		dropped[l] = true
	}
	seen := map[int]bool{}
	var out []int
	add := func(from, to int) {
		for l := from; l <= to; l++ {
			if !seen[l] && !dropped[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	var walk func(nodes []*SliceNode)
	walk = func(nodes []*SliceNode) {
		for _, sn := range nodes {
			add(sn.Line, sn.EndLine)
			walk(sn.Children)
		}
	}
	walk(sc.Body)
	sort.Ints(out)
	return out
}

// pathOf is a nil-safe entry-file name (the renderer header's source hint).
func pathOf(f *ir.File, entry string) string {
	if f == nil || f.Path == "" {
		return entry + ".pc"
	}
	return f.Path
}

// nodeRec is one kept node's line-ordered record for the response-value
// pass (SCEN-D9): the original text and the first query id it carries.
type nodeRec struct {
	line    int
	kind    Kind
	text    string
	sub     string
	queryID string
}

// resolveResponses implements SCEN-D9: for every non-error response add in
// the slice, the field's value is the last surviving write of its source
// variable before the add line (query results count via their SQL node);
// a kept later write marks the field unstable (loud). Dropped-branch
// rewrites — the mutual-exclusion ladders — never participate.
func resolveResponses(sc *Scenario, tree *Tree, recs []nodeRec) []ScenarioResponse {
	type addRec struct {
		line          int
		field, target string
		buf           string
	}
	var adds []addRec
	seen := map[string]bool{}
	var collect func(n *Node)
	collect = func(n *Node) {
		for _, op := range n.FmlOps {
			if op.Kind != ir.FmlAdd || op.Error || op.Target == "" {
				continue
			}
			// Response writes: Obuffer only (the endpoint's output). Sbuffer
			// adds are tpcall request payloads — TPCalls already own them.
			if !strings.HasSuffix(op.Buffer, "Obuffer") {
				continue
			}
			if op.Line <= 0 || !keptLine(sc, op.Line) {
				continue
			}
			key := fmt.Sprintf("%d|%s|%s", op.Line, op.Field, op.Target)
			if seen[key] {
				continue // annotate attaches ops to every containing node
			}
			seen[key] = true
			adds = append(adds, addRec{line: op.Line, field: op.Field, target: op.Target, buf: op.Buffer})
		}
		for _, c := range n.Children {
			collect(c)
		}
	}
	for _, r := range tree.Root {
		collect(r)
	}
	sort.SliceStable(adds, func(i, j int) bool { return adds[i].line < adds[j].line })

	sort.SliceStable(recs, func(i, j int) bool { return recs[i].line < recs[j].line })
	out := make([]ScenarioResponse, 0, len(adds))
	// Fget32 fills variables too — an FML read into var is a write whose
	// value is the request field (the value-flow the controller follows).
	var gets []struct {
		line          int
		field, target string
	}
	seenGet := map[string]bool{}
	var collectGets func(n *Node)
	collectGets = func(n *Node) {
		for _, op := range n.FmlOps {
			if op.Kind != ir.FmlGet || op.Target == "" || op.Line <= 0 || !keptLine(sc, op.Line) {
				continue
			}
			key := fmt.Sprintf("%d|%s", op.Line, op.Target)
			if !seenGet[key] {
				seenGet[key] = true
				gets = append(gets, struct {
					line          int
					field, target string
				}{op.Line, op.Field, op.Target})
			}
		}
		for _, c := range n.Children {
			collectGets(c)
		}
	}
	for _, r := range tree.Root {
		collectGets(r)
	}
	for _, a := range adds {
		resp := ScenarioResponse{
			Field:  a.field,
			Buffer: bufferTail(a.buf),
			Var:    a.target, Line: a.line, Stable: true,
		}
		// Merge the write sources into one line-ordered stream: kept node
		// texts (assignments/INTO/strcpy/MEMSET) and Fget32 fills — the
		// nearest preceding write resolves the value (SCEN-D9).
		var wlines []int
		var lastGet string
		for _, g := range gets {
			if g.line < a.line && g.target == a.target {
				wlines = append(wlines, g.line)
			}
		}
		for _, rec := range recs {
			kind, _ := writeOf(rec.text, a.target)
			if kind != "" && rec.line < a.line {
				wlines = append(wlines, rec.line)
			}
		}
		// Stability first (SCEN-D9): a kept later write outside an
		// exclusive-branch ladder marks the field unstable. Mutation must
		// precede the append — append copies by value.
		for _, rec := range recs {
			kind, _ := writeOf(rec.text, a.target)
			if kind != "" && rec.line > a.line && !exclusiveBranch(tree, a.line, rec.line) {
				resp.Stable = false
				sc.Residue = append(sc.Residue,
					fmt.Sprintf("response %s (&%s) rewritten after add at L%d (write at L%d)", a.field, a.target, a.line, rec.line))
			}
		}
		if len(wlines) > 0 {
			sort.Ints(wlines)
			last := wlines[len(wlines)-1]
			for _, g := range gets {
				if g.line == last {
					lastGet = g.field
				}
			}
			if lastGet != "" {
				resp.Value = "request " + lastGet
			} else {
				for _, rec := range recs {
					if rec.line != last {
						continue
					}
					if kind, rhs := writeOf(rec.text, a.target); kind != "" {
						resp.Value = valueOf(kind, rhs, rec)
					}
				}
			}
		}
		out = append(out, resp)
	}
	return out
}

// depthOf counts ancestor hops (root children are depth 0).
func depthOf(n *Node, parents map[*Node]*Node) int {
	c := 0
	for parents[n] != nil {
		n = parents[n]
		c++
	}
	return c
}

// exclusiveBranch reports whether two lines sit in mutually exclusive
// branches of one if/else-if/else chain (the corpus's ladder idiom — each
// scenario sees exactly one value, so a cross-branch rewrite is not
// unstable). The flow tree keeps chains as siblings; exclusive = the
// branch containing each line hangs from the same chain head, and the
// lines are not in the same branch.
func exclusiveBranch(tree *Tree, lineA, lineB int) bool {
	// Synthetic root: tree.Root's children hang from one virtual parent so
	// the climb has a single terminus.
	root := &Node{Kind: KindBranch, Sub: "if", Line: 0, EndLine: 1 << 30, Children: append([]*Node(nil), tree.Root...)}
	parents := map[*Node]*Node{}
	var build func(n *Node)
	build = func(n *Node) {
		for _, c := range n.Children {
			parents[c] = n
			build(c)
		}
	}
	build(root)
	deepest := func(line int) *Node {
		var best *Node
		var visit func(n *Node)
		visit = func(n *Node) {
			if n.Line <= line && line <= n.EndLine {
				best = n
			}
			for _, c := range n.Children {
				visit(c)
			}
		}
		for _, r := range tree.Root {
			visit(r)
		}
		return best
	}
	na, nw := deepest(lineA), deepest(lineB)
	if na == nil || nw == nil || na == nw {
		return false // same branch: a same-path rewrite
	}
	// Climb to the common ancestor level; compare the branch children.
	for da, dw := depthOf(na, parents), depthOf(nw, parents); da != dw; {
		if da > dw {
			na = parents[na]
			da--
		} else {
			nw = parents[nw]
			dw--
		}
	}
	for parents[na] != parents[nw] {
		na = parents[na]
		nw = parents[nw]
		if parents[na] == nil || parents[nw] == nil {
			return false
		}
	}
	pa := parents[na]
	if pa == nil {
		return false
	}
	if na == nw {
		return false
	}
	if na.Kind != KindBranch || nw.Kind != KindBranch {
		return false // a plain sibling statement is sequential, not exclusive
	}
	// Chain heads: a member climbs to the `if` its chain hangs from; a
	// standalone `if` (or unrelated branch kind) is its own head.
	head := func(n *Node) *Node {
		for n.Sub == "elseif" || n.Sub == "else" {
			idx := -1
			for i, c := range pa.Children {
				if c == n {
					idx = i
					break
				}
			}
			if idx <= 0 {
				break
			}
			prev := pa.Children[idx-1]
			if prev.Kind == KindBranch && (prev.Sub == "if" || prev.Sub == "elseif") {
				n = prev
				continue
			}
			break
		}
		return n
	}
	return head(na) == head(nw)
}

// bufferTail renders the buffer variable's role tail (ptr_fml_Obuffer →
// Obuffer) — the PF-4.1 tail-segment convention, one home here for the
// scenario layer's rendering.
func bufferTail(buf string) string {
	if i := strings.LastIndex(buf, "_"); i >= 0 && i+1 < len(buf) {
		return buf[i+1:]
	}
	return buf
}

// keptLine reports whether a 1-based line survives in the slice (body spans
// plus the preamble).
func keptLine(sc *Scenario, line int) bool {
	var walk func(nodes []*SliceNode) bool
	walk = func(nodes []*SliceNode) bool {
		for _, sn := range nodes {
			if line >= sn.Line && line <= sn.EndLine {
				return true
			}
			if walk(sn.Children) {
				return true
			}
		}
		return false
	}
	if walk(sc.Body) {
		return true
	}
	for i := 0; i+1 < len(sc.Preamble); i += 2 {
		if line >= sc.Preamble[i] && line <= sc.Preamble[i+1] {
			return true
		}
	}
	return false
}

// writeOf classifies node text as a write of var: "assign" (var = rhs),
// "into" (SQL INTO :var), "strcpy", "memset" — "" when not a write. String
// literals are stripped for matching (a userlog format like "c_usr_id =
// :%s:" is not an assignment) but RHS values read from the ORIGINAL text —
// the stripped copy keeps byte offsets (same length).
func writeOf(text, varName string) (string, string) {
	if text == "" || varName == "" {
		return "", ""
	}
	stripped := stripStrings(text)
	base := varName
	if i := strings.Index(base, "."); i > 0 {
		base = base[:i]
	}
	qm := regexp.QuoteMeta(base)
	// `var =` assignments (Go's RE2 has no lookahead — the == / != forms
	// are disambiguated by peeking the matched tail).
	for _, loc := range regexp.MustCompile(`\b`+qm+`(\.arr)?\s*=`).FindAllStringIndex(stripped, -1) {
		if loc[1] < len(stripped) && stripped[loc[1]] == '=' {
			continue // == / != comparison, not a write
		}
		rhs := strings.TrimSpace(text[loc[1]:])
		if i := strings.Index(rhs, ";"); i >= 0 {
			rhs = rhs[:i]
		}
		return "assign", strings.TrimSpace(rhs)
	}
	if regexp.MustCompile(`(?i)\bINTO\s+:` + qm + `\b`).MatchString(stripped) {
		return "into", ""
	}
	if m := regexp.MustCompile(`\bstrcpy\s*\(\s*` + qm + `[^,]*,\s*(.+?)\)\s*;?`).FindStringSubmatchIndex(stripped); m != nil {
		return "strcpy", strings.TrimSpace(text[m[2]:m[3]])
	}
	if regexp.MustCompile(`\bMEMSET\s*\(\s*` + qm + `\b`).MatchString(stripped) {
		return "memset", ""
	}
	return "", ""
}

// stripStrings blanks C string literal contents (newlines preserved) so
// write-pattern matching sees only code.
func stripStrings(s string) string {
	var sb strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr && c == '\\' && i+1 < len(s):
			i++
			continue
		case c == '"':
			inStr = !inStr
			sb.WriteByte(c)
		case inStr:
			sb.WriteByte('x')
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// valueOf renders a write's resolved value per SCEN-D9: queries surface
// their SQL source id, assignments their RHS text, MEMSET the zero state.
func valueOf(kind, rhs string, rec nodeRec) string {
	switch kind {
	case "into":
		if rec.queryID != "" {
			return "query " + rec.queryID
		}
		return "query"
	case "assign":
		if rhs != "" {
			return rhs
		}
		return rec.text
	case "strcpy":
		return rhs
	case "memset":
		return "(zeroed)"
	}
	return rhs
}

// ScenarioCondition synthesizes the condition-shaped value the plan/gen
// machinery consumes for a scenario endpoint (SCEN-5/6): Index 0 (the
// synthesized-condition contract — never collides with the 1-based
// inventory), full FML ops harvested from the kept nodes (preamble reads
// included — the request contract needs them; error adds flagged for
// contract exclusion), query IDs from the scenario census, and the span
// from the first to the last kept body line. Deterministic and identical
// wherever plan and gen re-derive it.
func ScenarioCondition(sc *Scenario, tree *Tree) *ir.Condition {
	kept := keptLineSet(sc, tree)
	c := &ir.Condition{
		Index:     0,
		Kind:      "scenario",
		Expr:      sc.Key,
		StartLine: sc.BodyExtent()[0],
		EndLine:   sc.BodyExtent()[1],
	}
	seen := map[string]bool{}
	var visit func(n *Node)
	visit = func(n *Node) {
		if !kept[n.Line] {
			return
		}
		for _, op := range n.FmlOps {
			if op.Line > 0 && (op.Line < n.Line || op.Line > n.EndLine) {
				continue
			}
			key := string(op.Kind) + "|" + op.Field
			if seen[key] {
				continue
			}
			seen[key] = true
			if isErrorAdd(op, n) {
				op.Error = true
			}
			c.FmlOps = append(c.FmlOps, op)
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	for _, n := range tree.Root {
		visit(n)
	}
	for _, q := range sc.Queries {
		c.QueryIDs = append(c.QueryIDs, q.ID)
	}
	return c
}

// keptLineSet maps every line of the scenario's surviving spans — preamble
// plus body — to true (the scenario-side mirror of the census walk).
func keptLineSet(sc *Scenario, tree *Tree) map[int]bool {
	kept := map[int]bool{}
	var walkSpans func(nodes []*SliceNode)
	walkSpans = func(nodes []*SliceNode) {
		for _, sn := range nodes {
			for l := sn.Line; l <= sn.EndLine; l++ {
				kept[l] = true
			}
			walkSpans(sn.Children)
		}
	}
	walkSpans(sc.Body)
	for i := 0; i+1 < len(sc.Preamble); i += 2 {
		for l := sc.Preamble[i]; l <= sc.Preamble[i+1]; l++ {
			kept[l] = true
		}
	}
	return kept
}

// KeptLines maps every source line the scenario slice keeps (preamble plus
// surviving body spans) — the line-level truth plan's endpoint-coverage
// check consumes (an arm is covered when its header line survives in some
// endpoint's slice).
func KeptLines(sc *Scenario, tree *Tree) map[int]bool {
	return keptLineSet(sc, tree)
}

// BodyExtent returns the scenario body's inclusive [first, last] kept line
// (0 when the body is empty — the fallback scenario over an empty tree).
func (sc *Scenario) BodyExtent() [2]int {
	lo, hi := 0, 0
	var walk func(nodes []*SliceNode)
	walk = func(nodes []*SliceNode) {
		for _, sn := range nodes {
			if lo == 0 || sn.Line < lo {
				lo = sn.Line
			}
			if sn.EndLine > hi {
				hi = sn.EndLine
			}
			walk(sn.Children)
		}
	}
	walk(sc.Body)
	return [2]int{lo, hi}
}

// ScenarioSource renders the scenario's kept lines as one code slice — the
// controller prompt's deterministic base (G-SCEN6): preamble first, then
// body, original line order, kept lines verbatim (no provenance prefixes —
// the /*L<n>*/ rendering is the human artifact, not the prompt payload).
// It returns the slice text plus, per reachable query, the inclusive
// [start,end] line range of the query's SQL region within the slice —
// the replacement coordinates budget.ReplaceQueries consumes. A query's
// region merges every kept SQL node carrying its id (flattened cursors
// replace DECLARE..CLOSE as one unit, matching the whole-file path).
func ScenarioSource(sc *Scenario, tree *Tree, src []byte) (string, map[string][2]int) {
	kept := keptLineSet(sc, tree)
	var out []int
	seen := map[int]bool{}
	add := func(from, to int) {
		for l := from; l <= to; l++ {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	for i := 0; i+1 < len(sc.Preamble); i += 2 {
		add(sc.Preamble[i], sc.Preamble[i+1])
	}
	for _, l := range bodyLines(sc) {
		add(l, l)
	}
	sort.Ints(out)

	lines := bytes.Split(src, []byte("\n"))
	text := make([]string, 0, len(out))
	idx := make(map[int]int, len(out))
	for _, l := range out {
		if l < 1 || l > len(lines) {
			continue
		}
		idx[l] = len(text) + 1
		text = append(text, strings.TrimRight(string(lines[l-1]), "\r"))
	}

	// SQL regions: walk the original tree over the kept spans, merging every
	// kept SQL node's span per query id.
	regions := map[string][2]int{}
	var visit func(n *Node)
	visit = func(n *Node) {
		if !kept[n.Line] {
			return
		}
		if n.Kind == KindSQL {
			for _, q := range n.QueryIDs {
				r := regions[q]
				if r[0] == 0 || n.Line < r[0] {
					r[0] = n.Line
				}
				if n.EndLine > r[1] {
					r[1] = n.EndLine
				}
				regions[q] = r
			}
		}
		for _, child := range n.Children {
			visit(child)
		}
	}
	for _, n := range tree.Root {
		visit(n)
	}
	slices := make(map[string][2]int, len(regions))
	for q, r := range regions {
		s, e := idx[r[0]], idx[r[1]]
		if s == 0 || e < s {
			continue // region boundaries outside the kept set — not replaceable
		}
		slices[q] = [2]int{s, e}
	}
	return strings.Join(text, "\n"), slices
}
