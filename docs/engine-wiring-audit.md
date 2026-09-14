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
| 1 | batchpy | `Result.Structure` (pychk structural + txn + seam issues, pygen.go:131-137) | cmd prints only `PyDetail` when `PyMode=="ast"`; failing modules written to `python_out/` with zero signal; only consumer folds into `SyntaxOK` inside the archived JSON | fixed — report wiring |
| 2 | pyplan | repo-shape fetch `BindKwargs` | iterator template (`py_batch_repo_fetch_iterator.tmpl:5`) renders `cur.execute(CONST)` unconditionally; `bindOrder` also counts the SELECT's `INTO :host` vars as binds (stripped from the const by `stripInto`) → wrong signatures, ORA-01008 class; per golden `bat_demo_returns.py:64-69` | fixed — runtime correctness |
| 3 | pyplan | `Orchestration` blocks (pyplan.go:429-446) | only `setup-before-first-loop` + loop extents; post-loop/inter-loop regions never reach the seam prompt while it demands "exactly these blocks … nothing else" → epilogue logic dropped from LLM bodies | fixed — tail coverage |
| 4 | buffers.roles | config registry + built-in Ibuffer/Obuffer/Sbuffer/Rbuffer roles (config.go:97-104, ir/extract.go:455) | honored by extract/plan/convertgo; discover (`flowir.go:18,20`), batchpy (`batchpy.go:235`), convertcs (`convertcs.go:82`) all pass `ir.Options{}` → every buffer role unknown on those paths (error-add idiom never fires, census inflated) | fixed — and deeper: the registry keys themselves never matched (see below) |
| 5 | plan cmd | `Plan.Warnings` (unmapped-arm advisories, plan.go:311-351) | `cmd/tuxconv/plan.go` and `plan/emit.go` WriteMD never render them; surfaces only if the user later runs convertgo **[verified]** | fixed — artifacts + cmd |
| 6 | Tier B validate | `validate.CompileAll` build/vet/test errors (validate.go:127-168) | pipeline reads only `DegradeReason`/`Summary`; `Errors`/`OK` zero consumers; doc claims failures "feed the bounded retry loop" — no loop sees them | fixed — errors print; doc claim corrected (no retry loop is fed: the seams' gates are per-body structural checks) |
| 7 | convertcs | `EndpointPlan.Residue` (loud SCEN-D4 slice evidence), `.Scenario`, `.LineSpan` | zero consumers anywhere; plan never archived either — computed and dropped (milestone wiring gap) **[verified]** | fixed — archived + seam prompt carries all three; span read from the plan, not re-parsed |
| 8 | llm seam | `Response.FinishReason`, token `Usage` | `RunSeam` reads only `Content` — a `finish_reason: length` truncation is indistinguishable from success; audit Exchange carries neither. Also `Endpoint.Stream` (config default true) has no reader and `Client.Stream` (SSE) zero production callers **[verified]** | fixed — truncation signal; Stream deferred to the Tier-2 sweep |
| 9 | gentest | testscan warnings (unreadable dir / unparseable files, testscan/scan.go:255+) | generate mode logs only the count; `testgen` reads only `rep.Services`; check-only mode prints them | fixed — warnings print |
| 10 | batchpy | `Retention.LogSitesTotal`/`LogCallsEmitted` (report.go:28-29, "reported alongside" per its own contract) | never printed by the cmd summary; discoverable only in the archived JSON | fixed — `log_sites=e/t` in the summary |
| 11 | batchpy | `res.Fidelity` per-const detail | archived as a count only; deviations/unverifiable reasons console-transcript-only | fixed — fidelity.json + report |
| 12 | extract | `-out` flag in directory mode | dir branch writes to `paths.state`, ignores the flag without a note | fixed — flag wins (C1) |

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
- ~~`Plan.Orphans` — rendered in plan.md as a "§4.6 completeness check",
  never computed by any engine~~ — removed (field + render): the JSON was
  always empty (`omitempty`), so no artifact ever carried it; the promise
  died with the render
- ~~`ledger.StatusBlocked` — counted and printed every run ("%d blocked"),
  no code path ever sets it~~ — status marked RESERVED (a later-version
  "blocked on unresolved dependency" state); the summaries no longer
  print the phantom count

### Tier-2 disposition (2026-09-14 sweep)

Dead API — deleted: `gen.ControllerSignature`/`sortedEndpoints`;
`profile.Register`/`For`/`ErrUnknownProfile` + the registry (Default()
returns gonav directly) + `Layout` (no engine read the tree shape);
`cmd scenarioSliceFor` (+ the orphaned `flow.FallbackScenario`);
`pred.Idents` (test-only); `flow.RenderTree` wrapper (`RenderSpan` is the
production path); `csgen.EpData.Todo`; `csplan.Prop.Alias`; gen's
write-only `hostVars` map (the uniform `sql.NullString` rule stays,
`CType`/`GoHint` stay live in csdraft/csplan).

Inert knobs — marked reserved in the config schema comments:
`elision.mode`, `retrieval.*`, `paths.target`; `batchpy.dmlLoop`/
`chunkSize` documented simple-shape-only (repo renders row-by-row) in the
config and the `-dml-loop` flag help.

Write-only data — given readers: `Scenario.Responses`/`ErrorAdds` render
in the scenario artifact header (SCEN-D9 provenance, unstable flagged);
`SourceFacts.ParseErrors` land in the IR (`parse_errors`) and WARN per
site at extract time ("never a silent drop" now true); the §4.7
query-replacement accounting (`budget.View.Report`/`ShrinkPct`) logs per
unit; pyplan `RepoMethod.RowShape` feeds the batchpy seam prompt's row
rule. Removal/reserved notes on: `Ledger.Map`/`Entry.Targets` (the JSON
artifact is the review surface), `analyzer.Report.LocalFns` (CSV schema
pins columns), `ir.TPCall.Function`, `ExecSQLStatement.Raw`,
`DispatchAxis.Normalized`, `ir.Condition.Predicate` (divergence risk
flagged), `Directive.IsHeader`/`IsSystem`, `DALFn.RowShape`,
`RepoMethod.InLoop`.

## Fixed during the audit (dac5659)

- `cschk.OracleParams` — `qp.Params[0]` unguarded (panic on zero-bind
  queries, a legitimate plan state) + `got` computed then discarded
- `cschk.containsSQLHead` — discarded its keyword and re-ran the generic
  SQL-head regex per keyword: one SELECT head reported five issues, four
  mislabeled, feeding the convertcs seam's retry notes verbatim

## Fixed after the audit (batchpy report wiring)

- Tier-1 #1: the batchpy cmd prints every `Result.Structure` issue
  (`structure: line N: msg`) alongside the module summary — failing
  modules are no longer silent writes (`writeBatchModuleReport`).
- Tier-1 #11: per-const `res.Fidelity` detail archives as
  `<module>.fidelity.json` (deviated kinds/details, unverifiable methods)
  and prints per deviation, not just the retention count.
- Tier-1 #2: `Binds` derives from the executable SQL (`bindsOf` =
  `bindOrder(emitSQL(q))`) — INTO host targets no longer count as binds —
  and the fetch-iterator template renders the binds (`cur.execute(CONST,
  {kwargs})`) like fetch-single/DML. The `bat_demo_returns.py` golden
  regenerates to a bind-less `fetch_demo_ret_tmp` (all five of its former
  "binds" were INTO targets; the old output failed at runtime on every
  call). Pinned by internal/pygen/wiring_test.go (tests verified to fail
  against the pre-fix engine).
