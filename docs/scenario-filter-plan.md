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

## 8. Recon verification + implementation notes (2026-09-21, `feat/scenario-filter`)

Verified against `main` = `140fefe`. Anchors re-grepped; the plan's line
numbers hold for the core functions, drifted for the scenario emitters:

| Plan anchor | Actual | Note |
|---|---|---|
| `DispatchAxisFor` :147 | :147 | matches (package func; tree method :521) |
| `pickAxis` :328 | :328 | matches (`betterDirectAxis` :299) |
| `ScenarioFor` :659 | :659 | matches |
| `Scenarios` :804 | :804 | matches |
| `foldExpr` :897 / `foldCmp` :1004 / `equalsFold` :1069 | same | matches |
| `censusOf` :1437 | :1357 | drifted |
| `RenderScenario` :1566 | :1598 | drifted |
| `ScenarioSource` :2188 | :2333 | drifted |
| `BodyExtent` :2300 | :2300 | matches |

Other verified facts:

* `DispatchAxis` is not JSON-marshaled anywhere today (no tags, no
  consumer) — adding `GuardLines`/`Kind` + tags is safe.
* `facts.Branches` carries `NestDepth` (enclosing braced if/else-if
  count) per guard — the primary/secondary classifier input already
  exists, no new scan.
* Endpoint resolution has exactly three homes: `plan.resolveEp`
  (`internal/plan/plan.go:210`), `csplan/plan.go:183`, plus the draft
  paths (`cmd/tuxconv/discover.go:127`, `internal/csdraft/draft.go:63`).
* Scenario artifacts are written by `cmd/tuxconv/scenarios.go:46`
  `scenarioArtifacts` (called from `discover.go:154`); the axes twin
  hangs there, same dir (`config.DefaultScenDir`), same collision
  discipline.
* `pred.Parse` already accepts the v1 filter grammar (`||` < `&&` <
  `==/!=` < `!`, parens, quotes, literals); validation is additive
  (Raw / unsupported-op / non-axis-atom rejects), not a new parser.

### Design refinements (decisions taken; deviations from §3/§4 marked)

1. **`AxesFor` is a *view* over a shared harvest, not a second
   detector.** Extract `DispatchAxisFor`'s passes 1–2 into
   `harvestAxes`; `candidates()` ranks every qualifying stats entry.
   `DispatchAxisFor` keeps its exact recognizer cascade and is forced to
   rank 0 by `Tree.AxesFor` (structural guarantee, not a re-derivation).
   Registry entries need `len(Domain) >= 2`; the cascade quirk where a
   1-value class-1 winner returns nil is preserved for
   `DispatchAxisFor` and documented for `AxesFor` (rank 0 falls through
   to the next class only in the `AxesFor` view).
2. **Dedupe by `Key()`** (scenario-key identifier), highest class wins:
   the normalize axis `ref=sql_x.arr/alias=x` and a char-compare
   candidate `ident=x` are one logical axis. Two genuinely different
   refs collapsing onto one Key is not a corpus shape; documented.
3. **Deterministic alias pick.** `pickAxis`'s normalize alias max is
   today chosen by map iteration (random on equal link counts) — fix to
   lexicographic tie-break while extracting; covered by the ranking
   test (repeat-run byte equality).
4. **`Kind` via guard nesting, not rank alone**: rank 0 = `primary`; a
   candidate whose minimal guard `NestDepth` exceeds the primary's is
   `secondary`; a top-level sibling chain stays `primary`.
5. **Class-2 merge order**: refs + symbs candidates sorted by (domain
   len desc, `Sites` desc, `RefName` asc), stable refs-first — provably
   reproduces `betterDirectAxis`'s winner as the first element.
6. **Chain semantics under `||` (refines "reuse the walk unchanged").**
   Per matching assignment the existing `taken` rule applies; an arm is
   dropped only when unreachable under *every* matching assignment, and
   the else arm survives iff ∃ a matching assignment with no earlier
   provably-true arm. The single-value case degenerates to today's walk
   exactly (`F||I` over `if F / elseif I / else` drops the else).
   `satisfied` requires true under all matching assignments; `mixed`
   keeps the predicate verbatim (never axis-stripped) — sound at
   runtime, and what "keeps both arms with their guards" requires.
