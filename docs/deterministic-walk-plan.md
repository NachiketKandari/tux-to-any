# Walk-Faithful Deterministic Controller Synthesis (Plan)

Status: **proposed** (2026-09-30). Feasibility verified against the code at
commit state 2026-09-30 and against `riskPipelineTest/tux.pc` + the staged
tree `conversion_logs/_staged/tux/`.
Source: design discussion (gentest LLM seam → deterministic walk), corpus
measurements below.

## Goal

Make the deterministic controller synthesizer (`internal/gen/deterministic.go`,
today best-effort with `tuxgo:TODO` escapes and an optional LLM fill) render
the legacy C **walk faithfully** — same statement order, same branches, same
loops, same field mapping — with every DB read/write going through the
converted store abstraction and every request/response field bound to the
converted models. No LLM in the controller path; unresolved facts degrade
loudly with a reason code, never a guess.

This is **not a new converter**: it is closing the provenance gap in the
existing deterministic path so the LLM seam becomes unreachable for
walk-covered functions.

## What the plan serves

1. `tuxconv convertgo` with `-no-llm` should produce controllers that are
   *faithful implementations* of the legacy walk, not best-effort scaffolds.
2. The human "make the API work" step shrinks to environment wiring (real DB,
   outbound clients), not logic reconstruction.
3. Unlocks deterministic gentest expectations for field-mapping controllers
   (same walk index, different output mode — Phase 6).

## Feasibility findings (measured 2026-09-30)

### The walk already exists and is measured

- `internal/flow/flow.go` — statement-level tree per function: `branch`,
  `loop`, `sql`, `stmt`, `decl`, `return`, `unknown`; each node carries
  `Cond`/`Predicate` (`internal/pred`), `FmlOps`, `QueryIDs`, `Calls`,
  `BufRoles`; plus a `Coverage` metric (classified vs residue).
- Corpus check (`tuxconv flow riskPipelineTest/tux.pc`): entry
  `SVC_RISK_PRFL` (lines 113–4317) is **2713/2723 code lines classified
  (99%)**; 10 unknown lines, enumerated (`856,857,1029,2690,2692,2883,2884,3131`).
- The same run emitted the idiom hints the renderer needs:
  `fetch-then-iterate` (cursor → row range), `response-fanout` (exact FML
  field lists per line), `request-guard`, `err-op-check-loop`,
  `debug-only-if` (elidable logging).
- `flow.Build` works for any function; only the CLI narrows the display to
  the entry (`cmd/tuxconv/flow.go:127` `flowTargets`). Per-helper trees are
  one call away.

### The facts for field mapping already exist

- `internal/ir/types.go`: `FmlOp{Kind(get/add), Field, Target, Buffer, Line,
  Code, Optional, Dropped, Error}`, `Query{SQL, Binds, RowShape, Tables,
  OwningFunction, CursorFlattened, …}`, `TPCall{Service, SendFML, RecvFML, …}`,
  `HostVar{Name, CType}`, `Define`.
- `internal/contract` is the unified language-neutral projection
  (`Field{FMLName, HostVar, CType, Kind, IsErr, Code}`, `QueryUnit`,
  `Endpoint{Request, Response, Queries, TPCalls}`).
- Bridges already shipped: `normHost` (`internal/gen/prompt.go:289`)
  normalizes `c_user_id` / `sql_user_id` / row columns to one key;
  `fieldFromFML` (`internal/gen/gen.go:158`) maps `FML_USR_ID → UsrId`;
  `detRequestMap` (`internal/gen/deterministic.go:411`) already maps FML GET
  targets → request fields — proven by the staged output line
  `s.store.GetUacUsrAccnts(c, request.MatchAccnt, request.UsrId)`.

### The current gap is concentrated and understood

- Staged tree `conversion_logs/_staged/tux/controller/tux.go`: **423
  `tuxgo:TODO`s**.
- **~345 (≈82%) are one pattern**: 23 endpoints × 15 helper params passing
  zeros to `FnFindRiskProfile` / `FnSaveRiskProfile`.
