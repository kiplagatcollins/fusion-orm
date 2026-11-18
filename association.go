package fusion

import (
	"fmt"
	"reflect"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// Association operates on one relation of a single model. It mirrors gorm's
// db.Association(column) surface with error-returning finishers.
type Association struct {
	db       *DB
	table    *schema.Table
	rel      *schema.Relation
	source   reflect.Value
	fullSave bool
}

// Association starts an association operation on column of db's model. The
// model must be a single struct carrying a primary key.
func (db *DB) Association(column string) *Association {
	tx := db.getInstance()
	result := &Association{db: tx}
	table, err := tx.modelTable()
	if err != nil {
		tx.AddError(err)
		return result
	}
	rel := relationFor(table, column)
	if rel == nil {
		tx.AddError(fmt.Errorf("%w: %s.%s", ErrInvalidRelation, table.Name, column))
		return result
	}
	values, err := modelValues(tx.Statement.Model)
	if err != nil || len(values) != 1 {
		tx.AddError(fmt.Errorf("%w: Association requires one struct model", ErrInvalidValue))
		return result
	}
	result.table, result.rel, result.source = table, rel, values[0]
	return result
}

func (a *Association) ready() bool {
	return a != nil && a.db != nil && a.db.Error == nil && a.rel != nil
}

func (a *Association) fail(err error) {
	if a != nil && a.db != nil {
		a.db.AddError(err)
	}
}

// Find loads the relation into dest.
func (a *Association) Find(dest any) error {
	if !a.ready() {
		return a.error()
	}
	a.db.loadRelation([]reflect.Value{a.source}, a.rel, &preloadEntry{Field: a.rel.Name})
	if a.db.Error != nil {
		return a.error()
	}
	field := fieldAt(a.source, a.rel.Field.Index)
	if !field.IsValid() {
		return fmt.Errorf("%w: relation field %s", ErrInvalidValue, a.rel.Name)
	}
	return assignDest(dest, field)
}

func assignDest(dest any, value reflect.Value) error {
	if dest == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidValue)
	}
	out := reflect.ValueOf(dest)
	if out.Kind() != reflect.Pointer || out.IsNil() {
		return fmt.Errorf("%w: destination must be a non-nil pointer", ErrInvalidValue)
	}
	target := out.Elem()
	if value.Kind() == reflect.Pointer && !value.IsNil() && value.Type().Elem().AssignableTo(target.Type()) {
		target.Set(value.Elem())
		return nil
	}
	if value.Type().AssignableTo(target.Type()) {
		target.Set(value)
		return nil
	}
	if target.Kind() == reflect.Pointer && value.Type().AssignableTo(target.Type().Elem()) {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		target.Elem().Set(value)
		return nil
	}
	if target.Kind() == reflect.Slice && value.Kind() == reflect.Slice && value.Type().ConvertibleTo(target.Type()) {
		target.Set(value.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf("%w: cannot assign %s to %s", ErrInvalidValue, value.Type(), target.Type())
}

// Append inserts the given values and associates them with the source model.
func (a *Association) Append(values ...any) error {
	if !a.ready() {
		return a.error()
	}
	for _, value := range values {
		if err := a.appendOne(value); err != nil {
			a.fail(err)
			return err
		}
	}
	return nil
}

func (a *Association) appendOne(value any) error {
	items, err := modelValues(value)
	if err != nil {
		return err
	}
	childTable, err := a.db.resolveTable(value)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := a.linkOne(item, childTable); err != nil {
			return err
		}
	}
	return nil
}

