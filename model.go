package fusion

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kiplagatcollins/fusion-orm/schema"
)

// resolveTable returns the parsed schema table for a model value or type.
func (db *DB) resolveTable(model any) (*schema.Table, error) {
	if model == nil {
		return nil, fmt.Errorf("%w: nil model", ErrInvalidValue)
	}
	typ := reflect.TypeOf(model)
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s is not a struct model", ErrInvalidValue, typ)
	}
	if db.tables == nil {
		return nil, schema.ErrInvalidModel
	}
	// Get by type directly: Parse(reflect.New(typ).Interface()) would
	// allocate a throwaway model instance on every resolution.
	return db.tables.Get(typ)
}

func (db *DB) modelTable() (*schema.Table, error) {
	if model := db.Statement.Model; model != nil {
		return db.resolveTable(model)
	}
	if dest := db.Statement.Dest; dest != nil {
		return db.resolveTable(dest)
	}
	return nil, fmt.Errorf("%w: no model or destination", ErrInvalidValue)
}

func (db *DB) tableName(table *schema.Table) string {
	if db.Statement.Table != "" {
		return db.Statement.Table
	}
	return table.Name
}

// pkFields returns the fields that identify a row: the parsed primary-key
// list, or the plain "id" column when the model relies on the naming
// convention. Composite keys yield more than one field.
func pkFields(table *schema.Table) []*schema.Field {
	if len(table.PKs) > 0 {
		return table.PKs
	}
	if f := table.Column("id"); f != nil {
		return []*schema.Field{f}
	}
	return nil
}

func primaryField(table *schema.Table) *schema.Field {
	if pks := pkFields(table); len(pks) > 0 {
		return pks[0]
	}
	return nil
}

// pkCondition builds a WHERE fragment matching every primary-key field of
// item ("a = ? AND b = ?") plus the bound values. It reports false when the
// table has no primary key or any PK field is zero, so callers fall back to
// insert semantics instead of matching a partial key.
func pkCondition(table *schema.Table, item reflect.Value) (string, []any, bool) {
	pks := pkFields(table)
	if len(pks) == 0 {
		return "", nil, false
	}
	cols := make([]string, 0, len(pks))
	vars := make([]any, 0, len(pks))
	for _, pk := range pks {
		fv := fieldAt(item, pk.Index)
		if !fv.IsValid() || pk.IsZero(fv) {
			return "", nil, false
		}
		cols = append(cols, pk.DBName+" = ?")
		vars = append(vars, fv.Interface())
	}
	return strings.Join(cols, " AND "), vars, true
}

// modelValues returns the struct values to operate on (one for a struct,
// one per element for a slice), plus their reflect type.
func modelValues(dest any) ([]reflect.Value, error) {
	if dest == nil {
		return nil, fmt.Errorf("%w: nil destination", ErrInvalidValue)
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, fmt.Errorf("%w: destination must be a non-nil pointer", ErrInvalidValue)
	}
	value := rv.Elem()
	if value.Kind() == reflect.Slice {
		out := make([]reflect.Value, value.Len())
		for i := 0; i < value.Len(); i++ {
			elem := value.Index(i)
			if elem.Kind() == reflect.Pointer {
				if elem.IsNil() {
					elem.Set(reflect.New(elem.Type().Elem()))
				}
				out[i] = elem.Elem()
			} else {
				out[i] = elem
			}
		}
		return out, nil
	}
	if value.Kind() == reflect.Pointer && value.Type().Elem().Kind() == reflect.Struct {
		if value.IsNil() {
			value.Set(reflect.New(value.Type().Elem()))
		}
		return []reflect.Value{value.Elem()}, nil
	}
	if value.Kind() == reflect.Struct {
		return []reflect.Value{value}, nil
	}
	return nil, fmt.Errorf("%w: expected struct or slice destination", ErrInvalidValue)
}

func setTimestamps(db *DB, value reflect.Value, table *schema.Table, create bool) {
	// Assign through typed pointers: reflect.ValueOf(time.Time) heap-boxes
	// the value on every call, and this runs per row.
	var now time.Time
	fetched := false
	for _, field := range table.Fields {
		if !(field.AutoCreateTime && create) && !field.AutoUpdateTime {
			continue
		}
		fv := fieldAt(value, field.Index)
		if !fv.IsValid() || !fv.CanSet() || fv.Type() != reflect.TypeFor[time.Time]() {
			continue
		}
		if !fetched {
			now = db.NowFunc()
			fetched = true
		}
		if p, ok := fv.Addr().Interface().(*time.Time); ok {
			*p = now
		}
	}
}

func callHook(db *DB, model any, name string) {
	if db.Statement.SkipHooks || model == nil {
		return
	}
	rv := reflect.ValueOf(model)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return
	}
	method := rv.MethodByName(name)
	if !method.IsValid() {
		return
	}
	results := method.Call([]reflect.Value{reflect.ValueOf(db)})
	for _, result := range results {
		if err, ok := result.Interface().(error); ok {
			db.AddError(err)
		}
	}
}

func callHooksOnValues(db *DB, values []reflect.Value, name string) {
	for i := range values {
		callHook(db, values[i].Addr().Interface(), name)
		if db.Error != nil {
			return
		}
	}
}

func assignValues(dest any, values any, table *schema.Table) error {
	if values == nil {
		return nil
	}
	targets, err := modelValues(dest)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(values)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Map {
		for _, target := range targets {
			for _, field := range table.Fields {
				key := reflect.ValueOf(field.DBName)
				entry := rv.MapIndex(key)
				if !entry.IsValid() {
					entry = rv.MapIndex(reflect.ValueOf(field.Name))
				}
				if !entry.IsValid() {
					continue
				}
				if entry.Kind() == reflect.Interface {
					entry = entry.Elem()
				}
				if !entry.IsValid() {
					continue
				}
				fv := fieldAt(target, field.Index)
				if fv.IsValid() && fv.CanSet() && entry.Type().AssignableTo(fv.Type()) {
					fv.Set(entry)
				}
			}
		}
		return nil
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("%w: assignments must be a struct or map", ErrInvalidValue)
	}
	for _, target := range targets {
		for _, field := range table.Fields {
			fv := fieldAt(rv, field.Index)
			if !fv.IsValid() || field.IsZero(fv) {
				continue
			}
			dst := fieldAt(target, field.Index)
			if dst.IsValid() && dst.CanSet() && fv.Type().AssignableTo(dst.Type()) {
				dst.Set(fv)
			}
		}
	}
	return nil
}
