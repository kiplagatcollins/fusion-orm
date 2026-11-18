# fusion-orm — Product Requirements Document

Define what you are building, who it is for, and how you will know it works.

## 1. Product Overview

- **Product name:** fusion-orm
- **Tagline:** bun's engine, gorm's experience — a Go ORM that is both.
- **Description:** fusion-orm is a Go ORM library that pairs bun's high-performance, SQL-first query engine (zero-alloc builders, precompiled appenders/scanners, dialect feature flags) with gorm's developer experience (fluent chaining, callback/plugin pipeline, generics facade, associations and preload). Studied references: `../bun-orm/` (bun v1.3.0) and `../gorm-orm/` (gorm core).

## 2. Problem

Go developers currently choose between two ORMs whose strengths are complementary but never combined:

- **bun** is fast and SQL-explicit but demands verbose, manual query composition; relations, associations, and conveniences are DIY.
- **gorm** is ergonomic and feature-rich but has slower row scanning/build paths, weaker dialect modeling, and extension seams that require understanding its callback pipeline.

Teams pick one and compromise: gorm users accept the performance tax; bun users hand-roll the conveniences. No mainstream Go ORM offers both the speed core and the DX surface.

## 3. Goals

- **Primary outcome:** gorm-style one-liner workflows (`db.Model(&user).Where(...).Preload("Posts").Find(&users)`) backed by bun-class performance — zero-alloc query assembly and no per-row reflection.
- **Secondary outcome:** one dialect layer (feature bitflags) so adding a database means declaring flags + a few appends, not forking query code.
- **Constraint that matters:** correctness and safety first — parameterized queries only, identifier quoting via dialect helpers, no silent rewrites of user intent (`ErrMissingWhereClause` guard, explicit `Save` semantics).

## 4. Target Users

- **Primary user:** backend developers building services on PostgreSQL/SQLite who want gorm-level ergonomics without the performance tax.
- **Secondary user:** teams migrating off gorm (tag-compatible-ish model definitions, familiar chaining) or off raw SQL/driver code (escape hatches everywhere).
- **Optional buyer:** performance-sensitive teams currently writing hand-tuned `database/sql` code who want typed models with a thin runtime.

## 5. Core Features

### Session & Connection Layer

- `Open(dialector, cfg)` with `Config`: logger, naming strategy, `DryRun`, `PrepareStmt` (LRU+TTL cache), `TranslateError`, `CreateBatchSize`, `SkipDefaultTransaction`, `AllowGlobalUpdate`.
- Copy-on-write chaining: `*DB` is safe to share; `Session`/`WithContext`/`Debug` produce safe derivatives.
- Transactions with panic-safe rollback, nested transactions via savepoints, explicit `Begin/Commit/Rollback/SavePoint/RollbackTo`.

### Model & Schema Layer

- Cached reflection: tables, fields, relations parsed once per type; two-phase init for recursive structs.
- `fusion:"..."` tags with gorm-like keys: `column,type,pk,autoincrement,notnull,default,index,unique,rel,foreignKey,many2many,polymorphic,embed,->,<-,-`.
- Naming strategy (`Namer` interface + `Tabler` escape hatch), pluralized snake_case default; embedded structs with prefixes.
- Precompiled per-field `Appender/Scanner/IsZero` closures — the perf core.

### Query Engine (callback pipeline)

- Chainable API (`Model, Select, Where, Order, Limit, Joins, Preload, Scopes, Clauses`) + finisher API (`Create, Find, First, Save, Update, Delete, Count, Pluck, Scan, Row/Rows, ToSQL`).
- CRUD as ordered callback processors (`create/query/update/delete/row/raw`) with register/replace/remove and `before/after` ordering — the extension spine.
- Clause registry (`clause/`) compiling to `[]byte` through dialect appenders; feature-flag branching per dialect.
- Batch inserts with `RETURNING`/`LastInsertID` backfill; auto timestamps; `ErrMissingWhereClause` guard.

### Relations

