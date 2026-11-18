package fusion

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/migrator"
)

type pgMigUser struct {
	ID    int64 `fusion:"pk,autoincrement"`
	Name  string
	Email string `fusion:"unique"`
}

func (pgMigUser) TableName() string { return "fusion_pg_mig_users" }

func TestPostgresMigrations(t *testing.T) {
	dsn := os.Getenv("FUSION_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FUSION_TEST_PG_DSN is not set")
	}
	db, err := Open(postgres.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	m, err := db.Migrator(migrator.WithTableName("fusion_pg_lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		pool, e := db.DB()
		if e != nil {
			return
		}
		_, _ = pool.ExecContext(ctx, `DROP TABLE IF EXISTS fusion_pg_mig_users`)
		_, _ = pool.ExecContext(ctx, `DROP TABLE IF EXISTS fusion_pg_lock`)
	})
	if err := m.AutoMigrate(ctx, &pgMigUser{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	cols, err := m.ColumnTypes(ctx, "fusion_pg_mig_users")
	if err != nil || len(cols) != 3 {
		t.Fatalf("columns = %+v, %v", cols, err)
	}

	m.Register(migrator.Migration{
		Version: 7,
		Name:    "create_pg_widgets",
		Up: func(pool *sql.DB) error {
			_, err := pool.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS fusion_pg_widgets (id BIGSERIAL PRIMARY KEY)`)
			return err
		},
		Down: func(pool *sql.DB) error {
			_, err := pool.ExecContext(ctx, `DROP TABLE IF EXISTS fusion_pg_widgets`)
			return err
		},
	})
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := m.Down(ctx, 1); err != nil {
		t.Fatalf("Down: %v", err)
	}
}
