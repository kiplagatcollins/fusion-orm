package fusion

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

type crudUser struct {
	ID        int64 `fusion:"pk,autoincrement"`
	Name      string
	Age       int
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (crudUser) TableName() string { return "crud_users" }

func setupCRUD(t *testing.T) *DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "crud.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if pool, err := db.DB(); err == nil {
			_ = pool.Close()
		}
	})
	ddl := `CREATE TABLE crud_users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT, age INTEGER, email TEXT,
		created_at DATETIME, updated_at DATETIME
	)`
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

func insertFixtures(t *testing.T, db *DB) {
	t.Helper()
	users := []crudUser{
		{Name: "alice", Age: 30, Email: "alice@example.com"},
		{Name: "bob", Age: 25, Email: "bob@example.com"},
		{Name: "carol", Age: 41, Email: "carol@example.com"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("Create fixtures: %v", err)
	}
}

func TestCreateBackfillAndTimestamps(t *testing.T) {
	db := setupCRUD(t)
	u := crudUser{Name: "dave", Age: 22}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == 0 {
		t.Error("primary key was not backfilled")
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Error("auto timestamps not set")
	}
	var got crudUser
	if err := db.First(&got, u.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if got.Name != "dave" || got.ID != u.ID {
		t.Errorf("got %+v", got)
	}
}

func TestCreateBatch(t *testing.T) {
	db := setupCRUD(t)
	users := make([]crudUser, 7)
	for i := range users {
		users[i].Name = fmt.Sprintf("u%d", i)
	}
	if err := db.CreateBatchSize(3).Create(&users).Error; err != nil {
		t.Fatalf("batch Create: %v", err)
	}
	for i, u := range users {
		if u.ID == 0 {
			t.Errorf("row %d has no id", i)
		}
	}
	var count int64
	if err := db.Model(&crudUser{}).Count(&count).Error; err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 7 {
		t.Errorf("count = %d, want 7", count)
	}
}

func TestFindFirstTakeLastNotFound(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)

	var users []crudUser
	if err := db.Find(&users).Error; err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(users) != 3 || users[0].Name != "alice" {
		t.Errorf("Find = %+v", users)
	}

	var first crudUser
	if err := db.First(&first).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	var last crudUser
	if err := db.Last(&last).Error; err != nil {
		t.Fatalf("Last: %v", err)
	}
	if first.Name != "alice" || last.Name != "carol" {
		t.Errorf("first=%s last=%s", first.Name, last.Name)
	}

	var one crudUser
	take := db.Take(&one, map[string]any{"name": "bob"})
	if take.Error != nil || one.Name != "bob" {
		t.Errorf("Take = %v, %+v", take.Error, one)
	}

	var missing crudUser
	if err := db.First(&missing, "name = ?", "nobody").Error; !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("First miss = %v", err)
	}
	var empty []crudUser
	if err := db.Find(&empty, "name = ?", "nobody").Error; err != nil || len(empty) != 0 {
		t.Errorf("Find miss = %v, %v", err, empty)
	}
}

func TestUpdateDeleteGuard(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)

	global := db.Model(&crudUser{}).Update("name", "everyone")
	if !errors.Is(global.Error, ErrMissingWhereClause) {
		t.Errorf("global Update = %v, want ErrMissingWhereClause", global.Error)
	}
	globalDelete := db.Model(&crudUser{}).Delete(nil)
	if !errors.Is(globalDelete.Error, ErrMissingWhereClause) {
		t.Errorf("global Delete = %v", globalDelete.Error)
	}

	ok := db.Model(&crudUser{}).Where("name = ?", "alice").Update("age", 31)
	if ok.Error != nil || ok.RowsAffected != 1 {
		t.Errorf("Update = %v, rows=%d", ok.Error, ok.RowsAffected)
	}
	var alice crudUser
	_ = db.First(&alice, "name = ?", "alice")
	if alice.Age != 31 {
		t.Errorf("age = %d", alice.Age)
	}

	updates := db.Model(&crudUser{}).Where("name = ?", "bob").Updates(map[string]any{"age": 26, "email": "new@example.com"})
	if updates.Error != nil {
		t.Fatalf("Updates: %v", updates.Error)
	}
	var bob crudUser
	_ = db.First(&bob, "name = ?", "bob")
	if bob.Age != 26 || bob.Email != "new@example.com" {
		t.Errorf("Updates result = %+v", bob)
	}

	del := db.Delete(&crudUser{Name: "carol"})
	if del.Error != nil {
		t.Errorf("Delete by value: %v", del.Error)
	}
	var count int64
	_ = db.Model(&crudUser{}).Count(&count)
	if count != 2 {
		t.Errorf("count after Delete = %d", count)
	}
}

