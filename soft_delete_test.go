package fusion

import (
	"path/filepath"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

type softUser struct {
	ID        int64 `fusion:"pk,autoincrement"`
	Name      string
	DeletedAt DeletedAt
}

type softFlag struct {
	ID      int64 `fusion:"pk,autoincrement"`
	Name    string
	Removed bool `fusion:"softdelete,zerovalue:false,deletevalue:true"`
}

func setupSoftDelete(t *testing.T) *DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "soft.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	for _, ddl := range []string{
		`CREATE TABLE soft_users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, deleted_at TIMESTAMP)`,
		`CREATE TABLE soft_flags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, removed BOOLEAN)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&[]softUser{{Name: "a"}, {Name: "b"}}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSoftDeleteRoundTrip(t *testing.T) {
	db := setupSoftDelete(t)

	var all []softUser
	if err := db.Model(&softUser{}).Find(&all).Error; err != nil || len(all) != 2 {
		t.Fatalf("Find = %+v, %v", all, err)
	}
	if err := db.Delete(&softUser{}, all[0].ID).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var live []softUser
	if err := db.Model(&softUser{}).Find(&live).Error; err != nil || len(live) != 1 || live[0].Name != "b" {
		t.Errorf("live rows = %+v, %v", live, err)
	}
	var count int64
	if err := db.Model(&softUser{}).Count(&count).Error; err != nil || count != 1 {
		t.Errorf("count = %d, %v", count, err)
	}
	var hidden softUser
	if err := db.Unscoped().First(&hidden, all[0].ID).Error; err != nil {
		t.Fatalf("Unscoped First: %v", err)
	}
	if !hidden.DeletedAt.Valid {
		t.Error("deleted_at not populated")
	}
	if err := db.Restore(&softUser{ID: all[0].ID}).Error; err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := db.Model(&softUser{}).Count(&count).Error; err != nil || count != 2 {
		t.Errorf("count after restore = %d, %v", count, err)
	}
}

func TestSoftDeleteCustomZero(t *testing.T) {
	db := setupSoftDelete(t)
	if err := db.Create(&softFlag{Name: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&softFlag{}, 1).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&softFlag{}).Count(&count).Error; err != nil || count != 0 {
		t.Errorf("count = %d, %v", count, err)
	}
	if err := db.Unscoped().Model(&softFlag{}).Count(&count).Error; err != nil || count != 1 {
		t.Errorf("unscoped count = %d, %v", count, err)
	}
}