- Root cause: `detHelperCalls` (`deterministic.go:593`) resolves helper
  arguments **only by matching the helper's declared parameter names against
  request field Go names**. The C call site (`tux.pc:448–459`) passes locals
  and expressions —
  `fn_find_risk_profile(c_ServiceName, c_user_id, c_match_accnt, l_sssn_id,
  &sql_rps_zero_invstmnt_flg, sql_rp_prof.arr, …, &sql_rp_debt_captl, …)` —
  whose provenance is walkable:
  `tux.pc:221` `Fget32(ptr_fml_Ibuffer, FML_USR_ID, 0, c_user_id, 0)`
  → `FmlOp{get, FML_USR_ID, Target: c_user_id}` → `request.UsrId`.
- The remaining **~78** are response-shaping gaps: `getUacUsrAccnts
  (UacUsrAccnts) has no response-field match` (×22), `getUsrUserMaster` (×3),
  `getRpqmRpQuestionMaster` (×2), a few `Update/Insert … no request-field
  provenance` bind misses, and `response fields without row match` lists.
- Silent defect: `FnFindRiskProfile` renders as empty `return 0` **with no
  TODO** (staged `controller/fns.go:10–14`). Its C body is FML + `tpcall`
  with no SQL, so `helperQueryIDs` returns nothing and `detEmitFnEvents`
  emits nothing; the tail returns zero without a reason code.

### The one hard boundary

- `SVC_RISK_PRFL` (entry, all 23 endpoints) contains **0 tpcalls** and there
  is no `EXECUTE IMMEDIATE` anywhere in the corpus. The whole endpoint walk
  is SQL + FML.
- The only **2 tpcalls in the file** are `tpcall("SVC_NETWORTH", …)` inside
  the helpers `fn_find_risk_profile` (`tux.pc:4395`) and
  `fn_save_risk_profile` (`tux.pc:4566`). Those helpers cannot be *faithful*
  without an outbound-service convention (decision in Phase 4).
- Session/error plumbing (`l_sssn_id`, `errlog`, `userlog`, `SETLEN`) is
  intentionally dropped/elided today; it needs a fixed "dropped by policy"
  reason code rather than counting as unknown.

**Verdict:** fully deterministic for the SQL+FML walk (endpoints, cursor
loops, shaping, helper argument/out-param plumbing). The two tpcall helpers
need a product decision; everything else is provenance wiring over facts
that already exist.

## Design

### New `internal/walk` package — the provenance index

Input: source text, a function's `flow.Tree`, `ir.File`, the `contract`
view, the plan's `FnHelper` table. Output: a per-function scope mapping each
local/host variable to a symbolic value.

```go
type ValueKind string // request | row | expr | const | dropped | unknown

type Value struct {
    Kind   ValueKind
    Expr   string // renderable Go expression, e.g. request.UsrId
    Source string // the C expression / host var this came from
    Line   int
    Capture string // for row values: the store-call capture name
    Field   string // for row values: the row struct field
    Reason  string // for dropped/unknown: a stable reason code
}

func Index(src []byte, tree *flow.Tree, f *ir.File, svc *contract.Service, helpers []plan.FnHelper) Scope
```

Resolution order (deterministic, source order, first match wins):

1. Function parameter → caller-provided binding (set at call sites).
2. FML GET target → `request.<Field>` (`detRequestMap` generalized to the
   whole function, not just the dispatch branch).
3. SELECT INTO / row var → `<capture>.<RowField>` (models row struct, db
   tags, `normHost` alignment).
4. Assignment statements: `x = y` copy, `x = expr` expression capture
   (rendered through a small typed C-expression → Go renderer; unrenderable
   shapes get a reason code), `&x` address-of → out-param binding.
5. Buffer ops: `.arr`, `strcpy`, `SETLEN` → string/buffer handling.
6. `Define` constants, literals.
7. Session/error plumbing → `dropped` with a policy reason.
8. Anything else → `unknown` with a reason code.

Reason codes (stable, greppable): `R-ARG-UNKNOWN`, `R-EXPR-UNRENDERED`,
`R-TPCALL` (the existing R8), `R-SESSION-DROPPED`, `R-NO-ROW-SOURCE`,
`R-UNKNOWN-LINE`, `R-NO-STORE-CALLS`.

### Renderers consuming the index (in `internal/gen`)