func TestAllowGlobalUpdate(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)
	allowed := db.Session(&Session{AllowGlobalUpdate: true}).Model(&crudUser{}).Update("name", "x")
	if allowed.Error != nil || allowed.RowsAffected != 3 {
		t.Errorf("allowed global Update = %v, rows=%d", allowed.Error, allowed.RowsAffected)
	}
}

func TestScopesCountPluckScanRows(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)

	adults := func(tx *DB) *DB { return tx.Where("age >= ?", 30) }
	var users []crudUser
	if err := db.Scopes(adults).Find(&users).Error; err != nil {
		t.Fatalf("Scopes Find: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("adults = %d", len(users))
	}

	var count int64
	if err := db.Model(&crudUser{}).Count(&count).Error; err != nil || count != 3 {
		t.Errorf("Count = %d, %v", count, err)
	}
	var names []string
	if err := db.Model(&crudUser{}).Order("id").Pluck("name", &names).Error; err != nil {
		t.Fatalf("Pluck: %v", err)
	}
	if fmt.Sprint(names) != "[alice bob carol]" {
		t.Errorf("names = %v", names)
	}

	var mapped map[string]any
	if err := db.Model(&crudUser{}).Select("name, age").Find(&mapped).Error; err != nil {
		t.Fatalf("map Find: %v", err)
	}
	if mapped["name"] == nil {
		t.Errorf("map = %v", mapped)
	}

	rows, err := db.Model(&crudUser{}).Order("id").Rows()
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	if n != 3 {
		t.Errorf("Rows count = %d", n)
	}

	var scanned []crudUser
	if err := db.Model(&crudUser{}).Scan(&scanned).Error; err != nil {
		t.Fatalf("Scan: %v", err)
	}
}

func TestFindInBatches(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)
	var seen []string
	result := db.FindInBatches(&[]crudUser{}, 2, func(tx *DB, batch int) error {
		var rows []crudUser
		if err := tx.Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			seen = append(seen, row.Name)
		}
		return nil
	})
	if result.Error != nil {
		t.Fatalf("FindInBatches: %v", result.Error)
	}
	if fmt.Sprint(seen) != "[alice bob carol]" {
		t.Errorf("batches saw %v", seen)
	}
}

func TestFirstOrInitAndCreate(t *testing.T) {
	db := setupCRUD(t)
	var init crudUser
	res := db.FirstOrInit(&init, map[string]any{"name": "eve"}, map[string]any{"age": 29})
	if res.Error != nil || init.Name != "eve" || init.Age != 29 {
		t.Errorf("FirstOrInit = %v, %+v", res.Error, init)
	}
	var count int64
	_ = db.Model(&crudUser{}).Count(&count)
	if count != 0 {
		t.Errorf("FirstOrInit wrote a row: count=%d", count)
	}

	var created crudUser
	res = db.FirstOrCreate(&created, map[string]any{"name": "frank"})
	if res.Error != nil || created.ID == 0 {
		t.Errorf("FirstOrCreate = %v, %+v", res.Error, created)
	}
	_ = db.Model(&crudUser{}).Count(&count)
	if count != 1 {
		t.Errorf("count after FirstOrCreate = %d", count)
	}
}

func TestToSQLStable(t *testing.T) {
	db := setupCRUD(t)
	fn := func(tx *DB) *DB {
		return tx.Model(&crudUser{}).Where("age > ?", 18).Order("id DESC").Limit(5).Find(&[]crudUser{})
	}
	first := db.ToSQL(fn)
	second := db.ToSQL(fn)
	want := `SELECT * FROM "crud_users" WHERE age > 18 ORDER BY id DESC LIMIT 5`
	if first != want || second != first {
		t.Errorf("ToSQL = %q / %q, want %q", first, second, want)
	}
}

