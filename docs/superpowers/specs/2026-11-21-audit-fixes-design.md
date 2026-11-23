# Design: gorm-backlog audit fixes (branch `audit-fixes`)

Date: 2026-11-21
Status: Shipped — fix phase complete, all verification green

## Goal

Fix every `AFFECTED` verdict from [`docs/AUDIT.md`](../../AUDIT.md) (classes
A–Q, P0 → P1 → P2), promote each audit repro test into the default suite, and
record the shipped decisions, known limitations, and the approved
dialect-robustness backlog for the next release.

## Scope shipped

11 classes fixed on `audit-fixes` (`9f5c590..bfc94d0`, 15 fix commits);
18 regression tests promoted to
[`audit_fixes_test.go`](../../../audit_fixes_test.go); the audit-tagged
`audit_repro_test.go` deleted.

| Priority | Classes | Commits |
|---|---|---|
| P0 | A (tx finalizers), H (guard order) | `9f5c590`, `acb8795` |
| P1 | F (OR/map), H (joins), G, I, K, M | `271310a`, `7f0c540`, `fa46d98`, `d0a37f2`, `cf2ab08`, `04aa30b`, `eb9ee5d`, `43d7696` |
| P2 | B, C, E, J | `0a7afe3`, `8d275f9`/`e6ab3ad`, `985433f`, `bfc94d0` |

## Shipped design decisions

### A — transaction finalizers

- `callbackEntry.finalizer` marks exactly one entry (`fusion:commit_or_rollback`,
  set in `newCallbackManager`) as the commit/rollback finalizer.
- `Execute` wraps the callback chain in a deferred `recover`: on panic or error
  it records `db.Error` (first error wins), runs finalizers, then re-panics —
  finalizers run on success, error, and panic paths alike; the panic still
  reaches the caller.

### F/H — condition building and soft delete

- `clause.Condition` gained `Conds []Condition` (nested group rendering
  `(...)`), joined by `Join` (default AND).
- Shared `buildConditions` in `clause/query.go` parenthesizes any multi-expr
  condition when it is not first, so `a OR b AND c` groups correctly on every
  dialect without relying on precedence.
- `injectSoftDeleteFilter` groups pre-existing conditions under a nested group
  when any of them joins with OR — the soft-delete predicate is ANDed with the
  *whole* group. Multi-line raw SQL expressions are not detected (limitation
  below); known shapes (map, grouped, clause-built) are.
- `deleteRows` runs the `ErrMissingWhereClause` guard *before* soft-delete
  rewriting, so global soft deletes error instead of updating everything.
- Relation joins append the soft-delete predicate to the join `ON` clause with
  the join alias (skipped under `Unscoped()`).
- Map conditions sort keys before rendering — stable SQL across runs.

### G — Count

`Count` removes the `order` clause before executing (dialect-independent:
avoids both PG's aggregate/ORDER BY restriction and SQLite's windowed-count
ordering errors).

### I/K — Save and associations

- `Save` on a slice dispatches element-wise: zero PK → `Create`, set PK →
  `Where(pk).Updates`; stops at the first error. Single values unchanged.
- `createRecord` updates instead of inserting when the child's PK is set:
  full-save → `Save`, otherwise `Updates` scoped by PK — no duplicate rows on
  re-save.
- M2M append pre-checks the junction (`SELECT COUNT(*)` with the dialect's
  bind vars) before inserting a link row.
- `AutoMigrate` now creates M2M junction tables (fresh and existing model
  paths) as two-column, composite-primary-key tables typed after the related
  tables' PKs. Junctions with a side lacking a PK are skipped (query-time
  error surfaces instead). `columnDefinition` inlines `PRIMARY KEY` only for
  single-column PKs (and autoincrement) — composite PKs are table-level, which
  also fixes a latent dual-`PRIMARY KEY` bug for any composite-PK model.

### M — index names

- Default index/unique names were already table-scoped (`idx_table_col`) and
  are unchanged (no migration churn).