- **Store-call args** (`detCallArgs`): provenance-first; name-match stays as
  fallback for legacy shapes.
- **Helper calls** (`detHelperCalls`): parse the call-site argument list from
  the flow/source span (respecting nesting, `&`, casts, `.arr`), bind
  positionally to `plan.FnHelper.Params`, declare caller locals for `&`
  arguments and pass their addresses; post-call reads use the same locals.
  This alone removes the ~345 TODOs.
- **Helper bodies** (`DeterministicFnHelperBody` / `detEmitFnEvents`): build a
  flow tree per helper; render branch predicates (`pred.Expr` → Go), cursor
  loops via the `fetch-then-iterate` hints, out-param writes, return tails,
  error returns. Empty results must carry `R-NO-STORE-CALLS`, never silence.
- **Response shaping** (`detRowPairs` / `detEmitShaping`): per `FmlAdd` field
  resolve value source: row match (existing fast path) → request copy →
  walk expression → constant → branch-local; provenance comment per field;
  retire fuzzy matching where the scope has an authoritative value.

### TPCall convention (Phase 4) — RESOLVED: Option A

> **Pinned 2026-10-01. See "Pinned: TPCall is Option A" below for the
> reasoning, what A still costs, and what would reopen it.** The two options
> are recorded here for the record:

- **Option A (chosen):** `tpcall` sites stay one reason-coded TODO each
  (`R-TPCALL`). Determinism claim is scoped to SQL+FML walks.
- **Option B (deferred):** from `ir.TPCall{Service, SendFML, RecvFML}` generate a typed
  outbound interface + payload structs (`NetworthClient.Send/Recv`) injected
  into the controller constructor; the walk calls it like a store call;
  tests mock it. Runtime client implementation stays out of scope.

## Phases

| Phase | Work | Acceptance (corpus) | Est. |
|---|---|---|---|
| **P0 — Instrument** | `walkreport` (new subcommand or `flow -report`): per-function coverage, resolved-symbol ratio, TODO census by reason code; baseline goldens; turn the silent-empty helper bug into `R-NO-STORE-CALLS`. | TODO count reported by reason; no behavior change; goldens byte-identical. | 0.5–1d |
| **P1 — Provenance index** | `internal/walk` + unit tests; retrofit `detCallArgs` and `detHelperCalls` (call-site arg parsing, out-param locals, session policy). | ≥90% of the 345 helper args resolve; census shows the drop; reruns byte-identical. | 2–3d |
| **P2 — Walk-body rendering** | Per-helper flow trees; branches, loops, out-params, tails, error returns; `R-NO-STORE-CALLS` instead of silence. | `FnInsertIntoUra` and the entry SQL walks render completely; silent-empty gone. | 2–3d |
| **P3 — Shaping completion** | **P3B and P3A/P3C DONE 2026-10-01; the emission redesign is NOT.** Response writes are now attributed to the read that produces them, and the two shapes of "unresolved" are split. See "Pinned: P3 landed in three parts" below. | Met for P3B/P3A/P3C: one row-source note per endpoint, and `R-RESPONSE-READ-KEPT` separated from real gaps. **Not met** for the original goal: 21 response writes still have no placement, because the append structure cannot express a field sourced by a different read than the one being iterated. | 2d + open |
| **P4 — TPCall decision** | **DONE 2026-10-01 under Option A.** Helper-scoped `R-TPCALL` emission, the code registered in the census, and the stub forced to the failure status. | Entry endpoints: 0 provenance/shaping TODOs. Helpers: 1 `R-TPCALL` each. Met: 2 sites, 2 gaps. | done |
| **P5 — Validation & determinism** | A/B vs LLM seam; byte-identical rerun check; compile gates in a real module; goldens for 2–3 fixtures; docs (`README`, `docs/`). | Same corpus `-no-llm`: only reason-coded TODOs; two runs `shasum`-identical. | 1–2d |
| **P6 — gentest evaluator (optional)** | Reuse `walk` as a symbolic evaluator: given mocked store returns, emit expected response values for tests; ladder log > walk-derived > LLM > skipped. | Mapping controllers produce walk-derived tests; cross-checked against log fixtures where present. | 2–3d |

Total P0–P5: ~8–12 days. P6 is a separate consumer and can ship after.

