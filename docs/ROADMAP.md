# fusion-orm Roadmap

Implementation phases for fusion-orm. Complements `DESIGN.md` (architecture,
milestones M1–M6) and `GAP_ANALYSIS.md` (what bun/gorm each contribute).
Phases below are the milestone detail with acceptance criteria.

## Phase 1 — Core plumbing (M1)

1. **Sentinel errors** — `ErrRecordNotFound`, `ErrMissingWhereClause`, `ErrDuplicatedKey`, `ErrForeignKeyViolated`, `ErrCheckConstraintViolated`, `ErrInvalidValue`, `ErrUnsupportedFeature`, `ErrDryRunModeUnsupported`; `AddError` wrap-chain semantics.
2. **Logger** — `logger.Interface` (`Info/Warn/Error/Trace`), levels, `SlowThreshold`, `ParameterizedQueries`, slog adapter, gorm-parity trace format.
3. **Dialect layer** — `Dialector` interface, `feature.Feature` bitflags (~35 flags), `BaseDialect` appenders (string/bytes/JSON/time with NUL-byte rejection + line-comment guard), identifier quoting, `dialect/sqlite`, `dialect/postgres`.
4. **Session layer** — `Config`, `Open`, `DB` (`Error/RowsAffected/Statement`), copy-on-write `getInstance()`, `Session/WithContext/Debug/ToSQL/Use(plugin)`.
5. **Transactions** — panic-safe `Transaction()`, savepoint-based nesting, `Begin/Commit/Rollback/SavePoint/RollbackTo`.

**Acceptance:** dialect unit tests + golden quotes pass; a `*DB` can open a SQLite file, ping, run `Exec` raw, and wrap a transaction. `go vet` clean.

## Phase 2 — Schema reflection (M2)

6. **Cached type parsing** — per-type `*Table` cache, two-phase init (relations resolved after fields), concurrent-safe.
7. **Tag parser** — `fusion:"..."` vocabulary (DESIGN_SYSTEM §4), lenient default + `StrictTags` opt-in, gorm-tag migration table.
8. **Naming strategy** — `Namer` interface, `Tabler` escape hatch, pluralized snake_case default, join-table/FK/index naming.
9. **Field metadata + closures** — data types, `AutoCreateTime/UpdateTime`, serializers, precompiled `Appender/Scanner/IsZero`.
10. **Embedded structs** — anonymous + `embed:` prefix, ambiguity detection.
11. **Relation metadata** — has-one/has-many/belongs-to/m2m, polymorphic, FK/reference pairs, constraint generation from tags.
12. **Hook detection** — runtime method presence booleans + explicit interfaces.

**Acceptance:** a model with tags/embeds/relations parses identically on repeat (cached), closure round-trip tests (append→scan) for stdlib types, alloc-count benchmark established.

## Phase 3 — Query engine (M3)

13. **Clause builders** — `clause/`: select/from/where/order/limit/offset/insert/values/set/on_conflict/returning/locking/with/expr; golden SQL per dialect.
14. **Statement + registry** — clause keys, `BuildClauses` order, `QuoteTo/AddVar`, `Settings`, `DryRun`/`ToSQL`.
15. **Scanning engine** — struct (embedded + `alias__column` joins), slice, map, scalar; `ErrRecordNotFound` semantics for `First/Take/Last`.
16. **Chainable + finisher API** — full gorm-parity surface incl. `FindInBatches`, `FirstOrInit/FirstOrCreate`, `Pluck`, `Row/Rows/Scan`.
17. **Default callbacks** — create/query/update/delete/raw/row chains; batch insert with `RETURNING`/`LastInsertID` backfill; auto timestamps; guard (`ErrMissingWhereClause`); `CreateBatchSize`.
18. **Processor registration API** — `Register/Replace/Remove/Before/After/Match`, topological ordering, conflict errors.

**Acceptance:** CRUD golden tests green on SQLite + PG; batch insert 10k rows benchmarked; guard tests prove no-WHERE protection; `ToSQL` output stable.

## Phase 4 — Relations (M4)

19. **Preload** — batched loading (one query per relation, composite `IN` w/ OR-fallback), apply-functions, nested/dotted paths.
20. **Inline joins** — `LEFT JOIN` for has-one/belongs-to, dotted-column scanning into mounted structs.
21. **Association API** — `Find/Append/Replace/Delete/Clear/Count`, batch ops, FK write-back.
22. **Polymorphic + m2m** — junction-table loading, `SetupJoinTable`, join conditions.
23. **Association saving in callbacks** — `save_before/after_associations` chains with `FullSaveAssociations` opt-in.

**Acceptance:** relation test suite (all four types + polymorphic) green on SQLite + PG; no N+1 verified by query-count assertions.

## Phase 5 — Extras (M5)

24. **Generics facade** — `G[T]`, checked chain interfaces, `WithResult`, shared golden tests with reflection API.
25. **Soft delete** — `DeletedAt` clause injection (DELETE→UPDATE rewrite), `Unscoped`, custom zero value.
26. **Prepared statements** — LRU+TTL cache, `ErrBadConn` purge, tx-aware wrapper.
27. **Error translation** — dialector constraint mapping, `TranslateError` mode.
28. **Migrations** — `AutoMigrate` (inspect→diff→ALTER) for sqlite/pg; versioned Go/SQL migrations with lock table.
29. **Observability hook** — `QueryHook` interface + slog hook; `QueryEvent` fields documented.

**Acceptance:** generics parity tests, soft-delete round-trip, migration up/down + diff tests on both dialects.

## Phase 6 — Hardening & docs (M6)

30. **Test depth** — env-gated PG integration suite, error-path coverage, race detector run, alloc benchmarks in CI.
31. **README + examples** — comparison table (fusion vs bun vs gorm), `example_test.go` featured flows, CHANGELOG scaffold.
32. **Lint/CI** — `golangci-lint` config, `go vet` in CI, module path finalized.

**Acceptance:** CI green; docs complete; v0.1 tag.

## Backend/quality backlog (cross-cutting)

- Feature-flag audit: every dialect branch must map to a named flag (no string sniffing).
- pgx/v5 stdlib quirks: `RETURNING` + batch exec verification against real PG.
- modernc.org/sqlite edge cases: multiple statements per exec, busy_timeout handling.
- Decide `Scopes` vs `Apply` naming before Phase 3 lands (PRD open question).
- Consider `unsafe` zero-copy helpers only if benchmarks demand them (deferred).

## Recommended sequencing

1. **Phases 1–2** in one push — foundation everything hangs on (cheap wins analog).
2. **Phase 3** — the usable-ORM milestone; ship to internal use after this.
3. **Phase 4** — relations; highest complexity, needs stable Phase 3.
4. **Phase 5** — extras as filler work.
5. **Phase 6** — hardening gate before v0.1.
