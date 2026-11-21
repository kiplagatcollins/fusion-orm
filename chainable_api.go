package fusion

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kiplagatcollins/fusion-orm/clause"
)

// preloadEntry is one node of a dotted preload path. Args holds apply
// functions, string conditions, or PK values for the relation's query.
type preloadEntry struct {
	Field    string
	Args     []any
	Children []*preloadEntry
}

func clonePreloads(entries []*preloadEntry) []*preloadEntry {
	if entries == nil {
		return nil
	}
	out := make([]*preloadEntry, len(entries))
	for i, entry := range entries {
		copyEntry := *entry
		copyEntry.Args = append([]any(nil), entry.Args...)
		copyEntry.Children = clonePreloads(entry.Children)
		out[i] = &copyEntry
	}
	return out
}

func addPreload(entries []*preloadEntry, parts []string, args []any) []*preloadEntry {
	if len(parts) == 0 {
		return entries
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return addPreload(entries, parts[1:], args)
	}
	var entry *preloadEntry
	for _, existing := range entries {
		if strings.EqualFold(existing.Field, name) {
			entry = existing
			break
		}
	}
	if entry == nil {
		entry = &preloadEntry{Field: name}
		entries = append(entries, entry)
	}
	if len(parts) == 1 && len(args) > 0 && len(entry.Args) == 0 {
		entry.Args = append([]any(nil), args...)
	}
	if len(parts) > 1 {
		entry.Children = addPreload(entry.Children, parts[1:], args)
	}
	return entries
}

// joinEntry is a raw JOIN expression or a named relation to LEFT JOIN.
type joinEntry struct {
	Relation string
	Raw      clause.Expression
	HasRaw   bool
}

// Preload requests a relation (or nested dotted path) to be loaded in one
// batched query after the main query. Args may be a func(*DB) *DB apply
// function, a SQL condition with args, or primary-key values.
func (db *DB) Preload(query string, args ...any) *DB {
	tx := db.getInstance()
	tx.Statement.Preloads = addPreload(tx.Statement.Preloads, strings.Split(query, "."), args)
	return tx
}

// Unscoped disables soft-delete filtering and DELETE rewriting for the
// statement, including relation preloads issued from it.
func (db *DB) Unscoped() *DB {
	tx := db.getInstance()
	tx.Statement.Set("fusion:unscoped", true)
	return tx
}

// Joins adds a raw JOIN expression, or LEFT JOINs a named has-one/belongs-to
// relation and selects its columns as alias__column for dotted scanning.
func (db *DB) Joins(query string, args ...any) *DB {
	tx := db.getInstance()
	q := strings.TrimSpace(query)
	upper := strings.ToUpper(q)
	if strings.HasPrefix(upper, "JOIN ") || strings.HasPrefix(upper, "LEFT ") ||
		strings.HasPrefix(upper, "RIGHT ") || strings.HasPrefix(upper, "INNER ") ||
		strings.HasPrefix(upper, "CROSS ") {
		tx.Statement.Joins = append(tx.Statement.Joins, joinEntry{
			Raw: clause.Expr(q, args...), HasRaw: true,
		})
	} else {
		tx.Statement.Joins = append(tx.Statement.Joins, joinEntry{Relation: q})
	}
	return tx
}

// Model sets the model used to derive table/column metadata.
func (db *DB) Model(value any) *DB {
	tx := db.getInstance()
	tx.Statement.Model = value
	return tx
}

// Table overrides the table name for the statement.
func (db *DB) Table(name string) *DB {
	tx := db.getInstance()
	tx.Statement.Table = name
	return tx
}

// Select restricts the selected columns. A single comma-separated string is
// split into columns.
func (db *DB) Select(columns ...string) *DB {
	tx := db.getInstance()
	var cols []string
	for _, c := range columns {
		cols = append(cols, strings.Split(c, ",")...)
	}
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	prev, _ := tx.Statement.Clause("select").(clause.Select)
	tx.Statement.AddClause(clause.Select{Columns: cols, Distinct: prev.Distinct})
	return tx
}

// Distinct adds DISTINCT to the SELECT clause.
func (db *DB) Distinct() *DB {
	tx := db.getInstance()
	prev, _ := tx.Statement.Clause("select").(clause.Select)
	tx.Statement.AddClause(clause.Select{Columns: prev.Columns, Distinct: true})
	return tx
}

// Where adds an AND-joined condition. Struct and map values build equality
// expressions from their non-zero fields.
func (db *DB) Where(query any, args ...any) *DB {
	return db.addCondition(query, args, false)
}

// Or adds an OR-joined condition.
func (db *DB) Or(query any, args ...any) *DB {
	return db.addCondition(query, args, true)
}

