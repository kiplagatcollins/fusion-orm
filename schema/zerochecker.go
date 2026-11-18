package schema

import "reflect"

// IsZeroFunc reports whether a field value is the zero value for its type.
// Resolved at table-init time along with the field's appender and scanner.
type IsZeroFunc func(reflect.Value) bool

type zeroer interface {
	IsZero() bool
}

var zeroerType = reflect.TypeFor[zeroer]()

// IsZero resolves the zero check for a Go type.
func IsZero(typ reflect.Type) IsZeroFunc {
	if typ.Implements(zeroerType) || reflect.PointerTo(typ).Implements(zeroerType) {
		return func(v reflect.Value) bool {
			if !v.CanInterface() {
				return v.IsZero()
			}
			if z, ok := v.Interface().(zeroer); ok {
				return z.IsZero()
			}
			if v.CanAddr() {
				if z, ok := v.Addr().Interface().(zeroer); ok {
					return z.IsZero()
				}
			}
			return v.IsZero()
		}
	}
	return reflect.Value.IsZero
}
