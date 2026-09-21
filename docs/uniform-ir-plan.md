# Uniform TUX Extracted Model — Assessment & Plan for Multi-Language / Multi-Template Conversion

Status: in progress (updated 2026-09-21). Goal: one language-agnostic parsed core that Go (multiple templates), Python, and C# all plug into by arrangement only.

Progress snapshot (2026-09-21 audit):
- DONE: `internal/contract` types + `Build` + goldens (§3.1 core); `internal/namer` Go/Py/Cs + type maps + tests; `ir.CanonicalCType`; `sqltext.CanonicalSQL`/`ExecutableBinds` with `csplan.cleanSQL` + `pyplan.emitSQL`/`bindsOf` delegating; `goHint` → `gen/gotype.go`, `TemplateID()` → `gen/txvariant.go` (deprecated shims kept); `common.Pascal`/`UpperSnake`/`Snake`/`StripHungarianPrefix` promoted; `docs/adding-a-language.md`; `contract_parity_test.go` in `csplan`/`pyplan`.
- PARTIAL: endpoint resolution still lives per-plan (`contract.Build` takes caller-resolved `EndpointInput`); `flow.ArmView` + `CallResolver` exist with `Resolver` kept for compat (backends cut over in Phase 4); `Condition.Predicate` still RESERVED/divergent.
- PENDING: Phase 4 backend delegation (naming now delegates — `csplan.pascalOf`/`propNameOf` → `common`, `pyplan.verbOf` → `contract`, `gen` row names → `GoNamer` — but query/param derivation still reads raw `ir`); Phase 5 second Go set + shim removal (blocked: IR goldens still pin `template_id`/`go_hint`); Phase 6 unified mapping (scaffolded as `contract.MappingView` adapters, YAMLs unchanged).

## 1. TL;DR answer

**Partially yes, not yet pluggable.**