func TestCallbackProcessor(t *testing.T) {
	p := &CallbackProcessor{}
	ordered := func(name string) Callback {
		return func(db *DB) {
			db.Statement.Settings.Store("fusion:test:"+name, true)
		}
	}
	_ = p.Register("first", ordered("first"))
	_ = p.After("first", "third", ordered("third"))
	_ = p.Before("third", "second", ordered("second"))
	if err := p.Register("first", ordered("dup")); !errors.Is(err, ErrDuplicateCallback) {
		t.Errorf("duplicate = %v", err)
	}
	if err := p.Remove("missing"); !errors.Is(err, ErrCallbackNotFound) {
		t.Errorf("remove missing = %v", err)
	}
	if err := p.Replace("second", ordered("second")); err != nil {
		t.Errorf("replace: %v", err)
	}
	if err := p.Match(func(*DB) bool { return false }); err != nil {
		t.Errorf("match: %v", err)
	}
	db := setupCRUD(t)
	p.Execute(db)
	var names []string
	db.Statement.Settings.Range(func(k, v any) bool {
		names = append(names, strings.TrimPrefix(k.(string), "fusion:test:"))
		return true
	})
	if len(names) != 2 {
		t.Errorf("executed = %v", names)
	}
}

func TestSessionParseStrictTagsStillWorks(t *testing.T) {
	db := setupCRUD(t)
	table, err := db.Parse(&crudUser{})
	if err != nil || table.Name != "crud_users" || table.Column("created_at") == nil {
		t.Errorf("Parse = %v, %+v", err, table)
	}
	if _, err := db.Parse(&struct {
		X int `fusion:"x,bogus:v"`
	}{}); err != nil {
		t.Errorf("lenient tag should warn, got %v", err)
	}
}

func TestSchemaSentinelReexport(t *testing.T) {
	if !errors.Is(ErrInvalidTag, schema.ErrInvalidTag) {
		t.Error("ErrInvalidTag alias mismatch")
	}
}

func TestRowAndUpdateColumns(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)

	row := db.Model(&crudUser{}).Where("name = ?", "alice").Select("age").Row()
	if row == nil {
		t.Fatal("Row returned nil")
	}
	var age int
	if err := row.Scan(&age); err != nil || age != 30 {
		t.Errorf("Row.Scan = %v, age=%d", err, age)
	}

	res := db.Model(&crudUser{}).Where("name = ?", "alice").UpdateColumn("age", 33)
	if res.Error != nil || res.RowsAffected != 1 {
		t.Errorf("UpdateColumn = %v, rows=%d", res.Error, res.RowsAffected)
	}
	var alice crudUser
	_ = db.First(&alice, "name = ?", "alice")
	if alice.Age != 33 {
		t.Errorf("age = %d", alice.Age)
	}
	before := alice.UpdatedAt
	_ = db.Model(&crudUser{}).Where("name = ?", "alice").Update("age", 34)
	_ = db.First(&alice, "name = ?", "alice")
	if !alice.UpdatedAt.After(before) {
		t.Errorf("UpdatedAt not refreshed: before=%v after=%v", before, alice.UpdatedAt)
	}
}

type hookUser struct {
	ID        int64 `fusion:"pk,autoincrement"`
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	Events    []string `fusion:"-"`
}

func (hookUser) TableName() string { return "hook_users" }

var hookEvents []string

func (*hookUser) BeforeCreate(*DB) error {
	hookEvents = append(hookEvents, "before_create")
	return nil
}

func (*hookUser) AfterCreate(*DB) error {
	hookEvents = append(hookEvents, "after_create")
	return nil
}

func (*hookUser) AfterFind(*DB) error {
	hookEvents = append(hookEvents, "after_find")
	return nil
}

func TestHooksFire(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "hooks.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	ddl := `CREATE TABLE hook_users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, created_at DATETIME, updated_at DATETIME)`
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	hookEvents = nil
	u := hookUser{Name: "hooked"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	var found []hookUser
	if err := db.Find(&found).Error; err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := "[before_create after_create after_find]"
	if got := fmt.Sprint(hookEvents); got != want {
		t.Errorf("hooks = %s, want %s", got, want)
	}
}

func TestSaveUpdatesExistingRecord(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)
	if err := db.Save(&crudUser{ID: 1, Name: "saved"}).Error; err != nil {
		t.Fatalf("Save: %v", err)
	}
	var got crudUser
	if err := db.First(&got, 1).Error; err != nil || got.Name != "saved" {
		t.Errorf("saved row = %+v, %v", got, err)
	}
}
