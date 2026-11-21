package fusion

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// Create inserts value (a struct or slice of structs).
func (db *DB) Create(value any) *DB {
	tx := db.getInstance()
	tx.Statement.Dest = value
	tx.callbacks.create.Execute(tx)
	return tx
}

// Save inserts value when its primary key is zero and updates it otherwise.
func (db *DB) Save(value any) *DB {
	table, err := db.resolveTable(value)
	if err != nil {
		tx := db.getInstance()
		tx.AddError(err)
		return tx
	}
	values, err := modelValues(value)
	if err != nil {
		tx := db.getInstance()
		tx.AddError(err)
		return tx
	}
	for _, v := range values {
		if pk := primaryField(table); pk != nil {
			if fv := fieldAt(v, pk.Index); fv.IsValid() && !pk.IsZero(fv) {
				return db.Where(pk.DBName+" = ?", fv.Interface()).Updates(value)
			}
		}
	}
	return db.Create(value)
}

// Find scans all matching rows into dest. A miss is not an error.
func (db *DB) Find(dest any, conds ...any) *DB {
	tx := db.getInstance()
	tx.Statement.Dest = dest
	if tx.Statement.Model == nil {
		tx.Statement.Model = dest
	}
	tx = applyConds(tx, conds...)
	applyScopes(tx)
	tx.callbacks.query.Execute(tx)
	return tx
}

// First scans the first row ordered by primary key; a miss returns
// ErrRecordNotFound.
func (db *DB) First(dest any, conds ...any) *DB {
	return db.firstWithOrder(dest, "ASC", conds...)
}

// Take scans the first row without implicit ordering.
func (db *DB) Take(dest any, conds ...any) *DB {
	tx := db.getInstance()
	tx.Statement.RaiseErrorOnNotFound = true
	tx.Statement.Dest = dest
	if tx.Statement.Model == nil {
		tx.Statement.Model = dest
	}
	tx = applyConds(tx, conds...)
	tx.Statement.AddClause(clause.Limit{N: 1})
	tx.callbacks.query.Execute(tx)
	return tx
}

// Last scans the last row ordered by primary key descending.
func (db *DB) Last(dest any, conds ...any) *DB {
	return db.firstWithOrder(dest, "DESC", conds...)
}

func (db *DB) firstWithOrder(dest any, direction string, conds ...any) *DB {
	tx := db.getInstance()
	tx.Statement.RaiseErrorOnNotFound = true
	tx.Statement.Dest = dest
	if tx.Statement.Model == nil {
		tx.Statement.Model = dest
	}
	tx = applyConds(tx, conds...)
	if table, err := db.resolveTable(tx.Statement.Model); err == nil {
		if pk := primaryField(table); pk != nil {
			tx.Statement.AddClause(clause.OrderBy{Columns: []string{pk.DBName + " " + direction}})
		}
	}
	tx.Statement.AddClause(clause.Limit{N: 1})
	tx.callbacks.query.Execute(tx)
	return tx
}

func applyConds(db *DB, conds ...any) *DB {
	table, err := db.modelTable()
	if err != nil {
		return db
	}
	if len(conds) > 0 {
		if query, ok := conds[0].(string); ok && strings.Contains(query, "?") {
			return db.Where(query, conds[1:]...)
		}
	}
	for _, cond := range conds {
		switch v := cond.(type) {
		case string:
			db = db.Where(v)
		case int:
			if pk := primaryField(table); pk != nil {
				db = db.Where(pk.DBName+" = ?", int64(v))
			}
		case int64:
			if pk := primaryField(table); pk != nil {
				db = db.Where(pk.DBName+" = ?", v)
			}
		default:
			if cond != nil {
				db = db.Where(cond)
			}
		}
	}
	return db
}

// Update sets one column on matching rows. A WHERE clause (or a model with
// a primary key) is required unless AllowGlobalUpdate is set.
func (db *DB) Update(column string, value any) *DB {
	return db.Updates(map[string]any{column: value})
}

