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

// scanBuffers holds the per-column scan destinations. They are allocated
// once per result set and reused for every row: rows.Scan overwrites the
// holder values in place, so per-row allocation is pure GC pressure.
type scanBuffers struct {
	raw     []any
	holders []any
}

func newScanBuffers(n int) *scanBuffers {
	buf := &scanBuffers{raw: make([]any, n), holders: make([]any, n)}
	for i := range buf.raw {
		buf.holders[i] = &buf.raw[i]
	}
	return buf
}

// appendOne extends an addressable slice by one element and returns the new
// slot. reflect.Append + Set heap-allocates a slice header on every call
// (grow's local header escapes), so one call per row becomes one allocation
// per row; this grows amortized (doubling) and updates the length in place.
func appendOne(slice reflect.Value) reflect.Value {
	n := slice.Len()
	if n == slice.Cap() {
		newCap := 8
		if c := slice.Cap(); c > 0 {
			newCap = c * 2
		}
		grown := reflect.MakeSlice(slice.Type(), n, newCap)
		reflect.Copy(grown, slice)
		slice.Set(grown)
	}
	slice.SetLen(n + 1)
	return slice.Index(n)
}

func scanSlice(rows *sql.Rows, slice reflect.Value, table *schema.Table, columns []string) (int, error) {
	elemType := slice.Type().Elem()
	if elemType.Kind() != reflect.Struct && !(elemType.Kind() == reflect.Pointer && elemType.Elem().Kind() == reflect.Struct) {
		return scanScalarSlice(rows, slice, elemType)
	}
	plan := newScanPlan(columns, table)
	buf := newScanBuffers(len(columns))
	count := 0
	for rows.Next() {
		if elemType.Kind() == reflect.Pointer {
			elem := reflect.New(elemType.Elem())
			if err := scanOne(rows, elem, plan, buf); err != nil {
				return count, err
			}
			appendOne(slice).Set(elem)
		} else {
			// Scan straight into the new slot: no temporary struct, no copy.
			if err := scanOne(rows, appendOne(slice), plan, buf); err != nil {
				return count, err
			}
		}
		count++
	}
	return count, rows.Err()
}

func scanStruct(rows *sql.Rows, value reflect.Value, table *schema.Table, columns []string, _ bool) (int, error) {
	plan := newScanPlan(columns, table)
	buf := newScanBuffers(len(columns))
	count := 0
	for rows.Next() {
		if err := scanOne(rows, value, plan, buf); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

func scanOne(rows *sql.Rows, value reflect.Value, plan *scanPlan, buf *scanBuffers) error {
	if err := rows.Scan(buf.holders...); err != nil {
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
		if err := target.field.Scanner(fieldValue, buf.raw[i]); err != nil {
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
	buf := newScanBuffers(len(columns))
	count := 0
	for rows.Next() {
		if len(columns) < 2 {
			return count, errors.New("scan map: need two columns")
		}
		if err := rows.Scan(buf.holders...); err != nil {
			return count, fmt.Errorf("scan row: %w", err)
		}
		key := reflect.ValueOf(columns[0])
		val := reflect.ValueOf(buf.raw[1])
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
		// Scan through a pointer into the new slot: one interface boxing of
		// an existing address, no per-row element allocation.
		dest := appendOne(slice).Addr().Interface()
		if err := rows.Scan(dest); err != nil {
			return count, fmt.Errorf("scan scalar: %w", err)
		}
		count++
	}
	return count, rows.Err()
}
