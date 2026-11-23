package schema

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	// ErrInvalidModel reports a non-struct model passed to Tables.Parse.
	ErrInvalidModel = errors.New("schema: invalid model")
	// ErrAmbiguousField reports two fields resolving to the same column.
	ErrAmbiguousField = errors.New("schema: ambiguous field")
)

// Hooks records which model hook methods are present, detected at parse time.
type Hooks struct {
	BeforeCreate, AfterCreate bool
	BeforeUpdate, AfterUpdate bool
	BeforeDelete, AfterDelete bool
	BeforeFind, AfterFind     bool
}

// Has reports whether the named hook method is present on the model.
func (h Hooks) Has(name string) bool {
	switch name {
	case "BeforeCreate":
		return h.BeforeCreate
	case "AfterCreate":
		return h.AfterCreate
	case "BeforeUpdate":
		return h.BeforeUpdate
	case "AfterUpdate":
		return h.AfterUpdate
	case "BeforeDelete":
		return h.BeforeDelete
	case "AfterDelete":
		return h.AfterDelete
	case "BeforeFind":
		return h.BeforeFind
	case "AfterFind":
		return h.AfterFind
	}
	return false
}

// Table is the parsed metadata for one model type.
type Table struct {
	Type      reflect.Type
	Name      string
	ModelName string
	Fields    []*Field
	PKs       []*Field
	FieldMap  map[string]*Field
	DBNames   map[string]*Field
	Relations map[string]*Relation
	Hooks     Hooks
	// InsertColumns lists the columns written by a plain INSERT: every
	// field except autoincrement primary keys. Precomputed at parse time.
	InsertColumns []string
	Strict        bool
	namer         Namer
}

// Field returns a field by Go name.
func (t *Table) Field(name string) *Field { return t.FieldMap[name] }

// Column returns a field by database column name.
func (t *Table) Column(name string) *Field { return t.DBNames[name] }

func (t *Table) String() string { return t.Name }

// NewTables creates a per-naming-strategy parse cache. strict rejects unknown
// tag keys; warn (when non-nil) receives each leniently ignored key.
func NewTables(namer Namer, strict bool, warn func(string)) *Tables {
	if namer == nil {
		namer = NamingStrategy{}
	}
	return &Tables{namer: namer, strict: strict, warn: warn, cache: make(map[reflect.Type]*Table), pending: make(map[reflect.Type]chan struct{}), errs: make(map[reflect.Type]error)}
}

// Tables caches parsed models by type. Parsing is deduplicated across
// concurrent callers and supports recursive models via two-phase init.
type Tables struct {
	namer   Namer
	strict  bool
	warn    func(string)
	mu      sync.Mutex
	cache   map[reflect.Type]*Table
	pending map[reflect.Type]chan struct{}
	errs    map[reflect.Type]error
}

// Parse parses model (a struct or pointer to struct) and returns its cached
// Table. Repeated parses return the identical pointer.
func (ts *Tables) Parse(model any) (*Table, error) {
	if model == nil {
		return nil, fmt.Errorf("%w: nil model", ErrInvalidModel)
	}
	typ := reflect.TypeOf(model)
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s is not a struct", ErrInvalidModel, typ)
	}
	return ts.get(typ, false)
}

// Get parses a struct type and returns its cached Table.
func (ts *Tables) Get(typ reflect.Type) (*Table, error) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: %s is not a struct", ErrInvalidModel, typ)
	}
	return ts.get(typ, false)
}

func (ts *Tables) get(typ reflect.Type, nested bool) (*Table, error) {
	for {
		ts.mu.Lock()
		if table := ts.cache[typ]; table != nil {
			done, ok := ts.pending[typ]
			ts.mu.Unlock()
			if !ok {
				return table, nil
			}
			if nested {
				return table, nil
			}
			<-done
			continue
		}
		if err := ts.errs[typ]; err != nil {
			ts.mu.Unlock()
			return nil, err
		}

		table := &Table{Type: typ, FieldMap: make(map[string]*Field), DBNames: make(map[string]*Field), Relations: make(map[string]*Relation), Strict: ts.strict, namer: ts.namer}
		done := make(chan struct{})
		ts.cache[typ] = table
		ts.pending[typ] = done
		ts.mu.Unlock()

		err := table.init(ts)

		ts.mu.Lock()
		delete(ts.pending, typ)
		if err != nil {
			delete(ts.cache, typ)
			ts.errs[typ] = err
		}
		close(done)
		ts.mu.Unlock()
		return table, err
	}
}

type relationCandidate struct {
	field *Field
	tag   Tag
}