func (a *Association) linkOne(item reflect.Value, childTable *schema.Table) error {
	switch a.rel.Kind {
	case schema.BelongsTo:
		fk := a.rel.ForeignKey
		var raw any
		if pk := primaryField(childTable); pk != nil {
			var ok bool
			raw, ok = relationFieldValue(item, pk)
			if !ok {
				if err := a.createRecord(item.Addr().Interface(), childTable); err != nil {
					return err
				}
				raw, _ = relationFieldValue(item, pk)
			}
		}
		return setFieldValue(a.source, fk, raw)
	case schema.HasOne, schema.HasMany:
		raw, ok := relationFieldValue(a.source, a.rel.Reference)
		if !ok {
			return fmt.Errorf("%w: source primary key is zero", ErrInvalidValue)
		}
		field := a.rel.ForeignKey
		if a.rel.Polymorphic {
			field = childTable.Column(a.db.NamingStrategy.ColumnName("", a.rel.PolymorphicValue+"_id"))
			if field == nil {
				return fmt.Errorf("%w: %s has no polymorphic id column", ErrInvalidRelation, childTable.Name)
			}
			typeField := childTable.Column(a.db.NamingStrategy.ColumnName("", a.rel.PolymorphicValue+"_type"))
			if typeField != nil {
				if err := setFieldValue(item, typeField, a.rel.PolymorphicValue); err != nil {
					return err
				}
			}
		}
		if field == nil {
			return fmt.Errorf("%w: %s has no foreign key column", ErrInvalidRelation, childTable.Name)
		}
		if err := setFieldValue(item, field, raw); err != nil {
			return err
		}
		return a.createRecord(item.Addr().Interface(), childTable)
	case schema.ManyToMany:
		var childKey any
		if pk := primaryField(childTable); pk != nil {
			var ok bool
			childKey, ok = relationFieldValue(item, pk)
			if !ok || a.fullSave {
				if err := a.createRecord(item.Addr().Interface(), childTable); err != nil {
					return err
				}
				childKey, _ = relationFieldValue(item, pk)
			}
		}
		sourceKey, ok := relationFieldValue(a.source, a.rel.Reference)
		if !ok {
			return fmt.Errorf("%w: source primary key is zero", ErrInvalidValue)
		}
		return a.insertJoinRow(sourceKey, childKey)
	}
	return nil
}

func (a *Association) createRecord(value any, table *schema.Table) error {
	tx := a.db.Session(&Session{NewDB: true}).getInstance()
	tx.Statement.SkipHooks = true
	tx.Statement.Set("fusion:skip_associations", true)
	useSave := false
	if a.fullSave {
		if values, err := modelValues(value); err == nil && len(values) > 0 {
			if pk := primaryField(table); pk != nil {
				if fv := fieldAt(values[0], pk.Index); fv.IsValid() && !pk.IsZero(fv) {
					useSave = true
				}
			}
		}
	}
	var res *DB
	if useSave {
		res = tx.Save(value)
	} else {
		res = tx.Create(value)
	}
	if res.Error != nil {
		return fmt.Errorf("save %s: %w", table.Name, res.Error)
	}
	return nil
}

func (a *Association) insertJoinRow(sourceKey, childKey any) error {
	spec := a.db.m2mSpec(a.rel)
	tx := a.db.Session(&Session{NewDB: true}).getInstance()
	tx.Statement.Table = spec.table
	tx.Statement.AddClause(clause.Insert{Table: spec.table, Columns: []string{spec.fkCol, spec.refCol}})
	tx.Statement.AddClause(clause.Values{Rows: []clause.Expression{clause.Expr("?, ?", sourceKey, childKey)}})
	tx.Statement.BuildClauses()
	res := tx.Exec(string(tx.Statement.SQL), tx.Statement.Vars...)
	return res.Error
}

// Replace removes existing relations and appends the given values.
func (a *Association) Replace(values ...any) error {
	if !a.ready() {
		return a.error()
	}
	if err := a.Clear(); err != nil {
		return err
	}
	return a.Append(values...)
}

