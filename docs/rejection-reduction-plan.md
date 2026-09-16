# Deterministic rejection-reduction plan (2026-09-17)

Executor notes: this plan is self-contained. Line numbers are as of commit
`035e184`; re-grep if drifted. Work items are ordered by risk-adjusted
payoff — do W1, then W2, then W3, then measure. W4 is optional/P2. After
EACH item: run the unit tests named, then the full suite (`go test ./...`,
`go vet ./...`, `gofmt -l`), commit, and only then start the next item.

## 0. Evidence (do not re-derive)

Census over all 8 A/B runs (4 pairs, `conversion_logs/audit/17092026_00*`):

| Class | Count | Verdict |
|---|---|---|
| undefined identifier | 29 | 24 are legacy C names injected/left by OUR view |
| rune literal | 21 | fixed already (`fixRuneLiterals`) |
| unused local | 20 | **real logic drops** — keep gate, never auto-blank |
| required call missing | 1 | real logic drop — keep gate |
| parse error (`missing ','`) | 1 | rare, leave |
| transport | 1 | infra |

Undefined identifiers by name: `c_ServiceName` 9, `c_errmsg` 9,
`c_d2u_active_flg` 3, `c_flag` 3, `dateRange`/`navHistoryRows`/`result`/
`rows`/`cD2uActiveFlg` 1 each.

Root causes confirmed by reading the rejected responses and the prompts:

1. `responseShaping` writes the literal text `for _, row := range result`
   (internal/gen/prompt.go:481-483) but the view's store call is bare — no
   `result` exists. Models copy the phantom name. Evidence:
   `17092026_004347/controller_method-GetDefaultMFNavList-attempt0.json`
   (`range result` rejected), attempt1 (`range rows` rejected).
2. `Fadd32(FML_ERR_MSG, c_errmsg)` is rewritten to
   `err = fmt.Errorf("%s", c_errmsg)` (internal/convert/seamrewrite.go:134)
   but the `errlog(..., "S31005", ...)` line that fills `c_errmsg` is
   stripped earlier as scaffold (internal/convert/pipeline.go:1287). The
   S-code is discarded and the injected line references an undefined
   buffer. 9+9 rejects.
3. Unresolved-fn calls keep session args in the view:
   `fn_is_d2u_active(c_ServiceName, ls_match_acc.arr, &c_d2u_active_flg, c_errmsg)`
   — accepted outputs call `fnIsD2uActive(sql_mf_nav_comp_cd, ls_match_acc, &c_d2u_active_flg)`
   and the system prompt already says session plumbing is middleware-owned.
4. Model re-emits the branch's leading `if (c_flag == 'F') {` header; the
   declaration of `c_flag` was hoisted, so the gate rejects undeclared
   `c_flag`. System prompt says never emit the header; nothing enforces it.

Re-mine any time with (from repo root):

```python
python3 - <<'EOF'
import json, glob, re, collections
runs = ["17092026_001002","17092026_001229","17092026_001554","17092026_001818",
        "17092026_003718","17092026_004005","17092026_004347","17092026_004609"]
cats = collections.Counter()
for run in runs:
    for f in glob.glob(f"conversion_logs/audit/{run}/*-attempt*.json"):
        for err in (json.load(open(f)).get("errors") or []):
            m = re.search(r'undefined identifier \\?"([A-Za-z0-9_]+)', err)
            if m: cats["undef:"+m.group(1)] += 1
            elif "rune literal" in err: cats["rune"] += 1
            elif "declared and not used" in err: cats["unused"] += 1
            elif "missing from the body" in err: cats["required"] += 1
            elif "does not parse" in err: cats["parse"] += 1
for k, v in cats.most_common(): print(v, k)
EOF
```

## Principles

- Fix the VIEW and the PROMPT; do not weaken gates. `unusedLocalErrs`,
  `requiredCallErrs`, `validateBody` stay exactly as they are — the
  rejected bodies they catch drop real business conditions.
- Deterministic rewrites must be total functions with unit tests, applied
  to both modes (they are pre-gate, mode-independent).
- Do not use `_ = name` blanking for unused locals: it would compile
  bodies that silently dropped the branch the value was computed for.
