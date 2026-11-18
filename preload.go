package fusion

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

func relationFor(table *schema.Table, name string) *schema.Relation {
	if rel := table.Relations[name]; rel != nil {
		return rel
	}
	for _, rel := range table.Relations {
		if strings.EqualFold(rel.Name, name) {
			return rel
		}
	}
	return nil
}

func relationKey(value any) any {
	if value == nil {
		return nil
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint())
	case reflect.String:
		return rv.String()
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return string(rv.Bytes())
		}
	}
	if rv.Type().Comparable() {
		return rv.Interface()
	}
	return fmt.Sprint(value)
}

func relationFieldValue(value reflect.Value, field *schema.Field) (any, bool) {
	if field == nil {
		return nil, false
	}
	fv := fieldAt(value, field.Index)
	if !fv.IsValid() || !fv.CanInterface() || field.IsZero(fv) {
		return nil, false
	}
	return fv.Interface(), true
}

func inPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func applyPreloadArgs(db *DB, args []any) {
	if len(args) == 0 {
		return
	}
	if _, ok := args[0].(func(*DB) *DB); ok {
		for _, arg := range args {
			if fn, ok := arg.(func(*DB) *DB); ok && fn != nil {
				db = fn(db)
			}
		}
		return
	}
	applyConds(db, args...)
}

// preload loads every requested relation in one query per level. Nested
// paths are loaded by the child query's own preload callback.
func (db *DB) preload() {
	if len(db.Statement.Preloads) == 0 || db.Error != nil || db.Config.DryRun {
		return
	}
	table, err := db.modelTable()
	if err != nil {
		db.AddError(err)
		return
	}
	sources, err := modelValues(db.Statement.Dest)
	if err != nil {
		sources, err = modelValues(db.Statement.Model)
		if err != nil {
			db.AddError(err)
			return
		}
	}
	for _, entry := range db.Statement.Preloads {
		rel := relationFor(table, entry.Field)
		if rel == nil {
			db.AddError(fmt.Errorf("%w: %s.%s", ErrInvalidRelation, table.Name, entry.Field))
			return
		}
		db.loadRelation(sources, rel, entry)
		if db.Error != nil {
			return
		}
	}
}

func (db *DB) loadRelation(sources []reflect.Value, rel *schema.Relation, entry *preloadEntry) {
	if len(sources) == 0 {
		return
	}
	join := rel.JoinTable
	switch rel.Kind {
	case schema.BelongsTo:
		keys := collectKeys(sources, rel.ForeignKey)
		match := rel.Reference
		db.loadTargets(join, keys, match, match.DBName, "", rel.ForeignKey, sources, rel, entry)
	case schema.HasOne, schema.HasMany:
		keys := collectKeys(sources, rel.Reference)
		fk := rel.ForeignKey
		fkColumn := ""
		polyValue := rel.PolymorphicValue
		if polyValue == "" {
			polyValue = db.NamingStrategy.ColumnName("", rel.Table.ModelName)
		}
		if rel.Polymorphic {
			fkColumn = db.NamingStrategy.ColumnName("", polyValue+"_id")
			if field := join.Column(fkColumn); field != nil {
				fk = field
			}
		} else if fk != nil {
			fkColumn = fk.DBName
		}
		polyType := ""
		if rel.Polymorphic {
			typeColumn := polyValue + "_type"
			if rel.PolymorphicField != nil {
				typeColumn = rel.PolymorphicField.DBName
			} else {
				typeColumn = db.NamingStrategy.ColumnName("", typeColumn)
			}
			polyType = typeColumn
		}
		db.loadTargets(join, keys, fk, fkColumn, polyType, rel.Reference, sources, rel, entry)
	case schema.ManyToMany:
		db.loadM2M(sources, rel, entry)
	}
}

func collectKeys(values []reflect.Value, field *schema.Field) []any {
	seen := make(map[any]bool)
	keys := make([]any, 0, len(values))
	for _, value := range values {
		raw, ok := relationFieldValue(value, field)
		if !ok {
			continue
		}
		key := relationKey(raw)
		if key == nil || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, raw)
	}
	return keys
}

