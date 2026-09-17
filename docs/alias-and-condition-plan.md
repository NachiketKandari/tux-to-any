# Plan: sanctioned computed-column `AS` aliases + condition transparency

Researched 2026-09-18 against the maintux staged output
(`conversion_logs/_staged/maintux/`) and the pipeline internals. Scope is the
two fixes agreed on 2026-09-18; explicitly out of scope: the non-compiling
`s.fnIsD2uActive` call (sleep on it), the dead `FML_SSSN_ID` request contract
(removed later anyway), and bind-arity assertions (`WithArgs` — separate
workstream if wanted).

## Why (findings these close)

1. **Computed columns scan-fail against real Oracle.** Row db tags come from
   the INTO host vars (`STR_DESC` for a `DECODE(...)` expression), but Oracle
   returns the expression text as the column name for unaliased computed
   items — sqlx name-matching fails at runtime; sqlmock's fabricated friendly
   column names mask it.
2. **Lost condition arms.** Staged `GetMfFreed` carries an empty
   `if getDmmD2uMatchMppngMstr != 0 {}` — the legacy `cnt_d2u > 0 ||
   i_cnt_d2us > 0` merge lost its OR arm; `GetMfNav` additionally *gates*
   `fnIsD2uActive` behind `count > 0` where the legacy called it
   unconditionally. Conditions the source carries must show up in the
   converted Go.

## Workstream A — deterministic computed-column aliases

**A1. Fix the latent `rowFields` miswiring first (prerequisite).**
`internal/gen/gen.go:248-251` uses `q.Aliases` as DB tags for the first N row
fields — but `internal/ir/query.go:133` sets `Aliases` from the FROM-clause
*table* aliases (`parseTables` → `tableList`), not select-list column
aliases. Any query with a table alias would tag its first row field with the
table alias. Verified: no golden in `testdata/goldens` carries an `aliases`
key, so removing this branch has zero golden churn.

**A2. Select-list analysis in `internal/sqltext`.** New exported helpers:
- `SelectItems(sql) []string` — top-level (paren-depth-0, literal-aware)
  comma split of the SELECT list, same boundary discipline as
  `ir/query.go:topKeywordIndex` / `sqlchk/compare.go:selectItems`.
- `IsComputedItem(item)` — true when the item is not a single identifier
  (bare or `t.col` qualified): functions (`NVL`, `TO_CHAR`, `DECODE`),
  operators, `CASE`, literals, subqueries. `*`/`t.*` never aliased.

**A3. Alias naming (pure function, no registry, worker-pool safe).**
`AliasFor(service, canonicalUnitID, ordinal) → TUXC_<SERVICE>_<UNIT>_<N>`,
sanitized (non-ident bytes → `_`, so fn-namespaced ids like `fn_gene_otp:q2`
are safe), capped at **30 bytes** (Oracle identifier limit) with a
deterministic hash suffix when truncating. Uniqueness across the whole staged
run falls out of (service, canonical unit, ordinal): dedup (q5 →
`DuplicateOf`=q3) resolves to the canonical unit, so both scenario arms share
one alias set — unique, deterministic, renamed later if wanted. Deliberately
**no new config knob** (engine-wiring audit Tier-2 flags inert knobs as a
smell).

**A4. Injection point: `gen.DBMethod` (`gen.go:528-541`).** After `StripInto`
+ `CollapseBinds`, insert ` AS <alias>` before each computed item's depth-0
comma/FROM. **Alignment guard:** only when
`len(selectItems) == len(RowShape)` (the INTO list is positional); otherwise
skip injection and log loudly (`Result.Warnings`), never silently.

**A5. DB tag wiring in `rowFields` (`gen.go:235-272`).** For a computed item
at position i: `DBTag = alias` (matches what Oracle returns for the aliased
column). **Go field `Name` stays host-var-derived** — staged controllers
(`row.StrDesc.String`) and testgen's field inventory stay byte-stable; only
the db tag changes. Bare-column items keep today's tag derivation.

**A6. Fidelity / observability.** `sqlchk` already tolerates this by design —
`sqlchk/compare.go:stripSelectAlias` strips trailing aliases on *both* sides
("alias names may differ, the compared expression may not", PF-6.2), so
`0 sql deviations` holds. Add:
- a regression test proving `Compare(source, aliased)` is clean on
  corpus-style queries;
- `Result.AliasedColumns` count + one summary field
  (`cmd/tuxconv/convert.go:326 printServiceSummary`) + per-unit audit record
  `sql-aliases.json` (item → alias trail), per the repo's anti-write-only
  rule.

**A7. Downstream flows verified to need no changes:** `testgen` scans the
*written* tree (db tags via `testgen/methods.go:dbCols`, greedy
`^select\s+(.+)` regex via `dbRegex`) so aliases flow through automatically;
`budget.ReplaceQueries` only puts store calls in views; IR JSON is untouched
(gen-time transform) so IR goldens stay byte-identical. **Scope guard:** Go
path only; the helper lives in `sqltext` so `pyplan` can adopt it later.

