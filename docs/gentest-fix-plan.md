# gentest corpus-run fix plan (F1–F11)

Status: **P0–P2 applied; P3 partially applied.** Written 2026-10-01 after
re-verifying every finding in `riskPipelineTest/GENTEST_REPORT.md` §6 against
the code at commit `bc4816b` and reproducing the compile failures in a throwaway
copy of the converted tree.

Scope: `internal/testgen`, `internal/templates`, `internal/gen`, plus
`testdata/gentest`. No change to the corpus or to `riskPipelineTest/` (it stays
the measurement harness, not an input to the tool).

## 0a. Corrections and new findings from applying the plan

Applying §5's "grow the fixture" item is what surfaced these. They are recorded
here because the plan as written is not the plan that shipped.

**F1's majority vote is wrong, and the fixture proved it.** §3 specified picking
ONE handle by majority vote over `dbFact.Recv` and passing `nil` for the rest,
on the reasoning that "a suite exercising the majority path never touches them".
That reasoning does not hold: a generated db suite emits a case for **every**
method in the store, so a store with a write handle has its write method
nil-panic. Measured on the grown fixture — `AddOrder` (on `g.writeDb`) panicked
at `demo.go:89` with a nil `*sqlx.DB` while the reads on `g.db` passed. What
shipped instead: **every** parameter whose declared type is a sqlx handle gets
`suite.sqlDB`, because a sqlmock suite has exactly one connection and both
handles are the same logical database. `dominantDBHandle` is deleted.

**A no-request controller method produced unparseable output (new).** F9 made
`extractCtrlFact` yield a fact for a method that takes no request — but the
controller template still emitted `request := &{}` for it. That does not parse,
and the parse gate then discarded the **entire controller suite**, not just the
one method. `TestControllerMethodData.NoRequest` now suppresses the request
entirely. This was invisible until the fixture grew a no-request endpoint.

**A struct-typed bind argument is emitted as literal `nil` (new, NOT fixed).**
`dbCallArgs` renders a store call's argument by value shape, and anything that
is not a basic literal becomes `nil`. A DML method taking
`request *models.OrderRequest` is therefore called as `AddOrder(ctx, nil)` and
nil-panics on `request.CompCode`. Found while growing the fixture; the fixture
was changed to take scalar binds instead, which is what the corpus does
(`rpdbfn.txt` passes `question_id, question_no, …`, never a request struct), so
this is out of scope for F1–F11 and remains an open defect. The corpus does not
expose it; a converted service that differs would.

