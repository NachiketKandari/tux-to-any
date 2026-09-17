# Scenario filter plan (`scenarioFilter`)

Executor notes: this plan is self-contained. It adds a 4th endpoint key
`scenarioFilter` (boolean over scenario variables) alongside
`condition | conditionRef | scenarioRef`, plus the multi-axis registry it
needs for intersections like `c_flag == 'H' && new_flag == 'K'`.
Line numbers as of the `scenarioFilter` discussion; re-grep if drifted.
Additive schema only — existing `scenarioRef` mappings stay byte-identical.

## 0. Evidence (do not re-derive)

* Mapping generator = `cmd/tuxconv/discover.go:382 renderScenarioDraft` +
  `internal/flow/scenario.go:804 Scenarios` / `:659 ScenarioFor` +
  `internal/flow/scenariodiff.go:49 DiffScenarios`. CS twin:
  `internal/csdraft/draft.go:80`.
* `kept lines X-Y` in drafts is `Scenario.BodyExtent()` (`scenario.go:2300`,
  used at `discover.go:411`): min `sn.Line` / max `sn.EndLine` over body
  `SliceNodes`. It is an **extent, not a kept set**: preamble excluded,
  dropped gaps included, entry `}` included (artifacts stop at 981,
  claim says 982). Example `mappings/mainTux.mapping.yaml:18` claims
  `c_flag=F kept 352-982` (631 lines); actual body kept is
  `352..577 + 977..981` = 218 lines; 413 lines inside the claim belong to
  other arms. Truth lives in `scenarios/SVC_MF_NAV_LIST.shared.json`
  (`unique F:352-577, H:180-350, I:581-807, default:810-974`,
  shared `74-89,91-130,132-141,145-179,977-982`) and the `/*Ln*/`
  prefixes in `scenarios/*.pc`.
* Slices are deterministic under `--no-llm` (two runs byte-identical):
  domain `sort.Strings` (`scenario.go:364`), tie-breaks
  (`betterDirectAxis:299`, `pickAxis:336`), census `sortedKeys`
  (`scenario.go:1437`), queries in file order. With LLM,
  `aiNameScenarios` (`discover.go:147`, `ainames_cmd.go:193`) is
  model-non-deterministic; `deterministicSuggestion` (`ainames.go:297`)
  is the deterministic fallback.
* Today `internal/plan/mapping.go:39 Endpoint` + `:157 Validate` enforce
  exactly-one-of `condition | conditionRef | scenarioRef`; resolution is
  `internal/plan/plan.go:210 resolveEp` via single-value
  `flow.ScenarioFor(t, axis, value)`. No filter field exists.
* `DispatchAxisFor` (`scenario.go:147`) harvests **all** candidates
  (`refs/idents/symbs`, `charCompareRe`, `identCompareRe`, guard sites at
  `:226`) then `pickAxis:328` discards all but one `best`. That discard
  is the single-axis bottleneck blocking `&&` across vars.
* `pred.Parse` (`internal/pred/pred.go:54`) already parses the wanted
  grammar (`||` < `&&` < `==/!=` < `!`, parens/calls/literals); `foldExpr`
  (`scenario.go:897`) + `foldCmp:1004` + `equalsFold:1069` already do
  `==/!=` + `&&/||/!` tribool folding for one var.

## 1. Goal + language

One endpoint covers a filtered set of scenarios:

```yaml
endpoints:
  - name: GetMfNavCombined
    route: /get-mf-nav-combined
    scenarioFilter: "c_flag == 'F' || c_flag == 'I'"
  - name: GetMfHistK
    route: /get-mf-hist-k
    scenarioFilter: "c_flag == 'H' && new_flag == 'K'"
  - name: GetMfNotHist
    route: /get-mf-not-hist
    scenarioFilter: "c_flag != 'H' && c_flag != 'default'"
```

Language v1: idents, `'C'` / `"str"` / number literals, `==`, `!=`,
`&&`, `||`, `!`, parens. C precedence. Both quote styles via existing
`unquote`. Everything else (`>`, `<`, arithmetic, calls) is a hard,
positioned reject.

## 2. Correctness rule (load-bearing)

A filtered endpoint is a **re-fold of the tree under the filter
assumption**, never the union of separately-folded slices. Union would
unwrap both `F` and `I` arms unconditionally; re-fold keeps both arms
**with their guards** so the handler still dispatches at runtime, and
drops `H`/`default` as contradicted. Same rule generalizes to `&&`:
both conjuncts must hold; either contradicted drops the subtree.

Non-axis atoms never drop code: they select on the axis part and ride
along as a documented runtime guard (sound over-approximation).

