# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-11-21

### Added

- `AutoMigrate` creates many-to-many junction tables as two-column,
  composite-primary-key tables (GORM #7365/#7006 class).
- `clause.OnConflict.UpdateAll` upserts every non-primary-key column with the
  primary key as the conflict target.
- Audit of the 450-issue gorm backlog: `docs/AUDIT.md` (classes A–Q, verdicts
  with `file:line` evidence) plus a promoted 18-test regression suite and the
  fix-phase design record
  (`docs/superpowers/specs/2026-11-21-audit-fixes-design.md`).

### Changed

- Explicit `index:`/`unique:` tag values render as `<name>_<table>` in
  `CREATE INDEX`, so two tables can declare the same index name; generated
  default names are unchanged (GORM #7346 class).
- `Save` on a slice dispatches element-wise by primary key (insert new rows,
  update rows with a set PK) instead of inserting everything (GORM #7540).
- Creating an association child whose primary key is set updates the existing
  row instead of inserting a duplicate (GORM #6119).
- `Row()` on a build error returns a failing sentinel row instead of `nil`
  (which panicked in `Scan`); the build error surfaces on the chain node's
  `Error` (GORM #7575).
- `OnConflict` renders a targetless `ON CONFLICT` without parentheses and
  never emits `ON CONFLICT ()` or a dangling `DO UPDATE SET`.

### Fixed

- Transaction finalizers (commit/rollback) run after errors and panics, so
  implicit transactions are always released (GORM #7897/#7908/#7459).
- The `ErrMissingWhereClause` guard now runs before soft-delete rewriting —
  global soft deletes error instead of updating every row (GORM #6338);
  the soft-delete filter is ANDed across OR groups (GORM #6998/#5652) and
  filters inline relation joins (GORM #7385).
- `clause.Or` renders `OR` with correct precedence/parenthesization; map
  conditions produce stable, key-sorted SQL (GORM #5652/#7351).
- `Count` strips `ORDER BY` before executing (GORM #7516).
- Many-to-many append skips already-linked pairs (GORM #5579).
- Prepared-statement cache: statements handed to callers are refcounted and
  can no longer be closed under them by concurrent eviction (GORM #7831);
  `preparedPool` forwards `Ping` (GORM #6181); `AutoMigrate` invalidates
  cached statements after DDL (GORM #7255).
- Errors raised inside `Scopes` propagate to the finisher chain
  (GORM #6283).
- Composite-primary-key tables no longer emit two `PRIMARY KEY` clauses in
  `CREATE TABLE`.

## [0.2.0] - 2026-11-21

### Added

- gorm-parity base models: `fusion.Model` (autoincrement `uint64` ID) and
  `fusion.UUIDModel` (UUIDv7 ID generated at create time), both with
  `CreatedAt`/`UpdatedAt` and soft-delete `DeletedAt`.
- `type:uuid` column type: native `uuid` on PostgreSQL, `TEXT` on SQLite.

## [0.1.1] - 2026-11-21

### Fixed

- `AutoMigrate` now preserves non-constant column defaults (e.g.
  `fusion:"default:CURRENT_TIMESTAMP"`) when adding columns to existing tables
  on dialects that reject them in `ALTER TABLE ADD COLUMN` (SQLite), by
  rebuilding the table with the full desired schema and copying rows across.

## [0.1.0] - 2026-11-18

### Added

- Core session layer, dialect framework, and cached schema reflection.
- Clause builders, statement registry, scanning engine, and CRUD callbacks.
- Batched `Preload`, inline `Joins`, association API, and association callbacks.
- `G[T]` generics facade, soft delete, prepared statements, error translation,
  migrations, and query hooks.
- README, runnable examples, error-path tests, and CI with vet/lint/race/bench.

### Changed

- Dialect-specific behavior is selected through named feature flags; no
  dialect-name string comparisons remain in library code.