// Delete removes the given values from the relation. With no arguments it
// clears the whole relation.
func (a *Association) Delete(values ...any) error {
	if !a.ready() {
		return a.error()
	}
	if len(values) == 0 {
		return a.Clear()
	}
	switch a.rel.Kind {
	case schema.ManyToMany:
		spec := a.db.m2mSpec(a.rel)
		sourceKey, _ := relationFieldValue(a.source, a.rel.Reference)
		for _, value := range values {
			items, err := modelValues(value)
			if err != nil {
				a.fail(err)
				return err
			}
			table, err := a.db.resolveTable(value)
			if err != nil {
				a.fail(err)
				return err
			}
			for _, item := range items {
				key, ok := relationFieldValue(item, primaryField(table))
				if !ok {
					continue
				}
				tx := a.db.Session(&Session{NewDB: true}).getInstance()
				tx.Statement.Table = spec.table
				tx.Statement.AddClause(clause.Delete{Table: spec.table})
				appendWhere(tx, spec.fkCol+" = ?", sourceKey)
				appendWhere(tx, spec.refCol+" = ?", key)
				tx.Statement.BuildClauses()
				if err := tx.Exec(string(tx.Statement.SQL), tx.Statement.Vars...).Error; err != nil {
					a.fail(err)
					return err
				}
			}
		}
	case schema.BelongsTo:
		field := a.rel.ForeignKey
		if fv := fieldAt(a.source, field.Index); fv.IsValid() && fv.CanSet() {
			fv.Set(reflect.Zero(fv.Type()))
		}
	default:
		for _, value := range values {
			items, err := modelValues(value)
			if err != nil {
				a.fail(err)
				return err
			}
			table, err := a.db.resolveTable(value)
			if err != nil {
				a.fail(err)
				return err
			}
			for _, item := range items {
				key, ok := relationFieldValue(item, primaryField(table))
				if !ok {
					continue
				}
				pk := primaryField(table)
				if err := a.db.Session(&Session{NewDB: true}).
					Where(pk.DBName+" = ?", key).Delete(reflect.New(table.Type).Interface()).Error; err != nil {
					a.fail(err)
					return err
				}
			}
		}
	}
	return nil
}

// Clear removes all relations from the source model without deleting the
// related records themselves.
func (a *Association) Clear() error {
	if !a.ready() {
		return a.error()
	}
	switch a.rel.Kind {
	case schema.BelongsTo:
		field := a.rel.ForeignKey
		if fv := fieldAt(a.source, field.Index); fv.IsValid() && fv.CanSet() {
			fv.Set(reflect.Zero(fv.Type()))
		}
	case schema.ManyToMany:
		spec := a.db.m2mSpec(a.rel)
		sourceKey, _ := relationFieldValue(a.source, a.rel.Reference)
		tx := a.db.Session(&Session{NewDB: true}).getInstance()
		tx.Statement.Table = spec.table
		tx.Statement.AddClause(clause.Delete{Table: spec.table})
		appendWhere(tx, spec.fkCol+" = ?", sourceKey)
		tx.Statement.BuildClauses()
		if err := tx.Exec(string(tx.Statement.SQL), tx.Statement.Vars...).Error; err != nil {
			a.fail(err)
			return err
		}
	default:
		sourceKey, ok := relationFieldValue(a.source, a.rel.Reference)
		if !ok {
			return nil
		}
		field := a.rel.ForeignKey
		if a.rel.Polymorphic {
			column := a.db.NamingStrategy.ColumnName("", a.rel.PolymorphicValue+"_id")
			field = a.rel.JoinTable.Column(column)
		}
		if field == nil {
			return nil
		}
		tx := a.db.Session(&Session{NewDB: true}).getInstance()
		tx.Statement.Table = a.rel.JoinTable.Name
		tx.Statement.AddClause(clause.Update{Table: a.rel.JoinTable.Name})
		tx.Statement.AddClause(clause.Set{Assignments: []clause.Assignment{{
			Column: field.DBName,
			Value:  clause.Expr("?", reflect.Zero(fieldAt(a.source, a.rel.Reference.Index).Type()).Interface()),
		}}})
		appendWhere(tx, field.DBName+" = ?", sourceKey)
		if a.rel.Polymorphic {
			column := a.db.NamingStrategy.ColumnName("", a.rel.PolymorphicValue+"_type")
			appendWhere(tx, column+" = ?", a.rel.PolymorphicValue)
		}
		tx.Statement.BuildClauses()
		if err := tx.Exec(string(tx.Statement.SQL), tx.Statement.Vars...).Error; err != nil {
			a.fail(err)
			return err
		}
	}
	return nil
}

