package schema

import (
	"database/sql"
	"reflect"
	"strings"

	"github.com/kiplagatcollins/fusion-orm/dialect"
)

// DataType is the broad SQL family inferred from a Go field type.
type DataType string

const (
	DataTypeBool      DataType = "bool"
	DataTypeInt       DataType = "int"
	DataTypeUint      DataType = "uint"
	DataTypeFloat     DataType = "float"
	DataTypeString    DataType = "string"
	DataTypeBytes     DataType = "bytes"
	DataTypeTime      DataType = "time"
	DataTypeJSON      DataType = "json"
	DataTypeInterface DataType = "interface"
)

var scannerMarker = reflect.TypeFor[sql.Scanner]()

// Field is the parsed metadata for one model field. Appender, Scanner and
// IsZero are closures compiled once during table init.
type Field struct {
	Table        *Table
	StructField  reflect.StructField
	Index        []int
	Name         string
	DBName       string
	IndirectType reflect.Type
	IsPtr        bool
	DataType     DataType
	SQLType      string
	DefaultValue string
	Serializer   Serializer
	Tag          Tag

	PrimaryKey     bool
	AutoIncrement  bool
	NotNull        bool
	NullZero       bool
	AutoCreateTime bool
	AutoUpdateTime bool
	Creatable      bool
	Updatable      bool
	Readable       bool
	IndexName      string
	UniqueName     string
	OnUpdate       string
	OnDelete       string

	Appender AppenderFunc
	Scanner  ScannerFunc
	IsZero   IsZeroFunc
}

// String returns the Go field name.
func (f *Field) String() string { return f.Name }

// Value returns the field value, allocating nil embedded pointers as needed.
func (f *Field) Value(strct reflect.Value) reflect.Value {
	return fieldByIndexAlloc(strct, f.Index)
}

// HasZeroValue reports whether the field's value is zero in strct.
func (f *Field) HasZeroValue(strct reflect.Value) bool {
	value, ok := fieldByIndex(strct, f.Index)
	if !ok {
		return true
	}
	return f.IsZero(value)
}

// Append renders the field value as a SQL literal through d.
func (f *Field) Append(b []byte, strct reflect.Value, d dialect.Dialector) []byte {
	value, ok := fieldByIndex(strct, f.Index)
	if !ok || (f.IsPtr && value.IsNil()) || (f.NullZero && f.IsZero(value)) {
		return dialect.AppendNull(b)
	}
	return f.Appender(b, value, d)
}

// Scan assigns src to the field, allocating nil embedded pointers as needed.
func (f *Field) Scan(strct reflect.Value, src any) error {
	if src == nil {
		if value, ok := fieldByIndex(strct, f.Index); ok {
			return f.Scanner(value, nil)
		}
		return nil
	}
	return f.Scanner(fieldByIndexAlloc(strct, f.Index), src)
}

// CloneWithPrefix returns a copy whose DBName is prefixed and whose index
// path is prefixed by the parent embedded field's path.
func (f *Field) CloneWithPrefix(prefix string, path []int) *Field {
	clone := *f
	clone.DBName = prefix + f.DBName
	clone.Index = append(append([]int(nil), path...), f.Index...)
	return &clone
}

func fieldByIndex(v reflect.Value, index []int) (reflect.Value, bool) {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}, false
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct || i >= v.NumField() {
			return reflect.Value{}, false
		}
		v = v.Field(i)
	}
	return v, true
}

func fieldByIndexAlloc(v reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

func dataTypeOf(typ reflect.Type) DataType {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == timeType {
		return DataTypeTime
	}
	if typ.Implements(scannerMarker) || reflect.PointerTo(typ).Implements(scannerMarker) {
		return DataTypeString
	}
	switch typ.Kind() {
	case reflect.Bool:
		return DataTypeBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return DataTypeInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return DataTypeUint
	case reflect.Float32, reflect.Float64:
		return DataTypeFloat
	case reflect.String:
		return DataTypeString
	case reflect.Slice:
		if typ == byteSliceType {
			return DataTypeBytes
		}
		return DataTypeJSON
	case reflect.Map, reflect.Struct:
		return DataTypeJSON
	case reflect.Interface:
		return DataTypeInterface
	default:
		return DataTypeInterface
	}
}

func newField(typ reflect.Type, sf reflect.StructField, tag Tag, dbName string, namer Namer, tableName string) *Field {
	indirect := typ
	isPtr := false
	for indirect.Kind() == reflect.Pointer {
		indirect = indirect.Elem()
		isPtr = true
	}

	var ser Serializer
	if tag.Has("json") || tag.Get("serializer") == "json" {
		ser = JSONSerializer{}
	}
	field := &Field{
		StructField:    sf,
		Name:           sf.Name,
		DBName:         dbName,
		IndirectType:   indirect,
		IsPtr:          isPtr,
		DataType:       dataTypeOf(indirect),
		SQLType:        tag.Get("type"),
		DefaultValue:   tag.Get("default"),
		Serializer:     ser,
		Tag:            tag,
		PrimaryKey:     tag.Has("pk") || tag.Has("primarykey") || sf.Name == "ID",
		AutoIncrement:  tag.Has("autoincrement"),
		NotNull:        tag.Has("notnull"),
		NullZero:       tag.Has("nullzero"),
		AutoCreateTime: sf.Name == "CreatedAt",
		AutoUpdateTime: sf.Name == "UpdatedAt",
		Creatable:      !tag.Has("->"),
		Updatable:      !tag.Has("->") && !tag.Has("<-"),
		Readable:       !tag.Has("<-"),
	}
	field.Appender = Appender(sf.Type, ser)
	field.Scanner = Scanner(sf.Type, ser)
	field.IsZero = IsZero(sf.Type)
	if ser != nil {
		field.DataType = DataTypeJSON
	}

	if name, ok := tag.Lookup("index"); ok {
		field.IndexName = name
		if field.IndexName == "" {
			field.IndexName = namer.IndexName(tableName, dbName)
		}
	}
	if name, ok := tag.Lookup("unique"); ok {
		field.UniqueName = name
		if field.UniqueName == "" {
			field.UniqueName = namer.UniqueName(tableName, dbName)
		}
	}
	if constraint, ok := tag.Lookup("constraint"); ok {
		field.OnUpdate, field.OnDelete = parseConstraint(constraint)
	}
	return field
}

func parseConstraint(s string) (onUpdate, onDelete string) {
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '|' }) {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "onupdate":
			onUpdate = strings.TrimSpace(value)
		case "ondelete":
			onDelete = strings.TrimSpace(value)
		}
	}
	return onUpdate, onDelete
}
