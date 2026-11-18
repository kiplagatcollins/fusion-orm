package schema

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type auditFields struct {
	ID        int64 `fusion:"pk,autoincrement"`
	CreatedAt time.Time
}

type tableUser struct {
	auditFields
	Name string
	Addr address `fusion:"embed:addr_"`
}

type address struct {
	City string `fusion:"city"`
}

func (tableUser) TableName() string { return "custom_users" }

func TestTableCacheAndTabler(t *testing.T) {
	tables := NewTables(nil, false, nil)
	first, err := tables.Parse(&tableUser{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	second, err := tables.Get(reflect.TypeFor[tableUser]())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if first != second {
		t.Error("repeat parse did not return the cached *Table")
	}
	if first.Name != "custom_users" {
		t.Errorf("Tabler table = %q", first.Name)
	}
	for _, column := range []string{"id", "created_at", "name", "addr_city"} {
		if first.Column(column) == nil {
			t.Errorf("missing column %q", column)
		}
	}
	if !first.Field("CreatedAt").AutoCreateTime {
		t.Error("embedded CreatedAt not marked auto-create")
	}
	if got := first.Column("addr_city").Index; len(got) != 2 || got[0] != 2 || got[1] != 0 {
		t.Errorf("embedded index = %v", got)
	}
}

type fullModel struct {
	auditFields
	Name   string     `fusion:"user_name,notnull,index"`
	City   address    `fusion:"embed:addr_"`
	Groups []relGroup `fusion:"rel:many2many"`
}

func TestRepeatParseWithTagsEmbedsRelations(t *testing.T) {
	tables := NewTables(nil, false, nil)
	first, err := tables.Parse(&fullModel{})
	if err != nil {
		t.Fatalf("first Parse: %v", err)
	}
	second, err := tables.Parse(&fullModel{})
	if err != nil {
		t.Fatalf("second Parse: %v", err)
	}
	if first != second {
		t.Fatal("repeat parse returned a different *Table")
	}
	if first.Column("user_name") == nil || first.Column("addr_city") == nil {
		t.Errorf("columns = %v", first.DBNames)
	}
	if rel := first.Relations["Groups"]; rel == nil || rel.JoinTable != second.Relations["Groups"].JoinTable {
		t.Error("relation not cached consistently")
	}
}

func TestConcurrentParse(t *testing.T) {
	tables := NewTables(nil, false, nil)
	const workers = 16
	results := make([]*Table, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = tables.Get(reflect.TypeFor[tableUser]())
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if results[i] != results[0] {
			t.Fatalf("worker %d got a different *Table", i)
		}
	}
}

type duplicateA struct {
	Name string
}
type duplicateB struct {
	Name string
}
type ambiguous struct {
	duplicateA
	duplicateB
}

func TestAmbiguousEmbeddedFields(t *testing.T) {
	_, err := NewTables(nil, false, nil).Parse(&ambiguous{})
	if !errors.Is(err, ErrAmbiguousField) {
		t.Errorf("Parse = %v, want ErrAmbiguousField", err)
	}
}

func TestRecursiveModel(t *testing.T) {
	type node struct {
		ID       int64 `fusion:"pk"`
		ParentID int64
		Parent   *node  `fusion:"rel:belongs-to"`
		Children []node `fusion:"rel:has-many,foreignKey:ParentID"`
	}
	table, err := NewTables(nil, false, nil).Parse(&node{})
	if err != nil {
		t.Fatalf("recursive Parse: %v", err)
	}
	if len(table.Fields) != 2 || len(table.Relations) != 2 {
		t.Errorf("fields=%d relations=%d", len(table.Fields), len(table.Relations))
	}
	if table.Relations["Parent"].JoinTable != table || table.Relations["Children"].JoinTable != table {
		t.Error("recursive relation resolved to a different table")
	}
}

type hookModel struct{}

func (hookModel) BeforeCreate() error { return nil }
func (hookModel) AfterFind() error    { return nil }

func TestHookDetection(t *testing.T) {
	table, err := NewTables(nil, false, nil).Parse(&hookModel{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !table.Hooks.BeforeCreate || !table.Hooks.AfterFind {
		t.Errorf("hooks = %+v", table.Hooks)
	}
	if table.Hooks.BeforeUpdate || table.Hooks.AfterDelete {
		t.Errorf("unexpected hooks = %+v", table.Hooks)
	}
}

func TestInvalidModel(t *testing.T) {
	if _, err := NewTables(nil, false, nil).Parse(nil); !errors.Is(err, ErrInvalidModel) {
		t.Errorf("nil = %v", err)
	}
	if _, err := NewTables(nil, false, nil).Parse(42); !errors.Is(err, ErrInvalidModel) {
		t.Errorf("int = %v", err)
	}
}
