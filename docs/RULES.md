# tux-to-any — Rules Audit

Date: 2026-09-24. Scope: full repo (`internal/*`, `cmd/tuxconv/*`, `configs/*`, `.tuxgo.yaml`, `README.md`, `docs/*.md`, `web/*`).
Method: repo-wide scan + spot-verified enforcement sites (`file:line`) and pinning tests.

> Note: repo has no `AGENTS.md` / `CLAUDE.md` / Cursor rules. All "rules" below are
> embedded rule systems: conversion invariants, gates, config precedence, and test pins.
> Each entry = **what** the rule is, **where** enforced, **how** (mechanism).

## 0. Rule index (summary)

| # | System | Representative rules | Status |
|---|---|---|---|
| 1 | Parse / scan / mask (`tsscan`) | EXEC word-boundary, lenient `;`-close, same-length mask, `''`→`0 `, broken-`#define`, banner live/dead, brace-outside-only, WASM pool | Followed, pinned |
| 2 | Condition parser (`pred`) | C-precedence, single-cmp, call-opaque, literal-only define subst, Raw-degrade | Followed, pinned |
| 3 | IR extraction (`ir`, `sqltext`, `namer`) | Entry pick, fragment rubric, BF, chains, `q<N>`/cursor flatten, factual dedup, FML/FNOTPRES, tpcall/tpacall, buffer/host/DefineAt/LiveFacts, `AS TUXC_` | Followed, pinned |
| 4 | Flow / scenario / discover | Axis cascade, default arm, coverage advisories, 4 ref-forms, filter-v1, merged naming, scan-then-tag, axes registry, census, tx/responses | Followed, pinned |
| 5 | Convert / gen / gates | 5-stage pipeline, 1-knob LLM mode, per-seam table, TODO/REJECTED/panic-stub degrade, body/SQL/chunk/retry/template/Tier-A+B gates, ledger resume, audit | Followed, pinned |
| 6 | CS / Python / gentest targets | 7-file CS tree, repo/DTO/log determinism, batchpy cursor-batch rubric, py gates, gentest scan/generate/twin | Followed, pinned |
| 7 | Operational / config / arch | `-config > ./.tuxgo.yaml > Default()`, strict keys, flag>config>default, budget estimator, inert fields, key resolution, workers, logging, ledger layout, corpusguard, goldens, sweep | Followed, pinned |
| 8 | Contradictions | 7 real drifts/overstatements + ~11 reconciled scoped-exceptions | See §8 |

---

## 1. Parse / scan / mask (`internal/tsscan`) — where + how

| Rule | Where enforced | How |
|---|---|---|
| `EXEC` word-boundary (any whitespace `EXEC<ws>SQL`, case-insensitive) | `internal/tsscan/region.go:55-91` (`matchWord:55`, `execMarkerEnd:77`, `startsExec:301`) | Gate on `isIdentByte` delimiters + ASCII fold. Pin: `scan_golden_test.go:74`, `README.md:67` |
| `EXEC` kind classification + cursor upper-case | `region.go:124-170` | Switch on `Fields(ToUpper(norm))`. Pin: `scan_golden_test.go:105`, `ir_test.go:57` |
| Terminator = first `;` outside quotes/comments | `region.go:316-372` | State-machine skip of `'…"`/comments. Pin: `scan_golden_test.go:74` |
| Lenient close (missing `;` closes at next `EXEC SQL`, emits `exec_sql_lenient` defect) | `region.go:362-365,377-381` | Fallback + defect fact, never silent merge. Pin: `TestScanLenientExecClose` |
| EOF region → `Unbalanced{exec_sql}`, never a statement | `region.go:383-385`, skip `scan.go:71-80` | Gate `if rg.unbalanced{continue}`. Pin: `unbalanced.pc`, `ir_test.go:246` |
| Same-length block mask (`EXEC…;` → `/*…*/`, newlines kept) | `region.go:452-472`, called `scan.go:28` | `out[start:start+2]='/*'`, interior→space except `\n`. Pin: `TestDeterminism`, parity goldens |
| Offset preservation (all C positions identical post-mask) | `facts.go:5-11`, `region.go:447-451`, `scan.go:23-28` | Invariant by construction. Pin: `TestFragmentScan` |
| Broken `#define` + debris share mask routine | `region.go:473-491` | Merged-list masking. Pin: `scan_golden_test.go:12,45` |
| Unclosed-`{`/comment padding past EOF (real positions untouched, `BodyEndLine=0`) | `scan.go:34-45`, `region.go:36-37`, `walk.go:284-285,339-346` | Suffix-only padding + `unmatchedAt` gate. Pin: `ir_test.go:246`, sweep |
| Empty char literal `''` → `0␣` (same length, valid C) | `region.go:496-533` (trigger `512-514`), via `region.go:492` | Comment/string-aware scan. Pin: `sweep_test.go:13`, `README.md:18-19` |
| Broken `#define` = unbalanced parens, strings skipped | `region.go:538-640` (`cutDefineHead`, `parenBalance`, `skipString`) | `balanced==false` gate. Pin: `TestScanWeirdDirectives` |
| Broken-define side channel (mask for grammar, keep `define/NAME VALUE` fact) | `scan.go:25-27,66-69,291-299`, `region.go:580-584`, `walk.go:628-641` | Sorted-merge side channel. Pin: same test (1 fn, 0 ParseErrors) |
| Directives recorded raw, never substituted | `walk.go:625-671` | `stripTrailingComment` + kind switch. Pin: `ir_test.go:148` |
| Comment inventory (block + line, 1-based) | `region.go:231-279` | Single-pass scanner. Pin: `scan_golden_test.go:105` (nav 83), `:216` |
| Banner = closed + `ver\.?\d` + `added\|comment\|start\|end`; `Live` = single-line only | `region.go:40-43,248-251,434-444`, `facts.go:239-249` | Classifier gate. Pin: `TestScanBannerCommentDebris`, `TestInComment` |
| Dangling `*/` debris self-heal (mask `[lastCommentEnd,i+2)`) | `region.go:192-197,473` | C-lexical early-terminate fallback. Pin: `SVC_DEMO`+`BUF_LEN` survive, `README.md:74-77` |
| `InComment(line,col)` = queryable "is code?" | `facts.go:309-318` | Range gate. Pin: `scan_golden_test.go:216` |
| Braces counted only in code state (comments/strings/`EXEC` skipped) | `region.go:175-213` | Pre-scan state machine. Pin: `README.md:83-85` |
| Open deficit → single `Unbalanced{braces}`; extra `}` each loud | `region.go:214-228`, `scan.go:34-44` | Collapse/dedup fallback. Pin: `ir_test.go:246` (`exec_sql,braces`) |
| WASM pin (`wasitter-c.wasm`, tree-sitter-c v0.24.2, wazero, no cgo) | `session.go:11-18` | Embed. Pin: 22 519-line zero-error smoke (`sweep_test.go:49`) |
| Pooled exclusive sessions (healthy return, ABI-fail discard) | `session.go:20-30,46-80`, use `scan.go:46-64` | `sync.Pool` + ownership gate. Pin: `TestDeterminism` |
| Every `ERROR`/`MISSING` → `ParseError`, never dropped | `scan.go:62,267-289` | Additive facts. Pin: sweep + `scan_golden_test.go:12` |
| 1-based line/col everywhere | `facts.go:283-285`, `region.go:104-114`, `+1` sites in `scan.go:130-205`, `walk.go` | Convention gate. Pin: all goldens |
| `NestDepth`: branches = enclosing braced `if/elseif` (`else` never); loops = +loops; `Depth` = brace depth at keyword | `scan.go:211-257`, `facts.go:165-195`, `walk.go:178-180` | Containment counters. Pin: `TestNavBranchingFactor` (75/142) |
| `else if/else` flatten as siblings | `walk.go:365-435`, `facts.go:156-163` | `walkIf(alt,ElseIf)` recursion. Pin: `ir_test.go:21` |
| Calls = only `call_expression→identifier` (`Args` raw; `IsTpCall=tpcall\|tpacall` case-sensitive; `fn_/chk_` prefixes) | `walk.go:583-603`, `facts.go:118-143` | AST gate. Pin: `README.md:69-73`, `ir_test.go:148` |
| Fragment wrap (`void __fragment`, rebase `-2`, `Fragment=true`) | `scan.go:93-124,128-185` | Wrapping fallback. Pin: `TestFragmentScan`, `ir_test.go:221` |