7. **No new mapping-validation state**: `scenarioFilter` joins the
   exactly-one-of set in `mapping.Validate`; value/variable existence
   defers to `resolveEp` (same as `scenarioRef`) so the registry's
   domains are checked against the file that actually dispatches.

### Work checklist

- [x] Recon + verified anchors (this section)
- [x] **S1** `flow: harvestAxes` extraction + `Tree.AxesFor` ranking +
      `GuardLines`/`Kind` fields + tests (rank[0] ≡ `DispatchAxisFor`,
      nested-secondary fixture, repeat-run determinism, no-axis) —
      `internal/flow/axes.go`, `axes_test.go`
- [x] **S2** axes artifact (`scenarios/<entry>.axes.{json,md}`) written
      by discover next to the scenario set + tests —
      `axes_report.go`, `cmd/tuxconv/scenarios.go:axesArtifacts`,
      `discover.go` wiring. Verified on `testdata/fixtures/nav`:
      rank 0 `c_flag` [F,H,I] + `default`, rank 1 secondary
      `c_demo_active_flg` [N,Y] (the `c_flag == 'H' &&
      c_demo_active_flg == 'Y'` intersection case, live)
- [x] **S3** `foldUnder`: the fold was generalized to assumption sets
      (`foldAssumptions` in `scenario.go` — one implementation for
      `ScenarioFor` and the filter) + `ScenarioForFilter`
      (`internal/flow/scenariofilter.go`) + tests. Decisions taken:
      per-assignment `taken` (an arm true under one assignment only is
      still the chain's match for it — required for `F||I` to drop the
      else); the preamble split scans the whole subtree
      (`nodeTouchesAny`), so a secondary-only filter keeps the outer
      chain live instead of emitting an empty body; `mixed` keeps the
      predicate verbatim (never axis-stripped). Verified on
      `testdata/fixtures/nav`: `F||I` keeps both guards + drops
      H/default; `F && Y` / `I && Y` fold the real co-occurrences;
      `H && Y` rejects loudly (Y lives only under F/I — the
      reachability prune); `c_demo_active_flg == 'Y'` alone keeps the
      outer dispatch chain live, kept 219
- [x] **S4** `plan`: `Endpoint.scenarioFilter` + `Validate` (syntax at
      load, like scenarioRef) + `resolveEp` branch (+ `RefOrIndex`) +
      `csplan` mirror (mapping + resolveEp + KeptLines coverage with the
      shared-brace probe) + tests. Also the minimal gen plumbing a filter
      endpoint needs to function: `gen.scenarioOf`/`ScenarioOf`/
      `conditionOf` recognize the key, and `convert.Run` builds the
      scenario context for it. `ainames`/`mappingpatch` know the ref kind.
      Verified with `convertgo testdata/fixtures/nav/… -mapping
      <filter> -no-llm`: filter resolves, arms F/I covered (H/default
      advisory), 8–9 files staged, 0 sql deviations. **Follow-up found
      (deterministic-only path):** `gen.DeterministicControllerBody`
      reads the raw branch span (`detBranchSource`, `deterministic.go:119`),
      not the folded slice, so a merged `F||I` body can trip its
      unused-local gate and skip the unit (loudly). The LLM path uses the
      folded scenario view; the no-llm synthesizer should too (or gate on
      the slice). Not S4 — track as S6b
- [x] **S5** `discover`: commented examples from the registry +
      `--list-axes`/`--filter` preview + honest kept-block comments +
      merged `Key` (`c_flag in {F,I}`) + tests —
      `cmd/tuxconv/discoverpreview.go` + `discoverpreview_test.go`,
      `flow.KeptBlocks`/`KeptBlockLines`. `--list-axes` prints rank/key/
      kind/ref/alias/domain(default)/sites/guards; `--filter "<expr>"`
      previews the merged key, matched/pruned assignments, honest kept
      blocks, fold counts, census, queries, tx, residue — read-only
      (no drafts, no artifacts, no LLM), and the pair carries the
      `commandFlags["discover"]` value entry so `--filter <expr>` splits
      correctly. Drafts gain 1–2 fold-verified commented examples (primary
      union + first reachable primary×secondary intersection) with
      matched-values preview, through the shared `flow.FilterExamples`
      helper that the CS draft (`discover -target cs`, `csdraft.Render`)
      uses too. Merged keys/artifact naming came with S3's
      `filterScenarioIdentity`; the extent claim itself is untouched for
      scenarioRef entries (acceptance gate: honest blocks replace it only
      where a filter is mapped — preview + examples), so existing drafts
      stay byte-identical