- `Preload` (batched loading: one query per relation, composite `IN`), inline `LEFT JOIN` for has-one/belongs-to with dotted-column scanning, apply-functions to shape relation loads.
- `Association` API (`Find/Append/Replace/Delete/Clear/Count`); polymorphic has-one/has-many; m2m via junction tables; `SetupJoinTable`.

### Developer Experience

- Generics facade `G[T](db)` with compile-time-checked chains returning `(T, error)`.
- Soft delete via `DeletedAt` field-type clause injection + `Unscoped`.
- Migrations: `AutoMigrate` (inspect → diff → ALTER) for SQLite/PG + versioned Go/SQL migrations.
- Single `QueryHook` observability seam; slog logger adapter.

## 6. Non-Goals (v1)

- Additional dialects beyond SQLite + PostgreSQL (MySQL/MSSQL designed-for but not implemented).
- Code generation (`gen`-style struct generation from live schemas).
- Read/write replica routing, YAML fixtures, OpenTelemetry plugin (seams provided, plugins later).
- ORM-managed optimistic locking, sharding, multi-schema tenancy.
- Cross-ORM compatibility guarantees (gorm or bun model structs port by editing tags, not zero-diff).

## 7. Constraints

- **Technical:** Go 1.22+; `database/sql` as the canonical execution interface; SQLite via `modernc.org/sqlite` (pure Go, CGO-free) and PostgreSQL via `jackc/pgx/v5` stdlib adapter — matching DataPulse's driver choices for code reuse.
- **Safety:** parameterized queries exclusively; identifiers quoted through dialect helpers; NUL-byte string rejection and line-comment injection guard carried over from bun's fixes (CVE-2024-44906 class).
- **Compatibility:** no behavior-break after v1.0 without a major version; feature flags gate dialect differences, never runtime string-sniffing.

## 8. Success Metrics

- gorm-parity one-liners: every gorm chainable/finisher example in our docs runs with the same call shape (**proposed**).
- Performance: within 1.5× of bun on the standard benchmarks (batch insert 10k rows, scan 100k rows) — vs gorm's current gap (**proposed**).
- Zero per-row reflection confirmed by benchmark/alloc tests (**proposed**).
- 100% of no-WHERE `UPDATE`/`DELETE` blocked without explicit `AllowGlobalUpdate` (unit-tested) (**proposed**).

## 9. Current Status

- **Built:** design set (DESIGN.md, GAP_ANALYSIS.md, this doc suite); **M1 core plumbing** (sentinel errors + AddError wrap-chain, `logger/` with slog adapter, `dialect/` with 35 feature flags + golden quoting + SQLite/PG dialects, session/clone layer, panic-safe transactions with savepoints); **M2 schema layer** (cached per-type `*Table` with concurrent dedupe + two-phase relation init, `fusion:"..."` tag parser with lenient/strict modes + gorm migration aliases, `Namer`/`Tabler` with pluralized snake_case via `jinzhu/inflection`, field metadata with precompiled zero-alloc Appender/Scanner/IsZero closures, embedded structs with prefix + ambiguity detection, four relation kinds with FK/ polymorphic/m2m metadata + constraint tags, hook-presence booleans + explicit root hook interfaces).
- **In progress:** Milestone M3 — query engine (`clause/` builders, callback pipeline, finishers, row scanning).
- **Known limitations:** no query engine yet (M3): `Exec` is the only raw statement; `ToSQL` renders raw SQL only. `PrepareStmt` and `CreateBatchSize` config fields arrive in M5/M3.

## 10. Open Questions

- ~~Module path~~ — **resolved:** `github.com/kiplagatcollins/fusion-orm` (package `fusion`), set at M1 scaffolding.
- Should `PrepareStmt` be on by default for SQLite, where it shows the largest win? (decide in M5)
- Tag parser: strict (unknown keys error) or lenient (warn via logger)? (design says lenient default + `StrictTags`; decide in M2)
- Do we expose gorm's `Scopes` under the same name or a clearer one (`Apply`)? (decide before Phase 3)
