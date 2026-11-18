# fusion-orm — Design Document

**Status:** Approved plan
**Date:** 2026-11-17

fusion-orm is a full-featured Go ORM that combines the strengths of two existing
codebases studied in this workspace:

- **bun** (v1.3.0, `../bun-orm/`) — SQL-first, zero-alloc query builders,
  dialect feature flags, precompiled field appenders/scanners.
- **gorm** (core, `../gorm-orm/`) — callback/plugin pipeline, clause registry,
  developer-friendly chaining API, generics facade, associations.

---

## 1. Design principles

| Concern                          | Taken from | Rationale |
|----------------------------------|------------|-----------|
| Dialect abstraction + feature flags | bun      | Per-dialect SQL without code forks; each dialect declares a `feature.Feature` bitset and queries branch on capability |
| Zero-alloc SQL building (byte slices, `unsafe` string/bytes conversion) | bun | The performance core |
| Precompiled per-field appenders/scanners | bun | Resolved once at table init; no reflection per row |
| Cached schema reflection          | bun + gorm | Both cache per-type; two-phase init handles recursive structs |
| Callback/plugin pipeline (CRUD as ordered `func(*DB)` callbacks) | gorm | The extension spine; drivers/plugins can register/replace/remove callbacks with `before`/`after` ordering |
| Clause registry + `Statement`     | gorm (hybrid) | Clauses *compile to bytes* via bun-style appenders — flexible **and** fast |
| Copy-on-write chaining, `Session`, `Config` | gorm | `*DB` safe for concurrent reuse; `getInstance()` clone semantics |
| GORM-style chaining + finishers + generics `G[T]` | gorm | Developer experience, compile-time type safety |
| Relations: `Preload` naming + bun's batched loading (one query per relation, composite `IN`) | both | No N+1 queries |
| Field-type clause injection for soft delete (`DeletedAt`) | gorm | Elegant, opt-in, no special-casing in callbacks |
| Versioned migrations + `AutoMigrate` (schema diff) | bun + gorm | Both migration styles in one package |
| LRU prepared-statement cache, error translation, sentinel errors, logger with slog adapter | gorm | Production DX |
| Single `QueryHook` observability interface | bun | Thin extension seam for slog/otel-style plugins |

**Primary API style:** GORM-style fluent chaining
(`db.Model(&user).Where(...).Find(&users)`), powered by bun-style SQL builders
underneath.

**Struct tags:** own dialect with gorm-like keys
(`fusion:"column,type,pk,autoincrement,notnull,default,index,unique,rel:has-many,foreignKey,many2many,polymorphic,embed"`),
plus naming conventions close to gorm (`Tabler` interface, pluralized
snake_case fallback).

**Databases (MVP):** SQLite (pure-Go shim) + PostgreSQL.

---

## 2. Architecture overview

```
┌──────────────────────────────────────────────────────────┐
│  User API                                                │
│  chainable_api.go ──► finisher_api.go ──► generics.go    │
├──────────────────────────────────────────────────────────┤
│  Session layer (fusion.go)                               │
│  Config / DB / Session / clone / getInstance             │
├──────────────────────────────────────────────────────────┤
│  Callback pipeline (callbacks.go, callbacks/)            │
│  processors: create | query | update | delete | row | raw│
├──────────────────────────────────────────────────────────┤
│  Statement + clause registry (statement.go, clause/)     │
│  clauses compile to []byte via dialect + appenders       │
├──────────────────────────────────────────────────────────┤
│  Schema layer (schema/)                                  │
│  cached reflection: tables, fields, relations, hooks     │
├──────────────────────────────────────────────────────────┤
│  Dialect layer (dialect/) + ConnPool + transactions      │
│  sqlite / postgres; feature bitflags; savepoints         │
└──────────────────────────────────────────────────────────┘
```

### Flow of a query

1. User calls chainable methods; each calls `getInstance()` (copy-on-write)
   and mutates `Statement.Clauses`.
2. A finisher (`Find`, `Create`, …) triggers the matching **callback
   processor** (`query`, `create`, `update`, `delete`, `row`, `raw`).
3. The processor: runs pending scopes → applies build clauses → applies
   `StatementModifier` on Dest → parses Model → resolves `ReflectValue` →
   executes the compiled callback chain → traces SQL via logger.
4. Callbacks assemble SQL into a reused `[]byte` using clause builders
   (bun-style appenders, dialect-quoted identifiers), then execute through
   the ConnPool (or cached prepared statements).
