# fusion-orm — Agent Instructions

Guidelines for AI coding agents working on this project.

## Before You Start

1. Read `DESIGN.md` for the approved design (take-what-from-where matrix, milestones).
2. Read `PRD.md` for product goals and non-goals (SQLite + PG scope is firm).
3. Read `DESIGN_SYSTEM.md` for API rules (naming parity, tag vocabulary, chaining contract).
4. Read `ARCHITECTURE.md` for structure and boundaries (core packages never import each other's internals).
5. Reference implementations live at `../bun-orm/` and `../gorm-orm/` — read them for proven patterns, but this module is original code, not a fork.
6. Inspect the folder structure and existing packages before creating files.

## Tech Stack

- **Language:** Go 1.22+ (generics required)
- **Execution:** `database/sql` `ConnPool` interface
- **Drivers:** `github.com/jackc/pgx/v5` (stdlib adapter), `modernc.org/sqlite` (pure Go)
- **Naming:** `jinzhu/inflection` or `gobuffalo/flect` for pluralization
- **Test runner:** Go standard library, table-driven tests

## Commands

```bash
go test ./...                          # full suite
go test ./clause/... -run TestGolden   # golden SQL per dialect
go test -bench . -benchmem ./...       # perf gates (alloc tests are normative)
go vet ./...                           # static analysis (run before finishing)
golangci-lint run                      # lint (once configured)
go build ./...                         # compile sanity
```

## General Rules

- Make small, verifiable changes. Read before editing.
- Reuse existing patterns before adding new ones; no stub packages, no duplicate logic.
- Do not add dependencies unless the current stack cannot solve the problem; ask before adding any.
- Match milestone order (DESIGN.md M1→M6); do not jump ahead to relations (M4) before the query engine (M3) compiles and tests green.
- Keep PRs within one milestone phase; the clause golden tests must stay green in both dialects.

## Code Guidelines (Go)

- Accept `context.Context` first on anything doing I/O; propagate, never `context.Background()` inside the library.
- Wrap errors with `%w` and action-oriented context ("create users: …"); branchable conditions are sentinels in `errors.go`.
- Never panic in library code; return errors (panics only for duplicate registration at init).
- Precompiled closures over reflection in hot paths: field `Appender/Scanner/IsZero` resolved once at table init (`schema/`); per-row reflection is a bug, not a style choice.
- `[]byte` builders with reused buffers for SQL assembly; zero per-query allocations is the goal (alloc tests enforce it).
- Table-driven tests beside the code they cover; exported symbols have doc comments.

## Boundaries

- `dialect/` — `Dialector` interface, feature bitflags, quotes, type mapping; no schema imports beyond field metadata; the only package with dialect-specific strings.
- `schema/` — cached reflection, tags, naming, closures; must not import DB/statement/callback types.
- `clause/` — pure byte-append builders; no I/O, no schema imports.
- `callbacks/` — the only package that knows CRUD order; callbacks are `func(*DB)`.
- `statement.go`/`fusion.go` — session/config/clone, clause registry, single error slot.
- Do not cross layers: no SQL strings outside `dialect/` + `clause/`; no chain state stored on shared `Config`.

## Testing

- Add or update tests for every behavior change; cover error paths, not only happy paths.
- Golden-SQL tests: every clause renders identically per dialect across runs — a failing golden test is a design change, discuss first.
- Guard tests must prove: every no-WHERE `Update`/`Delete` errors with `ErrMissingWhereClause` unless `AllowGlobalUpdate` is set.
- Integration tests run against real SQLite temp files (no daemons); PG paths are env-gated via `FUSION_TEST_PG_DSN` and skip cleanly when unset.
- Allocation-count benchmarks for scan/build hot paths; regressions block the milestone.
- Before finishing, run: `go test ./... && go vet ./...`.

## Security

- Parameterized queries only; identifiers quoted through the dialect's `QuoteTo`, never string-concatenated.
- Inherit bun's hardening by default: reject NUL bytes in strings, guard line-comment injection in rendered SQL (CVE-2024-44906 class), escape JSON appends.
- Never log DSNs with credentials or user data values; log placeholders, not bound vars, unless `ParameterizedQueries` is explicitly enabled.
- The `ErrMissingWhereClause` guard is a product requirement: never weaken it to make a test pass; extend tests and guard together.
- Validate tags and settings crossing parse boundaries (strict mode opt-in); unknown tag keys warn, never silently ignore in strict mode.

## Do Not Edit

- `internal/` generated LRU/flag helpers may be hand-written but frozen once reviewed
- Reference repos `../bun-orm/`, `../gorm-orm/` — read-only
- Lockfiles (`go.sum`) unless dependencies change
- `docs/DESIGN.md` milestone numbering — propose changes, don't renumber silently

## Definition of Done

- Behavior implemented and covered by tests; `go test ./...` and `go vet ./...` pass.
- Golden SQL green for both dialects; alloc benchmarks no worse than before.
- API surface matches `DESIGN_SYSTEM.md` (naming parity, sentinels, doc comments).
- No new warnings, dead code, or unrelated edits.
- Docs updated when behavior, structure, or the milestone plan changes.

## Review Checklist

- [ ] Correctness and error handling (context propagated, errors wrapped as sentinels)
- [ ] Layer boundaries respected (no dialect strings outside `dialect/`)
- [ ] No per-row reflection; no per-query avoidable allocations
- [ ] Guard not weakened; golden tests cover the change
- [ ] Formatting, vet, and lint pass
- [ ] No unrelated changes

## Need Help?

- Check `ARCHITECTURE.md` §12 (extension points) before proposing new packages.
- Proven patterns: bun's `schema/field.go` (precompiled closures), `dialect/feature/` (bitflags), `query_base.go` (builder plumbing); gorm's `callbacks.go` (processor design), `statement.go` (clause registry).
- Ask before large architectural changes or new dependencies.