// Updates sets columns from a map or struct. A WHERE clause (or a model with
// a primary key) is required unless AllowGlobalUpdate is set.
func (db *DB) Updates(values any) *DB {
	tx := db.getInstance()
	if tx.Statement.Model == nil && tx.Statement.Dest == nil {
		tx.Statement.Model = values
	}
	table, err := tx.modelTable()
	if err != nil {
		tx.AddError(err)
		return tx
	}
	assignments := assignmentsFrom(tx, table, values)
	if len(assignments) == 0 {
		tx.AddError(fmt.Errorf("%w: no values to update", ErrInvalidValue))
		return tx
	}
	tx.Statement.AddClause(clause.Set{Assignments: assignments})
	tx.callbacks.update.Execute(tx)
	return tx
}

// UpdateColumn is Updates without hooks or automatic timestamps.
func (db *DB) UpdateColumn(column string, value any) *DB {
	return db.UpdateColumns(map[string]any{column: value})
}

// UpdateColumns is Updates without hooks or automatic timestamps.
func (db *DB) UpdateColumns(values any) *DB {
	tx := db.getInstance()
	tx.Statement.SkipHooks = true
	tx.Statement.Set("fusion:skip_timestamps", true)
	return tx.Updates(values)
}

func assignmentsFrom(db *DB, table *schema.Table, values any) []clause.Assignment {
	if values == nil {
		return nil
	}
	rv := reflect.ValueOf(values)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	var out []clause.Assignment
	switch rv.Kind() {
	case reflect.Map:
		iter := rv.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			field := table.Column(key)
			if field == nil {
				field = table.Field(key)
			}
			if field == nil {
				db.AddError(fmt.Errorf("%w: unknown column %q", ErrInvalidValue, key))
				continue
			}
			out = append(out, clause.Assignment{Column: field.DBName, Value: clause.Expr("?", iter.Value().Interface())})
		}
	case reflect.Struct:
		for _, field := range table.Fields {
			fv := fieldAt(rv, field.Index)
			if !fv.IsValid() || field.IsZero(fv) || field.PrimaryKey {
				continue
			}
			out = append(out, clause.Assignment{Column: field.DBName, Value: clause.Expr("?", fv.Interface())})
		}
	default:
		db.AddError(fmt.Errorf("%w: Updates expects a map or struct", ErrInvalidValue))
	}
	return out
}

// Delete removes matching rows. A WHERE clause (or value with a primary
// key) is required unless AllowGlobalUpdate is set.
func (db *DB) Delete(value any, conds ...any) *DB {
	tx := db.getInstance()
	if value != nil {
		tx.Statement.Dest = value
		if tx.Statement.Model == nil {
			tx.Statement.Model = value
		}
	}
	tx = applyConds(tx, conds...)
	tx.callbacks.delete.Execute(tx)
	return tx
}

// Count stores the number of matching rows in count.
func (db *DB) Count(count *int64) *DB {
	tx := db.getInstance()
	tx.Statement.Dest = count
	tx.Statement.AddClause(clause.Select{Columns: []string{"COUNT(*)"}})
	// ORDER BY is invalid next to COUNT(*) on PostgreSQL (the ordering
	// column must appear in GROUP BY); drop whatever the caller or a scope
	// provided. GROUP BY/HAVING are left intact. GORM #7516.
	tx.Statement.RemoveClause("order")
	tx.callbacks.query.Execute(tx)
	return tx
}

// Pluck scans one column into dest (a slice or scalar).
func (db *DB) Pluck(column string, dest any) *DB {
	tx := db.getInstance()
	tx.Statement.Dest = dest
	tx.Statement.AddClause(clause.Select{Columns: []string{column}})
	tx.callbacks.query.Execute(tx)
	return tx
}

// Row builds the query and returns the first row for manual scanning.
func (db *DB) Row() *sql.Row {
	tx := db.getInstance()
	applyScopes(tx)
	buildQuery(tx)
	if tx.Error != nil {
		return nil
	}
	return tx.queryRowContext(statementContext(tx.Statement), string(tx.Statement.SQL), tx.Statement.Vars...)
}

// Rows builds the query through the row processor and returns the result
// set; the caller must close it.
func (db *DB) Rows() (*sql.Rows, error) {
	tx := db.getInstance()
	applyScopes(tx)
	buildQuery(tx)
	if tx.Error != nil {
		return nil, tx.Error
	}
	tx.callbacks.row.Execute(tx)
	if tx.Error != nil {
		return nil, tx.Error
	}
	value, ok := tx.Statement.Get("fusion:rows")
	if !ok || value == nil {
		return nil, fmt.Errorf("query rows: no result set")
	}
	return value.(*sql.Rows), nil
}