func (db *DB) loadTargets(join *schema.Table, keys []any, match *schema.Field, matchColumn, polyType string, sourceKey *schema.Field, sources []reflect.Value, rel *schema.Relation, entry *preloadEntry) {
	if len(keys) == 0 || matchColumn == "" {
		return
	}
	child := db.Session(&Session{NewDB: true}).getInstance()
	child.Statement.Model = reflect.New(join.Type).Interface()
	child.Statement.Preloads = entry.Children
	applyPreloadArgs(child, entry.Args)

	column := child.Statement.QuoteTo(matchColumn)
	expr := column + " IN (" + inPlaceholders(len(keys)) + ")"
	args := append([]any(nil), keys...)
	if polyType != "" {
		relValue := rel.PolymorphicValue
		if relValue == "" {
			relValue = db.NamingStrategy.ColumnName("", rel.Table.ModelName)
		}
		expr += " AND " + child.Statement.QuoteTo(polyType) + " = ?"
		args = append(args, relValue)
	}
	child.Where(expr, args...)

	dest := reflect.New(reflect.SliceOf(join.Type)).Interface()
	child.Find(dest)
	if child.Error != nil && !errors.Is(child.Error, ErrRecordNotFound) {
		db.AddError(child.Error)
		return
	}
	children, err := modelValues(dest)
	if err != nil {
		db.AddError(err)
		return
	}
	grouped := make(map[any][]reflect.Value)
	for _, item := range children {
		var raw any
		if match != nil {
			raw, _ = relationFieldValue(item, match)
		}
		key := relationKey(raw)
		grouped[key] = append(grouped[key], item)
	}
	assignRelation(sources, rel, sourceKey, grouped)
}

func (db *DB) loadM2M(sources []reflect.Value, rel *schema.Relation, entry *preloadEntry) {
	keys := collectKeys(sources, rel.Reference)
	if len(keys) == 0 {
		assignRelation(sources, rel, rel.Reference, nil)
		return
	}
	spec := db.m2mSpec(rel)
	pairs, err := db.queryJoinPairs(spec, keys)
	if err != nil {
		db.AddError(err)
		return
	}
	join := rel.JoinTable
	refs := make([]any, 0, len(pairs))
	for _, targets := range pairs {
		for _, target := range targets {
			found := false
			for _, existing := range refs {
				if relationKey(existing) == relationKey(target) {
					found = true
					break
				}
			}
			if !found {
				refs = append(refs, target)
			}
		}
	}
	if len(refs) == 0 {
		assignRelation(sources, rel, rel.Reference, nil)
		return
	}

	child := db.Session(&Session{NewDB: true}).getInstance()
	child.Statement.Model = reflect.New(join.Type).Interface()
	child.Statement.Preloads = entry.Children
	applyPreloadArgs(child, entry.Args)
	matchColumn := rel.ForeignKey.DBName
	refColumn := child.Statement.QuoteTo(matchColumn)
	child.Where(refColumn+" IN ("+inPlaceholders(len(refs))+")", refs...)

	dest := reflect.New(reflect.SliceOf(join.Type)).Interface()
	child.Find(dest)
	if child.Error != nil && !errors.Is(child.Error, ErrRecordNotFound) {
		db.AddError(child.Error)
		return
	}
	children, err := modelValues(dest)
	if err != nil {
		db.AddError(err)
		return
	}
	grouped := make(map[any][]reflect.Value)
	for _, item := range children {
		raw, _ := relationFieldValue(item, rel.ForeignKey)
		key := relationKey(raw)
		grouped[key] = append(grouped[key], item)
	}
	bySource := make(map[any][]reflect.Value)
	for sourceKey, targets := range pairs {
		bySource[sourceKey] = nil
		for _, target := range targets {
			bySource[sourceKey] = append(bySource[sourceKey], grouped[relationKey(target)]...)
		}
	}
	assignRelation(sources, rel, rel.Reference, bySource)
}

