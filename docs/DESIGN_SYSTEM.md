# fusion-orm — Design System

Define the API language so ORM work stays consistent. For a library, the "design
system" is the public API: naming, chaining contract, tag vocabulary, and
developer-experience rules. A future playground/docs UI, if any, would adopt
these same tokens.

## 1. Overview

- **Brand:** fusion-orm
- **Direction:** calm, explicit, SQL-honest developer API — gorm's fluency without silent magic; every convenience has an inspectable SQL output (`ToSQL`, `DryRun`).
- **Source of truth:** exported surface of the `fusion` package + `clause/` naming; golden-SQL tests encode the canonical output.

## 2. Principles

1. **Explicit beats implicit** — `Save` semantics, global updates, soft-delete bypass are all explicit (`Unscoped`, `AllowGlobalUpdate`); nothing rewrites user intent silently.
2. **One way per concept** — one chaining API, one finisher API, one tag vocabulary; generics facade is sugar over the same core, never a second engine.
3. **Errors you can branch on** — sentinel errors for every condition the caller must handle (`ErrRecordNotFound`, `ErrMissingWhereClause`, `ErrDuplicatedKey`); never string-matching.
4. **SQL visible on demand** — `DryRun`, `ToSQL()`, and logger tracing make every operation inspectable; log format includes caller, duration, rows, SQL.
5. **Dialect honesty** — feature flags gate rendering; if a dialect can't do something the API surface still type-checks, the error names the feature (`ErrUnsupportedFeature: returning`).
6. **Accessible docs by default** — every exported symbol has a doc comment with an example; models document their tags.

## 3. Naming Conventions

| Concept | Rule | Example |
|---|---|---|
| Package | `fusion` (core), `fusion/schema`, `fusion/clause`, `fusion/dialect/sqlite` | `import "…/fusion"` |
| Entry type | `*fusion.DB` | `db := fusion.Open(...)` |
| Chain verbs | gorm-compatible names | `Model, Select, Where, Order, Limit, Offset, Joins, Preload, Group, Having, Distinct, Scopes, Clauses` |
| Finishers | gorm-compatible names | `Create, Find, First, Take, Last, Save, Update(s), Delete, Count, Pluck, Scan, ToSQL` |
| Sentinels | `Err` prefix, branchable | `ErrRecordNotFound, ErrMissingWhereClause, ErrDuplicatedKey` |
| Clause names | lowercase-kebab registry keys | `"select"`, `"on_conflict"`, `"soft_delete"` |
| Interfaces | noun, `-er` only for single-method | `Dialector, QueryHook, Namer, Tabler` |

Gorm-name parity is a requirement, not coincidence — migrating users shouldn't relearn verbs.

## 4. Tag Vocabulary

Single dialect, gorm-like keys, parsed from `fusion:"..."`:

| Key | Meaning | Example |
|---|---|---|
| (bare) | column name | `fusion:"email"` |
| `column` | explicit column | `column:user_email` |
| `type` | SQL type override | `type:varchar(255)` |
| `pk` / `primarykey` | primary key | `pk` |
| `autoincrement` | identity | `autoincrement` |
| `notnull` / `nullzero` / `default:` | constraints | `notnull,default:now()` |

`default:` accepts literal values (`default:18`, `default:'x'`) and SQL
expressions (`default:CURRENT_TIMESTAMP`, `default:now()`). Expression
defaults apply through `CREATE TABLE`; when `AutoMigrate` must add such a
column to an existing table on a dialect that rejects it in `ALTER TABLE
ADD COLUMN` (SQLite), it rebuilds the table with the full desired schema
inside a transaction and copies the surviving rows across.
| `index` / `unique` | (unique) index, `index:idx_name,composite` | `unique` |
| `->` / `<-` / `-` | read-only / write-only / ignored | `->` |
| `embed` | embedded struct prefix | `embed:addr_` |
| `rel` | relation type | `rel:has-many` |
| `foreignKey` / `references` | FK columns | `foreignKey:UserID` |
| `many2many` | junction model | `many2many:user_posts` |
| `polymorphic` | polymorphic value | `polymorphic:owner` |
| `serializer:` / `json` | custom serialization | `serializer:json` |

Rules: unknown keys **warn via logger** (lenient; strict mode behind `Config.StrictTags`), keys are lowercase, values after `:`; `;`-separated from gorm maps onto this vocabulary 1:1 in the parser tests (documented migration table).

## 5. Chaining Contract (copy-on-write)

- `*DB` methods return a safe derivative; the receiver is never mutated. `Session{}`, `WithContext`, `Debug` all clone.
- Settings travel on `Statement.Settings` (sync.Map) — plugins read, never mutate the shared `Config`.
- `DryRun` buffers the SQL and stops before execution; `ToSQL()` is `DryRun` + formatted return.
- Every finisher updates exactly the `Error`, `RowsAffected` fields; no panics for user errors.

## 6. Callback Contracts

- Processors: `create | query | update | delete | row | raw`; default chains fixed in `callbacks/` (order documented in ARCHITECTURE §4).
- Registration: `Register/Replace/Remove` with `Before/After` anchors and optional `Match(db) bool` predicates; ordering conflicts are registration errors, not runtime surprises.
- Hooks: runtime-detected `BeforeCreate(tx *fusion.DB) error`-style methods (gorm-compatible); explicit interfaces provided for compile-time safety; `SkipHooks` honored by `UpdateColumn(s)`.
- Callbacks must be idempotent per statement and must not hold the statement across transactions.

## 7. Error Presentation

| Token | Trigger | Caller action |
|---|---|---|
| `ErrRecordNotFound` | `First/Take/Last` miss | branch or ignore |
| `ErrMissingWhereClause` | global update/delete | add `Where`/`WherePK` or `AllowGlobalUpdate` |
| `ErrDuplicatedKey` (+FK/check) | `TranslateError` on | map to 409-class handling |
| `ErrInvalidValue` / `ErrInvalidField` | bad dest/tag | fix code (programmer error) |
| `ErrUnsupportedFeature` | dialect lacks flag | dialect-aware fallback |
| `ErrDryRunModeUnsupported` | finisher misuse in DryRun | remove DryRun |

Wrapped errors keep the first as wrap head (`%w`), message pattern: `"create users: <cause>"`.

## 8. Generics Facade Rules

- `G[T](db)` returns compile-time-checked chain interfaces; unsupported transitions don't compile (no `Where` after finisher).
- Results are `(T, error)` / `([]T, error)` — no shared error slot on the facade.
- The facade must add zero behavior: same clauses, same callbacks, same SQL as the reflection API (asserted by shared golden tests).

## 9. Documentation Rules

- Every exported symbol: doc comment + one-line example where non-obvious.
- `example_test.go` for the README's featured flows (model + CRUD + preload + generics).
- Golden-SQL table in `clause/` tests doubles as the SQL reference per dialect.
- Breaking API changes require a `docs/CHANGELOG.md` entry (adopt after v1.0).

## 10. Rules

- Gorm-compatible names for shared concepts; bun-derived internals may keep bun names only if unexported.
- No `any` in exported signatures except `Value`-typed escape hatches (`Value(...) any` mirrors `database/sql`).
- No panics outside programmer-error paths (init of duplicate registration); user errors are returned.
- New chain methods must define clause effects + golden SQL for both dialects in the same PR.
- Logger output is structured (slog-compatible); never logs secrets or full DSNs.