## 2. Condition parser (`internal/pred`) — where + how

| Rule | Where | How |
|---|---|---|
| C-precedence (`\|\|` loosest → `&&` → `!`/cmp → primary; `&&`/`\|\|` n-ary) | `pred.go:181-270` | Recursive descent. Pin: `pred_test.go:42,62,83,105,127` |
| Single comparison only (`a<b<c`, arithmetic/ternary/lone `\|/&` → `Raw`) | `pred.go:231-258` | Gate. Pin: `TestParseDegradeRaw` (15 shapes) |
| Calls keep raw top-level-comma args; `for(…)` = `Call(for)` | `pred.go:356-428` | Quote/paren-aware split. Pin: `:138,164` |
| Glued `±digits` = `Lit`; `100L`/spaced sign → `Raw` | `pred.go:289-354` | Gate. Pin: `:164,210,234` |
| `Parse` never fails (leftover → `Raw{Fields-joined}`) | `pred.go:54-66` | Degrade fallback. Pin: `:174,250,323` |
| `Substitute` = `Ident→Lit` only via resolver; calls opaque, `Raw` untouched, copy-on-write | `pred.go:80-107` | Masking. Pin: `:363,398,415` |
| `IsLitText` (numeric/quoted only), `IsBareIdent` (single ident) | `pred.go:112-143` | Gates. Pin: `:424,457` |
| `ParseCode`: dotted chains → last segment, `'…'`→`"…"` | `code.go:14-77` | Rewrite. Pin: `equivalence_test.go:100` |
| `Equivalent`: spelling-insensitive idents + commutative `&&`/`\|\|` + literal norm (`'F'=="F"`, `NULL=nil`) | `equivalence.go:17-97` | Normalization gate. Pin: `:9,34,61,78` |

## 3. IR extraction (`internal/ir`, `sqltext`, `namer`) — where + how