- Every item ends with a real-run measurement (section 5).

## W1 — remove the phantom `result` from the shaping block (tiny)

**Change** `responseShaping` (internal/gen/prompt.go, the `if !mapped / else`
block at ~line 480): delete the `for _, row := range result { ... }`
snippet. Proposed text for the mapped branch:

```
  init: data = make([]*models.X, 0); then append the shaped element once per returned row
  (range over the slice you captured from the s.store call above; declare the capture)
```

No variable name is invented, so nothing can be copied verbatim.
Do not name any placeholder.

**Tests**: `internal/gen/gen_test.go:443` pins `"for _, row := range result"`
— remove that expectation and add an absence check
(`strings.Contains(ctx, "range result")` must be false). Run
`go test ./internal/gen/`.

**Gotcha**: keep the no-mapped fallback line unchanged (no loop case).

## W2 — error legs carry the errlog S-code instead of `c_errmsg` (medium)

**Change** (two parts):

1. Reorder the view passes in `controllerBody`
   (internal/convert/pipeline.go:465-485) so the seam rewrite runs BEFORE
   the scaffold strip, because the scaffold strip deletes the `errlog`
   line that carries the S-code:

   ```
   stripDeadComments → rewriteLegacySeams → stripLegacyScaffold → stripPreludeDecls
   ```

2. Track a pending S-code in `rewriteLegacySeams`
   (internal/convert/seamrewrite.go:56-76):
   - On an `errlog(` line matching `"(S\d{5})"`, set `pendingCode` and
     leave the line unchanged (the later scaffold pass drops it).
   - When a `Fadd32(FML_ERR_MSG, expr)` line is rewritten and `expr` is a
     known error buffer (`c_errmsg`, `c_err_msg`, `errmsg`, `err_msg`):
     emit `err = errors.New("<pendingCode>");` when `pendingCode != ""`;
     otherwise keep today's `err = fmt.Errorf("%s", expr);`. Clear
     `pendingCode` after any non-errlog line that is not the consuming
     Fadd32 (keep the window to adjacent lines — the corpus pairs them
     directly).
   - Literal exprs (`Fadd32(..., "text", 0)`) keep today's behavior.

   `errors.New` compiles: the controller file's import list is
   content-gated and adds `errors` when `errors.` appears
   (internal/convert/pipeline.go:1463-1464).

**Tests**: `internal/convert/seamrewrite_test.go` — add a case pairing
`errlog(..., "S31005", ...)` + `Fadd32(..., FML_ERR_MSG, c_errmsg, 0)` →
`err = errors.New("S31005");`; assert the no-errlog case still emits
`fmt.Errorf` (existing tests at seamrewrite_test.go:53-54 pin that and must
stay green). Check `TestStripLegacyScaffold` (pipeline_test.go:688) still
passes — the reorder does not change its standalone contract. Run
`go test ./internal/convert/`.

**Gotchas**:
- Only 1-line errlog calls exist in the corpus; use the existing
  `balancedParens`/`callArgs` helpers if a multiline form shows up.
- `fnHelperBody` (pipeline.go:~635) does NOT run `rewriteLegacySeams`;
  leave as-is unless it shows the same reject class.
- After the reorder, verify the run log's `legacy seams rewritten` counts
  still behave (they only tally; no consumer).

## W3 — strip session/buffer args from unresolved-fn calls (medium)

**Change** new function in internal/convert/seamrewrite.go, e.g.:

```go
// stripSessionArgs drops middleware-owned identifiers (session service
// name, error buffers, user/session ids) from calls to unresolved legacy
// fns in a view — the Go stub is variadic and the accepted shape omits
// them. Also drops the strcpy(c_ServiceName, rqst->name) prologue line.
func stripSessionArgs(src string, fnNames map[string]bool) (string, int)
```

- Session arg set (normalized via `normalizeSeamTarget`): `c_ServiceName`,
  `c_errmsg`, `c_err_msg`, `errmsg`, `err_msg`, `c_user_id`, `c_userid`,
  `li_session_id`, `l_sssn_id`, `DEF_USR`, `DEF_SSSN`.
