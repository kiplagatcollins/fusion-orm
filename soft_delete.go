package fusion

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// DeletedAt is a nullable soft-delete timestamp. Its zero value means the row
// is live; Value renders it as NULL so the default query condition is
// "deleted_at IS NULL".
type DeletedAt struct {
	Time  time.Time
	Valid bool
}

// Value implements driver.Valuer.
func (d DeletedAt) Value() (driver.Value, error) {
	if !d.Valid {
		return nil, nil
	}
	return d.Time, nil
}

// Scan implements sql.Scanner.
func (d *DeletedAt) Scan(src any) error {
	if src == nil {
		d.Time, d.Valid = time.Time{}, false
		return nil
	}
	switch value := src.(type) {
	case time.Time:
		d.Time, d.Valid = value, true
		return nil
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return err
		}
		d.Time, d.Valid = parsed, true
		return nil
	case []byte:
		return d.Scan(string(value))
	default:
		return fmt.Errorf("cannot scan %T into DeletedAt", src)
	}
}

// IsZero reports whether the row is live.
func (d DeletedAt) IsZero() bool { return !d.Valid }

func softDeleteField(table *schema.Table) *schema.Field {
	for _, field := range table.Fields {
		if field.SoftDelete {
			return field
		}
	}
	return nil
}

func isUnscoped(db *DB) bool {
	_, ok := db.Statement.Get("fusion:unscoped")
	return ok
}

func softValue(field *schema.Field, raw string) any {
	if raw == "" {
		return nil
	}
	switch field.IndirectType.Kind() {
	case reflect.Bool:
		if v, err := strconv.ParseBool(raw); err == nil {
			return v
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return v
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return v
		}
	case reflect.Float32, reflect.Float64:
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
	}
	return raw
}

func softDeleteCondition(db *DB, field *schema.Field) clause.Expression {
	column := db.Statement.QuoteTo(field.DBName)
	if field.SoftDeleteZero != "" {
		return clause.Expr(column+" = ?", softValue(field, field.SoftDeleteZero))
	}
	if field.IsPtr || field.IndirectType.Kind() == reflect.Struct {
		return clause.Expr(column + " IS NULL")
	}
	if field.IndirectType == reflect.TypeFor[time.Time]() {
		return clause.Expr(column+" = ?", time.Time{})
	}
	return clause.Expr(column + " IS NULL")
}

func softDeleteValue(db *DB, field *schema.Field) any {
	if field.SoftDeleteValue != "" {
		return softValue(field, field.SoftDeleteValue)
	}
	switch field.IndirectType.Kind() {
	case reflect.Bool:
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return 1
	case reflect.String:
		return "deleted"
	default:
		return db.NowFunc()
	}
}

func injectSoftDeleteFilter(db *DB, table *schema.Table) {
	if isUnscoped(db) {
		return
	}
	field := softDeleteField(table)
	if field == nil {
		return
	}
	condition := softDeleteCondition(db, field)
	expr := condition.SQL
	where, _ := db.Statement.Clause("where").(clause.Where)
	for _, existing := range where.Conds {
		for _, candidate := range existing.Exprs {
			if candidate.SQL == expr {
				return
			}
		}
	}
	where.Conds = append(where.Conds, clause.Condition{
		Exprs: []clause.Expression{condition},
	})
	db.Statement.AddClause(where)
}

// rewriteSoftDelete converts DELETE into an UPDATE of the soft-delete column.
// It reports whether the rewrite happened.
func rewriteSoftDelete(db *DB, table *schema.Table) bool {
	if isUnscoped(db) {
		return false
	}
	field := softDeleteField(table)
	if field == nil {
		return false
	}
	injectSoftDeleteFilter(db, table)
	db.Statement.RemoveClause("delete")
	db.Statement.AddClause(clause.Update{Table: db.tableName(table)})
	db.Statement.AddClause(clause.Set{Assignments: []clause.Assignment{{
		Column: field.DBName,
		Value:  clause.Expr("?", softDeleteValue(db, field)),
	}}})
	return true
}

func softZeroFor(field *schema.Field) any {
	if field.SoftDeleteZero != "" {
		return softValue(field, field.SoftDeleteZero)
	}
	switch field.IndirectType.Kind() {
	case reflect.Bool:
		return false
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return 0
	case reflect.String:
		return ""
	default:
		return nil
	}
}

// Restore undeletes rows that were soft-deleted.
func (db *DB) Restore(value any) *DB {
	tx := db.Session(&Session{NewDB: true}).Unscoped()
	tx.Statement.Model = value
	table, err := tx.modelTable()
	if err != nil {
		tx.AddError(err)
		return tx
	}
	field := softDeleteField(table)
	if field == nil {
		tx.AddError(fmt.Errorf("%w: %s has no soft-delete field", ErrInvalidValue, table.Name))
		return tx
	}
	values, err := modelValues(value)
	if err != nil {
		tx.AddError(err)
		return tx
	}
	pk := primaryField(table)
	if pk == nil {
		tx.AddError(fmt.Errorf("%w: %s has no primary key", ErrInvalidValue, table.Name))
		return tx
	}
	for _, item := range values {
		fv := fieldAt(item, pk.Index)
		if !fv.IsValid() || !fv.CanInterface() {
			continue
		}
		res := tx.Where(pk.DBName+" = ?", fv.Interface()).
			Update(field.DBName, softZeroFor(field))
		if res.Error != nil {
			tx.AddError(res.Error)
			return tx
		}
	}
	return tx
}
