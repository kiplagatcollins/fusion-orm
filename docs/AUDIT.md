# fusion-orm Audit Report — gorm Issue Backlog (classes A–Q)

**Scope**: all 450 open issues from `go-gorm/gorm` (pages 1–18), triaged into 17
bug classes A–Q and inspected against fusion-orm v0.2.0 (`f3c1f77`).
**Verdicts**: `AFFECTED` (bug present here), `SAFE` (code inspected, not affected),
`N/A` (feature/dialect does not exist in fusion-orm), `UNCERTAIN` (cannot be
decided without runtime evidence for a non-target platform).
**Method**: static inspection with `file:line` citations; every AFFECTED verdict
has a repro in [`audit_repro_test.go`](../audit_repro_test.go)
(`//go:build audit`) that asserts the *correct* behavior and currently **fails** —
the failure output below is the recorded evidence.

## Reproduce

```bash
go test -tags=audit -run TestAudit -v .    # 18/18 expected to FAIL (evidence)
go test ./...                              # default suite stays green
go vet -tags=audit ./...                   # audit build compiles clean
```

## Summary

| # | Class | Verdict | Repro test(s) | Representative GORM issues |
|---|-------|---------|---------------|------------------------------|
| A | Implicit tx / panic / rollback lifecycle | **AFFECTED (severe)** | `...ImplicitTxReleasedOnInsertError`, `...ImplicitTxReleasedOnHookPanic` | #7897, #7908, #7459 |
| B | Prepared-stmt cache races & lifecycle | **AFFECTED** | `...StmtCacheEvictionClosesInUseStmt`, `...PreparedPoolForwardsPing` | #7831, #7832, #7788, #7465, #6181, #7255 |
| C | Rows/Row lifecycle & NULL scanning | **AFFECTED (Row) / SAFE (rows)** | `...RowNeverNil` | #7575, #7855, #7403 |
| D | FindInBatches / limit & field mapping | SAFE (w/ composite-PK caveat) | — | #7737, #7384, #7657 |
| E | Session reuse / chain clone / scope errors | **AFFECTED (scope errors)** | `...ScopeErrorsPropagate` | #6283, #7765, #5846 |
| F | Condition building (Or/And/raw/map) | **AFFECTED (severe)** | `...ClauseOrRendersOr`, `...MapConditionSQLStable` | #5652, #7351, #5846 |
| G | Count / aggregation | **AFFECTED** | `...CountStripsOrderBy` | #7516, #6005 |
| H | Soft delete (guard, Or precedence, joins) | **AFFECTED (severe)** | `...GlobalSoftDeleteRequiresWhere`, `...SoftDeleteNotBypassedByOr`, `...RelationJoinOmitsSoftDeleted` | #6338, #6998, #5652, #7385 |
| I | Save / batch insert / value encoding | **AFFECTED (Save slice)** | `...SaveSliceWithPKs` | #7540, #7389, #7878, #7835 |
| J | OnConflict / upsert | **AFFECTED (low)** | `...OnConflictRendersNoEmptyTarget` | #7527 (feature gap) |
| K | Associations & M2M (create, dup, migrate) | **AFFECTED** | `...HasManyResaveNoDuplicate`, `...M2MAppendIdempotent`, `...AutoMigrateCreatesM2MJunction` | #6119, #5579, #7365, #7006 |
| L | Preload (N+1, joins, unscoped, FK) | SAFE | — | #6849 (known limit), #6750 (N/A) |
| M | AutoMigrate (indexes, concurrency, rebuild) | **AFFECTED** | `...IndexNameScopedPerTable` | #7346, #7894, #5794, #6073 |
| N | SetColumn / dirty-set semantics | N/A (API absent) | — | #7512, #7910 |
| O | Logging hooks (format strings) | SAFE | — | #5261, #7583 |
| P | Error wrapping / AddError | SAFE | — | #5964, #7459 |
| Q | Model/metadata resolution | SAFE (composite-PK caveat) | — | #6060, #6795 |

**Triage statistics**: 450 issues → 155 could-apply · 62 uncertain · 233 N/A.
17 classes (A–Q); 18 repro tests, all currently failing.

---

## Detailed findings

### A — Implicit transaction lifecycle  · AFFECTED (severe)

* **Leak on any error**: `CallbackProcessor.Execute` returns on the first
  non-nil `db.Error` (`callbacks.go:108-118`), skipping every remaining entry —
  including `fusion:commit_or_rollback`, which is **last** in all six chains
  (`callbacks/chain.go:12,30,39`). The implicit default tx opened by
  `begin_default_tx` (`callbacks_default.go:63`) is therefore never committed
  *or rolled back* when anything fails: duplicate keys, constraint errors,
  hook errors, … → the connection stays checked out forever.
  **Evidence**: `TestAuditImplicitTxReleasedOnInsertError` → `InUse=1 (want 0)`.