// Count returns the number of related records.
func (a *Association) Count() (int64, error) {
	if !a.ready() {
		return 0, a.error()
	}
	a.db.loadRelation([]reflect.Value{a.source}, a.rel, &preloadEntry{Field: a.rel.Name})
	if a.db.Error != nil {
		return 0, a.error()
	}
	field := fieldAt(a.source, a.rel.Field.Index)
	if !field.IsValid() {
		return 0, fmt.Errorf("%w: relation field %s", ErrInvalidValue, a.rel.Name)
	}
	if field.Kind() == reflect.Slice {
		return int64(field.Len()), nil
	}
	if field.Kind() == reflect.Pointer && !field.IsNil() {
		return 1, nil
	}
	return 0, nil
}

func (a *Association) error() error {
	if a != nil && a.db != nil && a.db.Error != nil {
		return a.db.Error
	}
	return fmt.Errorf("%w: association is not initialized", ErrInvalidValue)
}

func setFieldValue(value reflect.Value, field *schema.Field, raw any) error {
	if field == nil || !value.IsValid() {
		return fmt.Errorf("%w: invalid field destination", ErrInvalidValue)
	}
	fv := fieldAt(value, field.Index)
	if !fv.IsValid() || !fv.CanSet() {
		return fmt.Errorf("%w: field %s is not settable", ErrInvalidValue, field.Name)
	}
	if raw == nil {
		fv.Set(reflect.Zero(fv.Type()))
		return nil
	}
	rv := reflect.ValueOf(raw)
	if rv.Type().AssignableTo(fv.Type()) {
		fv.Set(rv)
		return nil
	}
	if rv.Type().ConvertibleTo(fv.Type()) {
		fv.Set(rv.Convert(fv.Type()))
		return nil
	}
	return fmt.Errorf("%w: cannot assign %s to %s", ErrInvalidValue, rv.Type(), fv.Type())
}

// SetupJoinTable overrides the junction table inferred for a many-to-many
// relation. The join model's first two ID-like fields become the FK and
// reference columns.
func (db *DB) SetupJoinTable(value any, field string, joinValue any) error {
	tx := db.getInstance()
	table, err := tx.resolveTable(value)
	if err != nil {
		return err
	}
	rel := relationFor(table, field)
	if rel == nil || rel.Kind != schema.ManyToMany {
		return fmt.Errorf("%w: %s.%s is not a many-to-many relation", ErrInvalidRelation, table.Name, field)
	}
	joinTable, err := tx.resolveTable(joinValue)
	if err != nil {
		return err
	}
	naming := tx.NamingStrategy
	fkField := joinTable.Column(naming.ColumnName("", table.ModelName+"_id"))
	refField := joinTable.Column(naming.ColumnName("", joinTable.ModelName+"_id"))
	if fkField == nil && len(joinTable.Fields) > 0 {
		fkField = joinTable.Fields[0]
	}
	if refField == nil {
		for _, candidate := range joinTable.Fields {
			if candidate != fkField {
				refField = candidate
				break
			}
		}
	}
	if fkField == nil || refField == nil || fkField == refField {
		return fmt.Errorf("%w: %s needs two distinct id columns", ErrInvalidRelation, joinTable.Name)
	}
	tx.joinTables.store(joinKey{model: table.Type, field: rel.Name}, joinSpec{
		table: joinTable.Name, fkCol: fkField.DBName, refCol: refField.DBName,
	})
	return nil
}

func appendWhere(tx *DB, sql string, args ...any) {
	where, _ := tx.Statement.Clause("where").(clause.Where)
	where.Conds = append(where.Conds, clause.Condition{
		Exprs: []clause.Expression{clause.Expr(sql, args...)},
	})
	tx.Statement.AddClause(where)
}
