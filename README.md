# tux-to-any — tree-sitter scanner + IR for Pro*C/Tuxedo

A Pro*C/Tuxedo (`.pc`) parse stack and conversion pipeline built on a real C
grammar through **tree-sitter-c**. A `.pc` file is C with embedded `EXEC SQL`
statements: the C side goes to the grammar, the SQL side to an auditable
byte-level pre-scan, and the two join into one `SourceFacts` record.

## How parsing works

1. **Pre-scan** (the only hand-rolled part, ~300 auditable lines) walks the
   raw bytes once: EXEC SQL regions (classified, with the documented lenient
   `;`-close), the comment inventory (block/line/banner with the live-flag
   rules), unbalanced accounting, broken `#define` lines, comment-tail
   debris, and dangling `'/'*'/…` residues.
2. **Mask**: each EXEC SQL region becomes a **same-length block comment**;
   every newline survives, so *every* byte offset, line, and column of the
   surrounding code is preserved exactly. Empty char literals (`''` — invalid
   C the corpus uses freely) become `0 `. Broken `#define` lines become
   comments (their directive facts are preserved through a side channel).
3. **Parse**: stock tree-sitter-c parses the masked source. Casts, function
   pointers, `for(;;)`, do-while, switch, nested initializers — all exact.
4. **Join**: SQL facts from the pre-scan, C facts from the AST, one
   `SourceFacts` record.

Raw `.pc` → grammar errors; masked `.pc` → clean AST. The 22,519-line
production monolith from the local corpus parses with **zero** error nodes
(exercised by the skip-guarded smoke test).

## Layout

- `internal/tsscan` — pre-scan + mask + AST walk → `SourceFacts`
  (functions, branches, loops, calls, decls, returns, directives, SQL
  statements, comments, unbalanced; additive: `Params`, `ParseErrors`,
  `Switches`). The parse runs on `internal/tsscan/grammars/wasitter-c.wasm`
  (vendored tree-sitter runtime + tree-sitter-c v0.24.2, sha256
  `5044f382…286b2`) executed by pure-Go wazero — no cgo.
- `internal/pred` — the condition parser (C-precedence boolean trees,
  literal-only define substitution, Raw degrade).
- `internal/ir` — the extraction fold: entry pick, branching factor,
  condition chains, query units (cursor flattening, tables/binds/row
  shapes/order-by, factual dedup), FML classifier (`FmlOpOf`) with FNOTPRES
  guards and legacy error-code harvest, tpcall correlation, buffer roles,
  host vars, scoped defines (`DefineAt`), external fns, `LiveFacts`.
- `cmd/xtux` — inspection CLI: `xtux scan <file>`, `xtux ir <file|dir>`
  (`-fragment` forces the fragment rubric).
- `cmd/tuxconv` — the conversion pipeline CLI (below).
- `testdata/fixtures` — the synthetic demo fixtures.
- `testdata/goldens` — pinned IR JSON for those fixtures, exercised
  byte-for-byte by the test suite.

## Fidelity guarantees

1. **Every `EXEC SQL` spelling** — keyword matching runs on word boundaries,
   so any whitespace variant between `EXEC` and `SQL` is captured.
2. **AST-resolved definitions** — a locally defined function is never
   misclassified as external, so no stub is generated for a local symbol.
3. **Real calls only** — `int fn_x(...);` declaration lines are never
   recorded as call sites; `call_expression` nodes are actual calls only.
4. **Comment-debris self-healing** — `fn_*/chk_*` inside a banner terminates
   the comment early per C lexical rules; the debris is masked (dangling
   `*/` detection) so the grammar still sees the directives and functions
   after it. A `#define` with unbalanced parens is handled the same way.
5. **Factual detail**: subscripts kept in row shapes (`mf_jthldr[0]`),
   indicator variables dropped from row shapes, struct member targets kept
   (`st_gst.d_cgst_amt`), FML targets named for member/subscript writes,
   pointer parameters typed for host variables, 2-D varchar decls typed,
   `long char`-style mangled types never emitted.
6. **Provable balance** — brace accounting happens outside comments and
   strings, so comment-buried braces can never produce phantom unbalanced
   facts; the AST corroborates.
7. **Deterministic and loud** — `workers=1` byte-identity holds by
   construction; anything unparseable survives as `ParseErrors` /
   `Unbalanced` records, never a silent drop, never a panic (pinned by the
   adversarial sweep).

## Test suite

