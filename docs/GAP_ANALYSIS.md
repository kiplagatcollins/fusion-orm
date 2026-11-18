# Gap Analysis — bun (reference) + GORM vs. fusion-orm

As it stands today (design only — zero code). Status verified against
`../bun-orm/` (bun v1.3.0) and `../gorm-orm/` (gorm core).
Cross-reference: [`DESIGN.md`](DESIGN.md) for architecture and milestones.

## What we already have

- **Design doc** — architecture overview, take-what-from-where matrix, query flow, directory layout, M1–M6 milestones, key decisions, risks.
- **Two fully mapped reference codebases** — bun's schema/dialect/callback internals and gorm's callback pipeline/clause system documented (see design doc §1–2).

## Missing — cheap wins (M1–M2: core plumbing + schema)

1. **Sentinel errors + `ErrorTranslator`** — `ErrRecordNotFound`, `ErrMissingWhereClause`, `ErrDuplicatedKey`, …; dialector translates driver errors.
2. **Logger** — `logger.Interface` (`Info/Warn/Error/Trace`), `SlowThreshold`, `ParameterizedQueries`, slog adapter.
3. **Dialect layer** — `Dialector` interface + feature bitflags (bun's `feature.Feature` model), shared `BaseDialect` appenders (string/JSON/time, NUL-byte rejection, line-comment injection guard), SQLite + Postgres.
4. **`Config` / `Open` / `DB` / `Session`** — copy-on-write chaining, `getInstance()` clone semantics, `WithContext`, `Debug`, `DryRun`, `ToSQL`, `Use(plugin)`.
5. **Transactions** — panic-safe `Transaction()`, nested via savepoints, `Begin/Commit/Rollback/SavePoint/RollbackTo`.
6. **Schema reflection** — cached per-type parsing, tag parser for `fusion:"..."` (gorm-like keys), naming strategy (`Namer` + `Tabler`), precompiled per-field appenders/scanners (bun's perf trick), hook-presence detection, relation metadata.

## Missing — medium effort (M3–M4: query engine + relations)

7. **Clause builders + `Statement`** — clause registry keyed by name; each clause compiles to `[]byte` via dialect + precompiled appenders.
8. **Scanning engine** — struct/slice/map/scalar scanning through compiled `ScannerFunc`s (bun `model.go` style).
9. **Chainable + finisher API** — gorm-style surface: `Model/Where/Order/Preload/Joins/Scopes` → `Create/Find/First/Save/Update/Delete/Count/Scan`; `FindInBatches`, `FirstOrInit/FirstOrCreate`, `Pluck`.
10. **Callback pipeline** — processors for create/query/update/delete/row/raw with register/replace/remove + `before/after` ordering (gorm's extension spine), default CRUD chains, batch insert with RETURNING/LastInsertID backfill, auto timestamps.
11. **Relations & Preload** — `Preload` naming (gorm) over bun's batched loading (one query per relation, composite `IN`); inline `LEFT JOIN` for has-one/belongs-to with dotted-column scanning; `Association` API; polymorphic + m2m; `SetupJoinTable`.

## Missing — large / later (M5–M6)

12. **Generics facade** — `G[T]`, compile-time-checked chains, `WithResult`.
13. **Soft delete** — `DeletedAt` field-type clause injection (DELETE → UPDATE rewrite), `Unscoped`.
14. **Prepared-statement cache** — LRU + TTL keyed by SQL, purge on `ErrBadConn`, tx-aware.
15. **Migrations** — `AutoMigrate` (inspect → diff → ALTER) for sqlite/pg + versioned Go/SQL migrations with lock table.
16. **Observability hooks** — single `QueryHook` interface; slog hook included.
17. **Test suites** — table-driven sqlite tests + env-gated PG integration; relation/soft-delete/migration/batch/generics coverage.
18. **README + comparison table** (fusion vs bun vs gorm).

## Reference specifics neither world gives us both ways

- **bun has it, gorm doesn't:** feature-flag dialect system; dbfixture (YAML fixtures); read/write replica resolver (`bunexp`); pgdriver extras (COPY FROM, LISTEN/NOTIFY); bundled custom wire-protocol drivers.
- **gorm has it, bun doesn't:** `FirstOrInit/FirstOrCreate`, `Scopes`, `Association` API; LRU prepared-stmt cache; error translation mode; `Gen` codegen (out of scope for now).
- **Both, differently (fusion must pick):** paging semantics — bun keeps explicit `Limit/Offset`, gorm wraps via clauses; fusion resolves this by keeping explicit paging on the chain API.
- **Consciously skipped:** bun's `unsafe` zero-copy helpers (only if benchmarks demand them), gorm's separate driver modules (we bundle two dialects), plugin ecosystems (via `QueryHook`/`Plugin` seams instead).

## Known quality gaps (from references, don't inherit)

- bun v1.3.0 breaking changes (`int64` Limit/Offset, NUL-byte string rejection) — design around these from day one.
- gorm's silent-rewrite behaviors (guard-style `ErrMissingWhereClause` we keep; gorm's implicit `Save` upsert semantics we make explicit).

## Recommendation

Do **M1–M2 (cheap wins) as one stage** — errors/logger/dialects/session + schema reflection give the foundation everything hangs on. Then **M3 (query engine)** is the true usable-ORM milestone; M4 (relations) next, M5 extras as filler. Matches the original plan: core first, DX second, extras last.
