package schema

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect"
)

// AppenderFunc renders a field value as a SQL literal using the active
// dialect. It is resolved once at table-init time; no per-row reflection.
type AppenderFunc func(b []byte, v reflect.Value, d dialect.Dialector) []byte

var timeType = reflect.TypeFor[time.Time]()
var valuerType = reflect.TypeFor[driver.Valuer]()
var byteSliceType = reflect.TypeFor[[]byte]()

// Appender resolves the literal appender for a Go type.
func Appender(typ reflect.Type, ser Serializer) AppenderFunc {
	if ser != nil {
		return func(b []byte, v reflect.Value, d dialect.Dialector) []byte {
			data, err := ser.Serialize(v)
			if err != nil {
				return dialect.AppendError(b, err)
			}
			return d.AppendJSON(b, data)
		}
	}
	if typ.Implements(valuerType) || reflect.PointerTo(typ).Implements(valuerType) {
		return appendValuer
	}

	switch typ.Kind() {
	case reflect.Pointer:
		elem := Appender(typ.Elem(), nil)
		return func(b []byte, v reflect.Value, d dialect.Dialector) []byte {
			if v.IsNil() {
				return dialect.AppendNull(b)
			}
			return elem(b, v.Elem(), d)
		}
	case reflect.Interface:
		return func(b []byte, v reflect.Value, d dialect.Dialector) []byte {
			if v.IsNil() {
				return dialect.AppendNull(b)
			}
			return dynamicAppend(b, v.Elem(), d)
		}
	case reflect.Bool:
		return func(b []byte, v reflect.Value, _ dialect.Dialector) []byte {
			return dialect.AppendBool(b, v.Bool())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(b []byte, v reflect.Value, _ dialect.Dialector) []byte {
			return dialect.AppendInt64(b, v.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(b []byte, v reflect.Value, _ dialect.Dialector) []byte {
			return dialect.AppendUint64(b, v.Uint())
		}
	case reflect.Float32:
		return func(b []byte, v reflect.Value, _ dialect.Dialector) []byte {
			return dialect.AppendFloat32(b, float32(v.Float()))
		}
	case reflect.Float64:
		return func(b []byte, v reflect.Value, _ dialect.Dialector) []byte {
			return dialect.AppendFloat64(b, v.Float())
		}
	case reflect.String:
		return func(b []byte, v reflect.Value, d dialect.Dialector) []byte {
			return d.AppendString(b, v.String())
		}
	case reflect.Slice:
		if typ == byteSliceType {
			return func(b []byte, v reflect.Value, d dialect.Dialector) []byte {
				if v.IsNil() {
					return dialect.AppendNull(b)
				}
				return d.AppendBytes(b, v.Bytes())
			}
		}
		return appendJSON
	case reflect.Map, reflect.Struct:
		if typ == timeType {
			return func(b []byte, v reflect.Value, d dialect.Dialector) []byte {
				return d.AppendTime(b, v.Interface().(time.Time))
			}
		}
		return appendJSON
	default:
		return func(b []byte, _ reflect.Value, _ dialect.Dialector) []byte {
			return dialect.AppendNull(b)
		}
	}
}

func appendValuer(b []byte, v reflect.Value, d dialect.Dialector) []byte {
	if !v.CanInterface() {
		return dialect.AppendNull(b)
	}
	valuer, ok := v.Interface().(driver.Valuer)
	if !ok && v.Kind() != reflect.Pointer && v.CanAddr() {
		valuer, ok = v.Addr().Interface().(driver.Valuer)
	}
	if !ok {
		return dialect.AppendNull(b)
	}
	value, err := valuer.Value()
	if err != nil {
		return dialect.AppendError(b, err)
	}
	if value == nil {
		return dialect.AppendNull(b)
	}
	return dynamicAppend(b, reflect.ValueOf(value), d)
}

var dynamicAppenders sync.Map

func dynamicAppend(b []byte, v reflect.Value, d dialect.Dialector) []byte {
	fn, ok := dynamicAppenders.Load(v.Type())
	if !ok {
		fn, _ = dynamicAppenders.LoadOrStore(v.Type(), Appender(v.Type(), nil))
	}
	return fn.(AppenderFunc)(b, v, d)
}

func appendJSON(b []byte, v reflect.Value, d dialect.Dialector) []byte {
	if !v.CanInterface() {
		return dialect.AppendNull(b)
	}
	data, err := json.Marshal(v.Interface())
	if err != nil {
		return dialect.AppendError(b, err)
	}
	return d.AppendJSON(b, data)
}
