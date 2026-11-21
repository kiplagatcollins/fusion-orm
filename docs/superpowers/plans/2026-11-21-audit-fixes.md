# GORM Audit Fixes — Implementation Plan

> **For agentic workers:** execute task-by-task (executing-plans style). Steps
> use checkbox (`- [ ]`) syntax for tracking. Every task: run its audit repro
> (must fail first), implement, promote the repro to the default suite, verify
> green, commit.

**Goal:** fix every AFFECTED verdict from `docs/AUDIT.md` (P0 → P1 → P2), then
fold the results plus the previously approved dialect-robustness backlog into
the next-release spec.

**Architecture:** surgical fixes at the same seams the audit identified —
callback finalizer pass, soft-delete injection grouping, clause rendering,
association save policy, migrator name scoping/junction DDL, stmt-cache
refcounting. No API breaks except additive fields (`OnConflict.UpdateAll`,
`schema.Field.IndexSQLName`).

**Tech Stack:** Go 1.27, SQLite temp-file tests, PG via `FUSION_TEST_PG_DSN`.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-11-21-audit-fixes-design.md` (written last,
  records shipped decisions).
- Test promotion: fixed tests move from `audit_repro_test.go` (build tag
  `audit`) into `audit_fixes_test.go` (default suite). When all 18 pass, delete
  `audit_repro_test.go`.
- Never nil contexts; no panics escaping library code (only re-panic through
  the explicit panic-recovery task); SQL strings localized to
  `dialect/`+`clause/` (DDL in `migrator/`).
- Every task: `go test ./...` green; gate: `gofmt -l .`, `go vet ./...`,
  `go vet -tags=audit ./...`, `golangci-lint run`.

---

### Task 1: P0-A — callback finalizer pass + panic recovery

**Files:** `callbacks.go` (Execute, callbackEntry, newCallbackManager),
`audit_repro_test.go` → `audit_fixes_test.go` (2 tests).

- [ ] `callbackEntry` gains `finalizer bool`; `newCallbackManager` marks
  `"fusion:commit_or_rollback"` as finalizer.
- [ ] `Execute`: error stops non-finalizers only; after the loop always run
  unexecuted finalizers (`runFinalizers`); `defer recover()` sets
  `db.Error` (so the finalizer rolls back) when it is still nil, runs
  finalizers, re-panics with the original value.
- [ ] Guard against finalizer double-execution (`executed` set, mark before call).
- [ ] Promote `TestAuditImplicitTxReleasedOnInsertError`,
  `TestAuditImplicitTxReleasedOnHookPanic` → default suite.
- Note: explicit `Transaction()` owns commit/rollback itself; `commitOrRollback`
  no-ops without the `fusion:tx` key — finalizer pass is safe there.

### Task 2: P0-H — guard before soft-delete rewrite

**Files:** `callbacks_default.go` (deleteRows), tests.

- [ ] In `deleteRows`, evaluate `guardConditions` **before**
  `rewriteSoftDelete` so the injected `deleted_at IS NULL` can never satisfy
  the global-delete guard; rewrite (which injects the filter) runs only after
  the guard passes.
- [ ] Promote `TestAuditGlobalSoftDeleteRequiresWhere`.

### Task 3: P0-H — parenthesized soft-filter grouping (OR precedence)

**Files:** `clause/query.go` (new `Group` + shared cond builder),
`soft_delete.go` (injectSoftDeleteFilter), tests.

- [ ] New `clause.Group{Conds []Condition}` building `(c1 <join> c2 ...)`
  reusing a shared `buildConditions(b, conds)` helper that `Where.Build` also
  uses (prefix join per cond, parenthesize a multi-expr cond when `i > 0`).
- [ ] `injectSoftDeleteFilter`: if any existing cond can create a top-level OR
  (`conds[i>0].Join == "OR"`, or `conds[0]` is a multi-expr OR), wrap all
  existing conds in one `Group` cond before appending the soft predicate.
- [ ] Promote `TestAuditSoftDeleteNotBypassedByOr`.

### Task 4: P0-H — soft-delete filter on joined relations

**Files:** `soft_delete.go` (softDeleteCondition column param),
`joins.go` (ON clause), tests.

- [ ] `softDeleteCondition(db, field, column)` takes the (optionally
  alias-qualified) column; existing call site passes `field.DBName`.
- [ ] In `applyJoins`, when the joined table has a soft-delete field and the
  statement is not unscoped, append the condition to the join's `ON`
  (`alias.col IS NULL` style) for has-one and belongs-to joins.
- [ ] Promote `TestAuditRelationJoinOmitsSoftDeleted`; G#6005 (count over
  joins) rides along — assert in `TestAuditCount...` if cheap.

### Task 5: P1-F — condition building correctness

**Files:** `clause/clause.go` (Condition.Build uses `c.Join`),
`clause/query.go` (Where parenthesization from Task 3), `chainable_api.go`
(map keys sorted), `clause/clause_test.go`, tests.

- [ ] `Condition.Build`: join `Exprs` with `c.Join` (default `AND`).
- [ ] `Where.Build`/`Group`: parenthesize multi-expr conds at `i > 0`
  (left-associative contract).
- [ ] `addCondition` map branch: sort keys → stable SQL.
- [ ] Optional: parenthesize multi-line raw `clause.Expr` SQL (#7351) — drop
  this sub-step if it churns existing goldens; document either way.
- [ ] Promote `TestAuditClauseOrRendersOr`, `TestAuditMapConditionSQLStable`.

### Task 6: P1-G — Count drops ORDER BY

**Files:** `finisher_api.go` (Count), tests.

- [ ] `Count`: `tx.Statement.RemoveClause("order")` before executing the query
  chain (keeps GROUP BY/HAVING).
- [ ] Promote `TestAuditCountStripsOrderBy`.

### Task 7: P1-I — Save over slices with primary keys

**Files:** `finisher_api.go` (Save), tests.

- [ ] `Save`: if the input resolves to multiple values (slice), partition per
  element — zero PK → `Create(elem)`, non-zero PK →
  `Where(pk = ?).Updates(elem)` — instead of passing the whole slice to
  `Updates`. Single-value path unchanged.
- [ ] Promote `TestAuditSaveSliceWithPKs`.

### Task 8: P1-K — association save policy (no duplicate children)

**Files:** `association.go` (createRecord), tests.

- [ ] `createRecord` without `fullSave` but with a set PK → update by explicit
  primary key (`Where(pk = ?).Updates(value)`), not a fresh `Create`
  (has-many re-save currently duplicates; belongs-to/M2M already skip).
- [ ] Promote `TestAuditHasManyResaveNoDuplicate`.

### Task 9: P1-K — idempotent M2M join-row insert

**Files:** `association.go` (insertJoinRow), tests.

- [ ] Before inserting, `SELECT COUNT(*)` on the junction table for
  `(fk, ref)` through `Statement.ConnPool` with dialect bind vars
  (`Dialector.BindVarTo`) and `Statement.QuoteTo`; skip the insert when the
  pair already exists.
- [ ] Promote `TestAuditM2MAppendIdempotent`.

### Task 10: P1-K — AutoMigrate creates junction tables

**Files:** `migrator/migrator.go` (+ helper), tests.

- [ ] For each parsed model, iterate `ManyToMany` relations; build a synthetic
  `*schema.Table` (`Name: rel.M2MName`; two `PrimaryKey` fields named
  `rel.M2MForeignKey`/`rel.M2MReferences`, types copied from
  `rel.Reference`/`rel.ForeignKey` with `AutoIncrement` cleared); create it
  when `HasTable` is false. Composite PK gives the unique constraint.
- [ ] Promote `TestAuditAutoMigrateCreatesM2MJunction`.

### Task 11: P1-M — table-scoped explicit index names

**Files:** `schema/field.go` (+`IndexSQLName`/`UniqueSQLName`),
`migrator/migrator.go` (createIndexes uses SQL names), tests.

- [ ] Parse: default names (already table-scoped) set both fields; explicit tag
  values set `IndexName`/`UniqueName` for display and
  `<name>_<table>` for the SQL name.
- [ ] `createIndexes` issues `CREATE ... INDEX IF NOT EXISTS <sqlname>`.
- [ ] Promote `TestAuditIndexNameScopedPerTable`; document the duplicate-index
  upgrade note (pre-1.0) in the spec.

### Task 12: P2-B — stmt-cache refcount, Ping, migrate invalidation

**Files:** `prepared_stmt.go`, `prepared_stmt_test.go`, `migrator/migrator.go`,
tests.

- [ ] `stmtCache` entries gain `refs`/`retired`; `get` returns
  `(stmt, release, err)`; eviction/TTL/close **retire** (remove from map) and
  close only at `refs == 0`; `release` is idempotent (`sync.Once`).
- [ ] Callers (`preparedPool`/`preparedTx` × Exec/Query/QueryRow) release after
  the `database/sql` call returns (rows hold their own dependency on the stmt —
  verified in `database/sql` source: `QueryContext` registers `addDep`).
- [ ] `preparedPool.Ping()` forwards to the wrapped pool (skips when absent).
- [ ] `InvalidatePrepared()` on pool/tx; `AutoMigrate` calls it via interface
  assertion after success (cache keyed by SQL text can outlive DDL).
- [ ] Promote `TestAuditStmtCacheEvictionClosesInUseStmt`,
  `TestAuditPreparedPoolForwardsPing`.

### Task 13: P2-C — Row never returns nil

**Files:** `finisher_api.go` (Row), tests.

- [ ] On build error, return
  `ConnPool.QueryRowContext(ctx, "SELECT 1 FROM \"__fusion_row_build_error\"")`
  — non-nil row whose `Scan` fails; the real cause stays on `db.Error`.
  Update the `Row()` doc comment. DryRun unchanged (query executes through
  database/sql either way — document).
- [ ] Promote `TestAuditRowNeverNil`.

### Task 14: P2-E — scope errors propagate

**Files:** `chainable_api.go` (applyScopes), tests.

- [ ] `applyScopes`: when `next.Error != db.Error`, carry the whole chain
  (`db.Error = next.Error`) — preserves wrap-head ordering, drops nothing.
- [ ] Promote `TestAuditScopeErrorsPropagate`.

### Task 15: P2-J — OnConflict normalization + UpdateAll

**Files:** `clause/write.go` (Build), `statement.go` (normalize in
BuildClauses), tests.

- [ ] `clause.OnConflict` gains `UpdateAll bool`; `Build` renders
  `ON CONFLICT` without `()` when `Columns` is empty; `DO NOTHING` keeps
  working targetless (valid on SQLite + PG).
- [ ] `Statement.BuildClauses` normalizes: `UpdateAll` → all column names;
  non-empty `UpdateColumns` + empty `Columns` → table PKs (error via
  `AddError` when the model has no PK).
- [ ] Promote `TestAuditOnConflictRendersNoEmptyTarget`.

### Task 16: docs — spec fold-in + AUDIT status

- [ ] Write `docs/superpowers/specs/2026-11-21-audit-fixes-design.md`:
  shipped decisions, known limitations (raw multi-line exprs, statement
  migration invalidation semantics, duplicate legacy indexes, composite-PK
  caveat), and the dialect-robustness backlog (multi-statement script
  executor, `busy_timeout` + retry, `ErrBusy`).
- [ ] Update `docs/AUDIT.md`: AFFECTED → FIXED (with test names), remaining
  caveats; update ROADMAP/PRD pointers if they reference the audit.

### Task 17: final gate

- [ ] `gofmt -l .` · `go vet ./...` · `go vet -tags=audit ./...` ·
  `golangci-lint run` · `go test -count=1 ./...` · `go test -race ./...` ·
  `go test -tags=audit ./...` (only unfixed/repro remnants, if any).
- [ ] `audit_repro_test.go` deleted (all 18 promoted) or documents the
  stragglers; commits per task.
