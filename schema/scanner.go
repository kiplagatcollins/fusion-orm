package schema

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// ScannerFunc assigns a database value to a field. Resolved at table-init
// time, so scanning rows performs no type dispatch through reflection.
type ScannerFunc func(dst reflect.Value, src any) error

var scannerType = reflect.TypeFor[sql.Scanner]()

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// Scanner resolves the value scanner for a Go type.
func Scanner(typ reflect.Type, ser Serializer) ScannerFunc {
	if ser != nil {
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			data, err := rawBytes(src)
			if err != nil {
				return err
			}
			return ser.Deserialize(data, dst)
		}
	}
	if typ.Implements(scannerType) || reflect.PointerTo(typ).Implements(scannerType) {
		return scanSQLScanner
	}

	switch typ.Kind() {
	case reflect.Pointer:
		elem := Scanner(typ.Elem(), nil)
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			if dst.IsNil() {
				dst.Set(reflect.New(typ.Elem()))
			}
			return elem(dst.Elem(), src)
		}
	case reflect.Interface:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			dst.Set(reflect.ValueOf(src))
			return nil
		}
	case reflect.Bool:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			switch v := src.(type) {
			case bool:
				dst.SetBool(v)
			case int64:
				dst.SetBool(v != 0)
			case string:
				b, err := strconv.ParseBool(v)
				if err != nil {
					return err
				}
				dst.SetBool(b)
			case []byte:
				b, err := strconv.ParseBool(string(v))
				if err != nil {
					return err
				}
				dst.SetBool(b)
			default:
				return cannotScan(src, typ)
			}
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			var n int64
			switch v := src.(type) {
			case int64:
				n = v
			case int:
				n = int64(v)
			case float64:
				n = int64(v)
			case string:
				parsed, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return err
				}
				n = parsed
			case []byte:
				parsed, err := strconv.ParseInt(string(v), 10, 64)
				if err != nil {
					return err
				}
				n = parsed
			default:
				return cannotScan(src, typ)
			}
			if dst.OverflowInt(n) {
				return fmt.Errorf("schema: %d overflows %s", n, typ)
			}
			dst.SetInt(n)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			var n uint64
			switch v := src.(type) {
			case int64:
				n = uint64(v)
			case uint64:
				n = v
			case float64:
				n = uint64(v)
			case string:
				parsed, err := strconv.ParseUint(v, 10, 64)
				if err != nil {
					return err
				}
				n = parsed
			case []byte:
				parsed, err := strconv.ParseUint(string(v), 10, 64)
				if err != nil {
					return err
				}
				n = parsed
			default:
				return cannotScan(src, typ)
			}
			if dst.OverflowUint(n) {
				return fmt.Errorf("schema: %d overflows %s", n, typ)
			}
			dst.SetUint(n)
			return nil
		}
	case reflect.Float32, reflect.Float64:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			var f float64
			switch v := src.(type) {
			case float64:
				f = v
			case float32:
				f = float64(v)
			case int64:
				f = float64(v)
			case string:
				parsed, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return err
				}
				f = parsed
			case []byte:
				parsed, err := strconv.ParseFloat(string(v), 64)
				if err != nil {
					return err
				}
				f = parsed
			default:
				return cannotScan(src, typ)
			}
			if dst.OverflowFloat(f) {
				return fmt.Errorf("schema: %g overflows %s", f, typ)
			}
			dst.SetFloat(f)
			return nil
		}
	case reflect.String:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			switch v := src.(type) {
			case string:
				dst.SetString(v)
			case []byte:
				dst.SetString(string(v))
			default:
				return cannotScan(src, typ)
			}
			return nil
		}
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return func(dst reflect.Value, src any) error {
				if src == nil {
					dst.SetZero()
					return nil
				}
				switch v := src.(type) {
				case []byte:
					dst.SetBytes(bytesClone(v))
				case string:
					dst.SetBytes([]byte(v))
				default:
					return cannotScan(src, typ)
				}
				return nil
			}
		}
		return scanJSON
	case reflect.Map, reflect.Struct:
		if typ == timeType {
			return scanTime
		}
		return scanJSON
	default:
		return func(dst reflect.Value, src any) error {
			if src == nil {
				dst.SetZero()
				return nil
			}
			value := reflect.ValueOf(src)
			if !value.Type().AssignableTo(typ) {
				return cannotScan(src, typ)
			}
			dst.Set(value)
			return nil
		}
	}
}

func scanSQLScanner(dst reflect.Value, src any) error {
	if dst.Kind() == reflect.Pointer && dst.IsNil() {
		dst.Set(reflect.New(dst.Type().Elem()))
	}
	if dst.CanAddr() {
		if scanner, ok := dst.Addr().Interface().(sql.Scanner); ok {
			return scanner.Scan(src)
		}
	}
	scanner, ok := dst.Interface().(sql.Scanner)
	if !ok {
		return fmt.Errorf("schema: %s does not implement sql.Scanner", dst.Type())
	}
	return scanner.Scan(src)
}

func scanTime(dst reflect.Value, src any) error {
	if src == nil {
		dst.SetZero()
		return nil
	}
	var tm time.Time
	switch v := src.(type) {
	case time.Time:
		tm = v
	case string:
		parsed, err := parseTime(v)
		if err != nil {
			return err
		}
		tm = parsed
	case []byte:
		parsed, err := parseTime(string(v))
		if err != nil {
			return err
		}
		tm = parsed
	default:
		return cannotScan(src, dst.Type())
	}
	if _, offset := tm.Zone(); offset == 0 {
		tm = tm.UTC()
	}
	value := reflect.ValueOf(tm)
	if value.Type().AssignableTo(dst.Type()) {
		dst.Set(value)
	} else if value.Type().ConvertibleTo(dst.Type()) {
		dst.Set(value.Convert(dst.Type()))
	} else {
		return cannotScan(src, dst.Type())
	}
	return nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range timeLayouts {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm, nil
		}
	}
	return time.Time{}, fmt.Errorf("schema: cannot parse time %q", s)
}

func scanJSON(dst reflect.Value, src any) error {
	if src == nil {
		dst.SetZero()
		return nil
	}
	data, err := rawBytes(src)
	if err != nil {
		return err
	}
	if dst.CanAddr() {
		return json.Unmarshal(data, dst.Addr().Interface())
	}
	if dst.Kind() != reflect.Pointer || dst.IsNil() {
		return fmt.Errorf("schema: cannot scan JSON into non-addressable %s", dst.Type())
	}
	return json.Unmarshal(data, dst.Interface())
}

func rawBytes(src any) ([]byte, error) {
	switch v := src.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("schema: %T is not JSON bytes", src)
	}
}

func bytesClone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cannotScan(src any, typ reflect.Type) error {
	return fmt.Errorf("schema: cannot scan %T into %s", src, typ)
}
