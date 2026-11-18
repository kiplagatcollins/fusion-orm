# fusion-orm — Architecture

Explain how the system is structured and how the pieces connect.

## 1. System Overview

fusion-orm is a Go library with a strict layer split: a high-performance dialect/clause core (bun's engine model), a reflection-cached schema layer, and a gorm-style session/callback API on top. The core never knows about chaining; the API layer never assembles SQL directly. SQL is built as `[]byte` through per-dialect appenders and executed through `database/sql` (or the prepared-stmt cache wrapper).

```text
[user code] → [chainable_api / finisher_api / generics G[T]]
                     ↓ getInstance() (copy-on-write)
              [Statement + clause registry]
                     ↓ finisher triggers
              [callback processor: create|query|update|delete|row|raw]
                     ↓ ordered callbacks
              [clause/ builders] → []byte via [dialect appenders]
                     ↓ resolveConn()
              [ConnPool | PreparedStmtDB cache] → database/sql
                     ↑ scan via compiled ScannerFuncs
              [schema layer: cached tables/fields/relations]
```

## 2. Tech Stack

| Layer | Choice |
|---|---|
| Language | Go 1.22+ (generics required for `G[T]` facade) |
| Execution | `database/sql` `ConnPool` interface (gorm-compatible) |
| PostgreSQL | `github.com/jackc/pgx/v5` via stdlib adapter (matches DataPulse choice; lib/pq is maintenance mode) |
| SQLite | `modernc.org/sqlite` (pure Go, CGO-free) |
| Inflection | `gobuffalo/flect` or `jinzhu/inflection` for pluralization |
| Tests | Go standard library, table-driven; SQLite-backed integration; env-gated PG (`FUSION_TEST_PG_DSN`) |
| Dependencies | minimal — schema/naming libs only; no framework lock-in |

## 3. Project Structure

```text
fusion-orm/
├── fusion.go            # Config, DB, Open, Session, clone/getInstance
├── chainable_api.go     # Model/Where/Select/Order/Preload/Joins/Scopes/Clauses
├── finisher_api.go      # Create/Find/First/Save/Update/Delete/Count/Scan/ToSQL
├── statement.go         # Statement, clause registry, QuoteTo/AddVar, byte builder
├── callbacks.go         # processor manager: create/query/update/delete/row/raw
├── callbacks/           # default chains: create, query, preload, update, delete,
│                        #   associations, transaction, callmethod
├── generics.go          # G[T] type-safe facade
├── soft_delete.go       # DeletedAt clause injection + Unscoped
├── errors.go            # sentinel errors + ErrorTranslator
├── scan.go              # struct/slice/map/scalar row scanning
├── association.go       # Association API
├── hook.go              # QueryHook interface + QueryEvent
├── prepared_stmt.go     # LRU+TTL stmt cache
├── schema/              # table.go, field.go, relation.go, naming.go, tag.go,
│                        #   appender.go, scanner.go
├── clause/              # select/from/where/order/limit/insert/values/set/
│                        #   on_conflict/returning/locking/with/expr
├── dialect/             # dialect.go (interface + feature flags), sqlite/, postgres/
├── logger/              # logger.Interface + slog adapter
├── migrator/            # AutoMigrate + versioned migrations
├── internal/            # lru, flag, unsafe-free helpers
└── tests/               # integration suites
```

## 4. Module Responsibilities

- `dialect/` — `Dialector` interface (`Initialize`, `DataTypeOf`, `BindVarTo`, `QuoteTo`, `ErrorTranslator`) + feature bitflags; owns identifier quoting, DSN-free (drivers supplied by caller). Must not import schema types beyond field metadata.
- `schema/` — cached per-type reflection (tables, fields, relations, hooks); owns tag parsing, naming, and precompiled `Appender/Scanner/IsZero` closures. Must not import DB, statement, or callback types.
- `clause/` — pure SQL clause builders: `func(b []byte) []byte` appends with vars bound through the statement. No I/O, no schema imports.
- `statement.go` + `fusion.go` — statement assembly, clause registry, session/config/clone semantics; owns the single error slot.
- `callbacks/` — the only place that knows CRUD order; each callback is a `func(*DB)`; drivers/plugins may register/replace.
- `scan.go` — row scanning through compiled scanners; handles struct (embedded + joined `alias__column`), slice, map, scalar dests.
- `logger/`, `migrator/`, `hook.go`, `prepared_stmt.go` — supporting packages; never imported by `clause/` or `schema/` internals.
- Nothing outside `dialect/` may contain dialect-specific strings.

## 5. Data Flow

1. Chainable method → `getInstance()` (clone==1 fresh statement / clone==2 cloned) → mutate `Statement.Clauses`.
2. Finisher → processor `Execute(db)`: run scopes → apply `BuildClauses` → run `StatementModifier` on Dest → parse model → resolve `ReflectValue` → run compiled callback chain → trace via logger.
3. Callbacks build SQL: clause registry renders each clause in `BuildClauses` order into a reused `[]byte`; placeholders bound as `[]any` vars.
4. `resolveConn()` → ConnPool / prepared-stmt cache → `QueryContext/ExecContext` with context cancellation.
5. Rows scanned via per-field `ScannerFunc`s (no reflection per row) into `Dest`; hooks fire (`AfterFind` etc.); error slot updated.

## 6. State Model

- **Statement (per-chain):** clauses, model, dest, vars, settings — owned by the chain instance, never mutated across `Session` boundaries.
- **DB (shared):** `Config`, dialect, logger, callback processors, conn pool — immutable after `Open` except via explicit plugin registration.
- **Schema cache (global):** per-type `*Table` metadata — built once, two-phase init for recursive relations, safe for concurrent `Get`.
- **Runtime:** cancel registry keyed by context (cancellation via `context.Context` only), prepared-stmt LRU, migration lock rows.

## 7. Key Decisions

- **Callback pipeline over direct builders** — chaining accumulates clauses; finishers trigger processors. Gets bun's zero-alloc build path with gorm's extensibility (plugins register callbacks, not forks).
- **Clause registry, not bun-style query structs** — one statement representation, dialect feature flags decide rendering (e.g. `ON CONFLICT` vs `INSERT IGNORE`), no parallel query-type hierarchy.
- **Precompiled field closures** — `Appender/Scanner` resolved once at table init; scanning/build hot paths are type-dispatched function pointers.
- **Feature bitflags over per-dialect forks** — `dialect.Feature` bitset (~35 flags: Returning, OnConflict, UpdateLimit, Savepoint, CompositeIn, …) branches rendering; adding MySQL later is flags + quotes.
- **Field-type clause injection for soft delete** — `DeletedAt` field implements clause interfaces; callbacks never special-case it.
- **Single error slot with wrapping** — `AddError` keeps the first error as wrap head; `TranslateError` maps driver constraint errors to sentinels (`ErrDuplicatedKey`, `ErrForeignKeyViolated`).
- **pgx/v5 + modernc.org/sqlite** — align with DataPulse for driver knowledge reuse; both actively maintained, CGO-free.

## 8. Error Handling

| Case | Behavior |
|---|---|
| No-WHERE `Update`/`Delete` (global) | `ErrMissingWhereClause`; explicit `AllowGlobalUpdate` or `Where`/`WherePK` required |
| Record not found (`First/Take/Last`) | `ErrRecordNotFound`; `Find` returns empty slice instead |
| Constraint violation with `TranslateError` | `ErrDuplicatedKey` / `ErrForeignKeyViolated` / `ErrCheckConstraintViolated` via dialector translation |
| Unknown column/field at build time | build error naming model + field; never silently dropped |
| Context cancel / timeout | error wrapped with action context; statement marked canceled in logs |
| Hook error | flows into the error slot; operation aborts at that callback |

Errors are wrapped with `%w`; never swallowed or only logged.

## 9. Testing

- **Unit:** tag parsing, naming strategy, clause rendering per dialect (golden SQL strings), page/clamp logic, scanner/append round-trips, guard semantics (no-WHERE blocked), statement clone isolation.
- **Integration:** full CRUD + relations + soft delete + migration round-trip against real SQLite (temp files, no daemons); PG suite env-gated behind `FUSION_TEST_PG_DSN`.
- **Benchmarks:** batch insert 10k rows, scan 100k rows, allocation-count tests to enforce zero per-row reflection.
- **Required checks:** `go test ./...`, `go vet ./...`, `golangci-lint run` (once configured).

## 10. Build and Deployment

```bash
go mod init github.com/<org>/fusion-orm      # once; confirm module path before M1
go test ./...                                # full suite
go test -bench . -benchmem ./internal/...    # perf gates
go vet ./... && golangci-lint run            # before finishing
```

- Library module: no binaries, no CLI; consumed via `go get`.
- Versioning: semver; dialect package versions move with the core (bun's version-drift lesson).

## 11. Constraints and Limits

- `database/sql` semantics: PG `RETURNING` vs SQLite `LastInsertID` backfill share one callback, gated by features.
- Composite-`IN` relation loading falls back to OR-equation batches where a dialect lacks it (`feature.CompositeIn`).
- Prepared-stmt cache purges on `driver.ErrBadConn`; LRU bounded (`PrepareStmtMaxSize`, TTL).
- Metadata/diff-based `AutoMigrate` assumes single-writer DDL; destructive diffs (drop column) require opt-in.
- Oracle-style quirks (uppercasing, ROWNUM) are out of scope until an Oracle dialect is declared.

## 12. Extension Points

- **New dialect (e.g. MySQL)** — implement `Dialector` + feature flags + quotes in `dialect/<name>/`; register in `Open`; no core changes.
- **New callback behavior** — `db.Callback().Create().Before("gorm:fusion:create").Register(...)` style registration; default chains remain replaceable.
- **Observability plugins** — implement `QueryHook` (slog hook ships in-tree); otel-style plugins possible without core changes.
- **Custom serializers/field types** — implement `fusion.DataTypeInterface` / clause-interfaces on field types (soft-delete pattern).
- **Migrations** — versioned migrations and `AutoMigrate` share the migrator interface; per-dialect inspectors plug in.
