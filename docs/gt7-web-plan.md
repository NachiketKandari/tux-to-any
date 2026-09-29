# GT-7 Web (visual) Plan

Status: **implemented** (2026-09-29) — companion to
`docs/gt7-log-fixtures-plan.md` (CLI, shipped in commit c078828).
Source: browser Tests step (`web/`) vs the shipped `gentest` CLI surface.
See "Implementation notes" at the bottom for the shipped surface and the
deliberate deviations; CLI preflight tests and the web build are green.

## Why this exists

GT-7 shipped three capabilities, all CLI-only:

| GT-7 surface | CLI flag | In the browser today? |
|---|---|---|
| Log-driven fixtures | `-log-file <path>` | **No** — no attach path, no provenance shown |
| Staged verified snapshot | `-out <dir>` (alias `-base`) | **No** — generate always writes in place |
| LLM test-name polish | `--nice-names` | **No** — and Generate never requests the LLM at all |

What the browser actually runs (`web/app/api/jobs/[id]/gentest/route.ts:30-33`):

- Gap report → `gentest <root> -check-only`
- Generate → `gentest <root> [-no-llm]` — the UI never passes `useLLM`
  (`web/app/page.tsx:226` calls `gentest(job.id, mode)` with the
  `useLLM=false` default in `web/lib/api-client.ts:106`), so every
  browser-generated suite is deterministic.

The Tests panel (`web/components/tests-panel.tsx`) is two buttons plus one
raw stdout `<pre>`. None of the GT-7 outputs — fixture provenance, gates,
`— N methods from log values, M assumed` — are structured for the UI, and
the runtime log never reaches the server.

## Goal

Surface GT-7 in the Tests step end-to-end: attach a runtime log, run the
log-driven generation, and visualize where every fixture value came from —
without weakening the local-only privacy contract.

## Workflow the UI serves

1. Convert to Go (existing step).
2. Run the edited app and capture its runtime log (existing CLI workflow).
3. Tests step → attach the log (drag-drop/paste).
4. Generate (deterministic by default; optional nice names; optional
   verified snapshot) → provenance table + gates.
5. Inspect generated tests in the tree, download the snapshot.

## UX — Tests step (visual)

The Tests step keeps its two sub-tabs (Tests, Metrics).

### Runtime log card (new, above the report)

- Drop zone / browse for `.txt` / `.log` (5MB cap, same as source
  uploads) + a paste-text fallback.
- Attached state: file name, size, "used by the next run", remove.
- Preflight line (after a Gap report with a log attached — needs the small
  CLI extension below): `log: N traces · X methods matched · M assumed ·
  W warnings`.
- Privacy note: "stays in this job's tmpdir; never sent anywhere".

### Generate options row

- **LLM (gap-fill)** toggle — off = `-no-llm`; on = seam when a key
  resolves. Requested-vs-effective verdict badge, reusing the Convert
  pattern (`web/app/api/jobs/[id]/convert/route.ts:65-79`) plus
  `GET /api/llm-status`.
- **Nice names** checkbox — appends `--nice-names`; disabled with a
  "needs an LLM key" hint when no key resolves. CLI parity: it prints
  `nice-names: no LLM client available — names left as generated`.
- **Verified snapshot (`-out`)** checkbox — stages a complete source+test
  snapshot and runs `go test -count=1` inside it; the converted tree is
  never modified. Off (default) keeps today's in-place behavior.

### Fixture provenance (new, replaces the raw tail)

- Summary chips: `N from log · M assumed · K llm calls`.
- Filterable table from `gentest_summary.json` `fixtures[]`
  (`service · layer · func · source`): `log <short-id>` (green) vs
  `assumed` (amber). Clicking a function opens its generated test file in
  the tree viewer.
- Gates list (`PASS/FAIL + first 400 chars`) with a non-fatal orange
  banner when `tests_failed` — same semantics as the CLI's
  `gentest: tests FAILED (see gates)`.
- The raw CLI output stays available behind a disclosure.

## API contract