func (db *DB) queryJoinPairs(spec joinSpec, keys []any) (map[any][]any, error) {
	q := db.Session(&Session{NewDB: true}).getInstance()
	q.Statement.Table = spec.table
	q.Statement.AddClause(clause.Select{Columns: []string{spec.fkCol, spec.refCol}})
	q.Statement.AddClause(clause.From{Table: spec.table})
	column := q.Statement.QuoteTo(spec.fkCol)
	q.Statement.AddClause(clause.Where{Conds: []clause.Condition{{
		Exprs: []clause.Expression{clause.Expr(column+" IN ("+inPlaceholders(len(keys))+")", keys...)},
	}}})
	q.Statement.BuildClauses()
	ctx := statementContext(q.Statement)
	begin := time.Now()
	rows, err := q.Statement.ConnPool.QueryContext(ctx, string(q.Statement.SQL), q.Statement.Vars...)
	trace(q, ctx, begin, string(q.Statement.SQL), q.Statement.Vars, err)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", spec.table, err)
	}
	defer rows.Close()
	pairs := make(map[any][]any)
	for rows.Next() {
		var source, target any
		if err := rows.Scan(&source, &target); err != nil {
			return nil, fmt.Errorf("scan %s: %w", spec.table, err)
		}
		sk, tk := relationKey(source), relationKey(target)
		pairs[sk] = append(pairs[sk], tk)
	}
	return pairs, rows.Err()
}

func assignRelation(sources []reflect.Value, rel *schema.Relation, sourceField *schema.Field, grouped map[any][]reflect.Value) {
	for _, source := range sources {
		raw, ok := relationFieldValue(source, sourceField)
		if !ok {
			continue
		}
		items := grouped[relationKey(raw)]
		field := fieldAt(source, rel.Field.Index)
		if !field.IsValid() || !field.CanSet() {
			continue
		}
		switch rel.Kind {
		case schema.HasOne, schema.BelongsTo:
			if len(items) == 0 {
				if field.Kind() == reflect.Pointer && !field.IsNil() {
					field.Set(reflect.Zero(field.Type()))
				}
				continue
			}
			mounted := mountValue(items[0], field.Type())
			if mounted.IsValid() {
				field.Set(mounted)
			}
		case schema.HasMany, schema.ManyToMany:
			if field.Kind() != reflect.Slice {
				continue
			}
			out := reflect.MakeSlice(field.Type(), 0, len(items))
			for _, item := range items {
				mounted := mountValue(item, field.Type().Elem())
				if mounted.IsValid() {
					out = reflect.Append(out, mounted)
				}
			}
			field.Set(out)
		}
	}
}

func mountValue(value reflect.Value, target reflect.Type) reflect.Value {
	if !value.IsValid() {
		return reflect.Value{}
	}
	if value.Type().AssignableTo(target) {
		return value
	}
	if target.Kind() == reflect.Pointer && value.Type().AssignableTo(target.Elem()) {
		ptr := reflect.New(target.Elem())
		ptr.Elem().Set(value)
		return ptr
	}
	if value.Kind() == reflect.Pointer && value.Type().Elem().AssignableTo(target) {
		return value.Elem()
	}
	return reflect.Value{}
}

type joinKey struct {
	model reflect.Type
	field string
}

type joinSpec struct {
	table  string
	fkCol  string
	refCol string
}

type joinTableRegistry struct {
	mu      sync.RWMutex
	entries map[joinKey]joinSpec
}

func (r *joinTableRegistry) store(key joinKey, spec joinSpec) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[joinKey]joinSpec)
	}
	r.entries[key] = spec
}

func (r *joinTableRegistry) lookup(key joinKey) (joinSpec, bool) {
	if r == nil {
		return joinSpec{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.entries[key]
	return spec, ok
}

func (db *DB) m2mSpec(rel *schema.Relation) joinSpec {
	key := joinKey{model: rel.Table.Type, field: rel.Name}
	if spec, ok := db.joinTables.lookup(key); ok {
		return spec
	}
	return joinSpec{table: rel.M2MName, fkCol: rel.M2MForeignKey, refCol: rel.M2MReferences}
}
