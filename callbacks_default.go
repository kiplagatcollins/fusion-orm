package fusion

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

func defaultCallback(name string) Callback {
	switch name {
	case "fusion:begin_tx":
		return beginDefaultTx
	case "fusion:commit_or_rollback":
		return commitOrRollback
	case "fusion:before_create":
		return namedHook("BeforeCreate")
	case "fusion:after_create":
		return namedHook("AfterCreate")
	case "fusion:before_update":
		return namedHook("BeforeUpdate")
	case "fusion:after_update":
		return namedHook("AfterUpdate")
	case "fusion:before_delete":
		return namedHook("BeforeDelete")
	case "fusion:after_delete":
		return namedHook("AfterDelete")
	case "fusion:before_query":
		return namedHook("BeforeFind")
	case "fusion:after_query":
		return namedHook("AfterFind")
	case "fusion:build_query":
		return buildQuery
	case "fusion:query":
		return queryRows
	case "fusion:preload":
		return func(db *DB) { db.preload() }
	case "fusion:save_before_associations":
		return saveBeforeAssociations
	case "fusion:save_after_associations":
		return saveAfterAssociations
	case "fusion:delete_before_associations":
		return deleteBeforeAssociations
	case "fusion:create":
		return insertRows
	case "fusion:update":
		return updateRows
	case "fusion:delete":
		return deleteRows
	case "fusion:row":
		return queryRowsForRow
	case "fusion:raw":
		return execRaw
	default:
		return nil
	}
}

func beginDefaultTx(db *DB) {
	if db.SkipDefaultTransaction || db.Config.DryRun {
		return
	}
	if _, ok := db.Statement.ConnPool.(*sql.Tx); ok {
		return
	}
	original := db.Statement.ConnPool
	tx := db.Begin()
	if tx.Error != nil {
		db.AddError(fmt.Errorf("begin transaction: %w", tx.Error))
		return
	}
	db.Statement.ConnPool = tx.Statement.ConnPool
	db.Statement.Set("fusion:tx", tx)
	db.Statement.Set("fusion:tx:pool", original)
}

func commitOrRollback(db *DB) {
	value, ok := db.Statement.Get("fusion:tx")
	if !ok {
		return
	}
	tx, ok := value.(*DB)
	if !ok {
		db.Statement.Delete("fusion:tx")
		return
	}
	db.Statement.Delete("fusion:tx")
	if db.Error != nil {
		tx.Rollback()
	} else if err := tx.Commit().Error; err != nil {
		db.AddError(fmt.Errorf("commit transaction: %w", err))
	}
	if value, ok := db.Statement.Get("fusion:tx:pool"); ok {
		if original, ok := value.(ConnPool); ok {
			db.Statement.ConnPool = original
		}
		db.Statement.Delete("fusion:tx:pool")
	}
}

func namedHook(name string) Callback {
	return func(db *DB) {
		// Hook dispatch is opt-in by method presence: skip the reflection
		// (modelValues + MethodByName) entirely for models without hooks.
		table, err := db.modelTable()
		if err != nil {
			return
		}
		if !table.Hooks.Has(name) {
			return
		}
		model := db.Statement.Model
		if model == nil {
			model = db.Statement.Dest
		}
		values, err := modelValues(model)
		if err != nil {
			return
		}
		callHooksOnValues(db, values, name)
	}
}

func buildQuery(db *DB) {
	table, err := db.modelTable()
	if err != nil {
		db.AddError(err)
		return
	}
	if err := applyJoins(db, table); err != nil {
		db.AddError(err)
		return
	}
	injectSoftDeleteFilter(db, table)
	if db.Statement.Clause("from") == nil {
		db.Statement.AddClause(clause.From{Table: db.tableName(table)})
	}
	if db.Statement.Clause("select") == nil {
		db.Statement.AddClause(clause.Select{})
	}
	if db.Statement.Clause("where") == nil && db.Statement.Model != nil {
		if values, err := modelValues(db.Statement.Model); err == nil && len(values) == 1 {
			if pk := primaryField(table); pk != nil {
				if fv := fieldAt(values[0], pk.Index); fv.IsValid() && !pk.IsZero(fv) {
					db.addCondition(pk.DBName+" = ?", []any{fv.Interface()}, false)
				}
			}
		}
	}
	db.Statement.BuildClauses()
}