`POST /api/jobs/:id/gentest` body grows (all optional; defaults preserve
today's behavior):

```json
{ "mode": "check|generate", "useLLM": false, "niceNames": false,
  "staged": false, "withLog": true }
```

- `check` + log → `gentest <root> -check-only -log-file <job>/gentest/runtime.log`
  (requires the small CLI extension below).
- `generate` → `gentest <root> [-no-llm] [--nice-names]
  [-log-file <path>] [-out <job>/go-gentest]`.
- Response gains `gentestSummary`, parsed from the newest
  `conversion_logs/audit/<run-id>/gentest_summary.json` written by the run
  (CLI cwd = job dir; fallback: parse the stdout tail, as today).

New sub-route `web/app/api/jobs/[id]/gentest/log`:

- `POST` multipart (`file`) or JSON (`{ name, text }`) → writes
  `job.dir/gentest/runtime.log`.
- `DELETE` → detach (removes the file + clears job fields).

## Job state (`web/lib/jobs.ts`)

```ts
gentestLog?: { name: string; bytes: number; traces?: number; matched?: number; assumed?: number; warnings?: number };
gentestSummary?: {
  fixtures: { service: string; layer: string; func: string; source: string }[];
  gates: string[];
  testsFailed: boolean;
  llmCalls: number;
  files: string[];
  staged: string[];
};
gentestNiceNames?: boolean;          // requested
gentestLLMEffective?: boolean;       // actual (requested-vs-effective pattern)
gentestLLMNote?: string;
gentestOutRoot?: string;             // staged snapshot root, when -out ran
```

## File-by-file touchpoints

| File | Change |
|---|---|
| `web/app/api/jobs/[id]/gentest/route.ts` | accept new body fields; build args; read/save summary |
| `web/app/api/jobs/[id]/gentest/log/route.ts` | **new** — attach/detach runtime log |
| `web/lib/jobs.ts` | new job fields (above) |
| `web/lib/api-client.ts` | `gentest()` signature + `uploadGentestLog()` / `clearGentestLog()` |
| `web/components/tests-panel.tsx` | log card, options row, provenance table, gates, badges |
| `web/app/page.tsx` | pass state/handlers to `TestsPanel` (wiring only) |
| `web/app/api/jobs/[id]/archive/route.ts` | include staged snapshot + `gentest-summary.txt`; never the raw log by default |
| `web/lib/metrics.ts` | `fixturesFromLog`, `fixturesAssumed`, `niceNames` fields; Metrics panel hint |
| `web/README.md` | Tests-step walkthrough + privacy note for logs |
| `cmd/tuxconv/gentest.go` | **optional, recommended** — `-check-only` parses `-log-file` and prints a `log:` coverage line (today check-only returns before the parse block: `:107-114` vs `:153-161`) |

## Privacy / guardrails

Runtime logs are exactly the artifact class that carried an internal
service endpoint during the GT-7 CLI session, and they can contain
request/response bodies. Therefore:

- logs live only in the job tmpdir, same as uploads — no egress (viewer is
  loopback-only, no telemetry);
- the raw log is **not** included in the Download .zip by default and never
  echoed into `web-metrics.jsonl`;
- size-capped (5MB) and UTF-8 sanitized on save; malformed-line tolerance
  stays server-side (the parser counts warnings, never fails a run);
- fixture values do land in generated test files — the UI says so next to
  the attach control.

## Verification plan

- **CLI parity**: every browser run logs the exact command (`runTux` already
  prints `$ …`), so a demo can be reproduced from the Logs sub-tab.
- **No-log run**: generate stays byte-identical to today (GT-7
  backward-compatibility guarantee); the panel shows assumed fixtures only.
- **Log run** (local corpus, deliberately not committed):
  `riskPipelineTest/logfile.txt` → expected `44 from log · 6 assumed ·
  0 llm calls` (the 2026-09-29 verified run), gates visible with the
  known non-fatal failure banner.
- **Nice names**: no key → checkbox warns, CLI note surfaces; with a fake
  OpenAI-compatible endpoint → literals-only polish, and a
  structure-changing response lands as "names left as generated".
- **Snapshot mode**: converted tree untouched; snapshot downloads with
  tests + staged sources.
- `npm run build` + lint clean.

## Open decisions

1. **Gap report with log**: do the small CLI extension (accurate preflight)
   or ship without preflight in v1? — recommend the extension; ~20 lines in
   `cmd/tuxconv/gentest.go`.
2. **Staged snapshot download**: extend `/archive` with a `?tree=gentest`
   selector vs a separate endpoint. — recommend the selector.
3. **Log lifetime**: per-job attach (survives step switches), cleared by
   re-upload — matches the job-snapshot model. Confirm acceptable.

## Implementation notes (shipped)

CLI (enabling the preflight):

- `cmd/tuxconv/gentest.go` — `-check-only` now parses `-log-file` and prints
  `log: … lines/traces/warnings` + `log: fixtures — N scanned functions with
  log values, M assumed (of T)`, then exits as before. No writes.
- `internal/testgen/logfixture.go` — exported `CoverLog(rep, data)` +
  `LogCoverage`/`LogCoverageEntry`; mirrors `FixtureSource.Provenance`
  (success trace first, failed fallback), skips the models layer.
- `internal/testgen/logfixture_test.go` — `TestCoverLog` pins the counts,
  the incomplete-trace → assumed fallback, the models-layer skip, and
  nil-safety.

Web:

| File | Change |
|---|---|
| `web/app/api/jobs/[id]/gentest/log/route.ts` | **new**: multipart/JSON attach + DELETE; 5MB cap; invalidates stale preflight/summary |
| `web/app/api/jobs/[id]/gentest/route.ts` | `useLLM`/`niceNames`/`staged`/`withLog`; `-in-place` default; preflight parse; newest `gentest_summary.json` → `gentestSummary`; metrics counters |
| `web/lib/jobs.ts`, `web/hooks/use-job.ts` | `gentestLog` / `gentestPreflight` / `gentestSummary` / effective-LLM fields |
| `web/lib/api-client.ts` | `GentestOptions`, `uploadGentestLog`, `clearGentestLog`, `readConvertedFile(…, tree)`, `downloadArchiveUrl(id, "gentest")` |
| `web/components/tests-panel.tsx` | runtime-log card (drop/paste), preflight chips, LLM + nice-names + snapshot options, provenance table, gates, file chips + inline viewer, snapshot download |
| `web/app/page.tsx` | handlers + props wiring |
| `web/app/api/jobs/[id]/files/route.ts` | `?tree=gentest` reads snapshot files |
| `web/app/api/jobs/[id]/archive/route.ts` | `?tree=gentest` zips the snapshot; summary carries fixture/gate lines |
| `web/lib/metrics.ts` | `fixturesFromLog` / `fixturesAssumed` / `niceNames` / `staged` |
| `web/README.md` | GT-7 section + privacy note |

Deliberate deviations from the plan text:

- Non-staged browser runs pass `-in-place` (the plan said "default remains
  in place"). Without the flag, module-less converted trees fall back to
  `paths.staged` (`conversion_logs/_staged/`) and the tests vanish from the
  Convert tree; `-in-place` writes next to the converted code, matching the
  panel's existing promise. Module-ful trees resolve the same folder.
- `niceNames` is forced off when the LLM toggle is off (the CLI would only
  warn and skip); requested-vs-effective is still reported.
- Snapshot summary file lists are deduped: module-less trees can write
  same-named layer test files to one flat `-out` path (the CLI keeps the
  last), so the UI shows each path once.
- Preflight coverage counts all scanned functions (including ones a later
  generate may skip as unsupported); the post-run provenance table is the
  authoritative per-rendered-unit view.

Verification (2026-09-29):

- `go test -p 1 -count=1 ./...` green; `gofmt` clean on touched files.
- `npm run build` + `npx tsc --noEmit` green.
- Live API pass (minimal sample, synthetic log): attach → preflight
  `2 from log · 10 assumed (of 12)`; staged generate summary `2 from log /
  4 assumed`, provenance + flat gate line (`skipped — outside any Go module`
  for this module-less tree); snapshot file read shows
  `// fixture: log aaaa1111 | assumed`; `archive?tree=gentest` returns the
  13-entry zip; in-place generate lists the three `_test.go` files in the
  Convert tree and reports `no key → nice names skipped` when requested.