**A scalar response was asserted as its zero value (fixed).** A controller
returning `(string, error)` has no fields to map, so `responseLiteral` fell back
to the type's zero and the deterministic route asserted `""` against
riskprofile's `EditMarks`, whose body says `return "Marks Edited Successfully",
nil`. `ctrlFact.ScalarResponse` now reads the body's own literal — a third
source below the log's real value.

**Three more shape-vs-body defects, all the same mistake (fixed).** The corpus
run found each independently:

  - `isDeleteTx` read "DELETE-in-a-tx tolerates zero rows" off the SQL verb.
    riskprofile's `DeleteQuestion` IS a DELETE-tx that returns
    `errors.New("unable to delete the question")`, so every zero-rows case
    failed. The branch is now chosen by whether the body states an error, which
    also removes F2's sentinel fallback — a method with no zero-rows branch has
    no way to produce one, so asserting the sentinel for it was the invented
    expectation F2 exists to eliminate.
  - `CtrlCallArgs` APPENDED across call sites, so a handler calling two
    controller methods produced `ListSection(c, &request, &request)`.
  - `isErrNoRowsSentinel` matched `HasSuffix("ErrNoRows")` and so missed the
    conventional lowercase `errNoRows`.

**Every mock payload hardcoded a two-element `(value, error)` form (fixed).**
Four sites across three files, each with its own copy, and the corpus broke on
all of them one after another: `EditMarks(ctx, …) error` (got 2, want 1),
`GetDB() *sqlx.DB` (got 2, want 1), `QuestionIdExists(…) (bool, error)` (nil is
not nillable), `AddQuestion(…) (string, error)` (nil is not nillable). One rule
— element count IS the declared result count, error last — replaces all four,
and the handler layer stopped being a second copy of the same bug.

**`fullTestGate` never ran (fixed).** It returned early on
`len(res.Files) == 0`, but a staged run populates `res.Staged` — and the CLI
only ever sets `FullTest` together with `Stage`. The one gate that actually
RUNS the generated tests was dead in every real invocation.

## 0c. State at `3e56f02` — the control-flow work, and the one blocker left

Two commits after the P0–P3 work: `38baa7b` (controller path model) and
`3e56f02` (handler validator tags + envelope).

| layer | deterministic | logroute |
|---|---|---|
| db | 124 pass / 0 fail | 127 pass / 0 fail |
| controller | 3 pass / 0 fail | **1 pass / 4 fail** |
| handler | **45 pass / 0 fail** | **47 pass / 0 fail** |

Handler went from 1/35 and 8/28 to 45/0 and 47/0. Its three old open items
below are now closed; the controller's is half-closed.

### Closed since 0b

  - **Validator tags in assumed values.** `internal/testgen/validate.go` picks
    a value against the field's `binding`/`validate` tag. Three things were
    hiding it, and each one silently disabled the whole approach: `tagValue`
    split tags on spaces (so `oneof=W X Y` read as `oneof=W`) and is now
    `reflect.StructTag.Get`; `required` consumed the rule so the `oneof` behind
    it was never reached (required is a precondition, not a constraint, and now
    falls through); `fieldType` stripped the models qualifier off slice elements
    so the struct lookup missed.
  - **The handler's response envelope.** The case table is read from the body —
    one case per `SuccessJSON` / `FailureJSON` / `BadRequestJSON` the handler
    actually writes. Two assertions were REMOVED rather than corrected, because
    the code was inventing them: the numeric status code (it is `GinContext`'s
    choice, and `GinContext` is outside the scanned service, so 500 and 204
    were wrong by construction) and the old `"Failure"` case, which mocked
    `(nil, nil)` and then asserted a `"No Data Found"` error no path produces.
  - **Missing support packages in the staged tree.** `stageSources` now copies
    the module's host packages (`stageSupport`). Generated suites import
    `network` for `HttpResponse` and `utils` for a sqlmock handle, and staging
    only took the service's own directories, so the out tree could not compile:
    `package …/pkg/network is not in std`. **This was also the controller's
    blocker — see below.**

#### Staging verified WITHOUT the harness

The corpus run cannot distinguish "the generator staged it" from "`sync_support`
copied it afterwards", so the fix was re-run with the harness removed
completely — `riskPipelineTest/src` as the input, `-out` a bare temp dir, no
`sync_support`, no `measure.sh`:

    go mod tidy     exit 0
    go build ./...  exit 0
    go vet          clean on db, controller, handler
    go test         db ok · handler ok · controller FAIL

The out tree was self-contained. **The defect was never a fixture or harness
artifact** — it would have hit any converted service, since `logger` / `network`
/ `utils` are host packages every one of them has. The controller `FAIL` above
is the same 4 logroute failures from the table, not a new one.

This also means the harness is now redundant for support packages. Leaving it
in place is deliberate — it is measurement-only and out of scope — but the
generator is no longer relying on it.

### The controller: model built, wiring not shipped

`internal/testgen/ctrlpath.go` enumerates every path through a controller
method from its body — one case per guard, the shape a hand-written Go table
test already uses. `ctrlFact.Paths` carries it to the pipeline. Pinned against
the two corpus methods that broke (`AddQuestion`, `AssessQnA`), the `GetDB`
path-dependence, the `ErrNoRows` split, declared arity, and walk-order
stability over 20 runs.

**NOT WIRED INTO THE CASE TABLE.** A first attempt passed the repo suite and
the golden, then made the compile gate drop the log route's controller suite
outright — the file was simply not staged. Cause: the generated suite needed
`pkg/utils` (for `utils.NewSqlxMockDB`) and `go-sqlmock`, and `stageSources`
copied neither. `3e56f02` fixes exactly that, so **retry this first**.

The wiring needs three coordinated changes, all written and then reverted:

  1. `templates.CtrlCall` gains `Times` (a looped call runs once per element,
     so the trip count IS the EXPECT arity) and `IsHandle` (a call returning
     `*sqlx.DB` cannot be mocked with nil — see below).
  2. `TestControllerFileData` gains `NeedsTx` + `UtilsPkg`; the file template
     gains a `sqlDB`/`sqlMock` suite field, `utils.NewSqlxMockDB()` in
     `SetupSuite`, and `ExpectBegin`/`ExpectCommit` around the handle's EXPECT.
  3. `gen.go` sets `NeedsTx` from whether any store call's declared result is a
     sqlx handle.

The flat `mockInputN` case layout already supports per-path EXPECT sets: a
path is always a PREFIX of the call list, and `if testCase.mockInputN != nil`
skips the rest. So the renderer only has to populate those fields per path.

**Why a nil handle is the third cause of the controller failure**, found by
reading `utils.ExecTransaction`:

    if db == nil { return nil }   // the closure is NEVER called

So `GetDB().Return(nil)` does not merely weaken an assertion — it makes every
call inside the transaction silently disappear, which is why `AddQuestion` and
`AddAnswer` go unmet. The handle must be the suite's sqlmock-backed one. A path
that fails inside the transaction rolls back, and `ExecTransaction` discards
that error, so leaving `ExpectCommit` unmet is harmless — nothing calls
`ExpectationsWereMet`.

### Still open, unchanged

  - **An inline backtick query literal is not extracted.** `dbFact.Query` is
    read from a `query` variable, `var` or `const`; a method passing the literal
    inline to `ExecContext` gets an empty `Query`, defeating `isDeleteTx`'s
    DELETE prefix test.
  - **A struct-typed bind argument renders as literal `nil`** and nil-panics.
  - **db coverage is short of the plan's bar**: 92 case rows against the human
    suite's 97, so 124 against a ≥129 target. A coverage gap, not a failure —
    zero cases fail.
  - **The human handler baseline is 11/48.** The generated 45/0 exceeds it, but
    that is not evidence of quality: the human suite simply does not cover
    those methods. Treat the handler numbers as "the generated cases run", not
    as a correctness claim.

### Staging limitations found while verifying the above

Two things surfaced in the isolation run. Neither is a failure, and neither
blocks the controller work, but both should be recorded rather than discovered
later.

  - **`stageSupport` copies the whole module, not what is needed.** It walks
    every non-test `.go` outside the scanned service with no notion of which
    packages the generated suites import. The copies are *required* — the out
    tree is its own module and cannot reach back into the source tree, so a
    support package must physically exist under `out/` — but the breadth is not
    tuned. **The scale cost is unmeasured.** The corpus has 7 support files /
    12 KB; there is no large real module here, so "a real service would copy a
    lot more" is a hypothesis, not a finding. The concrete risk is not disk: it
    is that `go mod tidy` must then resolve dependencies for every copied
    package, and one that does not compile standalone fails the whole build.

    The narrow replacement is cheap and exact. Generated output imports only six
    module-local packages, and three are service-internal and already staged by
    the `db`/`controller`/`handler`/`models` loop. The host set is exactly
    `pkg/logger`, `pkg/network`, `pkg/utils`.

  - **Host package paths are HARDCODED, never discovered.** All six come from
    string concatenation in `gen.go`:

        LoggerPkg:  sc.module + "/pkg/logger"
        NetworkPkg: sc.module + "/pkg/network"
        UtilsPkg:   sc.module + "/pkg/utils"

    There is no import-scanning logic anywhere in `internal/testgen`. A repo
    whose envelope package is `pkg/httpresponse` gets a hardcoded import that
    does not exist, and the generated suite will not compile. **This is
    pre-existing — not introduced by `stageSupport`** — and it is the recurring
    bug class named below in its purest form: the layout is inferred from the
    corpus, not read from the module. Fixing it means resolving the host
    packages from the handler body's own imports, which are already parsed.

### Corrections to earlier numbers

Two claims in this file were wrong and have been fixed in place:

  - A run-to-run nondeterminism was reported and then **disproved**. The
    outlier was a stale artifact in `riskPipelineTest/out/` from a mid-edit
    working tree, predating the commit. Generation is reproducible: 6/6
    identical full-tree hashes, both routes.
  - The measurement this section originally quoted ran against that stale
    artifact. Re-measured on fresh output, the controller's logroute failures
    went from 3 to 4. The table at the top of §0c is the measured one.

## 0b. Measured result, and what is still open

Superseded by §0c above, which re-measured both routes after `3e56f02`. Kept
for the record of what was believed at `bc4816b` + P0–P3.

After P0–P3, both corpus routes, re-measured with
`riskPipelineTest/run_routes.sh`:

| layer | deterministic | logroute |
|---|---|---|
| db | **pass** (124 subtests) | **pass** (127 subtests) |
| controller | **pass** | 2 subtests fail |
| handler | 1 method's cases fail | 1 method's cases fail |

db was failing every DML zero-rows case before; controller and handler did not
compile at all. Both now compile on both routes.

Three things remain, and all three are feature-sized rather than bug fixes:

  - **Error-path call flow (controller, logroute).** `ctrlCaseInputs` decides
    which store calls get an EXPECT from whether the log recorded them. For
    `GetDB()` — an argument to `utils.ExecTransaction`, reached only after
    `DeleteQnA` succeeds — the log can never record it (no SQL), so its absence
    is not evidence of absence. Emitting an EXPECT anyway fixes the success
    case and breaks the error cases, where the call genuinely never runs. Tried,
    measured, reverted: the failure count is identical either way and the
    special case is not worth it. Which calls execute is path-dependent, and the
    tool has no model of the path.
  - **The handler's response envelope (both routes).** The template asserts a
    fixed 500 / 204 / 200 table, and the corpus's `GinContext` helper returns
    204 whenever the payload is empty and 500/404 from its own error mapping —
    so a success case with an empty response disagrees with the table. Deriving
    each handler's envelope from `network.GinContext` plus the controller's real
    return shape is the work.
  - **Validator tags in assumed values (deterministic).** `c.BindJSON` rejects
    the synthesized placeholders on `oneof` / `positivenum` tags, so the handler
    returns 400 and never reaches the controller. Honouring validator tags when
    synthesizing values is a feature; the log route does not hit it because it
    supplies real ones.

None of these are F1–F11, and the plan already recorded that the handler layer
has no trustworthy baseline. They are listed here so the next person does not
rediscover them.

**An inline backtick query literal is not extracted (new, NOT fixed).**
`dbFact.Query` is read from a `query` variable, `var` or `const`. A method that
passes the literal inline to `ExecContext` gets an empty `Query`, which then
defeats `isDeleteTx`'s DELETE prefix test — so a DELETE-tx method is emitted as
an error case instead of a tolerance. The corpus always assigns to a variable,
so this is not a corpus defect either; the fixture was written to match.

## 0. What changed in my verification

Three things the report got slightly wrong or missed. They matter because they
change what "fixed" means.

**F3 is not a corpus quirk — it is the tool's own golden fixture failing.**
`testdata/gentest/pkg/services/demo/db/demo.go:25-33` is a scalar `GetContext`
read that propagates `err` unchanged. The byte-pinned golden
`testdata/gentest/expected/pkg/services/demo/db/demo_test.go:111-117` asserts
`Success-NoRows` with `expectedError: ""`. Running that golden verbatim:

```
--- FAIL: TestDemoStoreSuite/TestGetOrderCount/Success-NoRows
    Received unexpected error: sql: no rows in result set