- Explicit `index:`/`unique:` tag values now render as
  `<name>_<table>` (`Field.IndexSQLName`/`UniqueSQLName`) so two tables can
  declare the same index name without `CREATE INDEX` collisions
  (GORM #7346 class). The display name (`IndexName`) keeps the raw tag value.

### B — prepared-statement cache

- `stmtCache.get` returns `(*sql.Stmt, release func(), err error)`; every
  call site defers `release`. Eviction/TTL/`remove`/`close` *retire* an entry
  immediately but only `Close` the statement when the last holder releases
  (refcount under the cache mutex). A statement handed to a caller can never
  be closed under it (GORM #7831 class).
- Safe with in-flight rows because `database/sql` accounts for them:
  `Stmt.Close` → `removeDep` → `finalClose` only runs once every `Rows`
  dependency closes (verified against the Go source, `database/sql/sql.go`).
- `preparedPool.Ping()` forwards to the wrapped pool — Open's automatic ping
  runs under `PrepareStmt` (GORM #6181).
- `InvalidatePrepared()` on pool and tx drops the cache (same retire
  semantics); `AutoMigrate` invokes it via `migrator.WithCacheInvalidator`
  after DDL, wired from `DB.Migrator()` to every reachable cache (GORM #7255).

### C/E/J — row, scopes, upsert

- `Row()` never returns nil: on build error it returns a row over
  `SELECT 1 FROM "__fusion_row_build_error"` so `Scan` fails with a
  deterministic error instead of panicking (GORM #7575). A `*sql.Row` cannot
  carry a custom error, so the build error is also written to the receiver's
  `Error` (`m := db.Model(x); row := m.Row(); m.Error`).
- `applyScopes` carries a scope's error back onto the chain instance
  (first error wins) — scope failures reach the finisher (GORM #6283).
- `OnConflict.Build` never renders `ON CONFLICT ()` or a dangling
  `DO UPDATE SET`: an empty target renders no parentheses; nothing-to-do
  renders nothing. `Statement.normalizeOnConflict` (runs in `BuildClauses`)
  expands `UpdateAll` to non-PK columns and falls back to the PK as conflict
  target for DO UPDATE — the model resolves from `Statement.Model` or
  `Statement.Dest` (Create sets only Dest).

## Test strategy

- Each fix promoted its audit repro verbatim (plus helpers/types) into
  `audit_fixes_test.go`, default suite, real temp-file SQLite.
- Extra coverage: runtime upsert validated by the SQLite engine (manual
  columns and `UpdateAll`), prepared-cache invalidation after AutoMigrate,
  `Row()` error surfacing on the chain node.
- Gate: `gofmt -l .` clean, `go vet ./...` and `go vet -tags=audit ./...`
  clean, `golangci-lint run` 0 issues, `go test -count=1 ./...` green,
  `go test -race ./...` green, 18/18 `TestAudit*` pass.

## Known limitations

1. **Raw multi-line expressions** in WHERE conditions are not auto-grouped —
   parenthesization applies to known shapes (multiple conds, map conditions,
   nested `Condition.Conds`). Authors of raw multi-line SQL must add their own
   parens.
2. **Pre-existing explicit index names**: databases created before this fix
   keep their old `email_idx`-style index; migration adds `email_idx_<table>`
   alongside it. Old duplicates are not dropped (dropping user-named indexes
   is out of scope).
3. **Composite-PK caveats** from the audit remained for unrelated paths
   (classes D/Q: FindInBatches, metadata resolution) — only the junction
   migration and the dual-inline-`PRIMARY KEY` DDL bug were fixed here.
   *(Resolved 2026-11-23 by the composite-PK correctness pass — see
   `2026-11-23-composite-pk-design.md`.)*
4. **`Save` on slices** stops at the first error; no batched/upsert variant.
5. **`OnConflict{}` with nothing set** (no `DoNothing`, no columns, no
   `UpdateAll`) renders no clause — plain INSERT semantics, duplicate-key
   errors surface. `UpdateAll` without a resolvable model also renders no
   clause.
6. **Cache invalidation** runs after `AutoMigrate` only; direct
   `CreateTable`/`DropTable` calls do not invalidate (add
   `WithCacheInvalidator` call sites if this bites).
7. **`Row()` sentinel Scan error** reads `no such table:
   __fusion_row_build_error` — the root cause lives on the chain node's
   `Error`.

## Dialect-robustness backlog (approved, next release)

Not defects in the audit, but the approved robustness work folded into the
next-release spec:

1. **Multi-statement script executor** — an `ExecScript`-style API that runs a
   multi-statement SQL script: correct statement splitting per dialect (not
   naive `;` splitting — dollar-quoted strings/comments in PG, `BEGIN…END`
   trigger bodies in SQLite), executed inside one transaction, per-statement
   errors reported with statement index and SQL prefix.
2. **`busy_timeout` + retry** — set SQLite `PRAGMA busy_timeout` at open
   (configurable, sane default), and retry `SQLITE_BUSY`/`SQLITE_LOCKED`
   statements with bounded exponential backoff (attempt cap + jitter) inside
   the driver-facing layer so callers never see a transient busy error under
   a reasonable timeout.
3. **`ErrBusy` sentinel** — wrap busy/locked driver errors with a branchable
   `errors.Is` sentinel (`ErrBusy` in `errors.go`), so applications can
   distinguish exhausted retries from real failures and apply their own
   policy.

## Verification (reproducible)

```bash
gofmt -l .
go vet ./... && go vet -tags=audit ./...
golangci-lint run
go test -count=1 ./...
go test -race ./...
go test -run TestAudit -v .    # 18/18 promoted regression tests
```