## Validation & metrics

- **Census**: rerun `walkreport` per phase; the primary KPI is TODO count by
  reason code (not total lines).
- **Coverage**: `flow` coverage per function stays the parser-accuracy guard;
  unknown lines are tracked, not hidden.
- **A/B**: convert the same corpus with the LLM seam enabled and disabled;
  diff per method; walk-covered methods must not be "upgraded" (marker-gated:
  `tuxgo:deterministic-controller` / `-fnhelper`).
- **Determinism**: two consecutive `-no-llm` runs byte-compare
  (`shasum conversion_logs/_staged/tux/controller/*.go`).
- **Compile/test gates**: run inside a real target module (deps present), not
  the staging tree.

## Risks & guards

- **Expression long tail** (C operators, pointers, casts, formatting): build
  a small typed expression IR; render what is typed, reason-code the rest.
  Never guess.
- **Correlated oracle risk** (P6): walk-derived test expectations share the
  generator's assumptions with the walk-derived controller; always keep the
  log-derived ground truth as the cross-check and fail loudly on mismatch.
- **Scope creep in `gen`**: the index lives in `internal/walk`;
  `deterministic.go` stays the renderer; no new IR concepts without a parity
  test (see `docs/uniform-ir-plan.md` §3.2 rules).
- **TPCall creep**: Option B generates code only; the runtime client (Tuxedo
  gateway) is explicitly out of scope.
- **Golden churn**: P0 freezes goldens; P1–P4 updates are per-fixture,
  reviewed diffs with census numbers attached.

## Non-goals

- A runtime Tuxedo/TP client or gateway.
- Dynamic SQL semantics (none in this corpus; stays loud in general).
- Multi-language backends: the walk renderer is Go-specific; `contract`
  stays language-neutral.
- Removing the LLM seam for functions the walk cannot cover (it stays as the
  last rung, budgeted and gated).

## Open decisions

1. ~~**TPCall Option A vs B**~~ — **RESOLVED 2026-10-01: Option A, deferred.**
   See "Pinned: TPCall is Option A" below. The deciding fact is that
   `SVC_NETWORTH`'s source is not in this repository at all.
2. **P6 in or out** — whether deterministic gentest expectations are part of
   this feature or a follow-up plan.
3. ~~**`walkreport` shape**~~ — **RESOLVED in P0:** its own `walkreport`
   subcommand, not a `flow -report` flag. The census reads emitted output, so
   folding it into a source-analysis command would make it depend on a
   conversion having already run.

## Pinned: TPCall is Option A (2026-10-01)

**Decision.** `tpcall` sites stay one reason-coded `R-TPCALL` each. The
determinism claim stays scoped to SQL+FML walks. Option B is deferred, not
rejected.

**Why.** A tpcall is a call into another service's implementation, not into a
data store. Option B's estimate (0.5–3d) covered only generating a typed
outbound interface and payload structs from `ir.TPCall`; it did not cover
converting the service on the other end, which is where the real cost sits.
`SVC_NETWORTH` is a full Tuxedo service with its own SQL and FML walks, and
`fn_find_risk_profile` / `fn_save_risk_profile` are its consumers for
`MANAGE_RISK_PROFILE_VIEW`. A `NetworthClient` interface generated from what
`tux.pc` *sends* and *reads back* is a guess at the downstream contract: what
a caller sends and reads is not the callee's full contract, and it cannot be
compile-checked against a real implementation.

Decisively: **`SVC_NETWORTH`'s source is not in this repository.** It appears
only as the string literal `"SVC_NETWORTH"` in `tux.pc` (2 sites, lines 4395
and 4566). There is nothing to convert it *from*. Option B is therefore not
merely expensive here — it is unverifiable, and the project's rule is that an
unresolved fact gets a loud reason code rather than a plausible-looking
interface.

**What A costs — it is not free.** Three things must be done, or A is not
honest. **All three landed 2026-10-01:**

