package fusion

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

func BenchmarkBatchInsert10k(b *testing.B) {
	dsn := filepath.Join(b.TempDir(), "batch.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	if err := db.Exec(`CREATE TABLE crud_users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT, age INTEGER, email TEXT, created_at DATETIME, updated_at DATETIME
	)`).Error; err != nil {
		b.Fatal(err)
	}

	users := make([]crudUser, 10_000)
	for i := range users {
		users[i].Name = fmt.Sprintf("u%d", i)
		users[i].Age = i % 100
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := db.CreateBatchSize(1000).Create(&users).Error; err != nil {
			b.Fatal(err)
		}
	}
}
