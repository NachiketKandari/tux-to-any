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
in ~0.3s (exercised by the skip-guarded smoke test).

## Layout

- `internal/tsscan` — pre-scan + mask + AST walk → `SourceFacts`
  (functions, branches, loops, calls, decls, returns, directives, SQL
  statements, comments, unbalanced; additive: `Params`, `ParseErrors`,
  `Switches`).
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
pyplan pygen pychk analyzer testscan testgen`) under `internal/`. Every
downstream stage consumes the tree-sitter facts.

Commands (`cmd/tuxconv`):

```
go run ./cmd/tuxconv extract <file|dir>     # IR JSON; archived per run
go run ./cmd/tuxconv plan <file> -mapping <yaml>
go run ./cmd/tuxconv discover <file|dir> [-stdout]
go run ./cmd/tuxconv convert <file|dir> [-mapping <yaml|dir>] [-no-llm] [-base dir]
go run ./cmd/tuxconv batchpy <file|dir> [-no-llm] [-shape auto|repo] [-dml-loop batch|rowbyrow] [-out dir]
go run ./cmd/tuxconv analyze <file|dir> [-csv out.csv] [-weights csv] [-pattern mf_]
go run ./cmd/tuxconv gentest <converted tree> [-check-only] [-no-llm] [-layers db,controller,handler] [-base dir]
```

Verified output: `tuxconv convert testdata/fixtures/stripped -mapping
testdata/fixtures/stripped/mappings -no-llm` converts 4/4 services with
**0 sql deviations** (`diff -r` clean across all four services, with
controller bodies correctly skipped in deterministic-only mode). The nav
fixture end-to-end (`tuxconv convert testdata/fixtures/nav -mapping
configs/nav.mapping.yaml -no-llm`) converts cleanly, and the batch→Python
pipeline (`batchflow` + `pyplan` + `pygen` + `pychk`) writes deterministic
modules for both corpus shapes (`bat_min_simple` simple, `bat_min_repo`
repo) with 0 sql deviations.

Also included: `analyze` (`internal/analyzer` — triage rubric + CSV, with
the `# tuxgo marks:` marker kept verbatim so CSVs stay interchangeable) and
`gentest` (`internal/testscan` + `internal/testgen` — post-conversion test
generation). The flow machinery drives convert/discover and the scenario
artifacts.

### Modes: LLM vs deterministic-only

Every command has two modes, switched by **one knob**: `run.llm: false` in
the yaml, or `-no-llm` on any single run (CLI wins). With the knob unset,
the LLM is used only where a profile actually resolves: copy
[`configs/.tuxgo.example.yaml`](configs/.tuxgo.example.yaml) to the
working-directory root as `.tuxgo.yaml` and edit — `run.profile` selects the
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
| `batchpy` | stateful-batch service body | `# tuxgo:TODO service body` placeholder (simple shape is 100% deterministic either way) |
| `gentest` | field-mapping controller tests | `llm-required` notes (db/handler/passthrough are template-deterministic) |
| `discover` | endpoint name/route proposals | deterministic names from cursor/FML tokens, marked `# deterministic — edit freely` |

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

tree-sitter (runtime + C grammar) is the single dependency — cgo-compiled,
deterministic, no network at test time. Everything else is stdlib: the C
side goes to one real grammar, at the cost of a vendored C compilation step.