`go test ./...` runs the golden pins, units, and the 19-input adversarial
sweep. Every synthetic fixture (nav 982-liner, merge, tpcall, comment traps,
unbalanced, fragment, the stripped set) extracts byte-identical IR against
`testdata/goldens` in both file and corpus mode, including the nav golden:
entry `SVC_DEMO_LIST`, **4 conditions** (H/F/I/default), **7 query units**
with `q5→q3` dedup, **75 branch headers / 142 branching factor**, preamble
FML with dropped session fields and the FNOTPRES-optional mode flag, MERGE
3/4, fragment rubric, unbalanced kinds, ambiguous empty-contract tpcalls,
ghost/half cursor flattening. The real corpus (when present locally) is
exercised by a skip-guarded smoke test.

## Contracts kept

1-based line/col everywhere; `NestDepth` containment semantics (if/elseif
blocks only for branches; +loops for loops; else never); else-arms flatten
as chain siblings; banner comments live/dead rules; lenient missing-`;` EXEC
SQL close documented as residual; `q<N>` = query-kind ordinal with cursor
units named by cursor; dedup stays factual (`DedupKey`/`DuplicateOf`);
extraction never decides (no endpoint picks, no renaming, no silent
merges).

## Commands

```
go test ./...                 # goldens + units + adversarial sweep (+corpus smoke when present)
go run ./cmd/xtux scan <file> # scanner facts JSON
go run ./cmd/xtux ir <file>   # IR JSON (file mode)
go run ./cmd/xtux ir <dir>    # IR NDJSON (corpus mode: no fragmenting, cross-file resolution)
```

## The tuxconv pipeline

The tux→Go conversion pipeline runs **on this stack**: parse → IR → plan →
gen → convert, with the supporting packages (`common config telemetry audit
ledger profile goast validate templates budget llm sqlchk flow batchflow
pyplan pygen pychk analyzer testscan testgen csplan csgen cschk csdraft
corpusguard`) under `internal/`. Every
downstream stage consumes the tree-sitter facts.

Commands (`cmd/tuxconv`):

```
go run ./cmd/tuxconv extract <file|dir>     # IR JSON; archived per run
go run ./cmd/tuxconv plan <file> -mapping <yaml>
go run ./cmd/tuxconv discover <file|dir> [-stdout] [-target go|cs]
go run ./cmd/tuxconv ainames <file> -mapping <yaml> [-all]   # AI-name only the surviving endpoints, in place
go run ./cmd/tuxconv convertgo <file|dir> [-mapping <yaml|dir>] [-no-llm] [-base dir]
go run ./cmd/tuxconv convertbatchpy <file|dir> [-no-llm] [-shape auto|repo] [-dml-loop batch|rowbyrow] [-out dir]
go run ./cmd/tuxconv convertcs <file|dir> -mapping <yaml> [-no-llm] [-out dir] [-config path]
go run ./cmd/tuxconv analyze <file|dir> [-csv out.csv] [-weights csv] [-pattern mf_]
go run ./cmd/tuxconv gentest <converted tree> [-check-only] [-no-llm] [-layers db,controller,handler] [-base dir]
```

Verified output: `tuxconv convertgo testdata/fixtures/stripped -mapping
testdata/fixtures/stripped/mappings -no-llm` converts 4/4 services with
**0 sql deviations** (`diff -r` clean across all four services, with
controller bodies correctly skipped in deterministic-only mode). The nav
fixture end-to-end (`tuxconv convertgo testdata/fixtures/nav -mapping
configs/nav.mapping.yaml -no-llm`) converts cleanly, and the batch→Python
pipeline (`batchflow` + `pyplan` + `pygen` + `pychk`) writes deterministic
modules for both corpus shapes (`bat_min_simple` simple, `bat_min_repo`
repo) with 0 sql deviations.

Also included: `analyze` (`internal/analyzer` — triage rubric + CSV, with
the `# tuxgo marks:` marker kept verbatim so CSVs stay interchangeable) and
`gentest` (`internal/testscan` + `internal/testgen` — post-conversion test
generation, verified end-to-end over the nav fixture: db stores render
sqlmock suites whose `ExpectQuery` regexes are case-insensitive,
whitespace-tolerant and WHERE-optional (so lowercase `select … from dual`
queries match) with typed scalar placeholders; handler bodies render gin
suite tests against a gomock'd controller, the controller's response type
resolved from the controller *interface declaration* when controller bodies
are the LLM seam; passthrough controllers render directly, field-mapping
controllers ride the LLM seam; `New*` and wiring constructors skip by
design). The flow machinery drives convert/discover and the scenario
artifacts.

