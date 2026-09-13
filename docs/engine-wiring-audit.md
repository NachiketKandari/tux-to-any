# Engine-vs-wiring audit — computed-but-dropped findings (2026-09-14)

Method: five parallel agents swept the repo for one bug class — "engine
computes it, wiring drops it, or never finished wiring it" — exemplified
by `discover`'s dbMethods skeleton ignoring `aiSuggestion.Methods`
(fixed in a3941cf). Every Tier-1 item carries a producer site and a
verified no-consumer proof; items marked **[verified]** were re-checked
by hand, the rest are agent-reported with repo-wide grep evidence.
Two items in the convertcs blast radius were fixed immediately
(dac5659).

## Tier 1 — engine computes, wiring never surfaces (user-visible)

| # | Where | Engine computes | Wiring gap | Status |
|---|---|---|---|---|
| 1 | batchpy | `Result.Structure` (pychk structural + txn + seam issues, pygen.go:131-137) | cmd prints only `PyDetail` when `PyMode=="ast"`; failing modules written to `python_out/` with zero signal; only consumer folds into `SyntaxOK` inside the archived JSON | open |
| 2 | pyplan | repo-shape fetch `BindKwargs` | iterator template (`py_batch_repo_fetch_iterator.tmpl:5`) renders `cur.execute(CONST)` unconditionally; `bindOrder` also counts the SELECT's `INTO :host` vars as binds (stripped from the const by `stripInto`) → wrong signatures, ORA-01008 class; per golden `bat_demo_returns.py:64-69` | open — runtime correctness |
| 3 | pyplan | `Orchestration` blocks (pyplan.go:429-446) | only `setup-before-first-loop` + loop extents; post-loop/inter-loop regions never reach the seam prompt while it demands "exactly these blocks … nothing else" → epilogue logic dropped from LLM bodies | open |
| 4 | buffers.roles | config registry + built-in Ibuffer/Obuffer/Sbuffer/Rbuffer roles (config.go:97-104, ir/extract.go:455) | honored by extract/plan/convertgo; discover (`flowir.go:18,20`), batchpy (`batchpy.go:235`), convertcs (`convertcs.go:82`) all pass `ir.Options{}` → every buffer role unknown on those paths (error-add idiom never fires, census inflated) | open |
| 5 | plan cmd | `Plan.Warnings` (unmapped-arm advisories, plan.go:311-351) | `cmd/tuxconv/plan.go` and `plan/emit.go` WriteMD never render them; surfaces only if the user later runs convertgo **[verified]** | open |
| 6 | Tier B validate | `validate.CompileAll` build/vet/test errors (validate.go:127-168) | pipeline reads only `DegradeReason`/`Summary`; `Errors`/`OK` zero consumers; doc claims failures "feed the bounded retry loop" — no loop sees them | open |
| 7 | convertcs | `EndpointPlan.Residue` (loud SCEN-D4 slice evidence), `.Scenario`, `.LineSpan` | zero consumers anywhere; plan never archived either — computed and dropped (milestone wiring gap) **[verified]** | open |
| 8 | llm seam | `Response.FinishReason`, token `Usage` | `RunSeam` reads only `Content` — a `finish_reason: length` truncation is indistinguishable from success; audit Exchange carries neither. Also `Endpoint.Stream` (config default true) has no reader and `Client.Stream` (SSE) zero production callers **[verified]** | open |
| 9 | gentest | testscan warnings (unreadable dir / unparseable files, testscan/scan.go:255+) | generate mode logs only the count; `testgen` reads only `rep.Services`; check-only mode prints them | open |
| 10 | batchpy | `Retention.LogSitesTotal`/`LogCallsEmitted` (report.go:28-29, "reported alongside" per its own contract) | never printed by the cmd summary; discoverable only in the archived JSON | open |
| 11 | batchpy | `res.Fidelity` per-const detail | archived as a count only; deviations/unverifiable reasons console-transcript-only | open |
| 12 | extract | `-out` flag in directory mode | dir branch writes to `paths.state`, ignores the flag without a note | open |

