package fusion

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

type scanProfile struct {
	ID      int64 `fusion:"pk"`
	OwnerID int64
	Bio     string `fusion:"bio"`
}

type scanOwner struct {
	ID      int64 `fusion:"pk"`
	Name    string
	Profile *scanProfile `fusion:"rel:has-one,foreignKey:owner_id"`
}

func TestScanAliasColumns(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "scan.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	if err := db.Exec(`CREATE TABLE scan_owners (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&scanOwner{ID: 7, Name: "owner"}).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}

	var owners []scanOwner
	res := db.Model(&scanOwner{}).
		Select(`id, name, 'mounted' AS profile__bio`).
		Scan(&owners)
	if res.Error != nil {
		t.Fatalf("Scan: %v", res.Error)
	}
	if len(owners) != 1 || owners[0].Profile == nil || owners[0].Profile.Bio != "mounted" {
		t.Errorf("alias scan = %+v", owners)
	}
}

func TestScanScalarAndMap(t *testing.T) {
	db := setupCRUD(t)
	insertFixtures(t, db)

	var ages []int64
	if err := db.Model(&crudUser{}).Order("id").Pluck("age", &ages).Error; err != nil {
		t.Fatalf("Pluck: %v", err)
	}
	if fmt.Sprint(ages) != "[30 25 41]" {
		t.Errorf("ages = %v", ages)
	}
	var total int64
	if err := db.Model(&crudUser{}).Select(`COUNT(*) AS total`).Scan(&total).Error; err != nil || total != 3 {
		t.Errorf("scalar Scan = %d, %v", total, err)
	}
}