## Run configuration (.tuxgo.yaml)

Every command resolves the run config the same way
(`cmd/tuxconv/extract.go:100-115`): `-config <path>` wins; else `./.tuxgo.yaml`
when present in the working directory; else the stock defaults
(`internal/config.Default()`). The filename is only that default-lookup
convention — any path works via `-config`. Unknown keys fail at load (strict
decoding), and relative paths resolve against the working directory. Start
from [`configs/.tuxgo.example.yaml`](configs/.tuxgo.example.yaml).

The `run.*` budget engine (`internal/budget`, `internal/llm/seam.go`):

- **Estimator** — tokens ≈ chars / `charsPerToken`. Dense Pro*C measured
  ~2.8 chars/token at the provider; the default 4 undercounts, which the
  chunking margins absorb.
- **Prompt ceiling** — `maxPromptTokens` is a hard per-call check; an endpoint
  whose assembled prompt exceeds it (or whose projected body reaches 80% of
  the output ceiling) splits into statement fragments. Slices are sized at 70%
  of the prompt ceiling and ¾ of `maxOutputTokens`' char equivalent, with the
  projected Go translation inflated 130%.
- **Output ceiling** — `tokenPolicy: dynamic` sizes each call as
  `min(modelContextTokens − estInput − outputReserveTokens,
  modelMaxOutputTokens)` (a room under 256 tokens fails loudly); `static` keeps
  `maxOutputTokens`, and the request falls back to
  `models[].requestOptions.maxTokens`. Under static mode keep
  `requestOptions.maxTokens <= run.maxOutputTokens` — the output gate reads the
  run value.
- **Inert/reserved** — `run.maxContextTokens` is validated (≥
  `maxPromptTokens`) but never read; `retrieval`, `elision.mode`, and
  `paths.target` are reserved.

## The .NET Core target (convertcs)

