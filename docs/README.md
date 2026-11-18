# fusion-orm

A Go ORM that pairs **bun's engine** with **gorm's experience** — zero-alloc query building, precompiled field scanners, and dialect feature flags underneath; fluent chaining, a callback/plugin pipeline, generics facade, and associations on top.

Studied references: [bun](https://bun.uptrace.dev) (v1.3.0, `../bun-orm/`) and [gorm](https://gorm.io) (core, `../gorm-orm/`). This module is original code informed by both.

## Why

- **bun** is fast but manual: explicit query structs, DIY relations/conveniences.
- **gorm** is ergonomic but slower: reflection-heavy hot paths, weaker dialect modeling.
- **fusion-orm**: gorm-style one-liners (`db.Model(&user).Where(...).Preload("Posts").Find(&users)`) backed by bun-class performance — no per-row reflection, `[]byte` SQL assembly.

## Features

- **Session layer** — copy-on-write `*DB` chaining, `Session/WithContext/Debug/DryRun/ToSQL`, panic-safe transactions with savepoint nesting.
- **Schema layer** — cached per-type reflection, gorm-like `fusion:"..."` tags, naming strategy, embedded structs, precompiled `Appender/Scanner` closures.
- **Query engine** — chainable + finisher API (gorm-name parity), CRUD as ordered callback processors (register/replace/remove), batch inserts with `RETURNING`/`LastInsertID` backfill, `ErrMissingWhereClause` guard.
- **Relations** — batched `Preload` (no N+1), inline joins for has-one/belongs-to, `Association` API, polymorphic + m2m.
- **Extras** — generics `G[T]` facade, soft delete via `DeletedAt` clause injection, LRU prepared-stmt cache, error translation, `AutoMigrate` + versioned migrations, `QueryHook` observability seam.

## Supported Databases

| Engine     | Package             | Notes                              |
| ---------- | ------------------- | ---------------------------------- |
| SQLite     | `dialect/sqlite`    | `modernc.org/sqlite` (pure Go)     |
| PostgreSQL | `dialect/postgres`  | `jackc/pgx/v5` via stdlib adapter  |

MySQL/MSSQL are designed-for (feature-flag system) but not implemented in v1.

## Status

**Design phase — no code yet.** Milestones M1–M6 planned; see the docs.

## Documentation

| Doc | Purpose |
| --- | ------- |
| [`docs/PRD.md`](docs/PRD.md) | Product requirements, goals, non-goals, success metrics |
| [`docs/DESIGN.md`](docs/DESIGN.md) | Approved design: take-what-from-where matrix, architecture, milestones |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Detailed technical architecture, boundaries, data flow, key decisions |
| [`docs/DESIGN_SYSTEM.md`](docs/DESIGN_SYSTEM.md) | API design system: naming, tag vocabulary, chaining/callback/error contracts |
| [`docs/GAP_ANALYSIS.md`](docs/GAP_ANALYSIS.md) | Gap analysis: bun + GORM vs fusion-orm as it stands today |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Phase plan with acceptance criteria |
| [`docs/AGENTS.md`](docs/AGENTS.md) | Guidelines for AI coding agents working on this repo |

## Commands (once M1 lands)

```bash
go test ./...                  # full suite (golden SQL + guard + round-trip tests)
go test -bench . -benchmem ./...   # perf gates (alloc-count benchmarks)
go vet ./...                   # static analysis before finishing
golangci-lint run              # lint (from Phase 6)
```

## Development Workflow

1. Work milestone phases in order (`docs/ROADMAP.md`); Phase 1–2 first.
2. Golden-SQL tests must stay green for both dialects in every PR.
3. Before finishing: `go test ./... && go vet ./...`.
4. Agents: read `docs/AGENTS.md` before writing code.

## License

Not yet publicly licensed. All rights reserved.