## Tier 2 — dead engines, inert knobs, write-only data (verified no-consumer)

Config knobs parsed/validated but inert:
- `elision.mode` (G3 — never implemented/honored)
- `retrieval.*` (documented disabled, but validated as live schema)
- `paths.target` (defaulted, documented R8, zero readers)
- `batchpy.dmlLoop` / `batchpy.chunkSize` on the **repo shape** — the
  shape every stateful flow takes: `py_batch_repo_dml.tmpl` always
  row-by-row, `CHUNK_SIZE` defined-unused in goldens

Write-only engine outputs:
- `Ledger.Map` / `Entry.Targets` — the promised "what happened to this
  function/query?" reverse index has no reader surface
- `budget.View.Report` / `ShrinkPct` — the §4.7 query-replacement audit
  engine, computed on every convert seam call, never recorded
- `Scenario.Responses` (SCEN-D9 response-value provenance) — only its
  instability side-effect (Residue) escapes; list never rendered
- `Scenario.ErrorAdds` — census computed, absent from every artifact
- batchpy `RepoMethod.RowShape` / `.InLoop`, `DALFn.RowShape`,
  `Plan.RepoUsed` — populated, zero readers
- `analyzer.Report.LocalFns` — fn inventory computed per run, no CSV
  column, never emitted
- `ir.TPCall.Function` — call-site attribution unread
- `ExecSQLStatement.Raw` — byte-exact audit trail, everything reads
  `Normalized`
- `DispatchAxis.Normalized` — normalize-chain evidence never propagates
- `ir.Condition.Predicate` — duplicate parse (flow re-parses with
  define substitution, IR without) — divergence risk by construction
- `gen`'s `hostVars` map — the host-var typing engine (CType/GoHint/
  Array/Nullable) dead at the Go-gen stage (`hostType()` returns
  NullString unconditionally)
- `SourceFacts.ParseErrors` — survives in JSON dumps; no pipeline stage
  ever surfaces it (README claims "never a silent drop")
- `Directive.IsHeader`/`IsSystem` — classified, unread

Dead API (zero production callers):
- `gen.ControllerSignature` + `gen.sortedEndpoints` (re-inlined in
  `ControllerInterface` with drifted ordering semantics — drift trap)
- `profile.Register` / `profile.For` / `ErrUnknownProfile` / `Layout`
  (the target.profile selector routes nowhere)
- `cmd/tuxconv scenarioSliceFor` (G-SCEN1 fallback never wired)
- `pred.Idents`, `flow.RenderTree` (test-only)
- `csgen.EpData.Todo` (superseded by TodoSlot — dead state)
- `csplan.Prop.Alias` (abandoned alias tracking)

Inverse pattern — wiring promises what no engine feeds:
- `Plan.Orphans` — rendered in plan.md as a "§4.6 completeness check",
  never computed by any engine
- `ledger.StatusBlocked` — counted and printed every run ("%d blocked"),
  no code path ever sets it

## Fixed during the audit (dac5659)

- `cschk.OracleParams` — `qp.Params[0]` unguarded (panic on zero-bind
  queries, a legitimate plan state) + `got` computed then discarded
- `cschk.containsSQLHead` — discarded its keyword and re-ran the generic
  SQL-head regex per keyword: one SELECT head reported five issues, four
  mislabeled, feeding the convertcs seam's retry notes verbatim

## Proposed fix order

1. batchpy silent-write pair: print `Result.Structure`; archive
   `res.Fidelity` detail (Tier-1 #1, #11)
2. pyplan runtime correctness: cursor-bind fix (iterator template +
   `bindOrder` INTO pollution) and Orchestration tail coverage (#2, #3)
3. buffer-roles wiring: thread `irOptions(cfg)` through discover /
   batchpy / convertcs (#4)
4. plan-warnings + Tier-B surfacing (#5, #6), convertcs residue
   archiving (#7)
5. seam truncation signal (`FinishReason`/`Usage` into the audit
   Exchange) (#8)
6. Tier-2 cleanup sweep: delete dead API, mark inert knobs as reserved
   or wire them, give write-only data a reader or a removal note