`convertcs` converts a Tuxedo service into the seven-file C# component
tree: `Controller/<C>Controller.cs` (one action per mapped dispatch arm,
validator + ResponseHelper boilerplate), `DTO/<C>DTO.cs` (one response
class per action, properties named from the query's row shape),
`NamedQueries/<C>Queries.cs` (the source SQL verbatim, the Pro*C `INTO
:host` plumbing stripped), `Repository/I<C>Repository.cs` +
`<C>Repository.cs` (one method per query unit: `ExecuteQueryAsync` /
`ExecuteNonQueryAsync` + named `OracleParameter`s whose names stay the
source's own host binds), and `Service/I<C>Service.cs` + `<C>Service.cs`
(deterministic repo calls, row mapping via `DataReaderHelper.GetStr`, and
structured logging).

The pipeline is `csplan` → `csgen` → `cschk`: the mapping yaml carries the
namespace identity (`namespace` / `area` / `component`) and the endpoints
(the same `condition` / `conditionRef` / `scenarioRef` reference forms as
the Go mapping — a dispatch arm becomes one action). Gates: brace/type
structure, no raw SQL outside NamedQueries, and a normalized SQL-fidelity
comparison of every const against the source (comment-stripped,
whitespace-collapsed, bind names wildcarded). The goldens pin the full
pipeline over `testdata/fixtures/cs` — the CUSE fixture plus the
broad-arm-coverage variants: a DML arm (`UPDATE` + commit → `Task<int>`),
a cursor arm (`DECLARE`→`FETCH` choreography → `List<DTO>`), and a
multi-query arm (SELECT then UPDATE in one arm → `Action1`/`Action2`
repo-method suffixes) (regenerate with `CS_UPDATE_GOLDENS=1`).

### The convertcs LLM seam

Deterministic-first bodies: repo calls, row→DTO mapping, and structured
logging render deterministically; only the arm's **residual logic** — the
block between the prologue and the `return` — rides the LLM seam. The
prompt carries the query-replaced arm view (every `EXEC SQL` span already
presented as its deterministic repo call — the model never sees raw SQL),
the endpoint contract (action, DTO properties, params, return type), and
the rendered prologue; the model emits only the TODO block, never
signatures or SQL. Body gates: fixed signature and return statement,
every required repo call present, no raw SQL / `EXEC SQL`, brace balance
— gate errors feed the bounded retry loop, exhaustion degrades to the
`tuxgo:TODO` placeholder with a note, never a silent invention. Every
attempt is archived in the audit trail
(`convertcs-<endpoint>-attempt<n>.json`).

Filled bodies persist in the run's ledger
(`conversion_logs/ledger/convertcs-<component>.json`): a re-run re-gates
and reuses them — **filled bodies are never re-generated** — while TODO
seams retry the seam on the next LLM-enabled run.

```
go run ./cmd/tuxconv convertcs testdata/fixtures/cs/SVC_CUST_GET_DTL.pc \
    -mapping testdata/fixtures/cs/cust.mapping.yaml -no-llm
```

### discover -target cs: zero hand-written yaml

`discover -target cs <file|dir>` drafts `<name>.cs.mapping.yaml` — the
same scan-then-tag contract as the Go drafts, rendered for the convertcs
schema: namespace/area/component placeholders (filled from the
`convertcs` config defaults when set), one endpoint per qualifying
scenario slice with action/route suggestions, `requestFields`/`paramNames`
drafted from the FML read targets, and `dbMethods` const-name pins using
the exact names the plan derives — a new file's first pass needs no
hand-written yaml. Drafts never clobber; a fresh draft lands alongside a
kept one as a numbered sibling.

```
go run ./cmd/tuxconv discover testdata/fixtures/cs -target cs
```

### convertcs config

The `convertcs.*` section carries the run defaults (flag > config >
default precedence):

```yaml
convertcs:
  input: examples/SVC_CUST_GET_DTL.pc   # target when the CLI passes none
  mapping: mappings/cust.mapping.yaml   # mapping used when -mapping is absent
  out: conversion_logs/_staged          # output root for the component tree
  noLLM: false                          # deterministic-only kill switch
  namespace: OaoBackendApi              # draft-time namespace default
  area: OAOApplication.CustomerAuthenticate   # draft-time area default
```

Coverage advisories: every reachable dispatch arm the mapping leaves
unmapped prints the same style of `coverage:` line `convertgo` prints —
omission is a choice, silence about an arm is not — and a malformed
`scenarioRef` error names the axis the file actually dispatches on.

Real-corpus smoke: the env-guarded `TUX_CS_CORPUS` test (the same pattern
as `TUX_CORPUS`) runs the full deterministic path — draft → strict
mapping load → plan → generate → gates — against the local-only corpus
when the operator points it there; absent the variable everything skips,
and the corpus is never named in tracked content (the corpusguard keeps
it that way).

### Modes: LLM vs deterministic-only

Every command has two modes, switched by **one knob**: `run.llm: false` in
the yaml, or `-no-llm` on any single run (CLI wins). With the knob unset,
the LLM is used only where a profile actually resolves: copy
[`configs/.tuxgo.example.yaml`](configs/.tuxgo.example.yaml) to the
working-directory root as `.tuxgo.yaml` (or point any command at it with
`-config`) and edit — `run.profile` selects the
`models[]` entry (`onprem-vllm` on-prem by default, `local-dev-openrouter` for
local dev), keys resolve via `apiKeyEnv` (env first, the gitignored `apiKey`
literal as local-dev fallback — never commit a key). A profile whose key
doesn't resolve degrades to deterministic-only with the routing decision
logged (`key_source=unresolved`), never a hard failure.

What is deterministic (zero LLM calls) in every mode: the entire parse stack
(scan → IR), query classification, plan decomposition, models, db methods,
interfaces, handler glue, router, and the SQL-fidelity checks. The LLM fills
exactly one template-shaped gap per unit — and only in LLM mode:

| Command | The LLM seam | `-no-llm` degradation |
|---|---|---|
| `convert` | controller body per endpoint (from the query-replaced branch view + flow draft — never raw SQL) | body marked `skipped` in the ledger; an LLM-enabled re-run resumes exactly those |
| `convert` | one stub-synthesis attempt per unresolved external fn (call-site lines + inferred in/out signature shown; pure helpers land as idiomatic Go, declines keep the panicking stub) | all stubs stay panicking; synthesis runs first-run-only, a resume never re-calls |
| `batchpy` | stateful-batch service body | `# tuxgo:TODO service body` placeholder (simple shape is 100% deterministic either way) |
| `convertcs` | residual arm logic per endpoint (from the query-replaced arm view — never raw SQL); ledger resume never re-generates filled bodies | `tuxgo:TODO` residual-block placeholder, kept on seam exhaustion |
| `gentest` | field-mapping controller tests | `llm-required` notes (db/handler/passthrough are template-deterministic) |
| `discover` | endpoint name/route proposals | deterministic names from cursor/FML tokens, marked `# deterministic — edit freely` (`-target cs` is deterministic-only throughout) |

Dispatch-arm coverage: `discover` folds a dispatch-axis entry into one
scenario slice per detected value **plus the default arm** when the
dispatch chain ends in an `else` — the slice maps as
`scenarioRef: <var>=default`, and each arm's census stays exclusive to its
own slice (an arm's code never leaks into a sibling slice's reads/writes).
`convert` re-checks arm coverage and prints an advisory `coverage:` line
for every dispatch arm the mapping leaves unmapped — omission stays the
user's choice (`condition:`/`scenarioRef:`), silence about an arm is not.

Fn-library mode: a file (or directory) of helper functions with **no
Tuxedo entry** converts directly — no mapping draft, nothing to tag. Each
legacy fn becomes a Go method on the controller struct
(`controller/fns.go`: struct + store constructor + one method per fn),
with db methods for the library's SQL generated deterministically first.
Bodies ride the LLM seam with the same gates as controllers (parse, fixed
receiver/name, required store calls, no raw SQL); `-no-llm` leaves them
`skipped` in the ledger for an LLM-enabled resume. The output subtree
roots at the file stem (`foo.pc` → `foo/`).

Stub synthesis: unresolved external fns (`plan.Stubs`) render into
`controller/fnstubs.go` as panicking placeholders by default
(stub-and-carry-on). In LLM mode each stub first gets **one** best-effort
seam call showing its call-site lines plus the inferred input/output
signature (arg shapes + host declarations + the `-1`-on-failure return
convention), and the model either implements it as real idiomatic Go or
declines with `CANNOT_SYNTHESIZE`. The gate (fixed name, parse-clean, no
panic, no SQL/Tuxedo/FML, int return) decides; anything declined or
rejected keeps the panicking stub, never a guessed body. The run summary
marks each stub `(synthesized)` or `(stubbed: <reason>)`, and synthesis is
first-run-only — a resume whose `fnstubs.go` already landed makes no stub
calls. `-no-llm` keeps every stub panicking with a bare summary entry.

Mapping renames across runs: ledger unit IDs are positional, so renaming an
endpoint/method reuses its ID for a different unit. The ledger detects the
mismatch, resets those units to `planned` so they regenerate, and warns
(`mapping rename detected …`); files already on disk still carry the old
generation, so for a clean tree clear the staged dir and the service ledger
and re-run. The from-scratch recipe (default `./.tuxgo.yaml`, `-config` to
override, `-no-llm` for the deterministic draft):

```
rm -f mappings/mainTux.mapping.yaml
rm -rf conversion_logs/_staged/maintux conversion_logs/ledger/maintux.ledger.json
go run ./cmd/tuxconv convertgo tuxExamples/mainTux.pc -no-llm   # drafts mappings/, defers conversion
go run ./cmd/tuxconv convertgo tuxExamples/mainTux.pc -no-llm   # consumes the mapping, stages code + logs
```

`analyze` and `extract` never call the LLM at all. Egress note: live calls
send **derived** content only (query-replaced views, struct contracts); for
zero external egress run `-no-llm` or keep the on-prem `onprem-vllm` profile.
Every LLM exchange is archived with its assembled prompt and raw response in
`conversion_logs/audit/<run-id>/` (`<seam>-<name>-attempt<n>.json`).

Logging and artifacts (every run):

- `conversion_logs/logs/run-<id>.{jsonl,log}` — machine + human twins:
  run start/end with `duration_ms`, config routing, mapping load, the
  per-unit extraction trace (`logFileIR`), per-service start/complete,
  every WARN (unbalanced regions, unresolved external fns, tpcall targets).
- `conversion_logs/audit/<run-id>/ir-<file>.json` — the **IR snapshot of
  every ingested file**, written at extract time by `archiveAllIR` with a
  log line per file (path, entry, query/condition counts) so the parse
  result stays reviewable after the run. Same JSON schema as `xtux ir`.
- `conversion_logs/ledger/` — plan.json/plan.md + per-service ledgers.
- `conversion_logs/_staged/` — the staged output tree when the target
  service module is absent.

## Dependency note

The parse stack is tree-sitter (runtime + C grammar, v0.24.2 — same grammar as
before), embedded as `internal/tsscan/grammars/wasitter-c.wasm` and executed by
the pure-Go wazero runtime via `github.com/zema1/wasitter`. No cgo and no C
compiler on any platform — plain `go build` works everywhere, including
Windows (`CGO_ENABLED=0` is implicit). Deterministic, no network at test
time. Everything else is stdlib. To refresh the grammar, download the
matching `wasitter-c.wasm` from the wasitter release that matches the Go
package version and verify the published sha256.