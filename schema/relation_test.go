package schema

import (
	"errors"
	"testing"
)

type relUser struct {
	ID      int64 `fusion:"pk"`
	Name    string
	OwnerID int64
	Profile *relProfile `fusion:"rel:has-one,foreignKey:UserID"`
	Posts   []relPost   `fusion:"rel:has-many,foreignKey:UserID"`
	Groups  []relGroup  `fusion:"rel:many2many,many2many:user_groups"`
	Account *relAccount `fusion:"rel:belongs-to,foreignKey:OwnerID,references:ID,constraint:onUpdate:CASCADE|onDelete:SET NULL"`
}

type relProfile struct {
	ID     int64 `fusion:"pk"`
	UserID int64
	Bio    string
}

type relPost struct {
	ID        int64 `fusion:"pk"`
	UserID    int64
	OwnerID   int64
	OwnerType string
	Title     string
}

type relGroup struct {
	ID   int64 `fusion:"pk"`
	Name string
}

type relAccount struct {
	ID      int64 `fusion:"pk"`
	OwnerID int64
}

func TestRelationKindsAndFKPairs(t *testing.T) {
	tables := NewTables(nil, false, nil)
	tests := []struct {
		name       string
		kind       RelationKind
		fk, ref    string
		joinTable  string
		references bool
	}{
		{"Profile", HasOne, "user_id", "id", "rel_profiles", false},
		{"Posts", HasMany, "user_id", "id", "rel_posts", false},
		{"Groups", ManyToMany, "id", "id", "rel_groups", false},
		{"Account", BelongsTo, "owner_id", "id", "rel_accounts", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel, err := parseRelation(tables, tt.name)
			if err != nil {
				t.Fatalf("relation: %v", err)
			}
			if rel.Kind != tt.kind || rel.JoinTable.Name != tt.joinTable || rel.References() != tt.references {
				t.Errorf("kind=%s join=%s references=%v", rel.Kind, rel.JoinTable.Name, rel.References())
			}
			if rel.ForeignKey != nil && rel.ForeignKey.DBName != tt.fk {
				t.Errorf("FK = %v, want %s", rel.ForeignKey, tt.fk)
			}
			if rel.Reference != nil && rel.Reference.DBName != tt.ref {
				t.Errorf("Reference = %v, want %s", rel.Reference, tt.ref)
			}
		})
	}
}

func TestManyToManyNames(t *testing.T) {
	rel, err := parseRelation(NewTables(nil, false, nil), "Groups")
	if err != nil {
		t.Fatal(err)
	}
	if rel.M2MName != "user_groups" || rel.M2MForeignKey != "rel_user_id" || rel.M2MReferences != "rel_group_id" {
		t.Errorf("m2m = %q, %q, %q", rel.M2MName, rel.M2MForeignKey, rel.M2MReferences)
	}
}

func TestPolymorphicRelation(t *testing.T) {
	type item struct {
		ID        int64 `fusion:"pk"`
		OwnerID   int64
		OwnerType string
	}
	type owner struct {
		ID    int64  `fusion:"pk"`
		Items []item `fusion:"rel:has-many,polymorphic:owner"`
	}
	tables := NewTables(nil, false, nil)
	table, err := tables.Parse(&owner{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rel := table.Relations["Items"]
	if rel == nil || !rel.Polymorphic || rel.PolymorphicValue != "owner" {
		t.Fatalf("polymorphic relation = %+v", rel)
	}
	if rel.PolymorphicField == nil || rel.PolymorphicField.DBName != "owner_type" {
		t.Errorf("polymorphic type field = %v", rel.PolymorphicField)
	}
	if rel.Reference == nil || rel.Reference.DBName != "id" {
		t.Errorf("polymorphic reference = %v", rel.Reference)
	}
}

func TestRelationConstraints(t *testing.T) {
	rel, err := parseRelation(NewTables(nil, false, nil), "Account")
	if err != nil {
		t.Fatal(err)
	}
	if rel.OnUpdate != "CASCADE" || rel.OnDelete != "SET NULL" {
		t.Errorf("constraints = %q, %q", rel.OnUpdate, rel.OnDelete)
	}
}

func TestInvalidRelation(t *testing.T) {
	_, err := NewTables(nil, false, nil).Parse(&badRelModel{})
	if !errors.Is(err, ErrInvalidRelation) {
		t.Errorf("bogus rel = %v, want ErrInvalidRelation", err)
	}
}

type badRelModel struct {
	Bad []relPost `fusion:"rel:bogus"`
}

func parseRelation(tables *Tables, name string) (*Relation, error) {
	table, err := tables.Parse(&relUser{})
	if err != nil {
		return nil, err
	}
	return table.Relations[name], nil
}
