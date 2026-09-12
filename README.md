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
- `testdata/fixtures` — the synthetic demo fixtures (no proprietary bytes).
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