## 3. Axis registry (the `&&`-across-vars prerequisite)

* Split harvest (`scenario.go:164-251`) out of `DispatchAxisFor`; add
  `flow.AxesFor(...) []*DispatchAxis` returning **all qualifying** axes
  (same `guardSites >= 2`, `len(domain) >= 2` rubric), ranked
  deterministically (weight → sites → lex name). Rank `[0]` must equal
  today's `DispatchAxisFor` — zero golden churn.
* Per axis keep `Ref, RefName, Alias, Domain, Sites, HasDefault` + new
  `GuardLines []int`, `Kind` (`primary` = top dispatch chain,
  `secondary` = nested inside one arm, e.g. `new_flag` inside `H`).
* Track as artifact twin: `scenarios/<entry>.axes.json` + human
  `.axes.md` (var, domain, sites, guard lines, primary/secondary,
  example guard). Discover preview, mapping validation, and error
  suggestions read from this registry.

## 4. Filter semantics

* Parse with `pred.Parse`; reject `Raw`, non-`==`/`!=` ops,
  non-`ident ==/!= literal` atoms, with atom + position in the error.
* Generalize `foldExpr(e, ref, alias, value)` → `foldUnder(e,
  assumptions, axes)` where `assumptions` is `map[var]value`.
  Single-axis `ScenarioFor` becomes the 1-entry case.
* New `flow.ScenarioForFilter(tree, axes, filterExpr)`:
  * `&&` = intersection (combined env), `||` = union (OR assumption,
    guards kept), `!=`/`!`/parens via tribool composition.
  * Satisfied iff true under **all** matching assignments,
    contradicted iff false under all, else `mixed` + residual.
    Reuse the chain-exclusivity walk (`scenario.go:721`) unchanged.
  * `default` = "none of enumerated": `== 'X'` false for `default`
    unless `X` is the default key; `!= 'H'` matches `F,I,default`.
  * Unknown var/value, or filter matching no reachable lines
    (e.g. `H && K` where `K` never occurs under `H`, or
    `c_flag=='H' && c_flag=='K'`), is a hard plan error naming the
    arm/drop line — never an empty endpoint.
* Merged `Scenario.Key` canonical + sorted (`c_flag in {F,I}`);
  artifact `c_flag_F_or_I.pc` (existing collision-suffix logic in
  `cmd/tuxconv/scenarios.go:46` applies). Census/queries/tx/counts via
  existing `censusOf`, so `CarriesContract`, tx votes (`plan.go:284`),
  `deterministicSuggestion` work unchanged. Cap enumeration
  (e.g. 25 combos) so `4x3` is fine, pathological files stay loud.
* Draft/artifact comments list **true kept blocks** (unique/shared style),
  not `BodyExtent` min-max (see §0). Fix the extent comment as part of
  this feature or filter evidence inherits the lie.

## 5. UX

* Mapping is the source of truth (reproducible). `scenarioFilter`
  becomes the 4th exactly-one-of key (`mapping.go:39,157`);
  value-existence checks defer to plan build like `scenarioRef`
  (`plan.go:236`).
* Discover teaches it: keep per-value `scenarioRef`s, add 1–2 commented
  `scenarioFilter` examples built only from registry vars/values, with
  matched-values preview in the comment. No new command to learn.
* Fast loop: `discover --list-axes` + `discover --filter "<expr>"
  --stdout` preview (matched combos, kept/dropped, queries, honest
  blocks) without writing a mapping. `convert`/`validate` re-check the
  same expression.
* Errors suggest: `unknown var "new_falg" — did you mean "new_flag"
  (domain K,J)?`, `c_flag == 'X' matches nothing (domain F,H,I,default)`.

## 6. Build order

1. `flow: AxesFor` + axes artifact + tests (primary unchanged;
   secondary on nested-chain fixture).
2. `flow: foldUnder + ScenarioForFilter` + goldens (single-value ≡
   `ScenarioFor`; `F||I` keeps guards; `H&&K` intersection; empty
   intersection errors; non-axis atom never drops).
3. `plan: Endpoint.scenarioFilter` + `Validate` + `resolveEp` branch
   (+ `RefOrIndex`).
4. `discover/scenarios`: examples + `--list-axes`/`--filter` preview +
   honest block comments + merged naming.
5. Convert/audit: prompt carries filter + matched/dropped combos +
   residuals. Docs.

## 7. Risks / non-goals

* v1: no `>`, `<`, arithmetic, calls in filters.
* v1 multi-axis over the detected registry only; cross-file vars out
  of scope. Correlated conditions (two vars never independent) resolve
  by enumeration + reachability prune, surfaced in preview.
