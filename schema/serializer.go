package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Serializer customizes how a field is stored as a JSON/SQL value.
type Serializer interface {
	Serialize(reflect.Value) ([]byte, error)
	Deserialize([]byte, reflect.Value) error
}

// JSONSerializer marshals a field through encoding/json.
type JSONSerializer struct{}

// Serialize encodes a value as JSON bytes.
func (JSONSerializer) Serialize(v reflect.Value) ([]byte, error) {
	if !v.CanInterface() {
		return nil, fmt.Errorf("schema: cannot serialize unexported field")
	}
	return json.Marshal(v.Interface())
}

// Deserialize decodes JSON bytes into an addressable value.
func (JSONSerializer) Deserialize(data []byte, dst reflect.Value) error {
	if dst.CanAddr() {
		return json.Unmarshal(data, dst.Addr().Interface())
	}
	if dst.Kind() != reflect.Pointer || dst.IsNil() {
		return fmt.Errorf("schema: cannot deserialize into non-addressable %s", dst.Type())
	}
	return json.Unmarshal(data, dst.Interface())
}