1. ~~`renderTPCallPlaceholders` is entry-scoped (`plan.KindTPCall`). The entry
   `SVC_RISK_PRFL` has **0** tpcalls; both corpus tpcalls live in fn helpers,
   so the placeholder path never fires. Today the tpcall is absent from the
   output entirely — not even a TODO. A needs helper-scoped emission.~~
   **DONE.** `internal/gen/fntpcall.go` emits one `R-TPCALL` per site,
   sourcing them from `ir.TPCall.Function` (the IR already attributes each
   call to its owning function) rather than scanning source text.
2. ~~`R-TPCALL` is not in the `walkreport` reason vocabulary (8 codes, none of
   them this). A new code must be registered, or the gap lands in
   `R-UNCLASSIFIED`.~~ **DONE.** Registered as
   `walkreport.ReasonTPCallNotRendered`; corpus shows 2, unclassified 0.
3. **The stub must return the failure status.** This is the important one.
   Callers test `== -1`. `FnFindRiskProfile` is 100% tpcall — no SQL at all —
   so its body cannot render, and the generated stub returned a non-`-1`
   value that the caller's check accepted. Execution then continued and
   `UpdateRpdRiskProfileDevationq59` wrote the (nonexistent) risk profile to
   the database. A stub that silently "succeeds" and persists bad data is
   worse than no stub. **DONE** — `detFnTPCallTail` forces `return -1` on any
   helper carrying an unrendered tpcall, uniform across helpers whether or
   not their SQL also rendered, because the caller cannot tell which part ran.
   `FnInsertIntoUra` (no tpcall) keeps its legacy `return 1`, which is the
   control that stops the override from over-applying.

**Consequences accepted.**

- `R-NO-STORE-CALLS` stays at **1** (`FnFindRiskProfile`) for as long as A
  holds. **P2's acceptance criterion therefore cannot be met under A.** P2 is
  complete on `FnInsertIntoUra` (verified: 0 gaps) and on the control-flow
  accounting, but the empty-body marker will not reach zero. This is recorded
  as accepted debt, not as a missed phase.
- Every endpoint reaching `FnFindRiskProfile` / `FnSaveRiskProfile` fails
  loudly at runtime under A. That is intended: a loud failure is correct, a
  silent wrong answer is not.
- The P1 out-param work (250 `R-HELPER-ARG-UNRESOLVED`) stays blocked. The
  `recv_fml` targets in `ir.TPCall` *are* those out-params
  (`FML_OPN_RT`→`d_debt_amt`, `FML_CLS_RT`→`d_eq_amt`, `FML_HGH_RT`→
  `d_altrnet_amt`), so the tpcall is their producer. A cannot supply it.

**What would reopen it.** Obtaining `SVC_NETWORTH`'s source, and a decision on
whether this tool converts that service in the same pass (it would be a second
service conversion, with its own 23-or-so endpoints) or whether the runtime
client is hand-written against a contract doc instead.

**Known interaction — RESOLVED, both landed together 2026-10-01.**
`detFnSuccessRet` read the legacy success value but only recognised `return 1`
with a space; the corpus writes `return(1);`. So `fn_find_risk_profile` and
`fn_save_risk_profile` — the two tpcall helpers — emitted `return 0`, a status
the C never returns (`fn_insert_into_ura`, written `return 1;`, was correct).

This mattered because the parser fix and the tail override pull in opposite
directions. Repairing the parser moves both helpers to `return 1`, the legacy
*success* value — and since callers test `== -1`, on its own that would have
made the silent failure strictly worse: `0` accidentally slips past the check,
whereas `1` positively claims success. The parser now reads both spellings
(`return 1`, `return(1)`, `return (1)`, `return(1) ;`) so the success value is
correct for helpers that legitimately have one, and `detFnTPCallTail` overrides
it to the failure status for the two that do not. Neither change is correct on
its own; they are pinned together by
`TestHelperWithTPCallEndsOnTheFailureStatusNotTheLegacySuccess`, whose fixture
deliberately has both a tpcall and a parenthesised `return(1);`.


## Pinned: P3 landed in three parts (2026-10-01)

**What the phase table predicted, and what was actually there.** P3 was
budgeted at 2d as "response-field value resolution from the scope". The
measured starting state was 58 `R-RESPONSE-FIELD-UNRESOLVED` gaps, and the
session before this one narrowed the largest single case — `sql_rpam_answer_id`
as row shape `[4]` of `cur_get_qustans_lst` — to "a specific bug in
`detRowPairs`". That diagnosis was half right, and the half it missed is why
the phase took three distinct changes rather than one.

