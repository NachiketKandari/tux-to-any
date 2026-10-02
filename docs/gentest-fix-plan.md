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