func (t *Table) init(ts *Tables) error {
	t.ModelName = snakeModel(t.Type.Name())
	t.Name = t.tableName()
	t.Hooks = detectHooks(t.Type)

	var relations []relationCandidate
	if err := t.processFields(ts, t.Type, t.Type.Name(), nil, "", &relations); err != nil {
		return err
	}

	for _, candidate := range relations {
		rel, err := newRelation(ts, t, candidate.field, candidate.tag)
		if err != nil {
			return err
		}
		t.Relations[candidate.field.Name] = rel
	}
	// Insert columns are a property of the schema: compute once, reuse on
	// every Create (readers must not modify the returned slice).
	insertCols := make([]string, 0, len(t.Fields))
	for _, field := range t.Fields {
		if field.PrimaryKey && field.AutoIncrement {
			continue
		}
		insertCols = append(insertCols, field.DBName)
	}
	t.InsertColumns = insertCols
	return nil
}

func (t *Table) tableName() string {
	model := reflect.New(t.Type).Interface()
	if tabler, ok := model.(Tabler); ok {
		return tabler.TableName()
	}
	return t.namer.TableName(t.ModelName)
}

func (t *Table) processFields(ts *Tables, typ reflect.Type, goName string, path []int, prefix string, relations *[]relationCandidate) error {
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
		if sf.PkgPath != "" && !sf.Anonymous {
			continue
		}
		tag, err := ParseTag(sf.Tag.Get("fusion"), ts.strict, ts.warn)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", goName, sf.Name, err)
		}
		if tag.Has("-") {
			continue
		}
		index := append(append([]int(nil), path...), i)

		if _, ok := tag.Lookup("rel"); ok {
			field := &Field{
				StructField: sf, Index: index, Name: sf.Name,
				DBName: tag.Get("foreignkey"), Tag: tag,
				IndirectType: indirectType(sf.Type), Table: t,
			}
			*relations = append(*relations, relationCandidate{field: field, tag: tag})
			continue
		}

		if embeddedType, embeddedPrefix, ok := embedded(sf, tag); ok {
			childPath := index
			if sf.Type.Kind() == reflect.Pointer {
				childPath = append(index, 0)
			}
			if err := t.processFields(ts, embeddedType, goName+"."+sf.Name, childPath, prefix+embeddedPrefix, relations); err != nil {
				return err
			}
			continue
		}

		column := tag.Name
		if name, ok := tag.Lookup("column"); ok && name != "" {
			column = name
		}
		if column == "" {
			column = t.namer.ColumnName(t.Name, sf.Name)
		}
		column = prefix + column

		field := newField(sf.Type, sf, tag, column, t.namer, t.Name)
		field.Table = t
		field.Index = index
		if existing := t.DBNames[column]; existing != nil {
			return fmt.Errorf("%w: column %q on %s.%s (also %s)", ErrAmbiguousField, column, goName, sf.Name, existing.Name)
		}
		if existing := t.FieldMap[sf.Name]; existing != nil {
			return fmt.Errorf("%w: field %s.%s (also column %s)", ErrAmbiguousField, goName, sf.Name, existing.DBName)
		}
		t.Fields = append(t.Fields, field)
		t.FieldMap[sf.Name] = field
		t.DBNames[column] = field
		if field.PrimaryKey {
			t.PKs = append(t.PKs, field)
		}
	}
	return nil
}

func embedded(sf reflect.StructField, tag Tag) (reflect.Type, string, bool) {
	typ := sf.Type
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == timeType {
		return nil, "", false
	}
	prefix, explicit := tag.Lookup("embed")
	if !sf.Anonymous && !explicit {
		return nil, "", false
	}
	return typ, prefix, true
}

func indirectType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	return typ
}

var hookNames = []string{
	"BeforeCreate", "AfterCreate", "BeforeUpdate", "AfterUpdate",
	"BeforeDelete", "AfterDelete", "BeforeFind", "AfterFind",
}

func detectHooks(typ reflect.Type) (h Hooks) {
	ptr := reflect.PointerTo(typ)
	for _, name := range hookNames {
		_, ok := ptr.MethodByName(name)
		setHook(&h, name, ok)
	}
	return h
}

func setHook(h *Hooks, name string, present bool) {
	switch name {
	case "BeforeCreate":
		h.BeforeCreate = present
	case "AfterCreate":
		h.AfterCreate = present
	case "BeforeUpdate":
		h.BeforeUpdate = present
	case "AfterUpdate":
		h.AfterUpdate = present
	case "BeforeDelete":
		h.BeforeDelete = present
	case "AfterDelete":
		h.AfterDelete = present
	case "BeforeFind":
		h.BeforeFind = present
	case "AfterFind":
		h.AfterFind = present
	}
}

// snakeModel converts a model name to snake_case using the default strategy.
func snakeModel(name string) string {
	return (NamingStrategy{}).toDBName(name)
}
