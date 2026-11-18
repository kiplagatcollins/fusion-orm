package fusion

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

type countingPool struct {
	ConnPool
	prepares int
}

func (p *countingPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	p.prepares++
	return p.ConnPool.PrepareContext(ctx, query)
}

func TestPreparedStatementsAndTransaction(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "prepared.db")
	db, err := Open(sqlite.Open(dsn), &Config{
		Logger: logger.Discard, PrepareStmt: true, PreparedStmtCacheSize: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	if err := db.Exec(`CREATE TABLE prepared_users (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO prepared_users (name) VALUES (?)`, "a").Error; err != nil {
		t.Fatal(err)
	}
	var name string
	for i := 0; i < 2; i++ {
		rows, err := db.Model(&struct {
			ID   int64 `fusion:"pk"`
			Name string
		}{}).Table("prepared_users").Where("name = ?", "a").Rows()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id, &name); err != nil {
				t.Fatal(err)
			}
		}
		_ = rows.Close()
	}
	if name != "a" {
		t.Errorf("name = %q", name)
	}
	pool, ok := db.Config.ConnPool.(*preparedPool)
	if !ok {
		t.Fatal("PrepareStmt did not install the wrapper")
	}
	if pool.cache.len() == 0 || pool.cache.len() > 4 {
		t.Errorf("cache size = %d", pool.cache.len())
	}
	if err := db.Transaction(func(tx *DB) error {
		return tx.Exec(`INSERT INTO prepared_users (name) VALUES (?)`, "b").Error
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	var count int64
	if err := db.Model(&struct {
		ID int64 `fusion:"pk"`
	}{}).Table("prepared_users").Count(&count).Error; err != nil || count != 2 {
		t.Errorf("count = %d, %v", count, err)
	}
}

func TestStmtCacheLRUAndTTL(t *testing.T) {
	db := openTestDB(t)
	pool := &countingPool{ConnPool: db.Statement.ConnPool}
	cache := newStmtCache(1, time.Millisecond)
	ctx := context.Background()

	if _, err := cache.get(ctx, pool, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.get(ctx, pool, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if pool.prepares != 1 {
		t.Fatalf("prepares = %d, want 1", pool.prepares)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := cache.get(ctx, pool, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if pool.prepares != 2 {
		t.Errorf("prepares after TTL = %d, want 2", pool.prepares)
	}
	if _, err := cache.get(ctx, pool, `SELECT 2`); err != nil {
		t.Fatal(err)
	}
	if cache.len() != 1 {
		t.Errorf("cache size after LRU eviction = %d, want 1", cache.len())
	}
	cache.remove(`SELECT 2`)
	if cache.len() != 0 {
		t.Errorf("cache size after remove = %d", cache.len())
	}
	cache.close()
}
