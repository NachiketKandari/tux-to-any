# Adding a language (or a second Go template set)

This is the Phase 5 payoff of the uniform-IR work: new targets project the
same `contract.Service` instead of re-parsing TUX.

## The 5 steps

1. **Read `internal/contract/contract.go`.** `Service/Endpoint/QueryUnit/Field`
   is the only model you need: FML request/response, canonical executable SQL
   (`sqltext.CanonicalSQL`), executable-order binds, row fields, tpcall
   contracts, residue. Build it via `contract.Build` (deterministic,
   JSON-stable) — never from raw `ir` in new code.
2. **Implement `namer.Namer`.** Copy `CsNamer`/`PyNamer`/`GoNamer` in
   `internal/namer/namer.go`: `Method` (query → method/const name), `Param`
   (bind → param name), `Prop` (row entry → property name), `FieldType`
   (canonical CType → host scalar, via `ir.CanonicalCType`). Unit-test the
   type map once here.
3. **Project to template data.** Write `XxxData` structs for your files as
   functions of `contract.*` (see `templates/specs.go` for Go,
   `csgen/gen.go:72` `EpData/QData` for C#, `pygen/data.go` for Python).
   Rule: constructors take `contract.QueryUnit/Endpoint`, never `ir.Query`.
4. **Add `templates/*.tmpl` + IDs** in `internal/templates/embed.go`, render
   via `templates.Provider` (embedded default, `FileProvider` overlay for
   user-tunable sets). A second Go set (e.g. gorm/pgx) is just a new Provider
   over the same contract — zero parse changes.
5. **Wire `cmd` + golden fixture.** Add the subcommand mirroring
   `convertcs`/`batchpy`, pin one fixture's output as golden, and add a
   `contract_parity_test.go` like `csplan`/`pyplan` proving your SQL/binds/
   names equal the contract derivation.

## Rules

- `internal/ir`, `internal/tsscan`, `internal/pred` stay language-neutral
  (no `GoHint`, no `db_method_*`, no gin/sqlx/Oracle names).
- SQL canonicalization: call `sqltext.CanonicalSQL` / `ExecutableBinds` only.
- C types: switch on `ir.CanonicalCType`, never raw `CType`.
- Endpoint slicing: consume `flow.Tree`/`Scenario` + `flow.ArmView`
  (language-neutral placeholders), never `flow.RenderSpan`'s Go skeleton.
- LLM seams consume the arm view + endpoint contract; signatures, SQL, and
  required calls are deterministic inputs the model must not re-derive.