func (db *DB) addCondition(query any, args []any, or bool) *DB {
	tx := db.getInstance()
	var exprs []clause.Expression
	switch v := query.(type) {
	case string:
		exprs = []clause.Expression{clause.Expr(v, args...)}
	case map[string]any:
		// Sorted keys keep the SQL text stable across calls so prepared-stmt
		// caches and ToSQL reproducibility are not defeated by map order.
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			exprs = append(exprs, clause.Expr(fmt.Sprintf("%s = ?", quoteIdentifier(tx, key)), v[key]))
		}
	default:
		exprs = structConditions(tx, v)
	}
	if len(exprs) == 0 {
		return tx
	}
	cond := clause.Condition{Join: "AND", Exprs: exprs}
	if or {
		cond.Join = "OR"
	}
	prev, _ := tx.Statement.Clause("where").(clause.Where)
	conds := append(append([]clause.Condition(nil), prev.Conds...), cond)
	tx.Statement.AddClause(clause.Where{Conds: conds})
	return tx
}

func quoteIdentifier(db *DB, name string) string {
	return db.Statement.QuoteTo(name)
}

func structConditions(db *DB, model any) []clause.Expression {
	if model == nil {
		return nil
	}
	table, err := db.resolveTable(model)
	if err != nil {
		db.AddError(err)
		return nil
	}
	rv := reflect.ValueOf(model)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		db.AddError(fmt.Errorf("%w: Where expects a struct, map, or string", ErrInvalidValue))
		return nil
	}
	var exprs []clause.Expression
	for _, field := range table.Fields {
		value := fieldAt(rv, field.Index)
		if !value.IsValid() || field.IsZero(value) {
			continue
		}
		exprs = append(exprs, clause.Expr(quoteIdentifier(db, field.DBName)+" = ?", value.Interface()))
	}
	return exprs
}

// Order appends an ORDER BY expression (e.g. "name DESC").
func (db *DB) Order(value string) *DB {
	tx := db.getInstance()
	prev, _ := tx.Statement.Clause("order").(clause.OrderBy)
	cols := append(append([]string(nil), prev.Columns...), strings.Split(value, ",")...)
	tx.Statement.AddClause(clause.OrderBy{Columns: cols})
	return tx
}

// Limit caps the number of rows.
func (db *DB) Limit(n int) *DB {
	tx := db.getInstance()
	tx.Statement.AddClause(clause.Limit{N: n})
	return tx
}

// Offset skips rows.
func (db *DB) Offset(n int) *DB {
	tx := db.getInstance()
	tx.Statement.AddClause(clause.Offset{N: n})
	return tx
}

// Locking adds FOR UPDATE when enabled.
func (db *DB) Locking(enabled bool) *DB {
	tx := db.getInstance()
	tx.Statement.AddClause(clause.Locking{Enabled: enabled})
	return tx
}

// Clauses registers custom clause builders, replacing same-named clauses.
func (db *DB) Clauses(conds ...clause.Interface) *DB {
	tx := db.getInstance()
	for _, c := range conds {
		if c != nil {
			tx.Statement.AddClause(c)
		}
	}
	return tx
}

// Scopes applies chain functions lazily at finisher time (gorm-compatible).
func (db *DB) Scopes(funcs ...func(*DB) *DB) *DB {
	tx := db.getInstance()
	var all []func(*DB) *DB
	if prev, ok := tx.Statement.Get("fusion:scopes"); ok {
		all = append(all, prev.([]func(*DB) *DB)...)
	}
	all = append(all, funcs...)
	tx.Statement.Set("fusion:scopes", all)
	return tx
}

// CreateBatchSize sets the maximum rows per INSERT statement.
func (db *DB) CreateBatchSize(size int) *DB {
	tx := db.getInstance()
	tx.Statement.CreateBatchSize = size
	return tx
}

// Attrs supplies defaults used by FirstOrInit when no row is found.
func (db *DB) Attrs(values any) *DB {
	tx := db.getInstance()
	tx.Statement.Set("fusion:attrs", values)
	return tx
}

// Assign supplies values applied by FirstOrInit and FirstOrCreate.
func (db *DB) Assign(values any) *DB {
	tx := db.getInstance()
	tx.Statement.Set("fusion:assign", values)
	return tx
}

func applyScopes(db *DB) {
	value, ok := db.Statement.Get("fusion:scopes")
	if !ok {
		return
	}
	for _, fn := range value.([]func(*DB) *DB) {
		if fn == nil {
			continue
		}
		next := fn(db)
		if next == nil {
			continue
		}
		if next != db {
			db.Statement = next.Statement
			// Scopes run on their own instance; carry their error back so
			// it reaches the finisher chain instead of being dropped
			// (GORM #6283 class). First error wins.
			if next.Error != nil && db.Error == nil {
				db.Error = next.Error
			}
		}
	}
}