// Scan runs the accumulated query into dest.
func (db *DB) Scan(dest any) *DB {
	tx := db.getInstance()
	tx.Statement.Dest = dest
	applyScopes(tx)
	tx.callbacks.query.Execute(tx)
	return tx
}

// FindInBatches queries in primary-key order, calling fc for each batch.
func (db *DB) FindInBatches(dest any, batchSize int, fc func(tx *DB, batch int) error) *DB {
	tx := db.getInstance()
	if batchSize <= 0 {
		tx.AddError(fmt.Errorf("%w: batch size must be positive", ErrInvalidValue))
		return tx
	}
	table, err := db.resolveTable(dest)
	if err != nil {
		tx.AddError(err)
		return tx
	}
	pk := primaryField(table)
	if pk == nil {
		tx.AddError(fmt.Errorf("%w: model %s has no primary key", ErrInvalidValue, table.Name))
		return tx
	}

	destType := reflect.TypeOf(dest)
	if destType.Kind() != reflect.Pointer {
		tx.AddError(fmt.Errorf("%w: FindInBatches needs a pointer to a slice", ErrInvalidValue))
		return tx
	}
	base := db.getInstance()
	var last any
	page := 0
	for {
		batch := base.Session(&Session{})
		fresh := reflect.New(destType.Elem()).Interface()
		batch.Statement.Dest = fresh
		batch.Statement.Model = fresh
		if last != nil {
			batch = batch.Where(pk.DBName+" > ?", last)
		}
		batch = batch.Order(pk.DBName + " ASC")
		batch = batch.Limit(batchSize)
		batch.callbacks.query.Execute(batch)
		if batch.Error != nil {
			tx.AddError(batch.Error)
			break
		}
		if batch.RowsAffected == 0 {
			break
		}
		page++
		if err := fc(batch, page); err != nil {
			tx.AddError(err)
			break
		}
		values, err := modelValues(fresh)
		if err != nil || len(values) == 0 {
			break
		}
		fv := fieldAt(values[len(values)-1], pk.Index)
		if !fv.IsValid() {
			break
		}
		last = fv.Interface()
		if batch.RowsAffected < int64(batchSize) {
			break
		}
	}
	tx.RowsAffected = int64(page)
	return tx
}

// FirstOrInit finds the first matching row or initializes dest from the
// conditions and Attrs/Assign values without writing to the database.
func (db *DB) FirstOrInit(dest any, conds ...any) *DB {
	found := db.First(dest, conds...)
	if !errors.Is(found.Error, ErrRecordNotFound) {
		return found
	}
	tx := db.getInstance()
	tx = applyConds(tx, conds...)
	table, err := db.resolveTable(dest)
	if err != nil {
		tx.AddError(err)
		return tx
	}
	for _, key := range []string{"fusion:attrs", "fusion:assign"} {
		if value, ok := db.Statement.Get(key); ok {
			if err := assignValues(dest, value, table); err != nil {
				tx.AddError(err)
				return tx
			}
		}
	}
	if err := assignValues(dest, condMap(conds...), table); err != nil {
		tx.AddError(err)
	}
	return tx
}

// FirstOrCreate finds the first matching row or inserts an initialized dest.
func (db *DB) FirstOrCreate(dest any, conds ...any) *DB {
	found := db.FirstOrInit(dest, conds...)
	if found.Error != nil {
		return found
	}
	values, err := modelValues(dest)
	if err != nil {
		found.AddError(err)
		return found
	}
	table, err := db.resolveTable(dest)
	if err != nil {
		found.AddError(err)
		return found
	}
	if pk := primaryField(table); pk != nil {
		for _, value := range values {
			if fv := fieldAt(value, pk.Index); fv.IsValid() && !pk.IsZero(fv) {
				return found
			}
		}
	}
	return db.Create(dest)
}

func condMap(conds ...any) map[string]any {
	out := map[string]any{}
	for _, cond := range conds {
		if m, ok := cond.(map[string]any); ok {
			for key, value := range m {
				out[key] = value
			}
		}
	}
	return out
}