- Tier-1 #3: `Orchestration` covers inter-loop gaps and the post-loop
  epilogue (`between loops (source X-Y)`, `epilogue (source X-Y)`), so the
  seam prompt's "exactly these blocks … nothing else" contract no longer
  drops tail logic (`fn_close_bat`/`exit` in the BAT_DEMO_RETURNS corpus
  now reach the body contract).
- Tier-1 #4: `irOptions(cfg)` now threads through discover/discovercs
  (`extractFlowIR`), batchpy (`convertBatchFile`), and convertcs — and the
  re-verification found the audit's own premise half-wrong: the
  "honored" extract/plan/convertgo paths were inert too, because
  `DefaultBuffers()` supplied CamelCase keys ("Ibuffer") while
  `resolveBufferRole` matches lowercase ("ibuffer") — every buffer in
  every path resolved unknown-role, and the error-add exclusion never
  fired anywhere. Fixed on both sides: the resolver matches
  case-insensitively (yaml keys stay free-form), defaults are lowercase.
  Proven end-to-end: a non-ERR `Fadd32` into an input-role buffer no
  longer surfaces as a response `write` in discover drafts (census
  deflated 8→7 on the mainTux probe). Pinned by
  cmd/tuxconv/flowir_test.go + internal/flow/buffer_roles_test.go.
