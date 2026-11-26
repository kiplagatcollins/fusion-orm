package fusion

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// pruneJoinedRelations clears pointer fields mounted from an empty LEFT JOIN
// row (all joined columns were NULL).
func pruneJoinedRelations(db *DB, table *schema.Table, dest any) {
	if len(db.Statement.Joins) == 0 || dest == nil {
		return
	}
	values, err := modelValues(dest)
	if err != nil {
		return
	}
	for _, entry := range db.Statement.Joins {
		if entry.HasRaw {
			continue
		}
		rel := relationFor(table, entry.Relation)
		if rel == nil {
			continue
		}
		for _, value := range values {
			field := fieldAt(value, rel.Field.Index)
			if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Pointer || field.IsNil() {
				continue
			}
			if field.Elem().Kind() == reflect.Struct && field.Elem().IsZero() {
				field.Set(reflect.Zero(field.Type()))
			}
		}
	}
}

// applyJoins expands relation joins into JOIN clauses and, when the caller
// has not selected columns explicitly, qualifies the root columns and aliases
// each joined column as alias__column for the scanning engine.
func applyJoins(db *DB, table *schema.Table) error {
	if len(db.Statement.Joins) == 0 {
		return nil
	}
	root := db.tableName(table)
	seen := make(map[string]bool)
	var joined []*schema.Relation
	items, _ := db.Statement.Clause("join").(clause.Joins)
	items.Items = append([]clause.Join(nil), items.Items...)
	for _, item := range items.Items {
		if item.HasRaw {
			seen["raw:"+item.Raw.SQL] = true
		} else {
			seen["rel:"+strings.ToLower(item.Alias)] = true
		}
	}
	for _, entry := range db.Statement.Joins {
		if entry.HasRaw {
			key := "raw:" + entry.Raw.SQL
			if seen[key] {
				continue
			}
			seen[key] = true
			items.Items = append(items.Items, clause.Join{HasRaw: true, Raw: entry.Raw})
			continue
		}
		rel := relationFor(table, entry.Relation)
		if rel == nil {
			return fmt.Errorf("%w: %s.%s", ErrInvalidRelation, table.Name, entry.Relation)
		}
		if rel.Kind != schema.HasOne && rel.Kind != schema.BelongsTo {
			return fmt.Errorf("%w: %s.%s: inline joins support has-one and belongs-to only",
				ErrInvalidRelation, table.Name, entry.Relation)
		}
		alias := strings.ToLower(rel.Name)
		key := "rel:" + alias
		if seen[key] {
			continue
		}
		seen[key] = true
		joined = append(joined, rel)

		sourceColumn, targetColumn := rel.Reference.DBName, rel.ForeignKey.DBName
		if rel.Kind == schema.BelongsTo {
			sourceColumn, targetColumn = rel.ForeignKey.DBName, rel.Reference.DBName
		}
		on := clause.Expr(fmt.Sprintf("%s.%s = %s.%s",
			db.Statement.QuoteTo(root), sourceColumn,
			db.Statement.QuoteTo(alias), targetColumn))
		// Inline joins must filter soft-deleted relation rows the same way
		// preload does, otherwise a deleted profile still loads (GORM #6998).
		onExprs := []clause.Expression{on}
		if !isUnscoped(db) {
			if soft := rel.JoinTable.SoftDeleteField; soft != nil {
				qualified := db.Statement.QuoteTo(alias) + "." + db.Statement.QuoteTo(soft.DBName)
				onExprs = append(onExprs, softDeleteCondition(db, soft, qualified))
			}
		}
		items.Items = append(items.Items, clause.Join{
			Type: "LEFT", Table: rel.JoinTable.Name, Alias: alias, On: onExprs,
		})
	}
	if len(items.Items) > 0 {
		db.Statement.AddClause(items)
	}
	if len(joined) == 0 || db.Statement.Clause("select") != nil {
		return nil
	}

	q := &db.Statement
	cols := make([]string, 0, len(table.Fields)+8)
	for _, field := range table.Fields {
		cols = append(cols, q.QuoteTo(root+"."+field.DBName)+" AS "+q.QuoteTo(field.DBName))
	}
	for _, rel := range joined {
		alias := strings.ToLower(rel.Name)
		for _, field := range rel.JoinTable.Fields {
			cols = append(cols,
				q.QuoteTo(alias+"."+field.DBName)+" AS "+q.QuoteTo(alias+"__"+field.DBName))
		}
	}
	db.Statement.AddClause(clause.Select{Columns: cols})
	return nil
}
