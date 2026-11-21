# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