- Tier-1 #5: `Plan.Warnings` render in plan.md ("Coverage warnings"
  section) and plan.json, and `runPlan` prints them (`coverage:` prefix,
  the same one convertgo uses).
- Tier-1 #6: the convert summary prints every Tier-B error
  (`tier B error: …`) next to the one-line verdict — a failed go
  build/vet/test no longer degrades to a single word; the stale "feed the
  bounded retry loop" doc claim is corrected to describe reality (the
  seams' retry gates are per-body structural checks; Tier B is a batched
  post-conversion gate).
- Tier-1 #7: convertcs archives `<component>_csplan.json` (scenario refs,
  line spans, residue, requests — previously computed and dropped), and
  the seam prompt now carries `EndpointPlan.Scenario` (arm header),
  `Residue` (kept-verbatim regions the LLM must implement), and reads the
  arm span from `LineSpan` instead of re-parsing the Span string (the
  drift trap). Pinned by cmd/tuxconv/convert_summary_test.go,
  internal/plan/plan_test.go, internal/csgen/seam_surface_test.go.
- Tier-1 #8: `RunSeam` now reads the whole `Response` — every archived
  Exchange carries `finish_reason` + token usage (provider-reported or
  estimated, `usage_estimated` says which), and a non-"stop" finish
  reason logs a WARN and appends a truncation note that reaches the next
  attempt's prompt when a later gate rejects. Truncation alone does not
  flip the gate's outcome — the gate still decides. `Endpoint.Stream`
  stays inert (copied from config, never consulted; Chat force-disables,
  SSE `Client.Stream` has zero production callers) — wiring streaming is
  a transport change deferred to the Tier-2 cleanup sweep. Pinned by
  internal/llm/seam_test.go (the tests do not compile against the
  pre-fix Exchange).

## Proposed fix order

1. ~~batchpy silent-write pair: print `Result.Structure`; archive
   `res.Fidelity` detail (Tier-1 #1, #11)~~ — done
2. ~~pyplan runtime correctness: cursor-bind fix (iterator template +
   `bindOrder` INTO pollution) and Orchestration tail coverage (#2, #3)~~
   — done
3. ~~buffer-roles wiring: thread `irOptions(cfg)` through discover /
   batchpy / convertcs (#4)~~ — done (incl. the registry key-case bug)
4. ~~plan-warnings + Tier-B surfacing (#5, #6), convertcs residue
   archiving (#7)~~ — done
5. ~~seam truncation signal (`FinishReason`/`Usage` into the audit
   Exchange) (#8)~~ — done
6. ~~Tier-2 cleanup sweep: delete dead API, mark inert knobs as reserved
   or wire them, give write-only data a reader or a removal note~~ — done
   (disposition table under Tier-2)

Also closed during the sweep: Tier-1 #9 (gentest generate mode prints
scan warnings), #10 (batchpy summary shows `log_sites=emitted/total`),
#12 (`extract -out` honored in directory mode, flag > config).