- Only rewrite call lines whose callee is in `fnNames` (pass
  `opts.Plan.Stubs[i].Fn` from the call sites). Keep all other args,
  including out-params like `&c_d2u_active_flg`.
- Drop the literal statement line `strcpy(c_ServiceName, rqst->name);`
  (special-case that exact first arg; do NOT strip generic `strcpy` —
  pipeline_test.go:707 pins `x = strcpy(dst, src);` surviving).
- Call sites: `controllerBody` (after `rewriteLegacySeams`,
  pipeline.go:~480) and `fnHelperBody` (after its prelude strip,
  pipeline.go:~636).
- Usage of `callArgs`/`splitTopArgs`/`balancedParens` for arg surgery;
  rebuild the line as prefix + `name(remaining)` + `trailingSuffix`.

**Tests**: seamrewrite_test.go cases for arg dropping (fn call with and
without session args; strcpy line dropped; generic strcpy untouched). Add
one full-view integration assertion in pipeline_test.go that the fixture
view contains no `c_ServiceName`/`c_errmsg` (find the existing
`TestLegacySeamRewrites`-style test and extend it). Run
`go test ./internal/convert/`.

**Gotchas**: stub signature inference reads `opts.Source` (original), not
the view — `collectStubEvidence` unaffected. Do not touch `chk_sssn`
handling (already neutralized).

## W4 (P2, optional) — leading branch header

Only if post-W1-W3 measurement still shows `undefined identifier "c_flag"`
or leading-header parse failures. Options, in order of preference:
1. Post-generation repair: generalize `repairArmWrapper`
   (internal/convert/bodygate.go:267) to unwrap a leading
   `if <endpoint condition> { ... }` wrapper even when `axisVar == ""`,
   matching the branch's leading condition text instead.
2. Pre-prompt view strip: remove the leading `if (...) {` and its matching
   final `}` from the view using `sliceScanner` depth tracking.

Do not attempt without a reproducing rejection in the new runs.

## 5. Measurement protocol (after W1-W3)

Baseline default-mode (roll) runs to beat, same target
(`tuxExamples/mainTux.pc`), stage-clean between runs:

| Run | First-try | Attempts | Tokens |
|---|---|---|---|
| 17092026_001002 | 3/6 | 9 | 37,747 |
| 17092026_001554 | 3/6 | 11 | 44,906 |
| 17092026_003718 (rune fix) | 5/6 | 7 | 34,111 |
| 17092026_004347 (rune fix) | 2/6 | 12 | 46,694 |

Procedure (two runs for noise; each ~2.5 min, ~40k tokens):

```sh
clean() { rm -rf conversion_logs/_staged/maintux conversion_logs/ledger/maintux.ledger.json; }
clean && go run ./cmd/tuxconv convertgo tuxExamples/mainTux.pc   # run 1
clean && go run ./cmd/tuxconv convertgo tuxExamples/mainTux.pc   # run 2
go run ./cmd/tuxconv retrystats conversion_logs/audit/<run1>
go run ./cmd/tuxconv retrystats conversion_logs/audit/<run2>
```

Success criteria:
- **Zero** `undefined identifier` errors for `c_errmsg`, `c_ServiceName`,
  `result`, `rows` in both runs' audit Exchanges (re-run the census
  script).
- Attempts ≤ 7 per run with first-try ≥ 5/6 (match the best baseline, now
  that the mechanical classes are gone). Acceptable range: 6-8 attempts;
  investigate if > 9.
- No new rejection class appears.
- All units accepted (no transport failures counted).

If W1-W3 do not move the numbers, do not proceed to W4 — re-mine the
census and revisit root causes first. Nondeterminism is large (baseline
spread 7-12 attempts); treat single-run differences under 2 attempts as
noise and compare the rejection *classes*, which are deterministic.

## 6. Commit plan

One commit per item or W1+W2+W3 together, e.g.:

```
convert/gen: remove phantom result from shaping; errlog S-code in error
legs; strip session args from unresolved-fn views
```

Update `docs/retry-methodology-ab.md` only if the measured attempt counts
change materially (append a post-fix table).