```

So F3 reproduces on the repo's own fixture. Nothing in CI catches it because
**no test in the repo ever executes generated output** — `TestGoldenNoLLM`
only `bytes.Equal`s files (`internal/testgen/testgen_test.go:851`), and
`compileGate`/`fullTestGate` degrade to gate lines nobody asserts on. The
golden asserts a passing-shaped file; it is not a passing test.

**F10 is worse than "`-out` double-stages".** In an in-place run,
`runServiceMocks` (`internal/testgen/gen.go:927`) writes `mock_store.go` into
the *source* dir next to the corpus's hand-supplied `interface_mock.go`, and the
package does not compile:

```
pkg/services/riskprofile/db/mock_store.go:22:6: MockRiskProfileStore redeclared
    pkg/services/riskprofile/db/interface_mock.go:24:6: other declaration
```

So an in-place gentest run **mutates the user's tree into a non-compiling
state**, which the repo's own never-write-target rule
(`docs/RULES.md` §6) forbids. The `collisionName` rename in `stage.go:96` is
also dead for this case: it only fires on filename collision, and
`mock_store.go` vs `interface_mock.go` are different filenames in the same
package. Separately the generated mock imports `go.uber.org/mock/gomock`
(`internal/gen/mocks.go:83`, pinned v0.6.0) while the templates import
`github.com/golang/mock/gomock` (`test_controller_file.tmpl:17`) — two gomock
majors in one package even after the redeclaration is resolved.

**F11's fix has a scope constraint the report does not mention.** `-out` staging
copies `go.mod` but not `go.sum` (`stage.go:51-53`), so the gate reports
`missing go.sum entry`. Resolving it needs `go mod tidy` (network) or
`GOFLAGS=-mod=mod`; both **mutate the staged tree**, which is fine under `-out`
but must not be applied to an in-place run.

## 1. Fix order

Grouped by dependency, not by severity. F11 and F10 come first because until
they are fixed the compile gate cannot tell us whether any later fix worked.

| Phase | Fixes | Gate to pass before moving on |
| --- | --- | --- |
| P0 — make the gate honest | F11, F10 | `go vet` on the source tree reports *code* errors, not dependency/redeclaration errors |
| P1 — make db green | F1, F2, F3 | db suite compiles and runs; leaf pass rate measured against the human baseline |
| P2 — make controller/handler compile | F7, F6, F9, F8, F5 | all three layers `go vet` clean on the corpus tree |
| P3 — lock it in | new pinning tests | each fix has a test that fails before it and passes after |

P1 before P2 because db is the only layer with a trustworthy human baseline
(129/129), so it is the only place a fix can be *proven* rather than argued.

## 2. P0 — the gate must be able to lie less

### F11 — resolve module deps before the compile gate

`compileGate` (`gen.go:295-327`) runs `go vet` and `go test -run '^$'`
immediately. `-out` staged a `go.mod` with no `go.sum`, so every gate line reads
`missing go.sum entry` and no code error is ever surfaced.

Fix: before the first gate command for a module root, run
`go mod tidy` (fall back to `GOFLAGS=-mod=mod` + `go mod download` when tidy
fails offline), bounded by the existing 90s gate timeout, and record the step
as its own gate line so a dependency failure is distinguishable from a code
failure. **Only under `opts.Stage`** — an in-place run must not write
`go.sum`/`go.mod` into the user's tree.

Pins: `-out` run on a fixture with a real dependency set reports `clean` for a
package that has no code errors; a fixture with a deliberate code error still
reports `FAILED` (i.e. the gate is not silently disabled by the tidy step).

### F10 — stop writing generated mocks into a tree that already has them

Three separate defects, fix all three:

1. **Redeclaration.** `runServiceMocks` must not write a mock into a directory
   that already contains one for the same interface. Before generating,
   check for an existing `Mock<Iface>` in the layer dir (the corpus's
   `interface_mock.go` provides it) and skip. The generated tests import
   `db.MockRiskProfileStore`, which the existing mock already satisfies, so
   skipping is correct and is also the cheaper path.
2. **gomock major mismatch.** When no mock exists, the one gentest generates
   must match the gomock the templates import. Either pin mockgen to the
   `github.com/golang/mock` line, or make the template import follow whichever
   major the tree actually vendors. Prefer following the tree: a converted
   service inherits its module's dependencies, and the templates should not
   override that.
3. **In-place mutation.** With (1) in place, `runServiceMocks` becomes a no-op
   on any tree that ships its own mocks, which removes the never-write-target
   violation for the common case. For a tree with *no* mock, an in-place run
   must either write nothing or write only when the user asked; default to
   writing nothing and report the missing mock as a warning the templates'
   `MockGen` header comment already tells the user how to produce.

Pins: an in-place gentest run over a fixture that ships `interface_mock.go`
leaves the tree byte-identical except for the new `_test.go` files; a fixture
with no mock gets a `go.uber.org/mock`-or-`github.com/golang/mock` mock whose
import matches the test file.

## 3. P1 — the db layer

### F1 — `dbCtorCall` must know which argument is the handle

`gen.go:897-905` emits `NewRiskProfileStore(nil, suite.sqlDB)` whenever
`DBCtorArgs > 1`. The corpus ctor is
`NewRiskProfileStore(db, writeDb *sqlx.DB)` (`db/interface.go:61`) and 14 of 15
queries run on `g.db` (`g.writeDb` only for `EditMarks`,
`db/riskprofile.go:255`), so every read nil-panics and the suite aborts at
12 pass / 10 fail before asserting anything.

The information needed is already extracted: `dbFact.Recv` records `g.db` vs
`g.writeDb` per method (`extract.go:383`). The fix is to compute, across the
service's db facts, which ctor parameter name the dominant receiver names, and
pass `suite.sqlDB` to that parameter and `nil` to the rest. Majority vote over
`Recv`, deterministic and stable. Do **not** special-case position.

Note the human suite passes the same handle twice
(`NewRiskProfileStore(suite.db, suite.db)`, `rpdbtest.txt:45`) — that is a valid
alternative, but majority-vote is better because it also handles a service
whose write handle genuinely differs.

Pins: 2-arg ctor with reads on arg 1 emits `(suite.sqlDB, nil)`; reads on arg 2
emits `(nil, suite.sqlDB)`; 1-arg ctor unchanged.

### F2 — DML `NoRows` must assert the method's own error

`test_db_method.tmpl:26-29` hardcodes `expectedError: "sql: no rows in result
set"` for the `rowsAffected: 0` case. The corpus DML methods return a domain
error instead (`errors.New("unable to add the question")`,
`db/crudQnA.go:62`; six such methods). The human suite gets this right —
`NoRowsAffected` + `assert.EqualError` on the domain text.