### P3B — a response field is an output-buffer write

Two defects stacked on one confusion about what a "response add" is.

1. `flow.ScenarioCondition` deduped FML ops on `kind|field`. One field is
   written repeatedly across a scenario — the shared preamble's default, then
   one write per branch — so the first write claimed the key and every later
   write was silently discarded. In `riskPipelineTest` the preamble's
   `Fadd32(ptr_fml_Ibuffer, FML_POINT_TYPE, &sql_urf_mm_opt_stts_2)` claimed
   `FML_POINT_TYPE` and threw away the per-branch
   `Fadd32(ptr_fml_Obuffer, FML_POINT_TYPE, &sql_rpam_answer_id)`. The key now
   carries the write's whole identity: `kind|field|target|buffer`.

2. `detAdds` then admitted both writes, and the Ibuffer one — earlier in
   source order — won `detRowPairs`' first-wins target lookup. An `Fadd32`
   against the **input** buffer writes the request buffer: a default or a
   guard, never the response value. `detAdds` now admits `Obuffer` writes
   only, the line `flow.resolveResponses` already drew for SCEN-D9.

Census 339 → 330. `R-STORE-ARG-UNRESOLVED` 20 → 11. `GetGetQustansLst` now
emits `PointType: row.RpamAnswerId.String`. Five endpoints' `PointType`
resolved to the real row field, retiring 5 row-match lists; the 5 single-reads
those fields had been falsely riding were correctly reclassified.

### P3A — attribution, and an honest negative result

New `internal/walk` is the endpoint-wide provenance index: given the host a
response write names, which read produces it? Shaping had been asking every
read about every response field, which cannot work — a field belongs to one
read. Shaping now attributes once per endpoint and reports a field no read can
source **once** ("response fields without row source").

**P3A changed no emitted pair.** 14 rendered appends before, 14 after. An A/B
of the ownership rule (env-gated, probe since removed) produced byte-identical
output. The census rose 330 → 338, and that rise is the deliverable: eight
response-field notes that were always true and never emitted, because the old
note was gated on "this read fuzzy-matched at least one field", which is not
the same question as "this read feeds the response".

The negative result is the useful part. It establishes that the remaining gaps
are **not** a matching problem, so no further work on `detRowPairs`' matching
can move them.

### P3C — one message was doing two jobs

"has no response-field match — kept for its error check" was emitted for any
single-row read that shaped nothing, and that covers two situations needing
opposite responses:

- **45 reads that genuinely feed no response field.** Finished work.
  `getUacUsrAccnts` is 23 of them: every endpoint reads it and then passes
  `getUacUsrAccnts.UrfUsrId.String` into a later store call. Now
  `R-RESPONSE-READ-KEPT`, a registered code, so no LLM budget goes to it.
- **21 response writes whose value exists** — the write names a host some read
  carries — and which shaping failed to place. Real gaps; they stay
  `R-RESPONSE-FIELD-UNRESOLVED`.

The test is `detReadCouldSourceUnsourced`. Without it the 21 hid inside the 45.

### What is still open, and why it is not a matcher bug

The 21 remaining gaps need an **emission redesign**, not better resolution.
`detEmitShaping` emits one append per read — `for _, row := range <capture>` —
so a response field whose value lives in a *different* read's row has nowhere
to be written: `<capture>.Field` is the wrong capture and is not in scope.
`GetGetTblcDtls` is the clean example: `PointType` is written from
`sql_rps_c_table`, which `cur_rps_risk_prof_scrn` yields, while the append
iterates `getGetTblcDtlsRows` (query `q4`).

Closing this means building the response once from all reads rather than
inside one read's loop, and deciding what to do when the reads have different
cardinalities. That is a larger change than P3 was scoped for and has not been
made. Under the plan's own rule an unresolved fact gets a loud reason code
rather than a guess, so the 21 stay loud.

### Consequence for P6

Unchanged, and for the reason already recorded: P6 generates test
expectations from the same walk that renders the controllers, so a
mis-resolved field would be graded against itself. The 21 open gaps mean the
walk is not yet faithful for mapping controllers.