func queryRows(db *DB) {
	if db.Config.DryRun {
		return
	}
	table, err := db.modelTable()
	if err != nil {
		db.AddError(err)
		return
	}
	ctx := statementContext(db.Statement)
	rows, err := db.queryContext(ctx, string(db.Statement.SQL), db.Statement.Vars...)
	if err != nil {
		db.AddError(fmt.Errorf("query %s: %w", table.Name, err))
		return
	}
	defer rows.Close()

	count, scanErr := scanRows(rows, db.Statement.Dest, table)
	if scanErr != nil {
		db.AddError(fmt.Errorf("scan %s: %w", table.Name, scanErr))
	}
	pruneJoinedRelations(db, table, db.Statement.Dest)
	db.RowsAffected = int64(count)
	if count == 0 && db.Statement.RaiseErrorOnNotFound && db.Error == nil {
		db.AddError(ErrRecordNotFound)
	}
}

func queryRowsForRow(db *DB) {
	if db.Config.DryRun {
		return
	}
	rows, err := db.queryContext(statementContext(db.Statement), string(db.Statement.SQL), db.Statement.Vars...)
	if err != nil {
		db.AddError(fmt.Errorf("query rows: %w", err))
		return
	}
	db.Statement.Set("fusion:rows", rows)
}

func execRaw(db *DB) {
	if db.Config.DryRun {
		return
	}
	db.execSQL(string(db.Statement.SQL), db.Statement.Vars)
}

func (db *DB) execSQL(sql string, vars []any) {
	result, err := db.execContext(statementContext(db.Statement), sql, vars...)
	if err != nil {
		db.AddError(fmt.Errorf("exec %s: %w", firstSQLWord(sql), err))
	} else if result != nil {
		if n, raErr := result.RowsAffected(); raErr == nil {
			db.RowsAffected += n
		}
	}
}

func firstSQLWord(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "statement"
	}
	return strings.ToLower(fields[0])
}

func insertRows(db *DB) {
	table, err := db.modelTable()
	if err != nil {
		db.AddError(err)
		return
	}
	values, err := modelValues(db.Statement.Dest)
	if err != nil {
		db.AddError(err)
		return
	}
	for i := range values {
		setTimestamps(db, values[i], table, true)
		if err := applyPKGenerators(table, values[i]); err != nil {
			db.AddError(err)
			return
		}
	}
	returning := db.Dialector.Features().Has(feature.InsertReturning)
	// Resolve columns to fields once per statement, not once per row.
	columns := insertColumns(table)
	fields := make([]*schema.Field, len(columns))
	for i, col := range columns {
		fields[i] = table.Column(col)
	}
	ph := placeholdersFor(len(columns))
	batch := db.Statement.CreateBatchSize
	if batch <= 0 || batch > len(values) {
		batch = len(values)
	}
	for start := 0; start < len(values) && db.Error == nil; start += batch {
		end := min(start+batch, len(values))
		rows := make([]clause.Expression, 0, end-start)
		for _, value := range values[start:end] {
			vars := make([]any, len(columns))
			for i, field := range fields {
				if field == nil {
					continue
				}
				if fv := fieldAt(value, field.Index); fv.IsValid() {
					vars[i] = fv.Interface()
				}
			}
			rows = append(rows, clause.Expression{SQL: ph, Vars: vars})
		}
		db.Statement.AddClause(clause.Insert{Table: db.tableName(table), Columns: columns})
		db.Statement.AddClause(clause.Values{Rows: rows})
		if returning {
			if pk := primaryField(table); pk != nil {
				db.Statement.AddClause(clause.Returning{Columns: []string{pk.DBName}})
			}
		}
		db.Statement.BuildClauses()
		if db.Config.DryRun {
			continue
		}
		if returning {
			backfillFromReturning(db, values[start:end], table)
		} else {
			db.execSQL(string(db.Statement.SQL), db.Statement.Vars)
			if db.Error != nil {
				break
			}
			backfillFromLastInsertID(db, values[start:end], table)
		}
	}
}

func insertColumns(table *schema.Table) []string {
	return table.InsertColumns
}

// placeholderCache holds ready-made "(?, ?, ...)" bodies for common column
// counts so row building never rebuilds the same string.
var placeholderCache = func() []string {
	const max = 64
	out := make([]string, max+1)
	for n := 1; n <= max; n++ {
		var b strings.Builder
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('?')
		}
		out[n] = b.String()
	}
	return out
}()

func placeholdersFor(n int) string {
	if n <= len(placeholderCache)-1 {
		return placeholderCache[n]
	}
	return placeholders(n)
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('?')
	}
	return b.String()
}