Fix: extract the zero-rows error from the method body the same way `Recv` is
extracted. Two shapes exist in the corpus and both must be recognised:

* `count > 0 → return nil; … return errors.New("<text>")` — 9 methods
* `… return sql.ErrNoRows` — 6 methods

So `dbFact` grows a `NoRowsError string` (the literal to assert, `""` when the
method tolerates zero rows) and the template renders it. Where the body has no
recognisable zero-rows branch, keep today's behaviour and record a warning
rather than guessing — the tool never invents.

Pins: `errors.New` shape emits the domain text; `sql.ErrNoRows` shape emits
`sql: no rows in result set`; a method with no zero-rows branch emits no case.

### F3 — derive the single-row no-rows contract, do not assume it

`methods.go:325-333` hardcodes the comment "single-row and scalar reads
tolerate `sql.ErrNoRows`, returning the zero value with a nil error" and emits
`expectedError: ""`. Every corpus `GetContext` read propagates `err` unchanged
(verified: 12/12), so 13 cases fail.

The contract is readable from the body, which is the real fix and also fixes
the repo's own golden: classify each db method by what its error branch does.

* `if err != nil { … return nil, err }` → **propagating**: the no-rows case
  expects `sql.ErrNoRows`.
* `if err != nil { … return &T{}, nil }` / `if errors.Is(err, sql.ErrNoRows)`
  → **tolerating**: no-rows case expects `""` (today's behaviour).

Add `dbFact.NoRowsContract` (`propagate` | `tolerate`) set during extraction,
and let the template take the expected error from it. `methods.go` keeps a
fallback to `tolerate` when the body is unrecognisable, so no currently-passing
shape regresses.

Then regenerate the golden with `GT_UPDATE_GOLDENS=1` — `demo_test.go` will
change, and that is the correct new expected bytes.

Pins: propagating body emits `expectedError: "sql: no rows in result set"` and
the golden reflects it; tolerating body is unchanged.

## 4. P2 — controller and handler compile

These five are independent of route; none has a trustworthy human baseline for
the handler layer, so the gate here is *compiles + runs without panic*, not
*matches the human suite*.

### F7 — `ctrlCtorCall` must honour constructor arity

`gen.go:908-914` always emits `NewXController(suite.xStore)`. The corpus ctor is
`NewRiskProfileController(store db.RiskProfileStore, userStore commonDB.UserStore)`
(`controller/interface.go:34`). Reproduced:

```
vet: NewRiskProfileController: not enough arguments in call
    have (*db.MockRiskProfileStore)
    want (db.RiskProfileStore, common_db.UserStore)
```

Fix: capture ctor parameter *names and types* (today only `CtrlCtor` name and
`DBCtorArgs` count are kept) and render one argument per parameter: the db
store interface param gets the generated store mock, any other interface param
gets a `nil` (or a mock if one is discoverable — see open question below).
Mirror the `dbCtorCall` shape so both layers read the same way.

Pins: 1-arg ctor unchanged; 2-arg ctor emits both args.

### F6 — handler EXPECT must use the controller method name

`extractHandlerFact` captures `CtrlCall` (`extract.go:703-720`) and
`renderHandlerMethod` throws it away (`methods.go:724-734`); the template
hardcodes `{{.Name}}` (`test_handler_method.tmpl:36`). `handler.GetCustomerRiskProfile`
calls `controller.GetCustomerRP`, so the generated
`EXPECT().GetCustomerRiskProfile(...)` is undefined on the mock. Verified in
the generated file:

```
riskprofile_test.go:722:  GetCustomerRiskProfile(ctx, &request).
```

Fix: add `CtrlMethod` to `TestHandlerMethodData`, set it from `f.CtrlCall`, and
have the template use it for `EXPECT()` while keeping `{{.Name}}` for the
handler invocation itself (`suite.handler.{{.Name}}(ctx)`), which *is* the
handler's own name.

Pins: a handler whose name differs from its controller call emits
`EXPECT().<CtrlCall>` and `suite.handler.<Name>(ctx)`.

### F9 — a controller method with no request must not get one

`DisplayMarks` is the only method taking no `*models.X`
(`controller/riskprofile.go:249`); `extractCtrlFact` returns nil for it
(`extract.go:640-648`), so the controller unit is `unsupported` while the
handler fact still yields `RequestType` from its `var request
models.DisplayMarksRequest` (`handler/riskprofile.go:125`) and emits
`EXPECT().DisplayMarks(ctx, &request)` — over-arity. Same shape as F6, so fix
them together: `handlerFact` should record whether the controller call actually
passes a request, taken from the controller call site's arg count, not inferred
from a local `var`. When there is no request arg, emit `EXPECT().DisplayMarks(ctx)`.

Also fix `extractCtrlFact` so a no-request controller is not `unsupported`: it
should yield a fact with an empty `RequestType`, which lets Route B recover
`DisplayMarks` (the one method the log route currently misses).

Pins: no-request controller → `EXPECT().M(ctx)`, and the method is `template`
status, not `unsupported`.

### F8 — no-arg store methods take no matcher

`store.GetDB()` takes no arguments (`db/interface.go:21`) but the controller
template emits `EXPECT().GetDB(gomock.Any())`
(`test_controller_method.tmpl:39`, unconditional). Five occurrences in the log-route
output. Fix: `storeCall` records the call site's arg count; the template emits
the ctx matcher only when the method takes one. `GetDB` is also the write handle,
so its EXPECT must return a usable `*sqlx.DB` — this couples to F1: whichever
argument the majority vote picked is what `GetDB()` must hand back.

Pins: `GetDB()` → `EXPECT().GetDB().Return(suite.sqlDB)`; a 1-arg store method
keeps `gomock.Any()`.

### F5 — slice-typed request fields must keep their type

`AssumedFixtureSource.FieldValues` (`fixture.go:98-113`) ignores
`fieldInfo.Type` and always yields a string placeholder, and both templates
hardcode `{{.Name}} string` (`test_controller_method.tmpl:6`,
`test_handler_method.tmpl:6`). The corpus has `AnswerID []string`,
`AnswerText []string`, `Marks []string`, `QnA []models.QnA`,
`FrontendIdentifierProductType []string` (`models/models.go:46-48,58-60,104,180`).
Reproduced:

```
vet: cannot use testCase.AnswerID (variable of type string) as []string value
    in struct literal
```

Fix has two halves and both are required:

1. **Type-aware fixture.** `FieldValues` returns the field's Go type alongside
   its value. For `[]string` → a one-element slice literal; for
   `[]models.QnA` → a one-element composite literal over the element struct's
   own fields, recursively. `modelsInfo.Structs` already carries every field's
   type, so this is data, not inference.
2. **Type-carrying templates.** The case-struct field declaration becomes
   `{{.Type}}` instead of `string`, and the value is rendered as a typed
   literal. Both the controller and handler templates need it, plus
   `ReqField`/`CtrlCaseField` in `specs.go`.

Keep the *values* as placeholders in Route A — F4 is not a bug, it is the
documented assumed-fixture mode. Route B already supplies real array values from
the log and should keep doing so; check that `LogFixtureSource.fieldValues`
emits a slice literal for an array element rather than a scalar.

Pins: a fixture with a `[]string` field emits `[]string` in the case struct and
a slice literal in the value; a `[]models.QnA` field emits a composite literal;
all-string fixtures are byte-identical to today's golden.

## 5. P3 — close the verification gap that let all of this through

This is the part I would not skip. Eleven findings survived because the repo has
no test that runs what it generates.

**Add an execute-the-golden test.** `TestGoldenNoLLM` should, after the
byte-compare, `go test` the golden fixture (which needs `pkg/logger`,
`pkg/utils`, and a fixed `NewDemoStore` — the fixture's current
`return &store{}` drops the handle, which is why the panic above happens before
the F3 assertion is even reached). The fixture needs to be made runnable:
`NewDemoStore` must keep its argument. That is a fixture fix, not a tool fix,
and it should land with F3.

If running the golden is too heavy for the unit suite, gate it behind an env var
like the existing corpus guards — but it must exist, because F3 is a bug the
repo's own fixture reproduces and nothing caught it.

**Add a compile gate assertion.** `fullTestGate` currently produces gate lines
that nothing checks. At least one test should assert that a generated suite over
a known-good fixture reports `clean`.

**Grow the fixture.** `testdata/gentest/pkg/services/demo` has one db method
family with a 1-arg ctor, no slices, no DML, no controller/handler name
mismatch, no no-request method. Every one of F1, F2, F5, F6, F7, F8, F9 is
invisible in it. Extend the fixture with one representative of each shape —
they are ~15 lines each and they are the regression net for all of P1/P2.

## 6. Two open questions worth deciding before P2

* **Multi-dependency controller ctors.** `NewRiskProfileController` takes a
  `commonDB.UserStore` the corpus mocks by hand
  (`common_db.NewMockUserStore`, `rptestcontroller.txt:56`). gentest only
  generates mocks for the service's own two interfaces. Passing `nil` compiles
  and passes any method that does not touch `userStore`, but the three methods
  that call `c.userStore.GetHNICustomerType` will nil-panic. Options: (a) accept
  and let those cases fail loudly, (b) discover and mock cross-package
  interfaces. (b) is real work; (a) is a one-liner. Recommend (a) now, (b) as a
  follow-up — it is a feature, not a bug fix.
* **`-log-file` as default.** The report recommends it and the data supports
  it (0 LLM calls, closes the 8 field-mapping gaps, recovers log-exact fixture
  values). Worth doing as a separate change after the fixes, so the two are not
  conflated when measuring.

## 7. Measuring "fixed"

The current numbers are only meaningful for db, and only after F1. Re-measure
with `riskPipelineTest/run_routes.sh` + `riskPipelineTest/measure.sh` and hold
to these bars:

| Target | Bar |
| --- | --- |
| db, both routes | ≥ 129/127 leaf cases, matching the human baseline |
| controller | compiles; success paths pass |
| handler | compiles; success paths pass |
| compile gate | reports code errors, never `missing go.sum entry` or `redeclared` |
| repo suite | `go test ./...` green, goldens regenerated deliberately |

Report the handler number against the human 11/48 with its caveat intact — that
baseline is a stub limitation (the corpus does not ship the host's validator
error-translator), and fixing it is out of scope here.

## 8. Commands

```bash
# before starting
go build -o bin/tuxgo ./cmd/tuxconv
go test ./internal/testgen ./internal/templates ./internal/gen

# per phase
go test ./internal/testgen ./internal/templates
GT_UPDATE_GOLDENS=1 go test ./internal/testgen   # only after a deliberate template change

# corpus re-measure (gitignored; no tracked files touched)
./riskPipelineTest/run_routes.sh
./riskPipelineTest/measure.sh
```