# Scenario slice brace-fidelity fix plan (2026-09-17)

Executor notes: this plan is self-contained. Line numbers are as of commit
`a93eac5` on a clean tree; the conversion WIP in the worktree may shift a few
lines — re-grep if drifted. Do Phase 0 first, then Phase 1, Phase 2, Phase 3
as separate commits, then Phase 4/5. The user has not asked for commits yet;
only commit when explicitly requested.

## 0. Evidence (do not re-derive)

The scenario artifacts/views are boundary-unbalanced. Interior folding is
sound: contradicted branches drop as whole `[Line..EndLine]` spans
(`internal/flow/scenario.go:696-699`), so every dropped gap is brace-neutral.
A stateful scan (comments/strings aware) over all 70 `scenarios/*.pc` found
**no negative depth before the final emitted line**.

Boundary findings:

| Family | End depth | What leaks |
|---|---|---|
| `SVC_MF_NAV_LIST` (4) | −1 | dangling entry `}` (src L982), opener L72 never emitted |
| `SVC_OLN_GET_DTL` (5) | −1 | dangling entry `}` (src L1109) |
| `SVC_REIPV_OTP` (20) | −1 | dangling entry `}` (src L2415) |
| `SVC_RISK_PRFL` (23) | +1 | entry `{` (src L113) emitted, entry `}` (src L4317) never emitted |
| `SVC_MF_SUB_TRN` (9) | +3..+8 rendered | entry `{` (L685) emitted, closer absent; rendered counts inflated by prefix bug |
| `SVC_MF_CON_TRN` (9) | +0..+6 rendered | `ScenarioSource` is balanced (both braces present); rendered `.pc` is +6 from the prefix bug |

Root cause A (spans): `residualRun` (`internal/flow/flow.go:389-433`) sets
`Node.Line=from`, `Node.EndLine=to` even when the edge lines are bare braces
(folded out of `Text` by `lineIsCode`, `flow.go:503`); empty runs return nil,
so a leading `{`-only run vanishes, while the trailing run's `EndLine` clamps
to `BodyEndLine` and absorbs the entry `}`. Verified with a temporary probe:

- `mainTux.pc`: first root starts L74 (`{`@L72 lost), last root `L980..L982`
  (entry `}`@L982 absorbed).
- `risk.pc`: first root `L113..L117` (entry `{`@L113 kept), last root ends
  L4316 (entry `}`@L4317 never emitted).

Root cause B (renderer): `RenderScenario` prefixes every emitted line with
`/*L<n>*/` (`internal/flow/scenario.go:1653`, `1668`). A prefix landing inside
a multi-line `/* ... */` block terminates that comment early — commented-out
code becomes live text in the artifact and brace counts inflate (+6 on
`SVC_MF_CON_TRN.trn_cd_A.pc`, divergence at src L1830-1833, a commented block
with braces). `ScenarioSource` carries no prefixes and preserves comment flow
(checked statefully).

Downstream impact already observed:

- Non-chunked convert path: the entry `{` reaches the model and is echoed as a
  spurious bare scope block — `conversion_logs/_staged/risk/controller/risk.go:25-66`
  (`{ ... }` wrapping the whole body). Valid Go, so gates pass; structure is wrong.
- Chunked convert path: `dropTrailingMethodBrace`
  (`internal/convert/chunk.go:250`) strips *any* trailing brace-only line —
  today it strips the entry `}` for NAV/OTP-style views and already strips a
  legitimate arm close for RISK (`risk.pc:4223`). There is no leading-brace
  equivalent, which is why RISK/CON_TRN views leak the entry `{`.

Responsible code map:

- Build slice: `internal/flow/scenario.go:659` `ScenarioFor`, `:804`
  `Scenarios`; spans from `internal/flow/flow.go:312` `nest`, `:389`
  `residualRun`.
- Artifact: `internal/flow/scenario.go:1566` `RenderScenario`, written by
  `cmd/tuxconv/scenarios.go:59`.
