// Composite primary key correctness (audit groups D/Q caveats): scalar id
// conditions, Save matching, First/Last ordering, FindInBatches pagination,
// and relation inference must never silently bind or order by PKs[0] alone.
package fusion

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/schema"
)

// cpkSQLHook records every statement's SQL text.
type cpkSQLHook struct {
	sqls *[]string
}

func (h *cpkSQLHook) BeforeQuery(_ context.Context, event *QueryEvent) context.Context {
	*h.sqls = append(*h.sqls, event.SQL)
	return event.Context
}

func (h *cpkSQLHook) AfterQuery(context.Context, *QueryEvent) {}

type cpkRow struct {
	A    int64  `fusion:"pk"`
	B    int64  `fusion:"pk"`
	Name string `fusion:"notnull"`
}

const cpkDDL = `CREATE TABLE cpk_rows (
	a INTEGER NOT NULL,
	b INTEGER NOT NULL,
	name TEXT NOT NULL,
	PRIMARY KEY (a, b)
)`

func cpkSeed(t *testing.T, db *DB) {
	t.Helper()
	if err := db.Exec(cpkDDL).Error; err != nil {
		t.Fatal(err)
	}
	rows := []cpkRow{
		{A: 2, B: 1, Name: "b21"},
		{A: 1, B: 2, Name: "b12"},
		{A: 1, B: 1, Name: "b11"},
		{A: 2, B: 2, Name: "b22"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
}

// TestCompositePKScalarCondErrors: a scalar id condition cannot bind a
// composite key — it must error instead of matching every sibling sharing
// PKs[0] (audit D/Q).
func TestCompositePKScalarCondErrors(t *testing.T) {
	db := openTestDB(t)
	cpkSeed(t, db)

	var row cpkRow
	if err := db.First(&row, 42).Error; !errors.Is(err, ErrInvalidValue) {
		t.Errorf("First(42) error = %v, want ErrInvalidValue", err)
	}
	if err := db.Find(&[]cpkRow{}, int64(7)).Error; !errors.Is(err, ErrInvalidValue) {
		t.Errorf("Find(7) error = %v, want ErrInvalidValue", err)
	}
	if err := db.Delete(&cpkRow{}, 42).Error; !errors.Is(err, ErrInvalidValue) {
		t.Errorf("Delete(42) error = %v, want ErrInvalidValue", err)
	}
	var count int64
	if err := db.Model(&cpkRow{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("failed scalar conds must not touch rows: count = %d, want 4", count)
	}
}

// TestCompositePKSaveUpdatesExactRow: Save matches the full key tuple, so
// siblings sharing the first key column are untouched.
func TestCompositePKSaveUpdatesExactRow(t *testing.T) {
	db := openTestDB(t)
	cpkSeed(t, db)

	if err := db.Save(&cpkRow{A: 1, B: 2, Name: "edited"}).Error; err != nil {
		t.Fatal(err)
	}
	var edited cpkRow
	if err := db.Where("a = ? AND b = ?", 1, 2).First(&edited).Error; err != nil {
		t.Fatal(err)
	}
	if edited.Name != "edited" {
		t.Errorf("(1,2).Name = %q, want edited", edited.Name)
	}
	for _, key := range [][2]int64{{1, 1}, {2, 1}, {2, 2}} {
		var row cpkRow
		if err := db.Where("a = ? AND b = ?", key[0], key[1]).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.Name == "edited" {
			t.Errorf("sibling (%d,%d) was modified by Save", key[0], key[1])
		}
	}
}

// TestCompositePKSaveCreatesWhenKeyPartial: any zero PK field means the row
// is not persisted — Save inserts instead of updating a partial-key match.
func TestCompositePKSaveCreatesWhenKeyPartial(t *testing.T) {
	db := openTestDB(t)
	cpkSeed(t, db)

	if err := db.Save(&cpkRow{A: 3, Name: "fresh"}).Error; err != nil {
		t.Fatal(err)
	}
	var row cpkRow
	if err := db.Where("a = ? AND b = ?", 3, 0).First(&row).Error; err != nil {
		t.Fatalf("partial-key Save must insert: %v", err)
	}
	if row.Name != "fresh" {
		t.Errorf("inserted Name = %q, want fresh", row.Name)
	}
}

// TestCompositePKFirstLastTupleOrder: First/Last order by the whole key
// tuple, so results are deterministic when the first column ties.
func TestCompositePKFirstLastTupleOrder(t *testing.T) {
	db := openTestDB(t)
	cpkSeed(t, db)

	var first cpkRow
	if err := db.First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if first.A != 1 || first.B != 1 {
		t.Errorf("First = (%d,%d), want (1,1)", first.A, first.B)
	}
	var last cpkRow
	if err := db.Last(&last).Error; err != nil {
		t.Fatal(err)
	}
	if last.A != 2 || last.B != 2 {
		t.Errorf("Last = (%d,%d), want (2,2)", last.A, last.B)
	}
}

// TestCompositePKFindInBatches: row-value keyset pagination visits every row
// exactly once, in tuple order, across batch boundaries.
func TestCompositePKFindInBatches(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(cpkDDL).Error; err != nil {
		t.Fatal(err)
	}
	rows := make([]cpkRow, 0, 7)
	for _, key := range [][2]int64{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}, {3, 1}, {3, 2}} {
		rows = append(rows, cpkRow{A: key[0], B: key[1], Name: "n"})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	var got []cpkRow
	pages := 0
	if err := db.FindInBatches(&[]cpkRow{}, 3, func(tx *DB, batch int) error {
		pages++
		var rows []cpkRow
		if err := tx.Find(&rows).Error; err != nil {
			return err
		}
		got = append(got, rows...)
		return nil
	}).Error; err != nil {
		t.Fatal(err)
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
	if len(got) != 7 {
		t.Fatalf("visited %d rows, want 7", len(got))
	}
	seen := map[[2]int64]bool{}
	for i, row := range got {
		key := [2]int64{row.A, row.B}
		if seen[key] {
			t.Fatalf("row %v visited twice", key)
		}
		seen[key] = true
		if i > 0 {
			prev := got[i-1]
			if prev.A > row.A || (prev.A == row.A && prev.B >= row.B) {
				t.Fatalf("rows out of tuple order: (%d,%d) then (%d,%d)", prev.A, prev.B, row.A, row.B)
			}
		}
	}
}

// TestCompositePKFindInBatchesUsesRowValues: the composite cursor renders as
// a row-value comparison, not a single-column inequality.
func TestCompositePKFindInBatchesUsesRowValues(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(cpkDDL).Error; err != nil {
		t.Fatal(err)
	}
	var sqls []string
	db = db.QueryHook(&cpkSQLHook{sqls: &sqls})
	rows := []cpkRow{{A: 1, B: 1, Name: "n"}, {A: 1, B: 2, Name: "n"}, {A: 2, B: 1, Name: "n"}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.FindInBatches(&[]cpkRow{}, 2, func(tx *DB, batch int) error {
		var rows []cpkRow
		return tx.Find(&rows).Error
	}).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sql := range sqls {
		if strings.Contains(sql, ") > (") && strings.Contains(sql, "?, ?") {
			found = true
		}
	}
	if !found {
		t.Errorf("no row-value keyset comparison in %v", sqls)
	}
}

// TestCompositePKFirstOrCreate: Create happens only when the whole key tuple
// is zero; a fully set tuple is treated as persisted.
func TestCompositePKFirstOrCreate(t *testing.T) {
	db := openTestDB(t)
	cpkSeed(t, db)

	var created cpkRow
	if err := db.FirstOrCreate(&created, map[string]any{"name": "ghost"}).Error; err != nil {
		t.Fatal(err)
	}
	if created.A != 0 || created.B != 0 || created.Name != "ghost" {
		t.Errorf("created = %+v, want zero key with name ghost", created)
	}

	var count int64
	if err := db.Model(&cpkRow{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("count after create = %d, want 5", count)
	}

	preset := cpkRow{A: 9, B: 9, Name: "preset"}
	if err := db.FirstOrCreate(&preset, map[string]any{"name": "nope"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&cpkRow{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("fully-set key must not create: count = %d, want 5", count)
	}
}

// ---------------------------------------------------------------- relations

type cpkChild struct {
	ID      int64   `fusion:"pk,autoincrement"`
	ParentA int64   `fusion:"notnull"`
	Parent  *cpkRow `fusion:"rel:belongs-to,foreignKey:ParentA"`
}

// TestCompositePKBelongsToNeedsReferences: belongs-to onto a composite-key
// table must name its referenced column instead of guessing PKs[0].
func TestCompositePKBelongsToNeedsReferences(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(cpkDDL).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE cpk_children (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		parent_a INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}

	err := db.Model(&cpkChild{}).Find(&[]cpkChild{}).Error
	if !errors.Is(err, schema.ErrInvalidRelation) {
		t.Fatalf("error = %v, want schema.ErrInvalidRelation", err)
	}
	if !strings.Contains(err.Error(), "composite primary key") {
		t.Errorf("error should name the composite key: %v", err)
	}
}

type cpkChildRef struct {
	ID      int64   `fusion:"pk,autoincrement"`
	ParentB int64   `fusion:"notnull"`
	Parent  *cpkRow `fusion:"rel:belongs-to,foreignKey:ParentB,references:B"`
}

// TestCompositePKBelongsToWithReferences: an explicit single-column
// reference onto a composite-key table keeps working.
func TestCompositePKBelongsToWithReferences(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(cpkDDL).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE cpk_child_refs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		parent_b INTEGER NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	parents := []cpkRow{{A: 1, B: 2, Name: "p12"}}
	if err := db.Create(&parents).Error; err != nil {
		t.Fatal(err)
	}
	kids := []cpkChildRef{{ParentB: 2}}
	if err := db.Create(&kids).Error; err != nil {
		t.Fatal(err)
	}

	var got []cpkChildRef
	if err := db.Model(&cpkChildRef{}).Preload("Parent").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Parent == nil || got[0].Parent.Name != "p12" {
		t.Errorf("preloaded parent = %+v, want p12", got[0].Parent)
	}
}

type cpkCompositeParent struct {
	A    int64 `fusion:"pk"`
	B    int64 `fusion:"pk"`
	Name string
	Kids []cpkKid `fusion:"rel:has-many,foreignKey:ParentA"`
}

type cpkKid struct {
	ID      int64 `fusion:"pk,autoincrement"`
	ParentA int64
}

// TestCompositePKHasManyRejected: has-one/has-many from a composite-key
// source cannot be expressed with a single FK column — parse must fail
// loudly instead of preloading by PKs[0].
func TestCompositePKHasManyRejected(t *testing.T) {
	db := openTestDB(t)
	err := db.Model(&cpkCompositeParent{}).Find(&[]cpkCompositeParent{}).Error
	if !errors.Is(err, schema.ErrInvalidRelation) {
		t.Fatalf("error = %v, want schema.ErrInvalidRelation", err)
	}
}

type cpkM2MSource struct {
	A    int64    `fusion:"pk"`
	B    int64    `fusion:"pk"`
	Pals []cpkKid `fusion:"rel:many2many,many2many:cpk_pals"`
}

// TestCompositePKMany2ManyRejected: junction columns reference one key
// column per side; a composite key on either side must fail at parse time.
func TestCompositePKMany2ManyRejected(t *testing.T) {
	db := openTestDB(t)
	err := db.Model(&cpkM2MSource{}).Find(&[]cpkM2MSource{}).Error
	if !errors.Is(err, schema.ErrInvalidRelation) {
		t.Fatalf("error = %v, want schema.ErrInvalidRelation", err)
	}
}