5. Scanning dispatches to per-field `ScannerFunc`s compiled at table init.

---

## 3. Directory layout

```
fusion-orm/
├── fusion.go            # Config, DB, Open, Session, clone (gorm-style)
├── chainable_api.go     # Model/Where/Select/Order/Preload/Joins/Scopes...
├── finisher_api.go      # Create/Find/First/Save/Update/Delete/Count/Scan/ToSQL
├── statement.go         # Statement + clause registry, byte builder
├── callbacks.go         # processors: create/query/update/delete/row/raw
├── callbacks/           # default impls: create.go, query.go, preload.go,
│                        #   update.go, delete.go, associations.go,
│                        #   transaction.go
├── generics.go          # G[T] type-safe facade with checked chain interfaces
├── soft_delete.go       # DeletedAt field-type clause injection
├── errors.go            # sentinel errors + ErrorTranslator
├── scan.go              # struct/slice/map/scalar scanning (bun model style)
├── schema/              # cached reflection: table.go, field.go, relation.go,
│                        #   naming.go (Namer), tag.go, appender.go, scanner.go
├── clause/              # select, from, where, order, limit, insert, values,
│                        #   set, on_conflict, returning, locking, with, expr
├── dialect/             # dialect.go (interface + features), sqlite/,
│                        #   postgres/
├── logger/              # logger.Interface + slog adapter
├── migrator/            # AutoMigrate (schema diff) + versioned Go/SQL
│                        #   migrations
├── association.go       # db.Association() Append/Replace/Delete/Clear
├── hook.go              # QueryHook interface + model hooks
├── prepared_stmt.go     # LRU + TTL stmt cache (gorm style)
└── tests/               # integration + suite tests
```

---

## 4. Implementation milestones

### M1 — Core plumbing

- `errors.go`: sentinel errors (`ErrRecordNotFound`, `ErrInvalidTransaction`,
  `ErrNotImplemented`, `ErrMissingWhereClause`, `ErrDuplicatedKey`,
  `ErrForeignKeyViolated`, `ErrCheckConstraintViolated`, …).
- `logger/`: `logger.Interface` (`Info/Warn/Error/Trace`), levels,
  `SlowThreshold`, `ParameterizedQueries`, slog adapter.
- `dialect/`: `Dialector` interface (`Initialize`, `DataTypeOf`, `BindVarTo`,
  `QuoteTo`, `ErrorTranslator`, feature flags), `BaseDialect` shared appenders
  (string/bytes/JSON/time, NUL-byte rejection, line-comment injection guard),
  `sqlite/` (pure-Go shim driver), `postgres/` (lib/pq or pgx stdlib mode).
- `fusion.go`: `Config` (`SkipDefaultTransaction`, `NamingStrategy`,
  `Logger`, `NowFunc`, `DryRun`, `PrepareStmt`, `CreateBatchSize`,
  `TranslateError`, `AllowGlobalUpdate`, …), `Open`, `DB` with
  `Error/RowsAffected/Statement/clone`, `Session`, `WithContext`, `Debug`,
  `ToSQL`, `Use(plugin)`.
- Transactions: `Transaction()` with panic-safe rollback; nested tx via
  savepoints; `Begin/Commit/Rollback/SavePoint/RollbackTo`.

### M2 — Schema layer

- Cached per-type parsing (`sync.Map` + initialized-channel dedupe).
- Tag dialect parser for `fusion:"..."` keys (see §1).
- Naming strategy: `Namer` interface (`TableName/ColumnName/JoinTableName/…`),
  `Tabler` interface fallback, pluralized snake_case default.
- Field metadata: `DataType`, `PrimaryKey`, `AutoIncrement`, `Creatable/
  Updatable/Readable`, `AutoCreateTime/AutoUpdateTime`, `Serializer`,
  precompiled `ValueOf/Set/ReflectValueOf` closures.
- Embedded structs with `embed` prefix; ambiguity detection.
- Relations (schema/relation.go): `HasOne/HasMany/BelongsTo/Many2Many`,
  polymorphic, join-table FK naming, constraint generation from tags.
- Hook-presence booleans (`BeforeCreate`, `AfterFind`, …) detected via
  reflection at parse time.

### M3 — Query engine

- `clause/` builders producing `func(b []byte) []byte` appends with dialect
  quoting and bound vars.
- `statement.go`: clause registry keyed by name, `BuildClauses` order,
  `QuoteTo/AddVar`, `Settings` sync.Map.
