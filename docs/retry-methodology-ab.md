# Retry methodology A/B — repair vs re-roll (2026-09-17)

## Question

When a deterministic gate rejects an LLM attempt, the retry re-sent the
original prompt plus the accumulated gate notes — the model never saw its
own rejected output ("re-roll"). The alternative ("repair") sends the
rejected payload back as an assistant turn, followed by the same notes, so
the model patches its own output. With deterministic gates and a
non-deterministic model, which converges better?

## Methodologies

| | Re-roll (shipped default) | Repair (toggle) |
|---|---|---|
| Messages on retry | system + original prompt + user turn with gate notes | system + original prompt + **assistant turn with the prior gated payload** + user turn with gate notes |
| Gates / budgets / retry count | identical | identical |
| Audit | `retry_repair: false` | `retry_repair: true` |

Implementation: `llm.SeamInput.Prompt(prev, notes)` hands the prior payload
to the seam (`internal/llm/seam.go`); `convert.seamMessages` assembles both
shapes (`internal/convert/retry.go`); the switch is
`convertgo -retry-repair` or `convert.retryRepair: true`. Retry turns are
counted against the prompt ceiling (they are real input). Per-unit
read-out: `tuxconv retrystats <audit-A> [<audit-B>]`
(`internal/audit/stats.go`, `cmd/tuxconv/retrystats.go`).

## Deterministic rune fix (both modes)

Every pre-fix run burned retries on `'Y'`/`'N'`/`'H'` rune literals — a
mechanical Pro\*C → Go transliteration slip. `fixRuneLiterals`
(`internal/convert/bodygate.go`, called from `cleanBody`) now rewrites
plain 3-byte letter runes to strings pre-gate. Guardrails: digits, symbols
and escapes (`'\n'`) stay for the gate — they may be intentional int math —
and literals inside arithmetic or index/slice expressions are never
rewritten. Rune-literal gate rejections after the fix: **6 during the two
pre-fix runs, 0 across all four post-fix runs.**

## Experiment

Target `tuxExamples/mainTux.pc`, OpenRouter `qwen/qwen3-30b-a3b`,
temperature 0.1, staged output (Tier B skipped). Between runs:
`rm -rf conversion_logs/_staged/maintux conversion_logs/ledger/maintux.ledger.json`
so every unit regenerates. Four pairs (two pre-fix, two post-fix), modes
alternating.

| Pair | Run | Mode | First-try | Attempts | Tokens | Accepted |
|---|---|---|---|---|---|---|
| 1 (pre-fix) | 17092026_001002 | roll | 3/6 | 9 | 37,747 | 6/6 |
| 1 | 17092026_001229 | repair | 3/6 | 9 | 40,587 | 6/6 |
| 2 (pre-fix) | 17092026_001554 | roll | 3/6 | 11 | 44,906 | 6/6 |
| 2 | 17092026_001818 | repair | 5/6 | 9 | 43,970 | 6/6 |
| 3 (post-fix) | 17092026_003718 | roll | 5/6 | 7 | 34,111 | 6/6 |
| 3 | 17092026_004005 | repair | 3/6 | 10 | 43,600 | 5/6* |
| 4 (post-fix) | 17092026_004347 | roll | 2/6 | 12 | 46,694 | 6/6 |
| 4 | 17092026_004609 | repair | 4/6 | 8 | 31,614 | 6/6 |
| **All** | | roll | **13** | **39** | **163,458** | 24/24 |
| **All** | | repair | **15** | **36** | **159,771** | 23/24* |

\* transport error (`connection reset by peer`) on a one-shot stub-
synthesis call, not a methodology failure.

## Findings

1. **Repair patches; re-roll rewrites.** Attempt-0 → attempt-1 response
   similarity in repair mode: 0.97–1.00 (pair 1), vs 0.21–0.91 for roll.
   Example: `HandleSipInsurance` attempt 0 rejected with
   `declared and not used: count`; the repair attempt changed exactly
   `count` → `_` (2 chars) and passed.
2. **Repair is at parity-to-slightly-better**, never clearly worse, on
   this corpus: across all pairs 15 vs 13 first-try units, 36 vs 39
   attempts, 159.8k vs 163.5k tokens. Post-fix only: 18 vs 19 attempts,
   75.2k vs 80.8k tokens.
3. **Variance swamps the mode effect** at this sample size: identical
   inputs ranged from 7 to 12 attempts for roll and 8 to 10 for repair,
   and the same unit took 1–4 attempts across runs. Repair's real promise —
   rescuing units that exhaust the retry budget — was not exercised: every
   unit passed within budget in both modes except the transport failure.
4. **Remaining rejection classes** (candidates for deterministic fixes
   next): undefined legacy identifiers (`c_ServiceName`, `c_flag`,
   `result`, `dateRange`), unused locals, and missed REQUIRED CALLS.
5. **Decision:** re-roll stays the default (simpler prompt, no
   prior-output anchoring; parity on evidence). Repair is retained behind
   `-retry-repair` / `convert.retryRepair` for targeted experiments,
   ideally against a failure-pressure corpus or wired Tier B.

## Reproduce

```sh
clean() { rm -rf conversion_logs/_staged/maintux conversion_logs/ledger/maintux.ledger.json; }
clean && go run ./cmd/tuxconv convertgo tuxExamples/mainTux.pc            # roll
clean && go run ./cmd/tuxconv convertgo -retry-repair tuxExamples/mainTux.pc
go run ./cmd/tuxconv retrystats conversion_logs/audit/<roll-id> conversion_logs/audit/<repair-id>
```

Raw evidence: `conversion_logs/audit/<run-id>/<kind>-<name>-attempt<N>.json`
(prompt, response, errors, `retry_repair`, token usage) and
`conversion_logs/logs/run-<run-id>.log`.
