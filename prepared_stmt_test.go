package fusion

import (
	"context"
	"database/sql"
	"errors"
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

	_, release, err := cache.get(ctx, pool, `SELECT 1`)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, release, err = cache.get(ctx, pool, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	release()
	if pool.prepares != 1 {
		t.Fatalf("prepares = %d, want 1", pool.prepares)
	}
	time.Sleep(2 * time.Millisecond)
	if _, release, err = cache.get(ctx, pool, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	release()
	if pool.prepares != 2 {
		t.Errorf("prepares after TTL = %d, want 2", pool.prepares)
	}
	if _, release, err = cache.get(ctx, pool, `SELECT 2`); err != nil {
		t.Fatal(err)
	}
	release()
	if cache.len() != 1 {
		t.Errorf("cache size after LRU eviction = %d, want 1", cache.len())
	}
	cache.remove(`SELECT 2`)
	if cache.len() != 0 {
		t.Errorf("cache size after remove = %d", cache.len())
	}
	cache.close()
}

type prepareErrorPool struct {
	ConnPool
	err error
}

func (p prepareErrorPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, p.err
}

func TestPreparedPoolPrepareError(t *testing.T) {
	want := errors.New("prepare failed")
	pool := newPreparedPool(prepareErrorPool{err: want}, 1, 0)
	if _, err := pool.ExecContext(context.TODO(), `SELECT 1`); !errors.Is(err, want) {
		t.Errorf("ExecContext = %v, want %v", err, want)
	}
	if _, err := pool.QueryContext(context.TODO(), `SELECT 1`); !errors.Is(err, want) {
		t.Errorf("QueryContext = %v, want %v", err, want)
	}
}

func TestPreparedTxWithoutCommitter(t *testing.T) {
	db := openTestDB(t)
	tx := &preparedTx{tx: db.Statement.ConnPool, cache: newStmtCache(1, 0)}
	if err := tx.Commit(); !errors.Is(err, ErrInvalidTransaction) {
		t.Errorf("Commit = %v, want ErrInvalidTransaction", err)
	}
	if err := tx.Rollback(); !errors.Is(err, ErrInvalidTransaction) {
		t.Errorf("Rollback = %v, want ErrInvalidTransaction", err)
	}
}

// TestAutoMigrateInvalidatesPreparedCache: DDL may change tables under a
// cached prepared statement; AutoMigrate must drop the cache (GORM #7255
// class).
func TestAutoMigrateInvalidatesPreparedCache(t *testing.T) {
	db := openTestDB(t, &Config{PrepareStmt: true, Logger: logger.Discard})
	if err := db.Exec(`CREATE TABLE inv_users (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Statement.ConnPool.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM inv_users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	pool, ok := db.Config.ConnPool.(*preparedPool)
	if !ok {
		t.Fatal("PrepareStmt did not install the wrapper")
	}
	if pool.cache.len() == 0 {
		t.Fatal("expected the SELECT to be cached before migration")
	}
	type invUser struct {
		ID    int64 `fusion:"pk"`
		Name  string
		Email string // absent from the table: forces a real migration
	}
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoMigrate(t.Context(), &invUser{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	if pool.cache.len() != 0 {
		t.Errorf("prepared cache still holds %d entries after AutoMigrate (GORM #7255)", pool.cache.len())
	}
	// The repurposed query must work against the migrated schema.
	if err := db.Statement.ConnPool.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM inv_users`).Scan(&n); err != nil {
		t.Errorf("query after invalidation: %v", err)
	}
}