- `scan.go`: struct (with embedded + joined dotted columns), slice, map,
  scalar scanning via compiled `ScannerFunc`s.
- `chainable_api.go` + `finisher_api.go`: full gorm-style surface, including
  `FindInBatches` (keyset pagination), `FirstOrInit/FirstOrCreate`,
  `Pluck`, `Row/Rows/Scan`.
- `callbacks/` default chains:
  - create: begin_tx → before_create → save_before_associations → insert
    (batch, RETURNING/LastInsertID backfill) → save_after_associations →
    after_create → commit_or_rollback.
  - query: select → preload → after_query.
  - update: setup_reflect_value → before_update → save_before_associations →
    update → save_after_associations → after_update.
  - delete: begin_tx → before_delete → delete_before_associations → delete
    → after_delete → commit_or_rollback.
- Auto timestamps (`CreatedAt/UpdatedAt`), `ErrMissingWhereClause` guard,
  `AllowGlobalUpdate`.

### M4 — Relations

- `Preload(column)` with gorm naming; execution uses bun's strategy:
  one follow-up query per relation, child rows matched via composite `IN`
  (`(a,b) IN ((1,2),(3,4))`) or OR-equations; apply-functions to filter/order
  each relation load; nested/dotted preloads (`Preload("Profile.Posts")`).
- Inline `LEFT JOIN` loading for has-one/belongs-to with `alias__column`
  scanning into mounted structs.
- `db.Association(column)`: `Find/Append/Replace/Delete/Clear/Count/
  Unscoped`, batch operations on slices, FK write-back.
- Polymorphic has-many/has-one; m2m through junction tables;
  `SetupJoinTable`.

### M5 — Extras

- `generics.go`: `G[T](db, opts...)` facade; `Interface[T]`,
  `ChainInterface[T]` (compile-time checked chain), `ExecInterface[T]`
  returning `(T, error)` / `([]T, error)`; `WithResult()`.
- `soft_delete.go`: `DeletedAt` field type injecting Query/Update/Delete
  clauses (DELETE → UPDATE rewrite); `Unscoped` escape hatch; custom zero
  value.
- `prepared_stmt.go`: LRU + TTL `*sql.Stmt` cache keyed by SQL string;
  purges on `driver.ErrBadConn`; tx-aware wrapper.
- Error translation: dialector `ErrorTranslator` maps driver errors to
  `ErrDuplicatedKey` / `ErrForeignKeyViolated` / `ErrCheckConstraintViolated`.
- `migrator/`: `AutoMigrate` (inspect schema → diff → ALTER) for sqlite/pg;
  versioned migrations (Go funcs and `1_name.up.sql`/`down.sql` files) with
  lock table, `WithTableName`, `WithMarkAppliedOnSuccess`.
- `hook.go`: single `QueryHook` interface
  (`BeforeQuery(ctx, *QueryEvent) context.Context` / `AfterQuery`) for
  observability plugins (slog hook included).

### M6 — Tests & docs

- Table-driven unit tests against SQLite (pure-Go) for everything.
- Integration tests against PostgreSQL, skipped when unreachable
  (env-gated, e.g. `FUSION_TEST_PG_DSN`).
- Test suites: relations, soft delete, migrations, batch insert, generics,
  prepared statements, hooks, error translation.
- README with feature comparison table (fusion vs bun vs gorm).

---

## 5. Key architectural decisions

1. **Chaining does not call bun-style query structs directly.** Instead,
   `Statement.Clauses` accumulate, and finishers trigger callback processors
   that compile clauses to `[]byte` using dialect + precompiled appenders.
   Result: bun's zero-alloc build with gorm's extensibility.
2. **Hooks** are runtime-detected (`BeforeCreate(tx) error`-style, gorm
   compat); optional explicit interfaces provided for compile-time safety.
3. **Single error slot** with `AddError` chaining (first error kept as wrap
   head), gated by `TranslateError` for constraint mapping.
4. `DryRun`, `ToSQL`, `FindInBatches`, `FirstOrInit/FirstOrCreate`,
   optimizer/index hints, and named args included from the start.

## 6. Risks

- **Size:** full build is ~6k+ lines. M1–M3 alone yield a usable ORM;
  M4–M5 can land incrementally after that.
- **Driver variance:** PG `RETURNING` vs SQLite `LastInsertID` backfill must
  share one backfill callback; guarded by feature flags.
- **Composite-IN relation loading** differs per dialect; fall back to
  OR-equation batches where unsupported (mirrors bun's `feature.CompositeIn`).
