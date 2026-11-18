package fusion

import (
	"path/filepath"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

type genUser struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
	Age  int
}

func setupGenerics(t *testing.T) *DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "generics.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	if err := db.Exec(`CREATE TABLE gen_users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, age INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestGenericsCRUD(t *testing.T) {
	db := setupGenerics(t)
	var api Interface[genUser] = G[genUser](db)

	u := genUser{Name: "g", Age: 20}
	if err := api.Create(&u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := G[genUser](db).First(u.ID)
	if err != nil || got.Name != "g" {
		t.Fatalf("First = %+v, %v", got, err)
	}
	withResult := G[genUser](db).WithResult(genUser{})
	got, err = withResult.First(u.ID)
	if err != nil || got.ID != u.ID {
		t.Errorf("WithResult First = %+v, %v", got, err)
	}
	users, err := G[genUser](db).Where("age > ?", 18).Order("id").Find()
	if err != nil || len(users) != 1 {
		t.Errorf("Find = %+v, %v", users, err)
	}
	if err := G[genUser](db).Where("id = ?", u.ID).Update("name", "renamed"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var count int64
	if err := G[genUser](db).Count(&count); err != nil || count != 1 {
		t.Errorf("Count = %d, %v", count, err)
	}
	if err := G[genUser](db).Where("id = ?", u.ID).Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := G[genUser](db).First(u.ID); err == nil {
		t.Error("First after Delete should return ErrRecordNotFound")
	}
}

func TestGenericsSQLParity(t *testing.T) {
	db := setupGenerics(t)
	reflection := db.ToSQL(func(tx *DB) *DB {
		var out []genUser
		return tx.Model(&genUser{}).Where("age > ?", 18).Order("id").Find(&out)
	})
	generic := G[genUser](db).Where("age > ?", 18).Order("id").ToSQL(func(tx *DB) *DB {
		var out []genUser
		return tx.Find(&out)
	})
	if reflection == "" || generic != reflection {
		t.Errorf("generic SQL = %q, reflection SQL = %q", generic, reflection)
	}
}
