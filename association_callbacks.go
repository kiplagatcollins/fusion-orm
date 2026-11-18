package fusion

import (
	"fmt"
	"reflect"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

func skipAssociationCallbacks(db *DB) bool {
	if db.Config.DryRun {
		return true
	}
	if _, ok := db.Statement.Get("fusion:skip_associations"); ok {
		return true
	}
	return false
}

func whereKeyValue(db *DB, column string) (any, bool) {
	where, ok := db.Statement.Clause("where").(clause.Where)
	if !ok {
		return nil, false
	}
	for _, cond := range where.Conds {
		for _, expr := range cond.Exprs {
			if expr.SQL == column+" = ?" && len(expr.Vars) > 0 {
				return expr.Vars[0], true
			}
			if expr.SQL == db.Statement.QuoteTo(column)+" = ?" && len(expr.Vars) > 0 {
				return expr.Vars[0], true
			}
		}
	}
	return nil, false
}

func associationValues(db *DB) (*schema.Table, []reflect.Value, bool) {
	table, err := db.modelTable()
	if err != nil {
		return nil, nil, false
	}
	values, err := modelValues(db.Statement.Dest)
	if err != nil {
		values, err = modelValues(db.Statement.Model)
	}
	if err != nil {
		return nil, nil, false
	}
	return table, values, true
}

func relationValues(value reflect.Value, rel *schema.Relation) []reflect.Value {
	if !value.IsValid() {
		return nil
	}
	field := fieldAt(value, rel.Field.Index)
	if !field.IsValid() {
		return nil
	}
	switch field.Kind() {
	case reflect.Pointer:
		if field.IsNil() || field.Type().Elem().Kind() != reflect.Struct {
			return nil
		}
		return []reflect.Value{field.Elem()}
	case reflect.Slice:
		out := make([]reflect.Value, 0, field.Len())
		for i := 0; i < field.Len(); i++ {
			item := field.Index(i)
			if item.Kind() == reflect.Pointer {
				if item.IsNil() {
					continue
				}
				item = item.Elem()
			}
			if item.Kind() == reflect.Struct {
				out = append(out, item)
			}
		}
		return out
	case reflect.Struct:
		if field.IsZero() {
			return nil
		}
		return []reflect.Value{field}
	}
	return nil
}

// saveBeforeAssociations resolves belongs-to links before the root row is
// written, so their foreign keys are available for INSERT.
func saveBeforeAssociations(db *DB) {
	if skipAssociationCallbacks(db) {
		return
	}
	table, values, ok := associationValues(db)
	if !ok {
		return
	}
	for _, value := range values {
		for _, rel := range table.Relations {
			if rel.Kind != schema.BelongsTo {
				continue
			}
			items := relationValues(value, rel)
			if len(items) == 0 {
				continue
			}
			assoc := &Association{db: db, table: table, rel: rel, source: value, fullSave: db.FullSaveAssociations}
			if _, ok := relationFieldValue(value, rel.ForeignKey); !ok {
				childTable, err := db.resolveTable(items[0].Addr().Interface())
				if err != nil {
					db.AddError(err)
					return
				}
				if err := assoc.linkOne(items[0], childTable); err != nil {
					db.AddError(err)
					return
				}
			}
		}
	}
}

// saveAfterAssociations writes has-one/has-many children and many-to-many
// junction rows once the root primary key is available.
func saveAfterAssociations(db *DB) {
	if skipAssociationCallbacks(db) {
		return
	}
	table, values, ok := associationValues(db)
	if !ok {
		return
	}
	for _, value := range values {
		for _, rel := range table.Relations {
			if rel.Kind == schema.BelongsTo {
				continue
			}
			items := relationValues(value, rel)
			if len(items) == 0 {
				continue
			}
			assoc := &Association{db: db, table: table, rel: rel, source: value, fullSave: db.FullSaveAssociations}
			for _, item := range items {
				childTable, err := db.resolveTable(item.Addr().Interface())
				if err != nil {
					db.AddError(err)
					return
				}
				if err := assoc.linkOne(item, childTable); err != nil {
					db.AddError(fmt.Errorf("save %s: %w", rel.Name, err))
					return
				}
			}
		}
	}
}

// deleteBeforeAssociations removes dependent rows and junction entries while
// leaving the related records themselves intact where possible.
func deleteBeforeAssociations(db *DB) {
	if skipAssociationCallbacks(db) {
		return
	}
	table, values, ok := associationValues(db)
	if !ok {
		return
	}
	for _, value := range values {
		for _, rel := range table.Relations {
			switch rel.Kind {
			case schema.HasOne, schema.HasMany:
				key, ok := relationFieldValue(value, rel.Reference)
				if !ok {
					key, ok = whereKeyValue(db, rel.Reference.DBName)
				}
				if !ok {
					continue
				}
				column := ""
				polyType := ""
				if rel.Polymorphic {
					polyValue := rel.PolymorphicValue
					if polyValue == "" {
						polyValue = db.NamingStrategy.ColumnName("", table.ModelName)
					}
					column = db.NamingStrategy.ColumnName("", polyValue+"_id")
					typeColumn := db.NamingStrategy.ColumnName("", polyValue+"_type")
					if rel.PolymorphicField != nil {
						typeColumn = rel.PolymorphicField.DBName
					}
					polyType = typeColumn
				} else if rel.ForeignKey != nil {
					column = rel.ForeignKey.DBName
				}
				if column == "" {
					continue
				}
				tx := db.Session(&Session{NewDB: true}).getInstance()
				tx.Statement.Set("fusion:skip_associations", true)
				res := tx.Where(column+" = ?", key)
				if polyType != "" {
					res = res.Where(polyType+" = ?", rel.PolymorphicValue)
				}
				if err := res.Delete(reflect.New(rel.JoinTable.Type).Interface()).Error; err != nil {
					db.AddError(err)
					return
				}
			case schema.ManyToMany:
				key, ok := relationFieldValue(value, rel.Reference)
				if !ok {
					key, ok = whereKeyValue(db, rel.Reference.DBName)
				}
				if !ok {
					continue
				}
				spec := db.m2mSpec(rel)
				tx := db.Session(&Session{NewDB: true}).getInstance()
				tx.Statement.Table = spec.table
				tx.Statement.AddClause(clause.Delete{Table: spec.table})
				appendWhere(tx, spec.fkCol+" = ?", key)
				tx.Statement.BuildClauses()
				if err := tx.Exec(string(tx.Statement.SQL), tx.Statement.Vars...).Error; err != nil {
					db.AddError(err)
					return
				}
			}
		}
	}
}
