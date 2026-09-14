# convertcs — the .NET Core milestone plan

Status: milestone 1 shipped (`convertcs` end-to-end on the CUSE-shaped
fixture, golden-pinned). This plan covers the remaining phases: the LLM
seam for service bodies, discover mapping drafts, config, broad
arm-coverage hardening, and the env-guarded real-corpus smoke.

Design decisions locked at kickoff:

- **New subcommand** `tuxconv convertcs` (mirrors `convertgo`; the Go
  pipeline and its byte-pinned goldens stay untouched).
- **Deterministic-first bodies**: repo calls, row→DTO mapping, and
  structured logging render deterministically; only the arm's residual
  logic rides the LLM seam; `-no-llm` leaves `tuxgo:TODO` placeholders.
- **Broad arm coverage upfront**: single-row SELECT, cursor/multi-row,
  DML, MERGE, multi-query arms; `tpcall` arms stub with a loud warning.
- **Fidelity-first SQL**: the NamedQueries consts carry the source SQL
  verbatim minus the `INTO :host` plumbing; `OracleParameter` names stay
  the source's own host binds.

## Phase A — LLM seam for service bodies

- **A1** `csgen.Options` gains `Client` / `MaxRetries` / `Audit` /
  `Budget` (mirroring `pygen.Options`); `-no-llm` output stays
  byte-identical (the golden proves it every run).
- **A2** Body prompt: the query-replaced scenario slice (every
  `EXEC SQL` span already presented as its deterministic repo call), the
  endpoint contract (action name, DTO properties, params, return type),
  and the rendered deterministic prologue — the model emits only the
  TODO block, never signatures or SQL.
- **A3** Body gates: fixed signature and return statement, every
  required repo call present, no raw SQL / `EXEC SQL`, brace balance.
  Gate errors feed the retry loop; exhaustion degrades to the TODO
  placeholder with a note — never a silent invention.
- **A4** cmd wiring: client resolution from the configured profile, per
  attempt audit archive (prompt + raw response + gate errors), ledger
  resume semantics (filled bodies are never re-generated).
- **A5** Tests: `llm.FakeServer` happy path, gate-rejection retry, and
  the unchanged `-no-llm` golden.

## Phase B — discover drafts the cs mapping

- **B1** `discover -target cs` drafts `<name>.cs.mapping.yaml`:
  namespace/area/component placeholders, one endpoint per scenario slice
  with action/route suggestions.
- **B2** Draft `requestFields`/`paramNames` from the FML read targets,
  and `dbMethods` const-name pins — zero hand-written yaml for a new
  file's first pass.

## Phase C — config section

- **C1** `convertcs.*` in `internal/config` (`out`, `noLLM`,
  namespace/area defaults), example yaml entries, and the standard
  flag > config > default precedence in the cmd wiring.

## Phase D — broad-arm-coverage hardening

- **D1** DML-arm fixture variant (`UPDATE` + commit → `Task<int>` tx
  shape) + golden.
- **D2** Cursor / select-multi arm fixture variant (`List<DTO>` shape)
  + golden.
- **D3** Multi-query arm fixture variant (SELECT + UPDATE ordering,
  method-name suffixes) + golden.
- **D4** Advisories: an unmapped dispatch arm prints the same style of
  coverage warning convertgo prints — omission is a choice, silence
  about an arm is not; malformed scenarioRef errors name the axis.

## Phase E — real-corpus smoke

- **E1** Env-guarded smoke (`TUX_CS_CORPUS`, the same pattern as
  `TUX_CORPUS`): when the operator points it at the local-only corpus,
  the pipeline runs structurally against the local reference
  conversion; absent the variable everything skips. Never named in
  tracked content (the corpusguard keeps it that way).

## Phase F — docs + history

- **F1** README refresh for the full milestone, then phase-wise commits
  in the established style and push.

Build order: A → D (they share fixtures) → B → C → E → F.