| Rule | Where | How |
|---|---|---|
| Entry = first `SVC_*`; fragment-no-entry → `__fragment`; no-entry non-fragment → no conditions | `extract.go:220-228`, `conditions.go:15-17` | Gate. Pin: `ir_test.go:21,221,275` |
| Fragment iff no `SVC_*` + zero fns; file-mode auto-refrag; dir-mode bypasses | `extract.go:46-57,68-69,171-178` | Gate. Pin: `:221,275,293,333` |
| `BranchCount` = non-`else` headers; `BF=Σ2^NestDepth` | `extract.go:252-262` | Aggregation. Pin: `:21` (75/142), `:176` (3/4) |
| Chains group entry `Depth==1` braced arms; `≥2` branches (file) / `≥1` (fragment); fragment-no-chain synthesizes default | `conditions.go:109-138,14-28,81-103` | Gate + fallback. Pin: `:21,221,293` |
| `q<N>` = query-kind ordinal (cursors consume numbers; non-query `counter--`) | `query.go:15-34,124,153` | Counter gate. Pin: `TestNavQueries` (`q1…q5`) |
| Cursor flatten (`DECLARE CURSOR` → `SELECT_MULTI`, ID = raw cursor name, span to last `OPEN/FETCH/CLOSE`, `RowShape` = first `FETCH INTO`) | `query.go:39-112`, `types.go:187-191` | Flatten fallback. Pin: `cur_demo_hist` 4 binds/6 cols/232-343 |
| `SELECT…INTO` → `SINGLE` else `MULTI`; `INTO` positional (never binds) | `query.go:114-139,311-382` | `splitSelectRefs` + `clauseEndAfter`. Pin: `q1` dual |
| `INTO` keeps dotted/`[0]`, drops `:ind`/`INDICATOR` | `query.go:184-293` | Gate. Pin: `README.md:79-82` |
| Binds deduped first-appearance; `: name` legal | `query.go:295-358` | Dedup. Pin: `:176` |
| Tables per-kind clause-owned, alias dropped, subquery recursion | `query.go:386-608` | Classifier. Pin: `:57,176` |
| First `ORDER BY` any depth wins | `query.go:508-553` | Gate. Pin: `DEMO_HIST_DATE desc` |
| Factual dedup (same `DedupKey` → `DuplicateOf=first`, never delete) | `query.go:168-182`, `types.go:209-210,322-333` | No-delete link. Pin: `q5→q3`, unique 6 |
| `FmlOpOf` (`Fget=get`/`Fadd=add`, field/buffer/target positions, `<4` args reject) | `fml.go:33-72`, `util.go:27-143` | Classifier. Pin: `TestFmlOpOfExported`, `TestNavFMLAndBuffers` |
| `get.Optional` iff same-line guard has `FNOTPRES` | `fml.go:76-93` | Containment gate. Pin: c2 optional / c1 not |
| `Dropped` = `FML_USER_ID/FML_SESSION_ID`/ERR-field | `fml.go:67`, `types.go:73-104` | Gate. Pin: op0 dropped |
| `add.Code` = latest preceding same-func writer else own literal else `""` | `fml.go:99-148` | Correlation fallback. Pin: `S31005`, `0001/X1` |
| `CanonicalCType` (lower/trim, drop sign, `varchar2→varchar`, `long int→long`, …) | `ctype.go:17-39` | Normalization. Pin: `ctype_test.go:5` |
| tpcall correlate (`Service/Send/Recv` sync-only; window = innermost branch else func else file; `Fadd`-before/`Fget`-after) | `tpcall.go:17-71,115-141` | Correlation gate. Pin: `TestTPCallCorrelation` (35-40, 2+2) |
| Ambiguous (buffers ID'd + both contracts empty) → `Ambiguous`, never guess | `tpcall.go:66-67` | Defect fact. Pin: `TestAmbiguousTPCall` |
| `tpacall` async (`args[3]` = flags never buffer; recv via `tpgetrply` + post-`Fget`s) | `tpcall.go:29-111` | Gate + fallback. Pin: `tpacall_async_test.go:18,50,80` |
| Buffer roles (union FML + send/recv, sorted, full-or-last-`_` case-insensitive) | `extract.go:410-472` | Registry gate. Pin: `Ibuffer→input` |
| Host vars (union binds+rows+FML+flags; first `VarDecl` wins then `Params`; `varchar→Nullable`) | `extract.go:476-609` | Union + shadow gate. Pin: parity goldens |
| `DefineAt` scoped (func shadows file from line; `#undef` tombstones; macros never resolve) | `extract.go:277-355`, `types.go:260-270` | Scoped gate. Pin: `BUF_LEN@200=6144`, `@60` invisible |
| `LiveFacts` filter (comment-starting calls/SQL dropped) | `extract.go:185-213` | Filter gate. Pin: `TestLiveFacts` |
| Externals (called-not-defined `fn_*/chk_*`, sorted; dir-mode resolves `DefinedIn/HasSQL`) | `extract.go:63-145,378-406` | Gate + corpus join. Pin: `:148,333` |
| Preamble = entry-owned ops outside every span | `fml.go:150-188` | Filter gate. Pin: 7 preamble ops |
| `sqltext`: `StripINTO` / `CollapseBinds` / `CanonicalSQL` (idempotent) / `ExecutableBinds` (lower dedup, skip `:1`) | `sqltext.go:20-136` | Masking/pipeline/gate. Pin: `sqltext_test.go:5,27,48`, `canonical_test.go:8` |
| `Format` layout (heads col-0, 4sp indents, `INSERT/VALUES` one-per-line, `BETWEEN…AND` never splits, idempotent) | `format.go:7-301` | Pretty fallback. Pin: `format_test.go:5` (20 cases) |
| `AS TUXC_` (`TUXC_<SVC>_<UNIT>_<N>`, sanitize upper, empty→`X`, 30B + `FNV32 _%08X`, idempotent inject) | `alias.go:24-233` | Pure function + right-to-left insert. Pin: `alias_test.go:8,25,33,61,79,94` |
| `namer`: Go `Get<Cursor>/verb+Table`, `Param` lowerCamel, `Prop` exported, row→`sql.NullString`; Py `fetch_*`, CS `<Verb><Table>Query` | `namer.go:40-224` | Derivation + `CanonicalCType` switch. Pin: `namer_test.go:9,22` |

## 4. Flow / scenario / discover (`internal/flow`, `contract`, `analyzer`) — where + how

| Rule | Where | How |
|---|---|---|
| Axis cascade `normalize-chain > direct-compare > char-compare`; `≥2` values + `≥2` guards else nil; comment-masked harvest | `scenario.go:150-195,191-194,294-308`, `axes.go:68-180`, `scenario.go:225-257` | Priority cascade + guard-count gate. Pin: `scenario_test.go:100-200`, `axes_test.go:31`, `dispatch_symbolic_test.go:37,64` |
| Symbolic RHS must be defined (`DefineAt` chain), LHS not constant; `RefName` = alias else base ident | `axes.go:105-121`, `scenario.go:75-148` | Membership gate. Pin: `TestDispatchAxisSymbolicNeedsDefinedRHS` |
| Chain-aware fold (5 `FoldKind`s; later siblings dropped when earlier provably true; residue loud `L%d: cond`) | `scenario.go:387-632,683-1016`, `scenariofilter.go:528-597` | Tribool `foldExpr` + `taken` mask. Pin: `chain_test.go:75,107`, `scenario_test.go:310-453` |
| Preamble = whole chains before first axis chain (never cut inside); per-arm census from surviving leaves only; `CarriesContract` = has FML either side | `scenario.go:491-500,634-657,1267-1382` | Split scan + kept-map visit. Pin: `TestScenarioKeptAndCensus`, `chain_test.go`, `discover.go:151-155` |
| Exclusive partition (one arm → one slice); `Kept+Dropped == classified`; `ScenarioCondition` synthesized `Index 0`; `Source` verbatim vs `Render` with `/*L<n>*/`; `KeptBlocks` honest runs | `scenario.go:502-509,607-631,1483-1657,2131-2365` | Chain exclusivity + runs, not `BodyExtent`. Pin: `chain_test.go:107,144`, `scenario_test.go:761-1128` |
| Default arm = tree truth (`else`-terminated chain); `DefaultKey()` collision-free; `Scenarios()` = Domain + default; plan validates value ∈ Domain or default | `scenario.go:67-73,334-385,667-681`, `axes.go:415-441`, `plan.go:232-238` | `chainExtent` + key scan. Pin: `chain_test.go:46-75`, `discovery_test.go:248-312` |
| Coverage advisories (uncovered arm → `Warnings`, never fatal; hint suggests `scenarioRef: key=default`) | `plan.go:424-473`, `discover.go:396-587`, `return_anchored.go` | Span-containment check. Pin: `plan_test.go:280`, `discovery_test.go:289` |
| Exactly one of `condition \| conditionRef(c<n>[.k]) \| scenarioRef(var=value) \| scenarioFilter(expr)` | `plan/mapping.go:31-51,66-182`, `contract/mapping.go:16-40` | `set!=1 → error`. Pin: `plan_test.go:202`, `discovery_test.go:138-336` |
| Filter-v1: only idents/literals/`==/!=/&&/||/!/()` else positioned reject; De Morgan + DNF caps (32 terms / 25 assignments); one re-fold (not union); witness-required prune | `scenariofilter.go:23-449,490-802` | `pred.Parse` + `IsRaw→unsupported` + reach mask. Pin: `scenariofilter_test.go:23-326`, `plan/scenariofilter_test.go:16-97` |
| Merged naming (`in {F,I}` / `F_or_I` / `&&`); artifacts `<entry>.<var>_<val>.pc` + numbered siblings + `shared.md/json`; twin axes `<entry>.axes.json/md` | `scenariofilter.go:804-849`, `scenarios.go:16-113`, `axes.go:315-483`, `axes_report.go:8-62` | Sorted vars/values + `seen` map. Pin: `scenariofilter_test.go:52,92`, `axes_artifacts_test.go` |
| Discover scan-then-tag (tool never invents; AI names advisory); qualification = return-anchored (`TPSUCCESS/tpforward` proof; `TPFAIL` never); error-add = field-OR-value; keys `c<n>[.k]` deterministic; tail-feeder promotion; drafts never clobber (numbered sibling); `dbMethods` from `plan.DefaultMethodNames` (no drift) | `discover.go:24-394,413-625`, `discover.go:219-244`, `plan.go:752-791`, `cmd/tuxconv/discover.go:24-587` | Shared `qualifies` closure + `ConditionFor` replay; `os.Stat` sibling loop. Pin: `discover_test.go:35-137`, `return_anchored_test.go:194-320`, `chain_test.go:231`, `plan_test.go:246` |
| `--list-axes` / `--filter --stdout` read-only preview (no drafts/LLM; Go tree only; honest `KeptBlocks`) | `discover.go:39-74`, `discoverpreview.go:21-168`, `filterexamples.go:21-80` | Early return + real `Parse+ScenarioForFilter`. Pin: `discoverpreview_test.go`, `scenariofilter_test.go:250` |
| Flow draft (`Render` skeleton + TODO residue; `droppedCallees` elided; fetch-loop → `rows+range`); `flow` cmd read-only; `ArmView` language-neutral | `render.go:10-304`, `armview.go:39-120`, `cmd/tuxconv/flow.go:33-164` | `Resolver` placeholders when nil. Pin: `render_test.go:35-113`, `armview_test.go:5` |
| Condition census = same walk as draft (skeleton `#`-for-idents, exclusions for loops/`else`/guards/SQLCODE/FNOTPRES); seam requires every cond + effects + rejects `if{}` | `census.go:30-227`, `render.go:120-134,232-254`, `convert/conditions.go:27-139`, `convert/guards.go:153-217` | Additive walk; `recordConditionGaps` + `condition-census-<Ep>.json`. Pin: `census_test.go:46-115`, `conditions_test.go`, `bodygate_test.go` |
| Mixed guards live in both seams (`FoldMixed` + behavior, dedup `(Cond,Alt)`, `pred.Equivalent` under bindings) | `mixedguard.go:10-66`, `convert/guards.go:153-217` | Structure+literal gate. Pin: `guards_test.go:48` |
| Tolerance: unbalanced→EOF cover, control/preproc→`KindUnknown`, defines→`KindDecl`, out-of-range queries never attach | `flow.go:99-108,384-668` | Loud-residue fallback. Pin: `tolerance_test.go:29-164` |
| Tx spans (begin→commit same-kind nearest-pair, C discipline; aborts elide; per-query `Tx` iff DML inside) | `scenario.go:1115-1380`, `scenariodiff.go:19-175`, `plan.go:310-352` | Exact-line scanner facts. Pin: `scenario_test.go:545-794`, `discovery_test.go:415` |
| Responses (last surviving write per non-error `Obuffer` add; later kept write outside ladder → `Stable=false`) | `scenario.go:1776-1929` | Ladder climb. Pin: `:694-727` |
| Strict mapping (`KnownFields(true)`, service ident, `≥1` endpoint, unique names, route `/`, filter syntax at load / axis at build) | `plan/mapping.go:119-257` | Strict decode + `Validate`. Pin: `plan_test.go:202` |
| Fn-library (no entry/fragment, direct, `Tx=false`, `chk_*` dropped) vs same-file helper (`fn_*` def+called, deterministic `Go` sig `Fixed:true`, `chk_*`/tx-plumbing dropped, calls → `s.GoName()`) | `plan/fnlib.go:21-231`, `plan.go:89-133,354-519`, `convert/helpercalls.go:22-84` | Scanner-span authority + `helperSignature`. Pin: `fnlib_test.go:54`, `samefile_test.go:58`, `fnsig_test.go:13`, `helpercalls_test.go` |
| `contract.Build` (never invents; ambiguity loud; dupes canonical) + `MappingView` minimal surface | `contract.go:38-355`, `mapping.go:9-52` | Sort-stable + `CanonicalSQL/ExecutableBinds`. Pin: `contract_test.go`, `audit_test.go`, `gen/*parity/uniform` |
| `analyzer` OQ18 (`queries*Q + ext + tpcalls*T + BF*B`, only `fn_*/chk_*`, `else` never; tiers; CSV tuning) + corpus classify + tp/fn one-match BFS (self excluded) | `analyzer.go:26-691`, `tpdeps.go:48-333` | `LiveFacts`-first. Pin: `analyzer_test.go`, `livefacts_test.go:31`, `tpdeps_test.go` |

## 5. Convert / gen / validate / gates (`internal/convert`, `gen`, `validate`, `goast`, `templates`, `budget`, `llm`, `sqlchk`, `cs*`, `py*`, `test*`) — where + how

| Rule | Where | How |
|---|---|---|
| 5 stages `scan→IR→plan→gen→convert` (typed handoff; `convert` sees `budget.View` never raw SQL; `qID` collision hard-errors) | `convert/pipeline.go:156`, `convert.go:274`, `batchpy.go:254`, `convertcs.go:119-142` | Stage functions. Pin: `flow_test.go`, `gen_test.go`, `batchflow_test.go` |
| Deterministic units first (`models→DB+iface→glue→LLM bodies→tpcall→helpers→Tier-B`); staged-collision guard; DB pool ordered-merge; interface rebuild (never restack) | `pipeline.go:183-254,302-509,2187-2213` | `RunIndexed` indexed slices + `os.Remove` rebuild. Pin: `pipeline_test.go`, `deterministic_test.go` |
| One AI knob (`cfg.Run.LLM && !-no-llm` else nil = deterministic; client-build fail → WARN degrade) | `extract.go:247`, callers `convert.go:77`, `batchpy.go:107`, `gentest.go:130`, `discover.go:85`, `convertcs.go:124` | `llm.NewFromConfig`. Pin: `batchpy_test.go`, `pygen_test.go`, `seam_test.go` |
| `-no-llm` renders first-class deterministic bodies (`skipped`, upgradable, never downgrades landed LLM) | `pipeline.go:383-479`, `deterministic.go:30-196`, `gen/deterministic.go:38-179` | Same append/validate/ledger path + `tuxgo:deterministic-*` markers. Pin: `deterministic_test.go`, `pf_test.go` |
| Seam skeleton (`RunSeam`: clamp `MaxRetries+1`, `CheckInput`, `OutputCeiling`, `Chat`, `CheckOutputCap`, `Extract`, `Gate`, `Rejected`, audit) — per-seam temps/retries (controller 0.1/3, stub one-shot 0, py/cs/gentest abort-on-chat, discover advisory 0.2/1) | `llm/seam.go:91-222`, `pipeline.go:524,779`, `chunk.go:588-808`, `stubsynth.go:68`, `pygen/prompt.go:21`, `csgen/seam.go:24`, `testgen/llm.go:25`, `ainames.go:56-191` | Kind/name/unit audit `WriteExchange`. Pin: `seam_test.go`, `retry_test.go`, `chunk_test.go`, `pipeline_test.go` |
| Degrade placeholders (never invent): `tuxgo:TODO` gaps; `llm-required`; panicking `fnstubs.go` (+ one-shot synth or `CANNOT_SYNTHESIZE`); `tuxgo:REJECTED-BEGIN/END` commented last-payload (ledger `failed`, resume retries, `hasLiveMethod` ignores comments); CS `TODO` slot; py `# TODO + raise`; tpcall `TODO tp:` | `gen/deterministic.go`, `gen/tpcall.go:44-82`, `testgen/gen.go:62,426-437`, `gen/interfaces.go:209`, `stubsynth.go:17-200`, `pipeline.go:2007-2081,1936-2100`, `csgen/gen.go:396-416`, `pygen/pygen.go:202` | Pure synthesis + line-anchored live check. Pin: `rejected_test.go:75-144`, `pipeline_test.go:197-221`, `seam_test.go:94-196`, `pygen_test.go:154-212` |
| Body gates: fixed sig/return (no `req/resp`, no `data/err` shadow, terminal `return`); REQUIRED-CALLS (every `s.store.*`/`s.Helper(` in order, results captured); no SQL/`EXEC`/FML/ATMI/`unsafe`; gofmt-parse + brace pads; compile-level (unused, undef, non-const format, `'Y'→"Y"`, uncaptured store, terminal); census + `if{}` reject; scenario guards; tx (`ExecTransaction(` + `Name(ctx, tx,`) with `wrapTxBody` repair | `pipeline.go:571-648,744-772,1572-1695,1852-1879`, `bodygate.go:48-327`, `conditions.go:27-185`, `guards.go:158`, `chunk.go:581-899`, `promptfacts.go:49`, view passes `seamrewrite/txtemplate/fmlprobe/storecalls/helpercalls` | String + `go/ast` over `bodyParseWrap` + `go/types` allowlist. Pin: `bodygate/guards/conditions/chunk/fmlprobe/seamrewrite/txtemplate/helpercalls/storecalls/samefile_test.go` |
| SQL fidelity: normalized `Compare` (columns/tables/where/set/values/order/binds; empty = match); alias-strip + bind-wildcard; `CheckSQLFree` (Go: outside `dbFilePath`; CS: outside `*Queries.cs`); flag-only (`deviated` + `sql-fidelity/free.json`, never fail; missing = `unverifiable`); alias-inject observability (`sql-aliases.json`, misalignment → WARN skip) | `sqlchk/compare.go:192-326`, `normalize.go:50-206`, `sqlfree.go:17-25`, `convert/sqlcheck.go:23-105`, `cschk/check.go:39-264`, `gen/aliases.go:45-173`, `gen/gen.go:248-600` | Token projection + depth-0 split + AST string scan. Pin: `compare/extract/sqlfree/sqlcheck/aliases/sqlclean_test.go`, `pf_test.go` |
| Chunking: dual trigger (prompt > ceiling OR output-est > 70%); constants `70/130/70/2000/600/75%`; statement-boundary slicing (`;`/`}` depth-aware, never split `} else`, `total/N` under `sliceBudget`); hard ceiling + `dynamic min(ctx-in-reserve,maxOut)` vs `static` | `pipeline.go:667-731`, `chunk.go:35-650`, `budget.go:77-121`, `seam.go:146-161`, `config/validate.go:22-52` | `budget.Count=len/charsPerToken`; LLM never decides boundaries. Pin: `chunk_test.go`, `budget_test.go` |
| Retry bounded (`MaxRetries+1`, default 3; repair rides assistant turn only when `retryRepair`; truncation `finish_reason` feeds next prompt); fail-loud (`cap<256`, over-ceiling wiring error; composer skips with WARN, never fails) | `seam.go:92-222`, `retry.go:24`, `pipeline.go:90,731`, `chunk.go:568-789` | `Prompt(prev,notes)` + `Rejected` capture. Pin: `retry_test.go`, `seam_test.go`, `retrystats.go` A/B |
| Templates `flag>config>embedded` (partial override normal; unknown/empty/parse fails at wire-up; `Render` unknown errors); `goast.Emit` = one Go gate; version `v1`, 40 IDs | `templates/file.go:27-180`, `embed.go:29-136`, `wiring.go:70`, `goast.go:33-237`, `imports.go:20` | `go:embed` overlay + `format.Source`. Pin: `templates_test.go`, `file_test.go`, `goast_test.go`, `validate_test.go` |
| Structure: Tier-A (parse+gofmt every file) vs Tier-B (batched `build/vet/test` only where `mainGo→go.mod`, else WARN; `always`-without-target errors; never feeds retry); accumulating iface (same sig no-op, conflict errors); CS (braces/namespace/type/`sql-leak`/fidelity/`OracleParameter` count); gentest `vet + test -run ^$` 90s | `validate.go:49-187`, `pipeline.go:2343`, `convert.go:322`, `goast.go:82-237`, `cschk/check.go:39-264`, `csgen/seam.go:260`, `testgen/gen.go:227-267` | `ResolveModuleRoot` + `GO111MODULE=on` 5m. Pin: `validate_test.go`, `goast_test.go`, `cschk/check_test.go`, `golden/seam/guards_test.go` |
| Ledger resume (never regen filled; first-run-only synth; `generateFile` regens only appended-but-missing; atomic tmp+rename; statuses incl. `deviated`); rename detection (positional IDs; terminal→`planned` on kind/name mismatch + warn + clean-tree advice) | `ledger.go:65-198`, `pipeline.go:154-381,1928-2307`, `csgen/gen.go:222`, `convertcs.go:414-466` | `Get/AddMap/Save/Counts` + `hasLiveMethod/methodLanded/fnStubLanded`. Pin: `ledger_test.go:8-52`, `pipeline/rejected/deterministic_test.go` |
| Audit (every attempt `Exchange` → `<kind>-<name>-attempt<n>.json`; `WriteJSON` for census/fidelity/aliases/ledgers/plans/retention; nil-recorder skips; rotation keeps ledger/state) | `audit/recorder.go:41`, `exchange.go:50-66`, `seam.go:100`, `sqlcheck.go:63-105`, `conditions.go:185`, `pipeline.go:288`, `convert.go:309`, `convertcs.go:131`, `batchpy.go:347`, `testgen/gen.go:771`, `retain.go:9-15` | Mutex + plain-name only under `audit/<runID>/`. Pin: `recorder/stats_test.go`, `axes_artifacts_test.go` |
| Gentest (targets file\|layer\|service\|root, `models` never + loudly; inventory + `Test<Fn>`/call-site evidence; units skip-covered/by-design/unsupported/template/LLM; twin `_gentest_test.go+GenSuite`; deterministic db/handler + LLM field-mapping gate; staged-first out) | `testscan/scan.go:98-345`, `report.go:9-114`, `testgen/gen.go:36-771`, `methods/fixture/extract.go`, `llm.go:57-125`, `specs.go:290-379` | `SkipObjectResolution` + bounded pool. Pin: `testscan_test.go`, `testgen/dbtx/outpath_test.go` |
| CS 7 files (`Controller/DTO/NamedQueries/Repository×2/Service×2` under `<out>/<Component>/`; SQL const verbatim `CanonicalSQL`; `OracleParameter` names = source binds; prologue + `GetStr` + structured log pre-rendered; multi-query suffixes); guards twin (`pred.Equivalent` under 4 spellings) | `csgen/gen.go:148-452`, `csplan/plan.go:436-631`, `csgen/seam.go:161-316`, `guards.go:16-60`, `cschk/check.go`, `convertcs.go:163-187` | `Cs*File` templates + `unique QueryNames`. Pin: `golden/seam_surface/seam/guards_test.go`, `contract_parity_test.go` |
| Batchpy (cursor-batch rubric: DML binds ⊆ cursor `RowShape` → nearest prior max-overlap; `simple` iff all-covered + every group has DML else `stateful`; `TRUNCATE` ≤15 lines rebuild; `CodeView` `# <call>` placeholders, never SQL to LLM; gates `pychk` + txn/seam/contract + `Fidelity` + `Retention`) | `batchflow.go:108-413`, `pyplan.go:113-428`, `pygen/prompt.go:21-193`, `pygen.go:68-157`, `report.go:50`, `pychk.go:29-299` | `CanonicalSQL/ExecutableBinds` + `assembleModule` gate truth. Pin: `batchflow/pyplan/contract_parity/pychk/pygen/wiring/batchpy_test.go` |
| Misc hard: view-shape order (dead-comments→legacy-seams→tx→FML-probes→session-args→helpers→scaffold→consts→store-assign); prompt-facts canonical order; request/FML/row contracts; DB template resolution (`q.Type.TemplateID` wins, plain downgrade, MERGE keeps, `TxParam` only DML+Tx); corpus-guard bans; draft-and-stop (no mapping → draft + numbered sibling + exit) | `pipeline.go:571-648,812-846`, `promptfacts.go:49`, `gen/gen.go:405-600`, `corpusguard/guard_test.go:23`, `convert.go:244`, `discover.go:103-244`, `convertcs.go:357`, `csdraft/draft.go:36` | Sequential rewrites + strict load. Pin: `fmlprobe/seamrewrite/txtemplate/helpercalls/storecalls/scenariofilter/stripped_regression/gen/contract_parity/discoverpreview/axes_artifacts_test.go` |

## 6. Operational / config / arch / test — where + how

| Rule | Where | How |
|---|---|---|
| Config ladder `-config > ./.tuxgo.yaml > Default()` (filename = lookup convention only) | `extract.go:98-160`, `config.go:43-131` | Overlay on `Default()`. Pin: `config_test.go:11-90` |
| Strict unknown keys fail (config + Go/CS mappings) | `config.go:122-123`, `plan/mapping.go:125-126`, `csplan/mapping.go:94` | `KnownFields(true)` + `Validate`. Pin: `config_test.go:194-200` |
| Relative paths CWD-relative (docs contract; code `Abs` + CWD joins) | `example.yaml:7`, `README.md:208-209`, `validate.go:49-53`, `convert.go:263` | `filepath.Abs(anchor)`. Pin: `validate_test.go:47-57` |
| Universal `flag > config > default` (templates, mapping/input, request options, `convertcs.out`, `-base`, gentest `-base`, `extract -out`, `-no-llm` OR) | `wiring.go:28-90`, `extract.go:117-259`, `convertcs.go:31-70`, `convert.go:41-267`, `gentest.go:109-128`, `config.go:168-373` | Per-site resolve fns + `Merged/Route`. Pin: `config_test.go:190-192` |
| Budget estimator `tokens≈chars/ratio` (code default 4; example/local 3; measured dense ~2.8); slices 70% prompt / ¾ output-char / 130% inflation; room<256 loud | `budget.go:32-121`, `config.go:51-52,248-270`, `example.yaml:21-30`, `.tuxgo.yaml:9-16`, `README.md:212-228`, `chunk.go:558-566`, `validate.go:21-52` | `Count` ceil + `OutputCeiling`. Pin: `budget_test.go:19`, `config_test.go:38-40`, `chunk_test.go:520` |
| Inert/reserved (parsed+validated, never read): `maxContextTokens` (≥prompt check only), `retrieval`, `elision.mode`, `paths.target` (+ audit-listed `Condition.Predicate`, `StatusBlocked`, etc.) | `config.go:258-263,375-389,455-458`, `validate.go:30-36,99-103`, `example.yaml:15-16,60-64,143-145`, `README.md:229-231`, `engine-wiring-audit.md:29-38` | Explicit `RESERVED` markers. Pin: `config_test.go:182-184` |
| Profile/key (`apiKeyEnv` env-first, gitignored literal fallback, unresolved → deterministic-only logged never-fail never-log-value; R6 never commit) | `config.go:307-373`, `client.go:334-358`, `extract.go:242-282`, `example.yaml:41-58`, `.tuxgo.yaml:1-35`, `.gitignore:39-40`, `README.md:370-380` | `ResolveKey() (key,source,err)` + `key_source` log. Pin: `config_test.go:231-260` |
| `workers` opt-in; any `N` byte-identical to 1 (ordered merge) | `config.go:391-394`, `validate.go:104-106`, `pool.go:12-31`, `pipeline.go:65-2213`, `testgen/gen.go:163`, `example.yaml:67`, `README.md:86-89` | `RunIndexed`. Pin: `pipeline_test.go:368-479`, `testgen_test.go:646-685` |
| Logging twins (`run-<id>.{jsonl,log}` + console; `DDMMYYYY_HHMMSS+-N`; `duration_s`; WARN classes: externals/unbalanced/parse/tpcall; rotation 50 keeps ledger/state) | `logger.go:88-224`, `main.go:24-60`, `extract.go:326-375`, `convert.go:278-318`, `retain.go:9-15`, `README.md:504-509` | `Text+JSON` handlers + `run_id` in-record. Pin: `logger_test.go:26-119`, `retain_test.go:47-87` |
| IR snapshot (`audit/<run-id>/ir-<file>.json`, same schema as `xtux ir`, best-effort WARN) | `plan.go:196-218`, `extract.go:72-404`, `README.md:510-513` | `WriteJSON` flat. Pin: `audit/stats_test.go:35` |
| Layout single home (`DefaultStagedDir/MappingsDir/ScenDir/Paths{ledger,state,staged}`; logs/audit NOT configurable; ledger atomic tmp+rename; plan `_plan.json/md`; convertcs ledger `convertcs-<comp>.json`) | `config.go:18-39,450-467`, `ledger.go:65-198`, `plan.go:239-254`, `example.yaml:150-156`, `.gitignore:4-7` | `Load/Save/Get/AddMap/Counts`. Pin: `ledger_test.go:8-52`, `plan_test.go:67-142` |
| Never-write-target (stage under `-base`/`-out`/staged; Tier-B anchors `mainGo`, empty → syntax-only WARN) | `convert.go:256-267`, `gentest.go:109-128`, `convertcs.go:31,170-176`, `testgen/gen.go:36-495`, `validate.go:1-117`, `web/.../convert/route.ts:30-37` | `-base wins else mainGo-root else staged`. Pin: `pipeline_test.go:401-479`, `outpath_test.go` |
| From-scratch recipe (clear staged+ledger after mapping/template edit; two-pass draft→stage) | `README.md:266-270,476-490`, `retry-methodology-ab.md:42-94`, `rejection-reduction-plan.md:210-212`, `ledger.go:98-129` | Operator `rm -rf` (tool itself never clobbers; `pipeline.go:183-190` hard-errors fresh+existing). |
| Corpusguard (`TUX_CORPUS`/`TUX_CS_CORPUS`/`TUX_SCEN_CORPUS` skip-guarded; tracked content bans corpus tokens; `.gitignore` hides dirs) | `sweep_test.go:45-80`, `corpus_smoke_test.go:19-92`, `scenario_golden_test.go:26-36`, `guard_test.go:1-75`, `.gitignore:29-39`, `README.md:28-366` | Env-gated + `git ls-files` scan. Pin: `guard_test.go:45-75` (skips on CI) |
| Goldens byte-pinned (IR 8 file + dir; CS `CS_UPDATE_GOLDENS=1`; gentest `GT_UPDATE_GOLDENS=1`; scen `SCEN_UPDATE_GOLDENS=1`; marshal-twice identical) | `parity_test.go:60-158`, `csgen/golden_test.go:21-105`, `testgen_test.go:809-900`, `scenario_golden_test.go:19-120`, `README.md:56-102` | `bytes.Equal`/`diffMaps` drift errors. |
| Sweep never-panic (19 hostile inputs; unbalanced loud; deterministic re-scan; flow tolerance loud residue) | `sweep_test.go:11-43`, `tolerance_test.go:12`, `pipeline.go:1201`, `README.md:86-117` | Degrade-to-defect fallback. Pin: `go test ./...` |
| 1-based everywhere + extraction-never-decides (no endpoint picks/renames/silent merges; tool drafts, human tags) | `README.md:106-112`, `facts.go:284`, `types.go:305`, `mapping.go:32-210`, `alias.go:107`, `analyzer.go:589`, `extract.go:218-229`, `convert.go:240-254` | Inventory-only extraction + `≥1` endpoint required. Pin: mapping-validate + parity dedup |

## 7. How to check a rule (quick recipes)

```bash
go test ./internal/tsscan ./internal/pred ./internal/ir ./internal/sqltext ./internal/namer
go test ./internal/flow ./internal/plan ./internal/contract ./internal/analyzer
go test ./internal/convert ./internal/gen ./internal/budget ./internal/llm ./internal/sqlchk
go test ./internal/csplan ./internal/csgen ./internal/cschk ./internal/pyplan ./internal/pygen ./internal/pychk ./internal/batchflow
go test ./internal/testscan ./internal/testgen ./internal/templates ./internal/validate ./internal/goast ./internal/audit ./internal/ledger ./internal/config ./internal/corpusguard
go test ./...
GT_UPDATE_GOLDENS=1 go test ./internal/testgen   # regen gentest goldens only
CS_UPDATE_GOLDENS=1 go test ./internal/csgen     # regen CS goldens only
```

## 8. Contradictions / drifts (candidate → verdict)

Severity: `REAL` = fix-worthy contradiction; `DRIFT` = wording/scope gap; `OK` = reconciled scoped-exception, no action.

| # | A vs B | Severity | Verdict |
|---|---|---|---|
| C1 | `charsPerToken` code default **4** (`config.go:51`, `budget.go:32-54`) vs example/local **3** (`example.yaml:21`, `.tuxgo.yaml:16`, test wants 3) | OK | Documented divergence. `README.md:214-216` reconciles (4 undercounts, 3 calibrated to dense ~2.8, margins absorb). No action. |
| C2 | README "keep `requestOptions.maxTokens <= run.maxOutputTokens`, output gate reads run value" (`README.md:226-228`) vs code: `Merged()` prefers `Options.MaxTokens` (`config.go:357-365`), payload uses endpoint value (`client.go:215-217`); `validate.go:86-87` only `>=0`, never `<= run` | REAL (gap, not enforced) | README states a rule the loader never validates. Mismatch wastes calls (provider emits longer than gate allows → reject). Fix: add `validate` check or downgrade README to advisory. |
| C3 | `tokenPolicy` default `static` (`config.go:52`) vs example/local `dynamic` (`example.yaml:23`, `.tuxgo.yaml:12`); plus `budget.Dynamic()=ModelContext>0` (`budget.go:94`) vs policy-string switch (`validate.go:33-36`, `wiring.go:30-34`) | OK + DRIFT (minor) | Default-vs-example intentional (conservative stock, 16k opt-in). Wiring masks the presence-vs-string mismatch, but direct `budget.New` + manual fields could diverge. Low risk; harmonize predicate when touched. |
| C4 | Deterministic-only claims vs actual LLM calls | OK | Consistent. Deterministic set (scan→IR→plan→models/db/handlers/SQL checks) is zero-LLM every mode; seam = one template gap/unit (`README.md:368-399,492`, `extract.go:23,247-251`). No action. |
| C5 | Never-clobber vs `-force`/from-scratch `rm -rf` | OK | Scoped exception, explicit. `-force` exists only for `templates dump` (`templates.go:84-103`); mapping/scenario drafts have no overwrite flag (numbered siblings). `rm` recipe is operator clean-tree, guarded by staged-collision hard error (`pipeline.go:177-190`). No action. |
| C6 | Resume "filled bodies never re-generated" (`README.md:316-318`, `csgen/gen.go:39-41`, `pipeline.go:2298-2307`) vs rename-reset terminal→`planned` (`ledger.go:98-123`, `pipeline.go:192-202`, `README.md:476-481`) | OK | Same-generation vs new-generation (positional IDs). Documented both sides. No action. |
| C6b | "Synthesis first-run-only, resume never re-calls" (`README.md:395,464-465`) vs `fnStubLanded()==false` when ledger says appended but `fnstubs.go` missing → re-runs (`pipeline.go:210-218,2262-2289`) | DRIFT (omitted edge) | Cleared-tree + kept-ledger correctly re-burns synthesis; README overstates "never". Fix README: "first-run-only per landed file". |
| C7 | Coverage "omission is choice, silence is not" consistent across `convert/convertcs/plan/emit` (`README.md:357-359,406-408`, `plan.go:424-473`) vs known endpoint-resolution 3× duplication drift (`uniform-ir-plan.md:59`) | DRIFT (impl risk) | Claims consistent today; code has copy-paste drift risk (warning wording / `queriesInSpan` / tx votes). Track under uniform-IR Phase 4/6, not user-visible now. |
| C8 | `q<N>`/census claims vs `query.go`/`census.go` | OK | Consistent (`q5→q3` pinned `ir_test.go:34-87`). No action. |
| C9 | Banner live/dead README vs `facts.go:239-241` + `region.go:248-251` | OK | Literal single-line test, pinned (`scan_golden_test.go:222`). No action. |
| C10 | Lenient `;`-close "documented as residual" (`README.md:108-109`) vs enforced defect fact (`region.go:306-381`, golden pin) | DRIFT (term) | Enforcement loud in both; word "residual" collides with `flow/residualRun` (`flow.go:384-433`) and convertcs residual (`seam.go:15`). Fix: say "defect fact", not "residual". |
| C11 | "`workers=1` byte-identity by construction" (`README.md:86-87`) vs code: **any** `N` byte-identical (`pool.go:14-17`, `pipeline.go:243-244`, pins `pipeline_test.go:368-396`) | DRIFT (understatement) | Code stronger than README. Fix README: "any workers value byte-identical (ordered merge)". |
| C12 | Validate `compile:auto` vs syntax-only | OK | Consistent (`validate.go:73-137`; Tier-B never feeds retry per Tier-1 #6 fix). No action. |
| C13 | `paths.mainGo` empty → staged + syntax-only; `paths.target` RESERVED zero readers | OK | Consistent (`config.go:454-463`, `validate.go:84-89`). No action. |
| C14 | Template partial-override + `v1` | OK | Consistent (`file.go:13`, `embed.go:12-14`). Resume caveat matches `generateFile`. No action. |
| C15 | Gentest `models`-never-target etc. | OK | Consistent (`scan.go:93-207` loudly errors). No action. |
| C16 | CS "no raw SQL outside NamedQueries" (output gate `check.go:62-70`) vs "model never sees raw SQL" (input hygiene `seam.go:18,157`) | OK | Complementary by design; `NamedQueries` verbatim is the encoded exemption. No action. |
| C17 | "Corpus never named in tracked content" (`README.md:361-366`) vs guard exempts `docs/` + self + `.gitignore` (`guard_test.go:1-51`) | REAL (overbroad) | README overstates. Guard explicitly permits `docs/` planning records + token list itself. Fix README: "never named outside `docs/` records, `.gitignore`, and guard token list". |
| C18 | "Every fixture byte-identical in file **and** corpus mode" (`README.md:56-63,93-102`) vs fragment rubric (dir mode never fragments `extract.go:168-178`, `ir_test.go:359`; `-fragment` forces `xtux/main.go:23`) | REAL (overbroad) | Fragment fixtures cannot be cross-mode identical by design. Goldens are per-mode pinned with explicit regen vars. Fix README §Test suite: "byte-pinned per mode + rubric". |
| C19 | `duration_ms` in README (`:507`) vs `duration_s` in code (`main.go:99`, `convert.go:215,318`, `batchpy.go:174,192`) | DRIFT (stale) | Code emits `duration_s` (2-decimal). Fix README wording. Verified during audit. |
| C20 | "Relative paths resolve against working directory" (docs `example.yaml:7`, `README.md:208-209`) vs `config.go:450` comment "relative to config file" | DRIFT | Docs + `Abs`/CWD-join behavior is CWD; one code comment says config-file-relative. Fix the comment to CWD. Verified during audit. |

### Action list (only the REAL/DRIFT items)

1. C2: validate `requestOptions.maxTokens <= run.maxOutputTokens` under static (or soften README).
2. C17/C18: narrow the two overbroad README claims (corpus naming; cross-mode identity).
3. C6b/C10/C11/C19/C20: one-line README/comment fixes (stub re-burn edge, "defect fact", any-workers, `duration_s`, CWD comment).
4. C3/C7: opportunistic only (budget predicate harmonization; endpoint-resolution dedup per uniform-IR plan).
