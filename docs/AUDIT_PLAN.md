# Audit Plan — GORM Open-Issue Backlog vs fusion-orm

Status: **approved, executing** (2026-11-21).

## Goal

Triage all 450 open issues of go-gorm/gorm into 17 recurring bug classes (A–Q),
verify each class against fusion-orm's actual code, and produce an evidence-backed
verdict per class. Fixes are a **separate follow-on phase** — this audit does not
change library behavior.

## Scope

All 18 pages of `is:issue state:open` were triaged by research agents:

- **~155 could-apply / ~62 uncertain / ~233 N/A** (absent dialect or feature,
  pure questions, feature requests).

Consolidated classes:

| Class | Topic | Representative GORM issues |
|-------|-------|----------------------------|
| A | Transaction lifecycle: implicit-tx panic leak, error masking, Session-in-Scopes leak, callbacks after commit_or_rollback | #7897, #7908, #6826, #6671, #7592, #7367 |
| B | Prepared-stmt cache: eviction close race, expired overwrite, lock contention, pool-empty deadlock, staleness after DDL, Ping gap | #7831, #7832, #7788, #7465, #7350, #7255, #6181 |
| C | Scan/Rows/Row: panic-in-scan hang, ScanRows race, stale fields on NULL, nil-Dest panic, ctx-cancel panic, driver error capture | #7698, #7403, #7855, #6819, #7575, #6840, #6277, #5346 |
| D | FindInBatches: non-positive size, composite PK, dest-shape mismatch | #7904, #7636, #7737 |
| E | Session/clone state leakage: stale WHERE/ORDER, PrepareStmt downgrade, preload clone=0, clause accumulation | #7725, #7437, #6648, #7765, #7662, #5931, #6283, #7136 |
| F | Where/Or/clause generation: Or grouping, nested parens, Not/And, IN(?) expansion, ? escaping | #7618, #6835, #5652, #7294, #6794, #6889 |
| G | Count vs Select/Distinct/Order/Joins, soft-delete filtering in Count | #7407, #6790, #7516, #6798, #6005 |
| H | Soft-delete interplay: Joins not filtering deleted, Save with deleted FK, DELETE missing WHERE | #6998, #6383, #6338, #7385 |
| I | Create/batch: *map panic, RETURNING overwrite, uint64 ID overflow, slice-save timestamps, string PK return | #7389, #7878, #7835, #7540, #6947 |
| J | OnConflict: UpdateAll vs defaults, abnormal SQL, composite PK, batch+conflict, RETURNING rows | #7527, #7397, #6622, #6047, #6500 |
| K | Associations/m2m: duplicate join rows, missing join rows, UUID/generics m2m, embedded FK, nil association Find | #7365, #6119, #5579, #6360, #6411, #6208, #6894, #6751, #7374 |
| L | Preload: FK override, nested+limit, case sensitivity, IN 65k params | #6750, #4975, #5236, #5400, #6849 |
| M | Migrator: index-name collision, default diff noise, UUID FK type, destructive rebuild, generated columns, no-PK panic, concurrency | #7894, #7346, #7553, #7517, #6073, #5534, #6578, #5056, #5794, #6529, #6977 |
| N | Hooks: pointer-receiver + generics, SetColumn wrong column, nil-pointer hooks | #7910, #6927, #4990, #7090 |
| O | Slog: query level, % parsing | #7583, #5261 |
| P | Error translation: original-error wrapping, "<nil>" error | #7459, #5964 |
| Q | Schema misc: forced PK, embedded handling, composite-key Delete | #7323, #6955, #6214, #4879, #6993 |

## Method

1. **Code inspection** — for each class, read the implicated paths and assign a
   verdict with `file:line` evidence:
   - `AFFECTED` — bug class demonstrably present (or its precondition exists),
     with a concrete repro path.
   - `SAFE` — code path exists and provably does the right thing (evidence).
   - `N/A` — feature or dialect absent from fusion-orm.
   - `UNCERTAIN` — plausibly present; needs repro to decide (repro deferred
     to the fix phase unless time permits).
2. **Repro tests** — for high-severity `AFFECTED` verdicts (panic, deadlock,
   leak, data corruption), write deterministic repro tests guarded by the
   `audit` build tag:
   - File: `audit_repro_test.go`, header `//go:build audit`.
   - Tests assert the **correct** behavior, so they FAIL on current code —
     that failure *is* the evidence. They are excluded from default CI.
   - Temp-file SQLite only (project rule: no daemons); fake `ConnPool`
     wrappers where SQL alone cannot force the condition.
3. **PG items** (index collision, UUID FK, destructive rebuild) — inspect
   only; repro requires `FUSION_TEST_PG_DSN` and is deferred to the fix
   phase unless a container is available.

## Deliverables

- `docs/AUDIT_PLAN.md` — this document.
- `docs/AUDIT.md` — verdict per class A–Q with `file:line` evidence, GORM
   issue cross-references, N/A reason for non-applicable issues, repro-test
   results (pass/fail-on-current-code) for high-severity items.
- `audit_repro_test.go` — `//go:build audit` repro tests for AFFECTED
   high-severity verdicts.

## Verification

```
gofmt -l .
go vet ./...
go vet -tags=audit ./...
golangci-lint run
go test ./...
go test -race ./...
go test -tags=audit ./...   # expected: repro tests FAIL for AFFECTED items
```

Default suite must stay green. Audit-tagged failures are recorded in
`docs/AUDIT.md` as evidence, not fixed here.

## Out of scope

- Fixing any AFFECTED verdict (follow-on phase, designed separately and
  folded into the v0.2 spec together with the approved dialect-robustness
  work: script executor, busy-timeout/retry, `ErrBusy`).
- Individual triage of the ~233 N/A issues (summarized by category only).
- MySQL/MSSQL/Oracle/ClickHouse-specific behavior (dialects absent).
