# GT-7: Log-Driven Fixtures Plan

Status: **implemented** (2026-09-29) — see "Implementation notes" at the
bottom for the shipped surface and the deliberate deviations.
Source: `plan.txt` + design-tree interview; verified against code at commit state 2026-09-29.

## Goal

Build the GT-7 `LogFixtureSource` — the real backend promised in
`internal/testgen/fixture.go:1-5` ("The real backend (GT-7, on user logs)
swaps realism in without touching the pipeline"). Test generation keeps
on-disk Go code as the structural truth and takes fixture **values** from a
runtime log produced by the (edited) converted application.

## Workflow it serves

1. Convert with `tuxconv` → converted tree; developer fixes logic/names.
2. Developer runs the real app (e.g. `mutual-fund-be`) against a service.
3. Developer points `gentest` at the service folder + the captured log.
4. Tests are generated with real log values; in the real repo they compile
   and run for real.

## CLI

`tuxconv gentest <service-dir> [-log-file <path>] [-out <dir>] [-layers ...] [--nice-names]`

- `-log-file <path>` — parse runtime log for fixture values (empty = today's
  assumed-placeholder behavior; fully backward compatible).
- `-out <dir>` — explicit output root. May already exist; existing
  `db/controller/handler/models` folders and files are left untouched.
  `-base` remains as alias for backwards compatibility.
- `--nice-names` — optional LLM polish for names only, default off, never
  affects assertions or structure.
- Target dir = scope (existing `ModeService` resolution). No new scope flags;
  log is auto-filtered to the service matching the target dir.

## Log parser

Input: runtime logs like `riskPipelineTest/logfile.txt` (`.txt` or `.log`,
detected by content, not extension).

Format:
```
MM-DD-YYYY HH:MM:SS <ANSI LEVEL> <file:line> <pkg/services/<svc>/<layer>.(*recv).Method> <MSG> <JSON>
```

Rules:
- Strip ANSI escapes first; parse line-based.
- Skip `requestID: null` lines and unparsable lines, counting warnings
  (never fail the whole run on one bad line).
- Tolerate multi-line Go stacktraces after `ERROR` lines.
- Group by `requestID`; the `API Call End` line supplies `path`, `status`,
  `requestBody` (JSON string inside JSON), `responseBody`.
- "Complete" trace = `API Call End` + ≥1 layer caller for the method.
- "Successful" = `status == 200` AND no `ERROR` line with that `requestID`.
- Note: business SQL text is NOT in the log (only `ALTER SESSION`); DB
  effects come from `db.(*store).Method Result:` JSON + source SQL.

Caller ↔ code matching:
- Log caller `.../services/<svc>/<layer>.(*recv).Method` ↔ disk function
  matched by service-dir segment + method name.
- Method name case-sensitive; service-dir segment case-insensitive;
  pointer/value receivers and receiver names equivalent.
- Log-only methods with no code match → ignore with warning (log cannot
  invent code). Code-only methods → assumed fixtures (below).

## Fixture values

Per `(service, method)`:
- Row 1 = **first successful complete trace** in log order.
- Row 2 = **first failed complete trace** (status != 200 OR ERROR), if any.
- No successful trace at all: the first complete trace is still used.
- Determinism: same folder + same log = byte-identical output. Extra runs
  are ignored in v1; multi-row expansion is a later milestone.

Fallbacks:
- Method with zero log hits → `AssumedFixtureSource` values + per-method
  `// fixture: assumed` provenance + gap-report entry.
- Per-field misses within a hit method → assumed placeholder for that field.
- Per-file provenance comment: `// fixture: log <short-id> | assumed`.

Extractable values:
- `requestBody` FML_* JSON → handler/controller request fixtures (mapped to
  `json` tags in `models`).
- `db.(*store).Method Result:` → mock rows / expected structs
  (`sql.NullString{String,Valid}` etc.).
- `SuccessJSON data` + `responseBody` → expected responses.
- `ERROR` message + non-200 `status` → error-case rows
  (e.g. `AddQuestion` "Question number must be unique across customer type").

## Generation

- Deterministic DB/controller/handler blocks from on-disk AST
  (`internal/testgen/extract.go`, parse-only) + log values.
- Controller field mapping derived from log request/response bodies — no LLM
  in the default path (remove reliance on `internal/testgen/llm.go`; keep
  for `--nice-names` only).
- Test output naming (no overwrite rule):
  - No existing test file → `<svc>_test.go` (existing behavior).
  - Existing `*_test.go` → write `<svc>_gentest_test.go` twin
    (`internal/testgen/gen.go:451-481` behavior; human tests never touched).
- Non-test files staged into `-out`:
  - Name already exists in destination → `<name>_convertgo.go`
    (e.g. `interface.go` exists → write `interface_convertgo.go`).
  - Name absent → natural name.
  - `-out` thereby becomes a complete, compatible snapshot; target tree is
    never modified.
- Reuse `gen.RunMocks` (`internal/gen/mocks.go:24-37`).
- Provenance comments + gap report + audit archive
  (`gentest_summary.json`).

## Gates

- `go vet` + compile-gate (`go test -run '^$'`) always; non-fatal but
  visible in gates list.
- When `-out` is given: full `go test -count=1` per written package inside
  the out dir. Gate lines `PASS/FAIL + first 400 chars`. Non-fatal, but
  flips a visible summary line `gentest: tests FAILED (see gates)`.
  (Fails in this repo for missing deps; runs for real in the target repo.)
- Structural checklist vs human tests (`riskPipelineTest/rpdbtest.txt`,
  `rptestcontroller.txt`, `handler_test.txt`) — guidelines for template
  shape, not byte comparison: table-driven, one func per method,
  SQLError/NoRows/Success cases, `ExpectQuery`/`ExpectExec`,
  `assert.ErrorContains` / `NoError` / `Equal`.
- Mutation testing: explicitly out of scope for this milestone.

## Verified anchors

- Swap point: `internal/testgen/gen.go:596` (`AssumedFixtureSource`).
- Fixture interface: `internal/testgen/fixture.go` (`FixtureSource`).
- Extraction (on-disk truth): `internal/testgen/extract.go`.
- Renderers: `internal/testgen/methods.go`.
- Scan/target resolution: `internal/testscan/scan.go`.
- LLM seam: `internal/testgen/llm.go`.
- CLI: `cmd/tuxconv/gentest.go`.
- Log corpus: `riskPipelineTest/logfile.txt`; reference tests in
  `riskPipelineTest/*.txt`.

## Decisions log (Q&A summary)

- Scope = build GT-7 (not a new system); logs = target-app runtime logs.
- Source parsing stays primary; log adds values only.
- Validation = structural + human-test checklist; no byte compare; no
  mutation testing this milestone.
- Controller tests deterministic from AST + log; LLM only nice-names polish.
- Parser tolerance: ANSI strip, skip bad lines w/ warnings.
- Scope = target dir; no new scope flags.
- First successful complete trace wins; failed trace still usable.
- Two rows max per method (happy + logged error).
- Zero-hit methods get assumed fixtures with provenance.
- Twin file naming on existing tests; `_convertgo.go` on existing non-test
  files in `-out`.
- `-out` = complete compatible snapshot + full test run there.

## Implementation notes (shipped)

Code:

- Log parser: `internal/testgen/logparse.go` (`ParseLogFile`/`ParseLog`,
  `LogData.Trace(service, method, failed)`). ANSI stripped, `requestID: null`
  skipped, stacktraces tolerated, one malformed line = one warning. Consecutive
  db lines of one method are folded into one call (entry debug line + result
  line), so DML calls are counted without values and repeat-result reads
  (GetMarks) stay one-call-per-payload.
- Fixture backend: `internal/testgen/logfixture.go` (`LogFixtureSource`,
  `ForUnit`). Implements `FixtureSource` plus the GT-7 seams
  `ScopedFixtureSource`/`ResponseFieldSource`/`MethodLogValues` in
  `fixture.go`; per-field misses fall back to the assumed placeholder, and a
  method with no complete trace reports `assumed`.
- Renderers: `internal/testgen/methods.go` scopes every block
  (`serviceCtx.fixtureFor`), adds the `Logged#2` row (SELECT shapes only — a
  DML failed trace has no result row to mock) and the `Logged-Error`
  controller/handler case, and renders multi-call controllers deterministically
  from the log (`mockInput`/`mockInput2`/…, executed calls only, unknown call
  returns degrade to `[]any{nil, nil}` and never-run calls skip their EXPECT).
- Engine: `Options.Log/NiceNames/Stage/FullTest`, per-file provenance comment
  `// fixture: log <id> | assumed`, `Result.Fixtures` gap report, source
  snapshot staging with `_convertgo` collision naming, structural checklist
  lines, full `go test -count=1` gate for `-out` (flips
  `gentest: tests FAILED (see gates)`), and log-only method warnings.
- CLI: `cmd/tuxconv/gentest.go` — `-log-file`, `-out` (alias of `-base`),
  `--nice-names`; explicit `-out`/`-base` turns on staging and the full gate.
- Nice names: `internal/testgen/nicenames.go` — comments are ignored and only
  STRING literals may change; the token stream is otherwise identical, or the
  block is kept untouched.

Deliberate deviations from the spec text:

- Error attribution pins the layer (`LogTrace.ErrorForLayer`): method names
  repeat across layers (`ViewQuestions` is handler, controller and db), so a
  controller error would otherwise attach to the db method's fixtures.
- Scalar reads with a `sql.Null*` scan var now expect the method's return type
  (GetMarks → `"3"`, QuestionNumberExists → `true`), matching the human
  reference suite; this fixed a pre-existing assumed-path mismatch too.
- The second row for DML methods is skipped (no logged result to mock); the
  plan's "two rows max" still holds for every SELECT shape.
- Failed traces with no response error description fall back to the logged
  ERROR message for the handler case; no description and no error means no
  `Logged-Error` case.

Verification: `internal/testgen/logparse_test.go` (real
`riskPipelineTest/logfile.txt` corpus), `logfixture_test.go` (value mapping /
fallbacks / layer-pinned errors), `gt7_test.go` (end-to-end synthetic
riskprofile tree + log: log values in every layer, multi-call business-error
case, provenance, byte-identical workers=1/N, no-log backward compatibility),
`nicenames_test.go` (polish gate, checklist, snapshot collision, full-test
gate). The serial `go test -p 1 -count=1 ./...` run is green (2026-09-29), and
`--nice-names` was verified end-to-end through the CLI against a fake
OpenAI-compatible endpoint: a literals-only polish landed in the generated
tests (STRING tokens only vs the no-LLM baseline), while a structure-changing
response was rejected by the gate and left every file byte-identical to that
baseline. Existing byte-pinned goldens were regenerated once for the GT-7
template additions and are kept as the intended output shape.

## Re-running the risk-profile corpus (deterministic)

`riskPipelineTest/` holds a snapshot of the *converted* Go tree saved as
`.txt`: `rpdbfn.txt` / `rpcontrollerfn.txt` / `rphandlerfn.txt` are
concatenations of several files (each fragment starts with its own `package`
clause; the handler file carries a `-- handler interface.go ends ---` marker),
and `tux.txt` is the original Tuxedo C source — the input for
`tuxconv convert`, not a `gentest` target. To re-run test generation
deterministically:

1. Lay the snapshot out as a real module: split each `*fn.txt` at every
   `package` line into `pkg/services/riskprofile/{db,controller,handler}/`
   `partNN.go` (drop whitespace-only leading fragments and `--` marker
   lines), copy the interface / mock / models files to their `interface.go`,
   `mock_*.go`, `models/*.go` names, and add a `go.mod` with
   `module mutual-fund-be`.
2. Run the CLI (same folder + same log = byte-identical tests; `-no-llm`
   plus the default `workers: 1` is the deterministic path — without
   `-log-file` the same command emits assumed placeholders):

   ```sh
   go run ./cmd/tuxconv gentest <tree>/pkg/services/riskprofile \
     -log-file riskPipelineTest/logfile.txt -no-llm -out <out>
   ```

Verified run (2026-09-29): 56 functions → 50 template tests, 44 methods from
log values, 6 assumed, 0 LLM calls. Outputs:

- generated tests: `<out>/pkg/services/riskprofile/{db,controller,handler}/`
  `part01_test.go` (the stem follows the first scanned `.go` file);
- staged sources: the same out tree, `<name>_convertgo.go` on collision;
- provenance + gates: `conversion_logs/audit/<run-id>/gentest_summary.json`
  (`files`, `staged`, per-method `fixtures`, `gates`);
- logs: `conversion_logs/logs/run-*.log` / `.jsonl` (`-log-dir` relocates
  them; the audit stays under `conversion_logs/audit/`).

`gentest: tests FAILED (see gates)` is expected for this corpus: the staged
module declares no dependency requires, so the vet/compile/full-run gates
fail visibly and non-fatally.
