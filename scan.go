package fusion

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/kiplagatcollins/fusion-orm/schema"
)

// scanTarget maps one result column to a model field and its full index path.
type scanTarget struct {
	field *schema.Field
	index []int
}

// scanPlan is built once per result set, not once per row.
type scanPlan struct {
	targets []scanTarget
	columns []string
}

func newScanPlan(columns []string, table *schema.Table) *scanPlan {
	p := &scanPlan{columns: columns, targets: make([]scanTarget, len(columns))}
	for i, column := range columns {
		if field := table.Column(column); field != nil {
			p.targets[i] = scanTarget{field: field, index: field.Index}
			continue
		}
		// Joined columns arrive as alias__column; mount them into the
		// related struct when the model declares that relation.
		if alias, nested, ok := strings.Cut(column, "__"); ok {
			rel := table.Relations[alias]
			if rel == nil {
				for _, candidate := range table.Relations {
					if strings.EqualFold(candidate.Name, alias) {
						rel = candidate
						break
					}
				}
			}
			if rel != nil && rel.JoinTable != nil {
				if field := rel.JoinTable.Column(nested); field != nil {
					index := append(append([]int(nil), rel.Field.Index...), field.Index...)
					p.targets[i] = scanTarget{field: field, index: index}
				}
			}
		}
	}
	return p
}

func fieldAt(value reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				value.Set(reflect.New(value.Type().Elem()))
			}
			value = value.Elem()
		}
		if value.Kind() != reflect.Struct || i >= value.NumField() {
			return reflect.Value{}
		}
		value = value.Field(i)
	}
	return value
}

// scanRows scans all rows into dest using precompiled field scanners.
// It returns the number of rows consumed.
func scanRows(rows *sql.Rows, dest any, table *schema.Table) (int, error) {
	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("scan columns: %w", err)
	}

	if dest == nil {
		return 0, fmt.Errorf("%w: nil scan destination", ErrInvalidValue)
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return 0, fmt.Errorf("%w: scan destination must be a non-nil pointer", ErrInvalidValue)
	}
	value := rv.Elem()

	if value.Kind() == reflect.Slice {
		return scanSlice(rows, value, table, columns)
	}
	if value.Kind() == reflect.Map {
		return scanMap(rows, value, columns)
	}
	if value.Kind() == reflect.Struct || (value.Kind() == reflect.Pointer && value.Type().Elem().Kind() == reflect.Struct) {
		return scanStruct(rows, value, table, columns, false)
	}
	return scanScalar(rows, value)
}

func scanSlice(rows *sql.Rows, slice reflect.Value, table *schema.Table, columns []string) (int, error) {
	elemType := slice.Type().Elem()
	if elemType.Kind() != reflect.Struct && !(elemType.Kind() == reflect.Pointer && elemType.Elem().Kind() == reflect.Struct) {
		return scanScalarSlice(rows, slice, elemType)
	}
	plan := newScanPlan(columns, table)
	count := 0
	for rows.Next() {
		var elem reflect.Value
		if elemType.Kind() == reflect.Pointer {
			elem = reflect.New(elemType.Elem())
		} else {
			elem = reflect.New(elemType).Elem()
		}
		if err := scanOne(rows, elem, plan); err != nil {
			return count, err
		}
		slice.Set(reflect.Append(slice, elem))
		count++
	}
	return count, rows.Err()
}

func scanStruct(rows *sql.Rows, value reflect.Value, table *schema.Table, columns []string, _ bool) (int, error) {
	plan := newScanPlan(columns, table)
	count := 0
	for rows.Next() {
		if err := scanOne(rows, value, plan); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

func scanOne(rows *sql.Rows, value reflect.Value, plan *scanPlan) error {
	raw := make([]any, len(plan.columns))
	holders := make([]any, len(plan.columns))
	for i := range raw {
		holders[i] = &raw[i]
	}
	if err := rows.Scan(holders...); err != nil {
		return fmt.Errorf("scan row: %w", err)
	}
	for i, target := range plan.targets {
		if target.field == nil {
			continue
		}
		fieldValue := fieldAt(value, target.index)
		if !fieldValue.IsValid() {
			continue
		}
		if err := target.field.Scanner(fieldValue, raw[i]); err != nil {
			return fmt.Errorf("scan %s: %w", target.field.DBName, err)
		}
	}
	return nil
}

func scanMap(rows *sql.Rows, value reflect.Value, columns []string) (int, error) {
	if value.IsNil() {
		value.Set(reflect.MakeMap(value.Type()))
	}
	keyType, valType := value.Type().Key(), value.Type().Elem()
	count := 0
	for rows.Next() {
		if len(columns) < 2 {
			return count, errors.New("scan map: need two columns")
		}
		raw := make([]any, len(columns))
		holders := make([]any, len(columns))
		for i := range raw {
			holders[i] = &raw[i]
		}
		if err := rows.Scan(holders...); err != nil {
			return count, fmt.Errorf("scan row: %w", err)
		}
		key := reflect.ValueOf(columns[0])
		val := reflect.ValueOf(raw[1])
		if !val.IsValid() {
			val = reflect.Zero(valType)
		}
		if !key.Type().AssignableTo(keyType) || !val.Type().AssignableTo(valType) {
			return count, fmt.Errorf("%w: map[%s]%s cannot hold %s/%s", ErrInvalidValue, keyType, valType, key.Type(), val.Type())
		}
		value.SetMapIndex(key, val)
		count++
	}
	return count, rows.Err()
}

func scanScalar(rows *sql.Rows, value reflect.Value) (int, error) {
	count := 0
	for rows.Next() {
		if err := rows.Scan(value.Addr().Interface()); err != nil {
			return count, fmt.Errorf("scan scalar: %w", err)
		}
		count++
	}
	return count, rows.Err()
}

func scanScalarSlice(rows *sql.Rows, slice reflect.Value, elemType reflect.Type) (int, error) {
	count := 0
	for rows.Next() {
		elem := reflect.New(elemType)
		if err := rows.Scan(elem.Interface()); err != nil {
			return count, fmt.Errorf("scan scalar: %w", err)
		}
		slice.Set(reflect.Append(slice, elem.Elem()))
		count++
	}
	return count, rows.Err()
}