- Prompt slice: `internal/flow/scenario.go:2188` `ScenarioSource`.
- Consumers: `internal/convert/pipeline.go:1094` (`scenarioView`) and `:463`
  (`controllerBody`), `internal/convert/chunk.go:206/250/1047`,
  `internal/plan/plan.go:241`, `internal/gen/prompt.go:74`,
  `internal/csdraft/draft.go:81/89`, `internal/csplan/plan.go:293/302/320`.
- Goldens: `internal/flow/scenario_golden_test.go` (skips when
  `TUX_SCEN_CORPUS`/`testdata/scen` are absent — absent in this checkout).

## Target invariant

Scenario text (`ScenarioSource` and the `.pc` artifacts) never contains the
entry function's own `{`/`}` lines; folding internals unchanged; provenance
prefixes never terminate comments/strings. Slices stay fragments by design —
the chunker keeps owning wrapper-relative brace handling.

## Phase 0 — Baseline

Baseline is green as of this plan:

```
go build ./...                     # clean
go test ./internal/flow ./internal/convert ./internal/plan ./internal/gen ./internal/csdraft
```

Snapshot artifacts for before/after diffing (scenario dir lands next to
`-out`'s parent, see `cmd/tuxconv/discover.go:151`):

```
for f in tuxExamples/risk.pc tuxExamples/mainTux.pc tuxExamples/otp.pc \
         moreExamples/con_trn.pc moreExamples/sub_trn.pc moreExamples/orignial_dotnet.pc; do
  n=$(basename "$f" .pc)
  go run ./cmd/tuxconv discover "$f" -no-llm -out "/tmp/scen-before-$n/mappings"
done
```

Keep the stateful balance script used for the evidence table handy as the
acceptance oracle (comments + strings aware, per-line depth).

## Phase 1 — Exclude entry brace lines at emission (`internal/flow/scenario.go`)

Do NOT change `ScenarioFor` spans or `KeptLines`/`BodyExtent` metadata:
coverage checks key on arm header lines (`internal/plan/plan.go:420`) and
brace lines can never be headers, so metadata can stay brace-inclusive and
plan/gen/csplan/csdraft/DiffScenarios behavior is untouched. Filter only the
two emitters, which already receive `src`.

1. New helper `entryBraceLines(tree *Tree, src []byte) map[int]bool` in
   `internal/flow/scenario.go`:
   - bail on `tree == nil`, `tree.facts == nil`, `tree.facts.Fragment`,
     `len(src) == 0`, `tree.EndLine <= tree.StartLine`;
   - `code := maskedLines(tree.facts, splitLines(src))` (same masking `Build`
     uses, `flow.go:137`);
   - include `tree.StartLine` only when `strings.TrimSpace(code[l-1]) == "{"`;
     include `tree.EndLine` only when `== "}"`.
   - The exact-match guard protects `}}` on one line, signature+brace lines
     (`lineIsCode` sig case, `flow.go:124-127`), and comment tails.
2. `RenderScenario` (`scenario.go:1566`): skip these lines in the preamble
   `emit(from,to)` helper and in the `bodyLines(sc)` loop.
3. `ScenarioSource` (`scenario.go:2188`): skip them in `add(from,to)`; the SQL
   region `idx` map recomputes from the filtered `out` automatically.
4. Tests (`internal/flow/scenario_test.go`), new:
   - `TestScenarioSourceExcludesEntryBraces` — fixtures for each boundary
     shape: opener in preamble (risk-style), opener missing + close absorbed
     (nav/otp-style), both present (con_trn-style), last line `} else` (must
     NOT be excluded), same-line `}}` (must NOT be excluded).
   - Assert brace balance is 0 and neither entry brace line appears.
   - `TestRenderScenarioExcludesEntryBraces` — same via the artifact.

Acceptance: `go test ./internal/flow ./internal/plan ./internal/convert`
green; before/after artifact diff contains only the entry brace lines.

## Phase 2 — Comment/string-safe provenance prefixes (`RenderScenario`)

1. Precompute per-source-line start state:
   - comment interior from `tree.facts.Comments` (`tsscan/facts.go:228`):
     line `l` starts in a block comment when some block comment has
     `StartLine < l && (EndLine > l || (EndLine == l && EndCol > 1))`;
   - string continuation (`\` at end of previous emitted line) via a small
     scanner; skip prefixes inside strings too.
2. Emit loop: omit `/*L<n>*/` while the line starts inside a comment or
   string. Maintain rendered state; when a closer sat on a dropped line
   (source-state says code, rendered state says in-comment), inject a `*/`
   resync line; when an opener sat on a dropped line, inject a `/*`. Comments
   only — no semantic text. Suppress `ann[l]` when the line ends inside a
   comment.
3. Test: fixture where an emitted arm contains a multi-line commented-out
   block with braces. Assert the rendered `.pc` stays comment-faithful (no
   code leaking out of the comment, no `/*L` injected into the comment
   interior) and its brace balance matches `ScenarioSource`'s.
4. `h.PreambleN` adjusts automatically via `sc.Preamble`; no other header
   changes.

## Phase 3 — Chunker reconciliation (`internal/convert/chunk.go`) — separate commit

This is the only phase that can change model-visible fragments. Land it alone.

1. Remove `dropTrailingMethodBrace` (`chunk.go:250`, call site `:243`) now
   that scenario views never carry the entry close. It currently strips any
   trailing brace-only line, including legitimate closes (RISK evidence), so
   removal is also a latent-bug fix. Update the `splitStatements` contract
   comment (`chunk.go:198-205`).
2. Update tests: `internal/convert/chunk_test.go` reassembly expectations
   (`chunkView` fixture and `want := chunkView[:strings.LastIndex(chunkView, "\n")]`
   at ~L111-113 and ~L264-268); add `TestTrailingRealBlockCloseKept` (a view
   whose last statement is a braced block keeps its `}`).
3. Verification: `go test ./internal/convert`; then one oversized-endpoint
   run (`chunkReason` path, `pipeline.go:583-601`) and compare
   `go run ./cmd/tuxconv retrystats conversion_logs/audit/<before> <after>`.
   If any gate regression appears, revert this phase independently — Phases
   1-2 do not depend on it.

## Phase 4 — Regenerate artifacts/goldens

- Goldens are absent here (`testdata/scen` missing → `TestScenarioGoldens`
  skips). If present locally:
  `TUX_SCEN_CORPUS=<dir> SCEN_UPDATE_GOLDENS=1 go test ./internal/flow -run TestScenarioGoldens`.
- Regenerate `scenarios/` with the Phase 0 commands into scratch; spot-check:
  - NAV_LIST/OTP/OLN: no dangling `}`;
  - RISK/CON_TRN: no leading `{`;
  - CON_TRN: multi-line comments intact (no code leaking).
- Replace tracked `scenarios/` artifacts only if the user wants the in-repo
  copies refreshed (they are generated output).

## Phase 5 — Full verification

```
go build ./...
go test ./...
```

Optional end-to-end with LLM: convert `tuxExamples/risk.pc` and confirm the
generated controller no longer contains the bare `{ ... }` scope block
(cf. `conversion_logs/_staged/risk/controller/risk.go:25-66`); confirm gates
pass and retry counts are not worse.

## Open decisions (recommendations in bold)

1. Scope: emission-level filtering (**recommended**; low blast radius) vs
   normalizing `ScenarioFor` spans (metadata-consistent but touches
   plan/gen/csplan/csdraft/DiffScenarios).
2. Chunker: **delete** `dropTrailingMethodBrace` vs scope it to a passed
   entry-close line (a no-op after Phase 1).
3. Prefix policy: omit-only (minimal) vs **stateful resync** (faithful).
4. Whether refreshed `scenarios/` artifacts are committed (generated output).

## Risks

- Phase 3 changes fragment prompts; expected outcome is fewer spurious bare
  blocks (leading `{` gone) and no lost arm closes, but retry behavior must
  be A/B checked before/after.
- Any consumer asserting exact `BodyExtent` line numbers shifts by 1-2 lines
  only if a later migration moves filtering into `ScenarioFor`; Phase 1 keeps
  metadata untouched by design.
- `ScenarioSource` comment flow was verified intact for the six corpus
  families; Phase 2's resync covers the theoretical dropped-closer case.