- There **is** a single uniform parse: `tsscan.SourceFacts → ir.File → flow.Tree/Scenario`. Every backend (Go/Python/C#) already reads from it. That is the right spine and should be kept.
- But there is **no uniform template-input model**. Each language re-derives naming, row shapes, params, SQL cleanup, and its own `Plan` + template-data structs from raw `ir`. Plus two Go-specific leaks sit *inside* the supposedly neutral IR.
- So today: **one shared extraction, three independent derivations, three template-data dialects**. Adding a 2nd Go template set or a 4th language means copying derivation logic a 4th time.

This plan purifies `ir`, introduces one `contract` (universal service) model, centralizes derivation, and turns `plan/pyplan/csplan` + `gen/pygen/csgen` into thin language adapters over it.

## 2. What we have today (with evidence)

### 2.1 The good spine — keep it

| Layer | Types | Why it's already uniform |
|---|---|---|
| C/Pro*C facts | `tsscan/facts.go:18,40,90,109,120,134-257` `SourceFacts{Path,Functions,Calls,AllSQL,Branches,Loops,Returns,VarDecls,...}` `facts.go:272` | No Go/Python/C# concepts. `VarDecl.Type` is C text. `ScanFile/ScanBytes/ScanFragment` in `scan.go:11,23,97` only mask + tree-sitter-parse. |
| Deterministic IR | `ir/types.go:261` `File{Path,Entry,Functions,Defines,Conditions,FmlOps,Buffers,TPCalls,Queries,HostVars,ExternalFns,Unbalanced,ParseErrors}`, `Query{ID,Type,SQL,Aliases,Tables,Binds,BindArity,RowShape,Sites,DedupKey,...}` `:180`, `FmlOp{Kind,Field,Target,Buffer,Line,Code,Optional,Dropped,Error}` `:113`, `TPCall{Service,SendBuffer,RecvBuffer,SendFML,RecvFML,...}` `:148`, `Condition{Index,Kind,Expr,Predicate *pred.Expr,FlagVars,FmlOps,QueryIDs}` `:206`, `Define` `:251`, `BufferRole` `:138` | Factual, JSON-tagged, deterministic. Python/C#/Go all consume `SQL/Binds/RowShape/Tables/FmlOp/TPCall/Define/HostVar.Name+CType`. `flow.Build/TreeFor` (`flow/flow.go:91,173,185`) is the one shared derivation for slicing. |
| Predicate | `internal/pred/pred.go:39` `Expr{or/and/not/cmp/call/lit/ident/raw}` | C-boolean tree, not a Go AST. Correct choice. |
| Shared helpers | `sqltext.StripInto/CollapseBinds` (`sqltext/sqltext.go:10,52`), `templates.Provider{Render(ID,any)}` (`templates/provider.go:13`), `batchflow` explicitly "target-language-agnostic" (`batchflow/batchflow.go:1`) | Mechanism-level sharing works. |
| Template mechanism | `templates/embed.go:9,77` single registry mixing `ModelFile/PyBatch*/Cs*`, `file.go:19` `FileProvider` overlay | One seam for overrides — good. |

Verdict on spine: `ir.File` + `flow.Tree/Scenario` **is** the "tuxedo parsed logic" the user describes, and it is already arranged differently per language. Do not replace it; purify and build on it.

### 2.2 The two Go leaks inside `ir` — must move out

1. **`Query.TemplateID` + `QueryType.TemplateID()` → `db_method_*`** (`ir/types.go:29,39,183`, set in `ir/query.go:46,126,155`, threaded as `plan.Unit.TemplateID` in `plan/plan.go:60`, honored in `gen/gen.go:575`). The comment says "registry is a consumer concern" but the IR still stores a Go template id. C#/Python ignore it (own `QueryPlan.Kind` in `csplan/plan.go:359`, `RepoMethod.QueryKind` in `pyplan/pyplan.go:72`), proving it doesn't belong in IR.
2. **`HostVar.GoHint`** (`ir/types.go:168`, `ir/extract.go:530,591` `goHint()` mapping `char→string,int→int,...`). Only Go reads it (`convert/stubsynth.go:305`, `gen/gen.go:261` notes `CType` kept for csdraft/csplan while Go map deleted). C# deliberately uses `CType` (`csplan/plan.go:155`, `csdraft/draft.go:151`).

Both are small, mechanical to remove (deprecated accessors + move mapping to `gen`).

Also note `Condition.Predicate` is documented RESERVED/divergent (`ir/types.go:210-214`: flow re-parses `Expr`, does not read this field). Either sync it or drop it — it is a trap for new backends today.

### 2.3 No shared template-input model — three dialects

`templates` shares **mechanism only** (`Provider.Render`), not **data**:

- Go data: `templates/specs.go:7` `FieldSpec{Go Name/Type,JSONTag=FML,DBTag,OmitEmpty,Binding:gin}`, `:75` `DBMethodData`, `:115` `DBInterfaceData`, etc., built in `gen/gen.go:156,235,279,306,511,614` + `gen/interfaces.go:132,191,235,258,281`.
- Python data: `pygen/data.go:13,20,24,48,56` `headerData/constData/dalFetchData/repoData/serviceShellData` + `pygen.go:219,238`, binds via `pyplan.go:257,271,275,309` `emitSQL/bindsOf/bindOrder/bindIdx`.
- C# data: `csgen/gen.go:72,99,117` `EpData/QData/fileData` + `:391,404,422` `sigArgs/callArgs/oracleArgs`; `csplan/plan.go:359,423,444,456` `buildQueryPlan/cleanSQL/propNameOf/pascalOf`.

Row/param/SQL derivation is therefore reimplemented per target (Go uniform `sql.NullString` in `gen.go:263` vs C# `Prop` strings vs Python `RowShape []string` passthrough; `sqltext.StripInto` vs `csplan.cleanSQL`'s own INTO-strip).

### 2.4 Three independent `Plan`s, three drivers

| Go `plan` | Python `pyplan` | C# `csplan` |
|---|---|---|
| `plan.go:52` `Unit{Kind,Name,QueryIDs,TargetPath,TemplateID,LLM,Tx,TP *ir.TPCall}`, `:136` `Plan{Service,Module,Units,Stubs,FnHelpers}`, `mapping.go:23,78` `MethodPin{Name,Params,Row}/Mapping{Service,Module,ReadDBs,Endpoints,DBMethods}` | `pyplan.go:87` `Plan{Module,ServiceName,ClassName,RepoName,Shape,Consts,DAL,Phases,Repo,Flow}`, `:41,51,62,72` `QueryConst/DALFn/Phase/RepoMethod` | `plan.go:64` `Plan{Namespace,Area,Component,Controller/Service/Repo,...}`, `:38,51,18,27` `QueryPlan/EndpointPlan/Param/Prop`, `mapping.go:20,39` `MethodPin{Name}/Mapping{Namespace,...}` |

Drivers never share: Go `cmd/tuxconv/convert.go:286 → plan.Build → convert/pipeline.go:145 → gen.NewService`; Python `batchpy.go:268 → batchflow.Build → pyplan.Build → pygen.Generate`; C# `convertcs.go:100 → csplan.Build → csgen.Generate`. Endpoint-resolution (condition vs conditionRef vs scenarioRef + arm-coverage warnings) is copy-pasted three times with subtle drift (`plan.go:192-447` vs `csplan/plan.go:130-351`).

### 2.5 One Go-specific renderer in shared `flow`

`flow/render.go:14` `Resolver{StoreCall(queryID)->"s.store.GetX(c,...)", RowType->"*models.X"}` and `RenderSpan()` emitting Go skeleton (`// TODO(line N)`, `rows, err :=`, `return data, nil` in `:174-258`) is Go-flavored. Python has its own `CodeView` (`pyplan/pyplan.go:328`), C# its arm view (`csgen/seam.go:24`). The flow-level draft should be language-neutral with per-language resolvers.

## 3. Goal state

```
.pc/.pcf
  → tsscan.SourceFacts
  → ir.File (purified, language-neutral, JSON-stable)
  → flow.Tree + Scenario/Candidate (language-neutral slicing)
  → contract.Service (ONE universal model: endpoints, requests, responses,
       queries, params, row fields, tpcalls, residue — no language names)
  → namers: goNamer | pyNamer | csNamer (deterministic naming + type maps)
  → template data (per-language thin projection of contract)
  → templates.Provider.Render → Go set A / Go set B / Python / C# / ...
```

Non-goals: no behavior change in goldens during refactor; no LLM in the contract layer (deterministic only); no new template syntax.

### 3.1 The uniform contract (new package `internal/contract`)

New file `internal/contract/contract.go` — the **only** type templates are allowed to be built from (enforced by review, later by lint):

```go
// Kind of DB unit — mirrors ir.QueryType, no template ids.
type QueryKind string // select_one | select_many | insert | update | delete | merge
type FieldKind string // request | response | row | param | error

type Field struct {
  FMLName  string // raw FML id, e.g. FML_COMP_CD (never renamed)
  HostVar  string // :host bind, e.g. sql_mf_nav_comp_cd
  CType    string // canonical C type: char, varchar, int, long, double, ...
  Nullable bool
  Array    bool
  Kind     FieldKind
  IsErr    bool   // IsErrField || IsErrValue
  Code     string // legacy error code if correlated
}

type QueryUnit struct {
  ID        string   // canonical ir id (dedup-resolved, namespaced fn:id preserved)
  Kind      QueryKind
  SQL       string   // canonical executable SQL (INTO-stripped, binds collapsed — ONE function)
  Tables    []string
  Binds     []string // executable-order, unique, host-var-filtered
  RowFields []Field  // FROM RowShape+CType, indicator vars already dropped
  Params    []Field  // FROM Binds+HostVars+FML read targets
  Line      [2]int
  Tx        bool     // scenario-vote OR (G-SCEN6), preserved
}

type Request struct {
  Fields []Field // FML GETs + bind params, deduped, source order
}
type Response struct {
  Fields []Field // FML ADDs (non-err) + row projections
  Errors []Field // err-field / err-value carriers + codes
}

type TPDependency struct {
  Service string
  Send    []Field // SendFML as Fields
  Recv    []Field
  Ambiguous bool
  Line    [2]int
}

type Endpoint struct {
  Name      string // mapping pin or deterministic fallback (no language casing yet)
  Condition int    // ir condition index (0 when scenario-sliced)
  Scenario  string // scenario key when arm-sliced (c1.k etc.)
  Request   Request
  Response  Response
  Queries   []QueryUnit
  TPCalls   []TPDependency
  Residue   []string // loud non-SQL/non-FML lines the LLM seam must handle
  LineSpan  [2]int
  Warnings  []string
}

type Service struct {
  Name      string // logical service (nav, ordersvc, ...)
  Entry     string // TUXEDO entry fn
  Source    string // originating .pc path
  Endpoints []Endpoint
  Defines   []ir.Define
  Warnings  []string // arm-coverage advisories (D4/G-SCEN1), unified
  Skipped   []Skipped
}
```

Rules for `contract`:
- JSON-tagged, deterministic order (endpoints in mapping order, fields in source order), byte-stable marshal for goldens.
- Zero language names: no `GoHint`, no `db_method_*`, no `PascalCase`, no `sql.NullString`, no `OracleParameter`, no gin tags. Those live in namers.
- Built by **one** builder `contract.Build(ir.File, flow.Tree, MappingView)` — endpoint resolution (condition/conditionRef/scenarioRef), dedup (`UniqueQueries`), `queriesInSpan` fallback, scenario tx votes, arm-coverage warnings — implemented **once** here, replacing the three copies.
- `MappingView` is a minimal interface `{Endpoints() [{Name, Condition, ConditionRef, ScenarioRef}], DBPin(queryID) string}` that `plan.Mapping`, `csplan.Mapping`, (and future `pymap.Mapping`) adapt to — so mapping YAMLs can stay per-language while resolution logic unifies.

### 3.2 Purified `ir` (breaking leaks, keeping compat)

- Remove `HostVar.GoHint` → move `goHint()` to `internal/gen/gotype.go` (`GoTypeFor(CType)`). Keep deprecated accessor `HostVar.GoType() string` for one release that delegates, then delete.
- Remove `Query.TemplateID` + `QueryType.TemplateID()` → move to `internal/gen/txvariant.go` (`TemplateFor(QueryType, tx bool)`). Keep deprecated method delegating for one release. `plan.Unit.TemplateID` becomes a gen-local concern, not plan output.
- Resolve `Condition.Predicate`: either (a) make `flow` read it (single parse with define substitution at IR build) or (b) delete the field. Recommended: (a) — parse once in `ir` with `DefineAt` scope, `flow` consumes it; add parity test.
- Canonicalize `CType`: add `ir.CanonicalCType(string) string` (lowercase, strip `unsigned`, normalize `varchar2→varchar`, `sql_*` prefixes kept in HostVar.Name but typed separately). All namers switch on canonical values.
- `sqltext` becomes the **only** SQL canonicalizer: `CanonicalSQL(Query) (sql string, binds []string)` = StripInto + CollapseBinds + trailing-`;` trim. Delete `csplan.cleanSQL` and `pyplan.emitSQL/bindsOf` in favor of it (same semantics, one test suite).

### 3.3 Centralized naming + type maps (`internal/common/naming.go` + new `internal/namer`)

Today naming is scattered: `common.CamelGo/CamelPy/Export/PyIdent`, `plan.methodName/uniqueName/tableName`, `csplan.pascalOf/propNameOf/DefaultQueryName`, `pyplan.verbOf/firstTable`, `gen.rowFields/contractFields/fieldFromFML`.

- Extend `common/naming.go` with `Snake(s)`, `Pascal(s)`, `Camel(s)`, `UpperSnake(s)`, `StripPrefixRe` (the `sql_|vc_|v_|c_|l_|d_|i_|f_` rule, currently private `csplan.prefixRe`), `SafeIdent(s, lang)`.
- New `internal/namer` package: `type Namer interface { Endpoint(name string) string; Method(q QueryUnit) string; Param(bind string) string; Prop(bind string) string; Const(q QueryUnit) string; GoType(f Field) string; CsType(f Field) string; PyType(f Field) string }` with `GoNamer/PyNamer/CsNamer` implementations + `profile.Profile` hook for conventions. Type maps live here, tested once:
  - `char/varchar → Go string/sql.NullString | C# string | Python str`
  - `int/long → Go int/int64 | C# int/long | Python int`
  - `double/float → ... float64/decimal`, dates → `sql.NullTime/DateTime`, etc.
- `plan.DefaultMethodNames`, `csplan.DefaultQueryName`, `pyplan` verb logic all delegate to namers (goldens pinned before/after to prove no drift, or drift explicitly approved).

### 3.4 Neutral arm view (fix `flow/render.go`)

- Generalize `flow.Resolver` to language-neutral: `CallFor(queryID) (call string, ok bool)` + `RowFor(queryID) (row string, ok bool)` (drop `StoreCall/RowType` Go names, keep aliases for compat).
- Add `flow.ArmView{Lines []ViewLine{Line int, Kind sql|code|dropped|placeholder, Text string}}` built from `Tree+Scenario` with **no language syntax** (`EXEC SQL → [query:q1]`, `Fadd32 → [emit:FIELD]`, `tpreturn → [return]`).
- Each backend renders its seam prompt from `ArmView` via its namer (`gen/armview.go`, `pyplan.CodeView` reimplemented on top, `csgen/seam.go` reimplemented on top). Delete Go syntax from `flow` (`rows, err :=`, `return data, nil`, `s.store.`).

### 3.5 Thin language adapters

After `contract` exists, today's plans shrink:

- `plan.Build` → `contract.Build` + `GoNamer` + Go template-data projection (`DBMethodData`, `FieldSpec`, ...). `Kind/Units` (models→db→interface→controller→handler→router→mocks) stays — that's Go-architecture planning, correctly language-specific.
- `csplan.Build` → `contract.Build` + `CsNamer` + `QueryPlan/EndpointPlan` projection (now a view, not a re-derivation).
- `pyplan.Build` → `contract.Build` (via `batchflow.Flow` adapter for batch shape) + `PyNamer` + `QueryConst/DALFn/RepoMethod` projection.
- Template-data structs stay per language (correct — Go struct tags ≠ C# DTO attributes ≠ Python kwargs), but they are **projections** of `contract` fields, never fresh parses. New rule: template-data constructors take `contract.*` as input; grep for `ir.Query` imports in `gen/pygen/csgen` should go to zero except via `contract`.
- Multiple Go template sets become trivial: same `contract.Service` + same `GoNamer`, different `templates.Provider` (e.g. `sqlx` set vs `gorm` set vs `pgx` set). No re-parse.

## 4. Phased delivery plan

### Phase 0 — Freeze & inventory (0.5 day, no code changes to output)

- [x] Pin current goldens: `go test ./...` green (2026-09-21); `testdata/goldens` + `testdata/batch/expected` + `testdata/goldens/cs` are the baseline.
- [x] Add read-only audit test `internal/contract/audit_test.go` — documents the migration surface (ir imports per backend, `TemplateID`/`GoHint` shim readers, `contract` parity coverage). It asserts direction (shims only in `ir`, new code uses `gen`/`namer`/`contract`), never exact counts.
- [x] Decide package name (`contract`) and `QueryKind` values (`select_one/select_many/insert/update/delete/merge`).
- Acceptance: audit test green, baseline archived.

### Phase 1 — Purify `ir` + unify SQL (1–2 days, golden-neutral)

- [x] Add `ir.CanonicalCType`, `sqltext.CanonicalSQL`/`ExecutableBinds` with table-driven tests (INTO-strip, `: name` collapse, `;` trim, `:mi:ss` format-literal guard).
- [x] Migrate `csplan.cleanSQL`, `pyplan.emitSQL`/`bindsOf`/`bindOrder` to `sqltext.CanonicalSQL`/`ExecutableBinds`. Duplicates deleted (wrappers kept). Goldens byte-identical.
- [x] Move `goHint` → `gen/gotype.go` (`GoTypeFor`), `TemplateID()` → `gen/txvariant.go` (`TemplateFor`); deprecated shims in `ir` with `// Deprecated:` comments. New code uses the new homes.
- [ ] Resolve `Condition.Predicate` (sync-or-drop, see §3.2). Still RESERVED/divergent (`ir/types.go:222-227`) — deferred: `flow` re-parses `Expr` with define substitution; syncing needs a parity test first. Add `ir` JSON round-trip test when resolving.
- Acceptance: `go test ./...` green with zero golden diffs; `rg 'GoHint|TemplateID' internal/ir` shows only deprecated shims. (Holds today.)

### Phase 2 — Introduce `internal/contract` + unified builder (3–5 days, the core)

- [x] Create `contract/contract.go` (types above) + `contract/contract_test.go` (synthetic single-row/cursor/DML/tpcall arms, JSON determinism). `Build(BuildOptions)` arranges caller-resolved `EndpointInput`s — additive, e2e goldens untouched.
- [x] Unify FML→request/response derivation in `contract` (`contractFromOps`: `FmlGet→request`, non-err `FmlAdd→response`, err carriers →errors, `Dropped` excluded, `Code` correlated) with tests. `gen.contractFields` still has its own copy — converge in Phase 4.
- [x] Add `contract/mapping.go` `MappingView` + adapters for `plan.Mapping` and `csplan.Mapping` (no YAML changes; additive). Endpoint-resolution bodies (`condition/conditionRef/scenarioRef/scenarioFilter`, `queriesInSpan` fallback, dedup, tx votes, `Skipped` + arm-coverage `Warnings`) still live per-plan — unify in Phase 4 behind this interface (warning wording needs an approved golden pass).
- Acceptance: `contract` goldens pinned; existing e2e goldens untouched. (Holds today.)

### Phase 3 — Namers + neutral arm view (2–3 days)

- [x] Extend `common/naming.go` (`Pascal`/`UpperSnake`/`Snake`/`StripHungarianPrefix` promoted from `csplan`), add `internal/namer/{namer,go,py,cs}.go` with type maps + tests (every CType × language).
- [x] Add neutral `flow.ArmView` (`flow/armview.go`: `ArmView(tree, from, to) []ViewLine` with `query|fml_op|tpcall|return|dropped|code|placeholder` kinds carrying query/field/call identities, plus the `CallResolver` (`CallFor`/`RowFor`) interface generalizing the Go-named `Resolver`). `RenderSpan`, `pyplan.CodeView`, and the `csgen` arm view keep their current renderers (golden-neutral); new seam code consumes `ArmView`, backends cut over in Phase 4.
- [ ] Reimplement `RenderSpan`/`CodeView`/csgen arm view on top of `ArmView` behind a flag; golden-compare old vs new, then cut over.
- Acceptance: Go/Python/C# goldens byte-identical through the compatibility layer; namer unit tests green.

### Phase 4 — Rewire backends as projections (3–5 days, incremental per language)

Order: C# (smallest surface) → Python → Go (largest, most goldens).
- [x] Naming delegates (golden-neutral, 2026-09-21): `csplan.pascalOf` → `common.Pascal`, `csplan.propNameOf` → `common.UpperSnake`, `csplan.DefaultQueryName` → `CsNamer.Method` over the contract unit; `pyplan.verbOf` → `contract.QueryKindOf` + shared verb; `gen` row field `Name`/`Type` → `GoNamer.Prop`/`FieldType`. Parity tests pin each delegation.
- [ ] `csplan.buildQueryPlan` delegates params/props/SQL to `contract.QueryUnitFor` + `CsNamer` (binds: `ExecutableBinds` vs raw `q.Binds` needs a golden-reviewed cutover — INTO-leak fixtures change). `csgen` template data constructors take `contract.*`.
- [ ] `pyplan.Build` delegates binds/SQL/rowshape to `contract`; `pygen` data constructors take `contract.*`.
- [ ] `gen` row/contract fields (`rowFields:gen.go:246`, `contractFields:290`, `fieldFromFML:156`, `ModelFile:317`, `DBMethod`, `dbParams`) delegate fully to `contract` + `GoNamer` (names done; derivation + `queriesInSpan`/dedup still per-plan).
- [ ] Enforce: audit test (`contract/audit_test.go`) documents the surface; flip it to fail on new `ir.Query` imports in `gen/pygen/csgen` once derivation delegates (today it records, not gates — many readers remain).
- Acceptance: full suite green; golden diffs only where explicitly approved (e.g. warning wording unification); audit test shows the shrunken surface.

### Phase 5 — Multi-template + multi-language proof (2 days)

- [ ] Demonstrate the payoff: add a **second Go template set** (e.g. `gorm` or `pgx` variant behind `templates.Provider` overlay + `profile`) consuming the same `contract.Service` with zero parse changes. Golden-pin one fixture under both sets. (Not started — `gen.Options.WithGorm` is a store-handle flag, not a contract-driven set.)
- [x] Add `docs/adding-a-language.md` (5-step recipe). Done early; validated against `contract` + `namer` + parity tests.
- [ ] Remove deprecated `ir` shims (`GoHint`, `TemplateID()`), delete old derivation copies (per-plan endpoint-resolution copies; `csplan.cleanSQL`/`pyplan.bindOrder` already delegate via wrappers — delete wrappers then). BLOCKED: IR goldens (`testdata/goldens/**/*.ir.json`) still pin `template_id`/`go_hint` — removal needs an approved golden migration (strip keys + `ir` round-trip test), not a silent delete. Update `docs/engine-wiring-audit.md` Tier-2 notes then.
- Acceptance: second Go set renders from same contract; deprecated shims gone; `rg 'TemplateID|GoHint' internal/ir` empty.

### Phase 6 — Mapping & config unification (optional, 2–3 days — do after 1–5)

- [x] Scaffold: `contract/mapping.go` `MappingView` (`Endpoints()` + `DBPin()`) with adapters for `plan.Mapping` and `csplan.Mapping`. No YAML changes; old files work unchanged. Full unification (shared `service.mapping.yaml` with per-target `go/cs/py` sections) still pending.
- [ ] Unify user-facing mapping: shared `service.mapping.yaml` (`service/endpoints[{name, condition|conditionRef|scenarioRef|scenarioFilter, route}]/dbMethods`) with per-target sections for names the namers need. Keep old files working via adapter for one release.
- [ ] `discover` emits the unified draft (`-target go|cs|py` becomes a projection, not a separate derivation — fixes the `DefaultMethodNames` vs `DefaultQueryName` drift risk).

## 5. File-by-file change sketch

| File | Change |
|---|---|
| `internal/ir/types.go:29,39,168,183,210` | Delete `Template*` consts, `TemplateID()`, `GoHint`, resolve `Predicate` note; add `CanonicalCType` |
| `internal/ir/extract.go:591` | Delete `goHint()` (moved to `gen/gotype.go`) |
| `internal/ir/query.go:46,126,155` | Stop setting `TemplateID` |
| `internal/sqltext/sqltext.go` | Add `CanonicalSQL`; `csplan.cleanSQL`, `pyplan.emitSQL` delegate then delete |
| `internal/contract/*.go` (new) | `Service/Endpoint/QueryUnit/Field/Request/Response/TPDependency` + `Build` + goldens |
| `internal/namer/*.go` (new) | `Namer` iface + `Go/Py/Cs` impls + type maps |
| `internal/common/naming.go` | Add `Pascal/Snake/UpperSnake/StripHungarian` (promote `csplan.prefixRe`) |
| `internal/flow/render.go:14,22` | Neutralize `Resolver`, add `ArmView`, keep `RenderSpan` as Go formatter |
| `internal/flow/scenario.go, discover.go` | Consumed by `contract.Build`, not duplicated per plan |
| `internal/plan/plan.go` | Resolution delegates to `contract.Build`; `Unit.TemplateID` gen-local |
| `internal/csplan/plan.go:359,423,444,456` | `buildQueryPlan/cleanSQL/propNameOf/pascalOf` delegate to `contract+namer` |
| `internal/pyplan/pyplan.go:257,271,275` | `emitSQL/bindsOf/bindOrder` delegate to `sqltext.CanonicalSQL` + `contract` |
| `internal/gen/gen.go:156,235,279,306,511,614` | Field/row/param derivation from `contract` + `GoNamer` |
| `internal/templates/specs.go` | Document each `*Data` as projection of `contract.*` (no struct churn needed day 1) |
| `internal/templates/embed.go` | Add second Go set ids (Phase 5), e.g. `db_method_*_gorm` |
| `docs/adding-a-language.md` (new) | Recipe for N+1th language |

## 6. Risks & guards

- **Golden churn**: every phase is golden-neutral except explicitly approved warning-wording unification (Phase 4) and shim removal (Phase 5). Keep pre-refactor baseline from Phase 0; any diff must be `git diff`-reviewed per fixture.
- **Over-abstraction**: `contract` must stay a *projection of Tuxedo facts* (FML/SQL/conditions/tpcalls), not a generic ORM model. No invented fields; ambiguity stays loud (`Ambiguous`, `Residue`, `Warnings`, `Skipped`).
- **LLM seam creep**: contract builder is deterministic (`ir` package rule: "never decides" extends here — it arranges, never invents). LLM stays in `convert`/`csgen`/`pygen` body seams consuming `ArmView`.
- **Mapping compat**: no YAML breakage before Phase 6; adapters keep old files working.

## 7. Effort & sequencing

Total ~10–15 days incremental, each phase independently shippable and test-gated. Recommended order: 0 → 1 → 2 → 3 → 4 (cs, py, go) → 5 → 6. Do not skip 0–1 (they buy the safety net); do not start 6 before 2–4 (unified mapping needs the unified model first).

## 8. Definition of done

- [ ] `ir` imports zero language concepts (`rg -i 'goHint|templateID|gin|sqlx|oracle|pascal' internal/ir internal/tsscan internal/pred` clean).
- [ ] One `contract.Service` JSON golden per representative fixture, consumed by all three backends' tests.
- [ ] Two Go template sets render from the same `contract.Service` with no parse change.
- [ ] `docs/adding-a-language.md` recipe validated by review (a new contributor can follow it without reading `gen`).
- [ ] Full `go test ./...` green, baseline goldens preserved except approved diffs.