func backfillFromReturning(db *DB, values []reflect.Value, table *schema.Table) {
	pk := primaryField(table)
	if pk == nil {
		return
	}
	rows, err := db.queryContext(statementContext(db.Statement), string(db.Statement.SQL), db.Statement.Vars...)
	if err != nil {
		db.AddError(fmt.Errorf("returning id: %w", err))
		return
	}
	defer rows.Close()
	i := 0
	for rows.Next() && i < len(values) {
		var id any
		if err := rows.Scan(&id); err != nil {
			db.AddError(fmt.Errorf("scan returning id: %w", err))
			return
		}
		setPrimaryKeyValue(values[i], table, pk, id)
		i++
	}
	db.RowsAffected += int64(i)
}

func backfillFromLastInsertID(db *DB, values []reflect.Value, table *schema.Table) {
	pk := primaryField(table)
	if pk == nil || len(values) == 0 {
		return
	}
	// The last insert id is only reliable for a single-row statement.
	if len(values) != 1 {
		return
	}
	// Recover it via the driver result captured by execSQL.
	if raw, ok := db.Statement.Get("fusion:last_insert_id"); ok {
		if id, ok := raw.(int64); ok && id != 0 {
			setPrimaryKeyValue(values[0], table, pk, id)
		}
	}
}

// applyPKGenerators fills zero primary keys whose dialect-agnostic generator
// was resolved at table init (uuid v7). Pre-set ids are respected.
func applyPKGenerators(table *schema.Table, value reflect.Value) error {
	for _, field := range table.Fields {
		if field.PKGenerator == nil {
			continue
		}
		fv := fieldAt(value, field.Index)
		if !fv.IsValid() || !fv.CanSet() || !field.IsZero(fv) {
			continue
		}
		if err := field.PKGenerator(fv); err != nil {
			return err
		}
	}
	return nil
}

func setPrimaryKeyValue(value reflect.Value, table *schema.Table, pk *schema.Field, id any) {
	fv := fieldAt(value, pk.Index)
	if !fv.IsValid() || !fv.CanSet() {
		return
	}
	src := reflect.ValueOf(id)
	if src.Type().AssignableTo(fv.Type()) {
		fv.Set(src)
	} else if src.Type().ConvertibleTo(fv.Type()) {
		fv.Set(src.Convert(fv.Type()))
	}
}

func guardConditions(db *DB, table *schema.Table) bool {
	if where, ok := db.Statement.Clause("where").(clause.Where); ok && len(where.Conds) > 0 {
		return true
	}
	if db.AllowGlobalUpdate {
		return true
	}
	if values, err := modelValues(db.Statement.Dest); err == nil && len(values) == 1 {
		if exprs := structConditions(db, values[0].Interface()); len(exprs) > 0 {
			prev, _ := db.Statement.Clause("where").(clause.Where)
			conds := append(append([]clause.Condition(nil), prev.Conds...), clause.Condition{Join: "AND", Exprs: exprs})
			db.Statement.AddClause(clause.Where{Conds: conds})
			return true
		}
	}
	db.AddError(ErrMissingWhereClause)
	return false
}

func updateRows(db *DB) {
	table, err := db.modelTable()
	if err != nil {
		db.AddError(err)
		return
	}
	set, ok := db.Statement.Clause("set").(clause.Set)
	if !ok || len(set.Assignments) == 0 {
		return
	}
	if _, skip := db.Statement.Get("fusion:skip_timestamps"); !skip {
		for _, field := range table.Fields {
			if !field.AutoUpdateTime {
				continue
			}
			alreadySet := false
			for _, assignment := range set.Assignments {
				if assignment.Column == field.DBName {
					alreadySet = true
				}
			}
			if !alreadySet {
				set.Assignments = append(set.Assignments, clause.Assignment{
					Column: field.DBName,
					Value:  clause.Expr("?", db.NowFunc()),
				})
			}
		}
		db.Statement.AddClause(set)
	}
	if !guardConditions(db, table) {
		return
	}
	db.Statement.AddClause(clause.Update{Table: db.tableName(table)})
	db.Statement.BuildClauses()
	if db.Config.DryRun {
		return
	}
	db.execSQL(string(db.Statement.SQL), db.Statement.Vars)
}

func deleteRows(db *DB) {
	table, err := db.modelTable()
	if err != nil {
		db.AddError(err)
		return
	}
	// The guard must run before the soft-delete rewrite: rewriting injects
	// "deleted_at IS NULL", which would otherwise satisfy the guard and turn
	// a condition-less delete into a global soft delete (GORM #6338 class).
	if !guardConditions(db, table) {
		return
	}
	rewritten := rewriteSoftDelete(db, table)
	if !rewritten {
		db.Statement.AddClause(clause.Delete{Table: db.tableName(table)})
	}
	db.Statement.BuildClauses()
	if db.Config.DryRun {
		return
	}
	db.execSQL(string(db.Statement.SQL), db.Statement.Vars)
}
