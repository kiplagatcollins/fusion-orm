package schema

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidRelation reports a malformed or unresolvable relation tag.
var ErrInvalidRelation = errors.New("schema: invalid relation")

// RelationKind identifies a model relation.
type RelationKind string

const (
	HasOne     RelationKind = "has-one"
	HasMany    RelationKind = "has-many"
	BelongsTo  RelationKind = "belongs-to"
	ManyToMany RelationKind = "many2many"
)

// Relation describes an association between two parsed tables.
type Relation struct {
	Name             string
	Kind             RelationKind
	Field            *Field
	Table            *Table
	JoinTable        *Table
	M2MName          string
	M2MForeignKey    string
	M2MReferences    string
	ForeignKey       *Field
	Reference        *Field
	Polymorphic      bool
	PolymorphicValue string
	PolymorphicField *Field
	OnUpdate         string
	OnDelete         string
}

// References reports whether the declaring table owns the foreign-key
// constraint (belongs-to and non-PK has-one relations).
func (r *Relation) References() bool {
	if r.Kind != BelongsTo {
		return false
	}
	if r.ForeignKey == nil {
		return false
	}
	return !r.ForeignKey.PrimaryKey
}

func newRelation(ts *Tables, table *Table, field *Field, tag Tag) (*Relation, error) {
	kind := RelationKind(tag.Get("rel"))
	switch kind {
	case HasOne, HasMany, BelongsTo, ManyToMany:
	default:
		return nil, fmt.Errorf("%w: %s.%s has unknown rel %q", ErrInvalidRelation, table.Name, field.Name, kind)
	}

	targetType := field.IndirectType
	join, err := ts.get(targetType, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %s.%s: %v", ErrInvalidRelation, table.Name, field.Name, err)
	}

	rel := &Relation{
		Name:      field.Name,
		Kind:      kind,
		Field:     field,
		Table:     table,
		JoinTable: join,
	}
	if value, ok := tag.Lookup("polymorphic"); ok {
		rel.Polymorphic = true
		rel.PolymorphicValue = value
		if rel.PolymorphicValue == "" {
			rel.PolymorphicValue = snakeModel(table.Type.Name())
		}
		rel.PolymorphicField = lookupField(join, rel.PolymorphicValue+"_type")
	}
	if constraint, ok := tag.Lookup("constraint"); ok {
		rel.OnUpdate, rel.OnDelete = parseConstraint(constraint)
	}

	sourcePK := primaryField(table, "")
	targetPK := primaryField(join, "")

	switch kind {
	case BelongsTo:
		fkName := tag.Get("foreignkey")
		if fkName == "" {
			fkName = field.Name + "ID"
		}
		rel.ForeignKey = lookupField(table, fkName)
		if rel.ForeignKey == nil {
			return nil, fmt.Errorf("%w: %s.%s: no foreign key %q", ErrInvalidRelation, table.Name, field.Name, fkName)
		}
		rel.Reference = primaryField(join, tag.Get("references"))
	case HasOne, HasMany:
		fkName := tag.Get("foreignkey")
		if fkName == "" {
			fkName = table.Type.Name() + "ID"
		}
		rel.Reference = sourcePK
		if !rel.Polymorphic {
			rel.ForeignKey = lookupField(join, fkName)
			if rel.ForeignKey == nil {
				return nil, fmt.Errorf("%w: %s.%s: %s has no column %q", ErrInvalidRelation, table.Name, field.Name, join.Name, fkName)
			}
		}
	case ManyToMany:
		rel.M2MName = tag.Get("many2many")
		if rel.M2MName == "" {
			names := []string{table.Name, join.Name}
			sort.Strings(names)
			rel.M2MName = table.namer.JoinTableName(names[0] + "_" + names[1])
		} else {
			rel.M2MName = table.namer.JoinTableName(rel.M2MName)
		}
		rel.M2MForeignKey = snakeModel(table.Type.Name()) + "_id"
		rel.M2MReferences = snakeModel(join.Type.Name()) + "_id"
		rel.Reference = sourcePK
		rel.ForeignKey = targetPK
	}
	return rel, nil
}

func primaryField(t *Table, preferred string) *Field {
	if preferred != "" {
		if f := lookupField(t, preferred); f != nil {
			return f
		}
	}
	if len(t.PKs) > 0 {
		return t.PKs[0]
	}
	return lookupField(t, "id")
}

func lookupField(t *Table, name string) *Field {
	if f := t.FieldMap[name]; f != nil {
		return f
	}
	if f := t.DBNames[name]; f != nil {
		return f
	}
	return t.DBNames[snakeModel(name)]
}
