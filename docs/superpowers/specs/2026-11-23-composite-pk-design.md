# Composite primary key correctness — design

Date: 2026-11-23
Status: approved (approach A — fail loudly + full-key semantics)
Branch: `fix/composite-pk`
Target release: v0.4.0 (intentional behavior change: silent wrong results become
correct results or loud errors)

## Problem

The v0.2 audit (docs/AUDIT.md groups D and Q) left documented caveats: on
models with a composite primary key (`A, B` both `fusion:"pk"`), several
finisher paths bind or order by `PKs[0]` only, silently matching the wrong
rows. Silent wrong-row matching is the worst failure class an ORM can have;
every fix below either produces the correct result or a loud error.

## The five defects

| # | Path | Current behavior on composite PK `a,b` | Effect |
|---|---|---|---|
| 1 | `applyConds` int/int64 finisher conds (`finisher_api.go`) | `WHERE a = 42` | `First/Find/Delete(&X{}, 42)` matches N rows |
| 2 | `Save` / `FirstOrCreate` (`finisher_api.go`) | match/decide on `PKs[0]` only | `Save` can UPDATE sibling rows sharing `a`; `FirstOrCreate` skips Create when only `a` is set |
| 3 | `firstWithOrder` (`finisher_api.go`) | `ORDER BY a` only | `First`/`Last` non-deterministic tie ordering |
| 4 | `FindInBatches` (`finisher_api.go`) | cursor `a > last ORDER BY a` | skips/repeats rows across batches |
| 5 | relation inference (`schema/relation.go`) | `primaryField` falls back to `PKs[0]` | belongs-to joins the wrong referenced column; has-one/has-many/m2m from a composite-PK source preloads incompletely |

## Design decisions

1. **Scalar int/int64 finisher conditions on a composite-PK table error**
   with `ErrInvalidValue` ("composite primary key: use a map or explicit
   condition"). A single scalar cannot bind N columns; guessing `PKs[0]` is
   the bug. Single-PK behavior is byte-for-byte unchanged.
2. **`Save` matches on all PK fields.** Every PK field non-zero →
   `WHERE a = ? AND b = ?` Updates; any zero PK → Create (generalizes the
   existing single-column rule). Same rule for slice `Save` elements.
3. **`First`/`Last` order by the full PK tuple** (`ORDER BY a, b ASC/DESC`).
   Single-PK SQL is unchanged (one column).
4. **`FindInBatches` uses row-value keyset pagination** for composite PKs:
   `WHERE (a, b) > (?, ?) ORDER BY a, b LIMIT n`. Gated on a new
   `feature.RowValues` flag (declared by SQLite and PostgreSQL; both engines
   support row-value comparisons). Dialects without the flag get a clear
   `ErrUnsupportedFeature`-style error instead of wrong pagination. Single-PK
   path keeps the existing `a > last` cursor.
5. **Relation inference fails loudly on composite keys:**
   - belongs-to whose referenced (join) table has a composite PK and no
     explicit `references:` tag → `ErrInvalidRelation` naming the table.
   - has-one/has-many/many2many declared on a table with a composite PK →
     `ErrInvalidRelation` (the single `ForeignKey`/`Reference` pair cannot
     express a composite join; full composite relation support is backlog,
     not a silent guess).
   - Explicit `foreignkey:`/`references:` tags keep working (single-column
     relations onto composite-PK tables remain possible when the user names
     the column).

## Out of scope (backlog)

- Full composite relations (multi-column FK/Reference pairs, composite
  preload `IN` tuples, association API on composite keys).
- `Where(struct)`/`Where(map)` on composite PKs already work (they build
  conditions from all set fields) and are untouched.

## Testing

- New `composite_pk_test.go`: temp-file SQLite, composite-PK model
  (`A, B int64` both pk). Cases: scalar-cond error for
  First/Find/Delete/Updates; Save updates exactly one row among siblings
  sharing `a`; First/Last return the lowest/highest tuple; FindInBatches
  visits every row exactly once in tuple order across shuffled data;
  FirstOrCreate creates only when all PKs zero; relation parse errors for
  #5 with and without explicit tags.
- Golden SQL: row-value keyset and tuple ORDER BY render through the
  existing clause builders (raw `clause.Expr` + `OrderBy`), covered by
  behavior tests on both shipped dialects where practical.
- Existing single-PK suite must pass unchanged.
- Gate: `gofmt -l .` clean, `go vet ./...`, `golangci-lint run` 0 issues,
  `go test -count=1 ./...`, `go test -race ./...`.

## Docs

- CHANGELOG under a new Unreleased section (Changed + Fixed).
- `docs/AUDIT.md` D/Q caveat lines and the audit-fixes spec "Known
  limitations" item 3 updated to point at this fix.