## Workstream B — condition transparency (the lost-OR-arm class)

**B1. Census in `flow.Render` (`internal/flow/render.go`).** Extend `Render`
with `Conditions []CensusCond` collected *at the same walk* that emits
`if`/`else if` headers — zero divergence risk with the draft. Each entry:
`{Line, Cond, Skeleton, Idents, Effects, HasElse}`. `Effects` = LHS
identifiers of direct assignments in the branch body (reuse
`render.go:topLevelAssignIndex`/`splitAssign`). Exclusions mirror the
renderer's own elisions: `isRequestGuard`, `isDebugIf`, `isErrOpLoop`
(`patterns.go`), fetch-loop `SQLCODE` children, `while(1)`. Loops stay out of
scope (fetch-then-iterate consumes them); plumbing predicates (`SQLCODE`,
`Ferror32`/`FNOTPRES`, `DEBUG_*`-only) are filtered.

**B2. Rename-robust skeleton.** `pred.Expr` → canonical string: operators and
*literal values* kept, identifiers → `#`. So `cnt_d2u > 0 || i_cnt_d2us > 0`
→ `# > 0 || # > 0`, while the staged output's `getDmmD2uMatchMppngMstr != 0`
plus separate `i_cnt_d2us > 0` match neither skeleton → caught. Renames
(C ident → request field) still match.

**B3. Seam gate `conditionPresenceErrs(census, body)` + `emptyIfErrs(body)`:**
- **Empty-if: unconditional reject** (`if cond { }` with no body/else in the
  output) — deterministic, catches the exact staged shape
  (`_staged/maintux/controller/maintux.go:20-21`).
- Census condition satisfied when the body has an IfStmt whose condition is
  skeleton-equal **or** shares ≥1 identifier (both sides normalized via
  `common.CamelLowerGo` so renames pass), **and** whose body (or else-chain)
  assigns every `Effect` (LHS matched the same way). Misses feed a targeted
  retry note: *"condition at line N (`<cond>`) lost — implement the branch
  with its effect"*.
- **Hook sites:** branch path Gate (`internal/convert/pipeline.go:622-627`),
  the chunked path's combined gate (`internal/convert/chunk.go:475`,
  census passes through `chunkCtx`), optionally `fnHelperGate`
  (`pipeline.go:784`) later.
- **Degrade contract:** census runs only when `flowDraft` is non-empty (same
  additive/never-fatal semantics as the draft, `pipeline.go:1048-1066`).

**B4. Run-level surfacing.** New `Result.ConditionGaps []string` beside
`SQLDeviations` (`pipeline.go:96-111`), printed in `printServiceSummary`,
per-unit audit `condition-census.json`, and a ledger note — a census failure
after retries fails the unit loudly (conditions must be shown; the retry
budget bounds it via `MaxRetries`).

## Tests

| Package | Test |
|---|---|
| `sqltext` | SelectItems split (parens/literals/commas-in-strings), IsComputedItem, alias insertion idempotence, no-op on already-aliased items |
| `gen` | computed column → alias in SQL + db tag, Go field name unchanged; bare column unchanged; q5→q3 single alias set; fn-namespaced sanitization; >30-byte truncation; alignment-guard loud skip |
| `sqlchk` | `Compare(source, aliased)` = clean on corpus-style queries |
| `flow` | census on the OR-arm shape (contains the merged condition + effects `c_enable_d2u_flg`); guard/debug/fetch-leg exclusions |
| `convert` | staged-bug fixture (empty if + split OR) → gate errors + retry note; correctly merged body passes; renamed identifiers pass |
| e2e | `convertgo testdata/fixtures/stripped -no-llm` (4/4, 0 deviations), nav fixture, goldens regenerated only where injection fires; full `go test ./...` + adversarial sweep |

## Sequencing

1. A1+A2+A3 (pure functions + unit tests) → A4-A6 → regolden → e2e
   stripped/nav.
2. B1+B2 (flow, unit tests) → B3+B4 (gates + surfacing).
3. Re-stage `maintux` (LLM seam run) and diff against `_staged` — the
   empty-if and split-OR artifacts become gate-visible.

## Risks & guards

- LLM retry pressure is bounded by `MaxRetries`; census is strict but with
  the ident-overlap fallback so legitimate rewrites pass.
- 30-byte Oracle cap; sanitization for fn-namespaced query ids.
- WHERE-only expressions never aliased (select list only).
- Duplicate SQL (dedup) → one canonical alias set; sqlchk compares per
  method against raw source SQL — aliased text still matches (aliases
  stripped on both sides).
- Goldens: none carry aliases today; stripped fixtures with computed columns
  get regenerated goldens with explicit review.