- [x] **S6** convert/audit: prompt carries filter + matched/pruned combos +
      residuals; docs; full `go test ./...` + corpus smoke. `Scenario`
      carries `filter`/`filter_matched`/`filter_pruned` (additive JSON);
      `scenPrompt` + `writePromptFacts` render the filter line and every
      unfold residue; the CS twin carries the same facts through
      `csplan.EndpointPlan` → `csgen.EpData` → the seam prompt. The audit
      exchange archives the assembled prompt, so the evidence rides the
      audit trail unchanged. README documents the 4th key and the preview
      loop
- [x] **S6b** deterministic no-llm synthesizer. Root cause turned out to be
      narrower than the S4 note guessed: `detEmitShaping` kept only the
      **first** scalar capture, so any slice reaching two COUNT reads
      (e.g. merged F||I, each arm with its own count) declared the second
      capture and never used it — `unusedLocalErrs` then skipped the unit
      loudly. `detEmitShaping` now handles every scalar (first by-position
      guess, the rest `_ = capture` + TODO), pinned by a merged-filter
      synthesis test; the nav F||I `-no-llm` run no longer skips
      `GetMfNavCombined`

Also fixed while testing S5: the union witness attribution was not
reach-gated, so `(H && K) || (default && K)` cross-witnessed `K` for the H
assignment through the default arm's nested chain and folded H as if K
lived there. `filterFold.walk`/`foldArm` now thread a per-assignment reach
mask: fold verdicts stay global (union arms keep their guards live, the
pinned S3 rule), while witness evidence and chain-exclusivity marks are
limited to assignments that actually reach the guard — matching §6
refinement 6's "per matching assignment the existing `taken` rule
applies". Covered by `TestScenarioForFilterUnionWitnessNoCrossTalk`.

Real-corpus verification (`tuxExamples/mainTux.pc`, `F||I`) surfaced two
CS-side gaps the merged arms expose: two distinct queries whose SQL starts
from the same table derived one `DefaultQueryName` (duplicate
`GetMFCOMPANIESQuery` consts), and the OracleParameter gate counted a bind
name shared by the two methods file-wide. `csplan.QueryNamesInOrder` +
`assignUniqueQueryNames` now suffix collisions over the *planned* query
order, so a single-arm mapping keeps its names (bytes-identical on
mainTux's scenarioRef path) while merged arms get unique ones; the
discover draft derives the same order from its slices, so emitted
dbMethods pins and the unpinned fallback agree. `EndpointPlan.QueryIDs`
dedupes canonical IDs (an F and an I site of one query unit is one const),
and `cschk.countOracleParams` counts inside each const's own method. The
full real round-trip (`discover -target cs` → `convertcs`) is clean: 0 sql
deviations, 0 structural issues; the Go real run lands the combined
controller body with both arms' store calls.

Open follow-up **S7** (found by a real OpenRouter run on mainTux F||I):
the accepted merged-filter body kept the `c_flag == "I"` runtime guard but
dropped the `c_flag == "F"` one, and no gate checks mixed-guard retention
for scenario endpoints — `conditionPresenceErrs` is wired only to the
census-draft path and matches on a shared identifier, which two
value-guards of one axis satisfy loosely. The filtered slice's
`FoldMixed` guards are the runtime dispatch contract, so this needs a
scenario-guard presence gate (skeleton + literal per mixed node) and/or
prompt hardening. The deterministic `-no-llm` body stays flat by design
(best-effort, LLM resume upgrades) and does not encode dispatch either.

Acceptance gates: S1 must be byte-neutral for every existing consumer
(`go test ./internal/flow ./internal/plan ./internal/gen` green with no
golden diff); S3's single-value equivalence is a pinned property test;
S5's regenerated artifacts replace the extent claim only where a filter
is mapped.
