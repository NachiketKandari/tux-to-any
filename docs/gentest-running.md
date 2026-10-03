# Running `gentest` on your own converted service

`gentest` reads a converted Go service (the `db` / `controller` / `handler` /
`models` tree that `convertgo` produced) and writes table-driven Go tests for
the `db`, `controller` and `handler` layers.

Everything here is deterministic — no LLM is in the loop unless you ask for one.
The same input always produces byte-identical output.

For what is *known not to work yet*, read [Known limitations](#known-limitations)
before you trust a green run.

---

## 1. Build

```sh
cd tux-to-any
go build -o bin/tuxconv ./cmd/tuxconv
```

There is no `mockgen` requirement to get started: if `mockgen` is not on your
`PATH`, mocks are generated via `go run go.uber.org/mock/mockgen@v0.6.0`, which
needs network access on first use.

---

## 2. See the gap before writing anything

```sh
./bin/tuxconv gentest -check-only /path/to/your/service
```

Prints a per-layer report of which functions have no test, and exits without
writing. Run this first — it is the cheapest way to confirm the tool found your
service and parsed it.

Optionally add `-log-file <path>` to have it also report what it would recover
from a captured runtime log (see [Using a runtime log](#4-using-a-runtime-log)).

---

## 3. Generate

There are two output modes. **Use `-out` unless you have a specific reason not
to** — it is the mode verified end-to-end, and `-in-place` currently has two
known compile failures (see below).

### `-out` — a standalone snapshot  ← use this

```sh
./bin/tuxconv gentest -no-llm -out /tmp/mytests /path/to/your/service
```

Writes the generated tests **plus a copy of the service's non-test sources** into
`/tmp/mytests`, so the result is a self-contained module you can build and test
on its own. Your original tree is never modified.

This is the verified path: on the repo's own fixture, every gate passes and
`go build ./...` succeeds.

If a destination filename is already taken, the staged copy is renamed to
`<name>_convertgo.go` rather than overwriting.

### `-in-place` — tests land next to your code  ← has known failures

```sh
./bin/tuxconv gentest -in-place -no-llm /path/to/your/service
```

Writes each `*_test.go` into the same package folder as the code it covers.
Nothing is copied or staged. Attractive because the tests end up in your repo —
but on the fixture today the **handler suite does not compile**:

```go
demo_test.go:48:9: undefined: utils.RegisterValidations
demo_test.go:52:58: cannot use gomock.NewController(suite.T())
    (value of type *"github.com/golang/mock/gomock".Controller)
    as *"go.uber.org/mock/gomock".Controller
```

Two distinct causes, both worth knowing about:

  - **`utils.RegisterValidations` is assumed to exist.** The handler template
    emits a call to it, but it is only meaningful if your `pkg/utils` actually
    has a gin-validator registration helper. The repo fixture does not, and a
    real repo that never registered validators will not either.
  - **The gomock major can be picked inconsistently.** `treeGomock` decides
    which major the *test template* imports by reading `go.mod` — and it checks
    `github.com/golang/mock` first, so a `go.mod` requiring **both** majors
    selects legacy. Meanwhile the *generated mock's* major comes from whichever
    `mockgen` binary ran (`go.uber.org/mock@v0.6.0` when `mockgen` is not on
    `PATH`). Nothing reconciles the two. A `go mod tidy` in `-out` mode hides
    this by pruning the unused module; `-in-place` has no such step, so both
    majors stay importable and the mismatch surfaces.

### Other flags

| Flag | Meaning |
|---|---|
| `-layers db,controller,handler` | Restrict to a subset. Default: every layer found. |
| `-no-llm` | Deterministic only. **Use this** unless you specifically want the LLM seam. |
| `-log-file <path>` | Take fixture values from a captured runtime log. |
| `-base <dir>` | Alias of `-out`. |
| `-config <path>` | Use a specific `.tuxgo.yaml`. |
| `-templates <dir>` | Per-template `.tmpl` overrides. |
| `-nice-names` | LLM polish for test *names* only. Never touches assertions. |

---

## 4. Using a runtime log

Without `-log-file`, request values are synthesized from the model — placeholders
that satisfy the field's own `binding` / `validate` tags.

With `-log-file`, the values are taken from a captured log instead. This is worth
doing when you have one, because those values are known-good: they already passed
your service's own binder.

```sh
./bin/tuxconv gentest -no-llm -log-file ./app.log -out /tmp/mytests /path/to/your/service
```

The same log against the same folder reproduces byte-identical output.

---

## 5. Run what it generated

```sh
cd /tmp/mytests          # or your repo, for -in-place
go mod tidy
go test ./...
```

`go mod tidy` is usually needed because `-out` copies your `go.mod` but the
generated tests may reference a test-only dependency.

---

## Known limitations

Read these before trusting a run. Details and measurements are in
[gentest-fix-plan.md §0c](gentest-fix-plan.md).

### Staging copies more than it needs to

On a `-out` run, the service's support packages must exist under the output root
— it is a separate module and cannot reach back into your tree. The current
implementation copies **every** non-test `.go` file in the module outside the
scanned service, rather than only the ones the generated tests import.

On the reference corpus this copies **7 files**, of which only 3 packages are
actually imported. The 4 dead-weight copies are:

```
pkg/repo/repo.go
pkg/services/common/db/mock_user.go
pkg/services/common/db/user.go
pkg/services/common/utils/utils.go
```

The risk is not disk — it is that `go mod tidy` must resolve dependencies for
every copied package, so one that does not compile standalone breaks the whole
build.

To see exactly what a given run copied, list the staged files:

```sh
./bin/tuxconv gentest -no-llm -out /tmp/mytests /path/to/your/service
find /tmp/mytests -name '*.go' ! -name '*_test.go' | sort
```

Anything outside the service's own `db`/`controller`/`handler`/`models`
directories came from `stageSupport` — the implementation is
`internal/testgen/stage.go`, function `stageSupport`.

### Host package paths are hardcoded

The generated tests import the response-envelope and helper packages by
hardcoded path:

```
<module>/pkg/logger
<module>/pkg/network
<module>/pkg/utils
```

These are **not discovered** from your source. If your repo names them
differently — say `pkg/httpresponse` instead of `pkg/network` — the generated
import will not resolve and the suite will not compile.

**Check this first if a generated suite fails to build.** The fix on your side
is a `.tmpl` override via `-templates`, renaming the import. The proper fix in
the tool is to read the host packages from the handler body's own imports,
which are already parsed.

### Known defects, by layer

  - **controller, from a runtime log**: 4 of 5 cases fail. Cause is understood
    and the model is built, but the wiring into the case table is not shipped.
    See §0c for the three coordinated changes it needs.
  - **inline backtick queries**: a store method that passes its SQL literal
    directly to `ExecContext` instead of via a `query` variable does not get it
    extracted.
  - **struct-typed bind arguments**: rendered as a literal `nil`, which
    nil-panics at run time.

### On a green run

A green controller suite means the generated cases execute. It is not a claim
that the service is correct, and it is not a coverage measurement — use
`-check-only` for that.

### Triage

Errors you are most likely to hit, and what each one actually means:

| Error | Cause | Fix |
|---|---|---|
| `package <mod>/pkg/network is not in std` | Support package not staged | Should not happen on `-out`; if it does, the staging walk missed your layout |
| `undefined: utils.RegisterValidations` | Template assumes a validator-registration helper | Add the helper to your `pkg/utils`, or override via `-templates` |
| `cannot use gomock.NewController(...) as *go.uber.org/mock/gomock.Controller` | Two gomock majors; template and mockgen disagreed | See the `-in-place` note in §3 |
| `failed on the 'oneof' / 'required' / 'positivenum' tag` | A model validator rejected the request before the handler ran | Should not happen; if it does, the field's `binding` tag has a form `validate.go` does not model |
| `Unexpected call to Mock…Controller.X: there are no expected calls` | Handler routes one field to several methods; only one EXPECT was written | Known; see §0c |
| No mocks generated, silently | `mockgen` failed (often invalid Go upstream of it) | Put `mockgen` on `PATH`, or expect the `go run` fallback to need network |

---

## Repository layout

`docs/gentest-fix-plan.md` — the design rationale, the measured results, and the
history of what was tried and reverted.

`internal/testgen/` — extraction and case generation.
`internal/templates/` — the Go test file templates.

`testdata/gentest/` — a small self-contained fixture used by the repo's own
golden tests. Useful as a worked example of the input shape.