* **Leak on panic**: there is no `recover()` anywhere on the callback path, so a
  panicking `BeforeCreate`/`AfterCreate`-style hook unwinds straight through the
  open `*sql.Tx` → same leak plus the panic escapes to the caller.
  **Evidence**: `TestAuditImplicitTxReleasedOnHookPanic` → panic propagates
  (`r != nil` ✓) but `InUse=1 (want 0)`. Explicit `Transaction()` is **SAFE**:
  it uses a `panicked` flag and compensates.
* **SAFE**: rollback-error masking (#7908) — the original `db.Error` is
  preserved and rollback failures are discarded (`callbacks_default.go:81`).
  `AddError` chains causes with `%w; %w` (`errors.go:90-108`), so `errors.Is`
  still matches the first cause (#7459 SAFE).
* **Fix direction**: `Execute` must always run a "finalizer" pass (or
  `commitOrRollback` must be invoked from a `defer` in the finisher), and the
  implicit-tx block needs `defer recover()` → rollback → re-panic.

### B — Prepared-stmt cache lifecycle  · AFFECTED

* **Eviction closes in-use statements**: `stmtCache.get`
  (`prepared_stmt.go:34-82`) has no reference counting; the LRU eviction and
  TTL sweeper call `stmt.Close()` while another goroutine may already hold the
  `*sql.Stmt` between lookup and execution → `sql: statement is closed`
  (#7831). **Evidence**: `TestAuditStmtCacheEvictionClosesInUseStmt`.
* **Ping not forwarded**: `preparedPool`/`preparedTx` implement only
  `ConnPool`+`BeginTx`, not `Ping()`; `Open`'s type-asserted automatic ping
  (`fusion.go`) is silently skipped whenever `PrepareStmt: true` (#6181).
  **Evidence**: `TestAuditPreparedPoolForwardsPing`.
* **SAFE**: expired-statement overwrite closes the old stmt correctly (#7832);
  cache lock is released before `PrepareContext` so a slow prepare can't stall
  the mutex (#7788); no "pool empty" deadlock in the open path (#7465).
* **Low**: `QueryRowContext` has no `driver.ErrBadConn` retry
  (`prepared_stmt.go:145-154`); the cache is not invalidated after
  `AutoMigrate` alters tables (#7255, mitigated by column-identity checks).

### C — Rows/Row lifecycle  · AFFECTED (Row) / SAFE (rows)

* `Row()` returns a nil `*sql.Row` whenever the statement build fails —
  e.g. `db.Model(42).Row()` — so the caller's mandatory `row.Scan(...)`
  nil-derefs (#7575). `Row()` also ignores `DryRun`.
  **Evidence**: `TestAuditRowNeverNil`.
* **SAFE**: `Rows()` closes via `defer` (`callbacks_default.go:163`); every
  `NULL` scan goes through `Scanner`/`SetZero` (`schema/scanner.go`), so
  nullable → zero-value works (#7855 SAFE); context cancellation surfaces as an
  error, no panic. `ScanRows` API doesn't exist (#7403 N/A).

### D — FindInBatches / mapping  · SAFE

`batchSize <= 0` guarded (`finisher_api.go:288`); `fieldAt` bounds-checks the
  field index (`scan.go:55-66`) so the #7737 panic class cannot occur (worst
  case a silent no-op mapping). **Caveat**: `primaryField` resolves to `PKs[0]`
  only (`schema/relation.go:129,136`) — composite-PK pagination walks only the
  first key (#7384/§Q).

### E — Session reuse / scopes  · AFFECTED (low)

* **Scope errors dropped**: `applyScopes` copies only `next.Statement`
  (`chainable_api.go:287-300`); if a scope function raises an error on its
  derived `*DB` (e.g. an invalid condition), `next.Error` is silently lost and
  the finisher proceeds unscoped (#6283 class).
  **Evidence**: `TestAuditScopeErrorsPropagate`.
* Map-key `Where` produces nondeterministic SQL (#5846) → see F.
* **SAFE**: the M6 chain-reuse/clone fix holds — `Session{NewDB: true}` forks
  properly; preload children get fresh sessions (`preload.go:184,261,300`) so
  hook/session state doesn't bleed. Clause inheritance across a session is by
  design. `Session` cannot override `PrepareStmt` (field absent — API gap).

### F — Condition building  · AFFECTED (severe)

* **`clause.Or` is broken**: `Condition.Build` (`clause/clause.go:56-63`) joins
  its `Exprs` with a hardcoded `"AND"` and ignores `Condition.Join`, so
  `clause.Or(a, b)` renders `a AND b`. **Evidence**:
  `TestAuditClauseOrRendersOr` → `WHERE name = 'a' AND name = 'b'`.
* **Map conditions unstable**: map iteration order leaks into SQL
  (`chainable_api.go:156-160`) → different SQL per call, breaking prepared-stmt
  reuse and `ToSQL` reproducibility (#5846).
  **Evidence**: `TestAuditMapConditionSQLStable` (two distinct renderings).
* Multi-line raw strings are never parenthesized (#7351) — a `WHERE (raw)` is
  required whenever the raw text contains top-level `OR`.
* **SAFE** otherwise: user `Or()` chains keep correct precedence via flat
  conds; the soft-delete *injection* ordering bug is class H, not here.

### G — Count  · AFFECTED

`Count` (`finisher_api.go:228`) builds with the caller's full query —
`ORDER BY` survives → PostgreSQL rejects it ("must appear in GROUP BY",
#7516). **Evidence**: `TestAuditCountStripsOrderBy` →
`SELECT COUNT(*) ... ORDER BY name`. Joins also lack the soft-delete filter,
so counts over joined relations include deleted rows (same root cause as H,
#6005).

### H — Soft delete  · AFFECTED (severe)

* **Global-delete guard bypassed**: `deleteRows`
  (`callbacks_default.go:448-449`) runs `rewriteSoftDelete` **before**
  `guardConditions`; the injected `deleted_at IS NULL` predicate satisfies the
  guard, so `db.Delete(&softUser{})` (no caller conditions) soft-deletes every
  live row instead of failing with `ErrMissingWhereClause` (#6338 class).
  Existing tests only cover non-soft-delete models, so the suite stayed green.
  **Evidence**: `TestAuditGlobalSoftDeleteRequiresWhere` →
  `Delete = <nil>` and `rows = 0 (want 2)`.
* **OR-precedence leak**: the soft filter is appended as another flat cond, so
  `Where("a").Or("b")` renders `WHERE a OR b AND deleted_at IS NULL` →
  `a` wins and deleted rows reappear.
  **Evidence**: `TestAuditSoftDeleteNotBypassedByOr` returns deleted row `a`.
* **Join filter missing**: `applyJoins` (`joins.go:45+`) adds no
  soft-delete predicate to LEFT/INNER JOINed tables, unlike `preload` which
  handles it → `Joins("Profile")` returns soft-deleted relations (#6998).
  **Evidence**: `TestAuditRelationJoinOmitsSoftDeleted`.
* **SAFE/by-design**: combined `UpdatedAt`+`DeletedAt` column behavior (#7385).

### I — Save / encoding  · AFFECTED (low)

* **`Save(slice with PKs)` fails**: non-zero primary keys route to
  `Updates(&slice)` (`finisher_api.go:23-44`), and `assignmentsFrom` rejects
  slice values → `"Updates expects a map or struct"`; nothing is written
  (#7540 class). **Evidence**: `TestAuditSaveSliceWithPKs`.
* **SAFE**: `*map` dest is rejected with a clear error (#7389); batch INSERT
  clauses are rebuilt per batch and `RowsAffected` is summed (#7878); `uint64`
  values go through `reflect` conversion, no sign-flip wrap (#7835);
  timestamps are set per value.

### J — OnConflict / upsert  · AFFECTED (low)

`OnConflict.Build` itself is correct (`clause/write.go:95-128`, golden-tested).
But an empty `Columns` with `UpdateColumns` set renders the invalid
`ON CONFLICT () DO UPDATE ...`.
**Evidence**: `TestAuditOnConflictRendersNoEmptyTarget`.
`UpdateAll` as a first-class option (GORM #7527) is an API feature gap.

### K — Associations & M2M  · AFFECTED

* **Duplicate child on re-save**: the has-many branch of `linkOne` calls
  `createRecord` unconditionally (`association.go:173`), and `createRecord`
  (`association.go:195-220`) plain-`Create`s the child unless `fullSave` is on
  with a set PK — GORM would upsert/skip; we insert a duplicate row (#6119 class).
  **Evidence**: `TestAuditHasManyResaveNoDuplicate` → `rows = 2 (want 1)`.
* **M2M append not idempotent**: `insertJoinRow` (`association.go:221`)
  inserts unconditionally; the junction table has no unique constraint →
  re-appending the same pair duplicates the row (#5579/#7365).
  **Evidence**: `TestAuditM2MAppendIdempotent` → `junction rows = 2 (want 1)`.
* **Junction table never migrated**: `AutoMigrate` knows junction tables only
  by name (`schema/relation.go:112-121`); no migrator path creates them —
  tests must hand-write the DDL (`relation_test.go`). Feature gap (#7006-adjacent).
  **Evidence**: `TestAuditAutoMigrateCreatesM2MJunction`.
* **UNCERTAIN**: `[16]byte` UUID field values — fusion-orm has no UUID type;
  string UUIDs are fine.

### L — Preload  · SAFE

Children are preloaded through fresh sessions (no state bleed), `Unscoped`
propagates to relation queries, all keys go into a single `IN` (known 65k
parameter / SQLite variable limit, #6849 — documented limitation). FK-override
APIs don't exist (#6750 N/A).

### M — AutoMigrate  · AFFECTED

* **Global index-name collision**: `createIndexes`
  (`migrator/migrator.go:224-249`) issues `CREATE INDEX IF NOT EXISTS <name>`;
  when two *tables* legitimately declare the same explicit index name, the
  second `IF NOT EXISTS` silently no-ops → the second table has no index
  (#7346/#7894). **Evidence**: `TestAuditIndexNameScopedPerTable` →
  `table idx_model_bs lost its email index`. Default (unnamed) index names are
  table-scoped and SAFE (`schema/field.go:235-243`).
* **Concurrency**: protection is a per-instance mutex (`migrator/migrator.go:62`,
  locked at `391-392`); two separate `DB` handles migrating the same database
  concurrently can still interleave (#5794 — accepted limitation; document it).
* **SAFE**: no default-expression diffing storm (#7553); column rebuild only
  for missing columns with non-constant defaults, done transactionally with
  row copy (#6073 mostly SAFE); no silent PK rewriting (#7323);
  `DropColumn(string)` API (#5795 N/A); no model reordering (#6578 N/A).
* **Gaps**: FK creation and check/generated-column diffing are not implemented
  (feature gaps, #7517/#7621/#7191).

### N — SetColumn  · N/A

No `SetColumn` API; hook write-back mutates the destination in place (safe).
GORM #7910's `Set().Update` surface does not exist in the facade.

### O — Logging hooks  · SAFE

`SlogQueryHook` passes SQL and arguments as structured attributes — no format
string ever interprets user data (#5261 SAFE). Log level is configurable
(zero-value = Info; note for #7583).

### P — Error wrapping  · SAFE

`AddError` chains every cause with `%w; %w` and nil-guards input
(`errors.go:90-108`); `errors.Is` matches any cause in the chain (#5964, #7459).

### Q — Model/metadata resolution  · SAFE (composite-PK caveat)

Embedded structs resolve correctly (`schema/table.go:194`); `primaryField`
falls back to `PKs[0]`/`"id"` so common models never hard-fail (#6060, #6795).
**Caveat**: integer `Where(42)`-style conds bind to `PKs[0]` only
(`schema/relation.go:129,136`) — on
composite-PK tables this matches the wrong rows (extends D's pagination
caveat).

---

## Not applicable (233 issues)

| Bucket | Reason |
|---|---|
| MySQL/MSSQL/Oracle/ClickHouse dialect specifics, ENUM, timezone handling | only sqlite + postgres dialects are in scope for v0.1/v0.2 |
| `serializer` JSON/Bytes tags | serializer package absent; plain `DataTypeJSON` |
| `JSONArrayQuery` | not implemented |
| `Raw` / `NamedExpr` / `Exec` placeholder modes | facade has no `Raw`; `clause.Expr` covers needs |
| `ParameterizedQueries`, `QueryFields`, `SetColumn`, `ReorderModels`, `ScanRows` | API absent by design |
| Feature requests (mocks, `WhereIf`, index options, hooks registry, …) | backlog, not defects — folded into the v0.2 spec discussion |

## Fix-phase priorities (input to the v0.2 spec)

* **P0 (data loss / resource leaks)**: H (global-delete guard + OR
  precedence + join filter), A (finalizer pass + panic recovery).
* **P1 (correctness)**: F (`clause.Or`, map ordering), K (idempotent
  associations, junction migration), M (index-name scoping), I (`Save` slice),
  G (Count strips ORDER BY).
* **P2 (robustness)**: B (stmt-cache refcount + Ping forwarding), C (`Row`
  nil), E (scope-error propagation), J (empty `ON CONFLICT` target).

Each fix lands with its audit-tag test flipped to the default suite (removed
from `audit_repro_test.go` or de-tagged) — see `docs/AUDIT_PLAN.md`.
