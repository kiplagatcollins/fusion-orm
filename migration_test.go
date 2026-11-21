package fusion

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/migrator"
)

type migUserV1 struct {
	ID   int64  `fusion:"pk,autoincrement"`
	Name string `fusion:"notnull"`
}

func (migUserV1) TableName() string { return "mig_users" }

type migUserV2 struct {
	ID    int64  `fusion:"pk,autoincrement"`
	Name  string `fusion:"notnull"`
	Email string `fusion:"unique"`
}

func (migUserV2) TableName() string { return "mig_users" }

func setupMigration(t *testing.T) *DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "migration.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	return db
}

func TestAutoMigrateCreateAndDiff(t *testing.T) {
	db := setupMigration(t)
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.AutoMigrate(ctx, &migUserV1{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	exists, err := m.HasTable(ctx, "mig_users")
	if err != nil || !exists {
		t.Fatalf("HasTable = %v, %v", exists, err)
	}
	cols, err := m.ColumnTypes(ctx, "mig_users")
	if err != nil || len(cols) != 2 {
		t.Fatalf("columns = %+v, %v", cols, err)
	}

	if err := m.AutoMigrate(ctx, &migUserV2{}); err != nil {
		t.Fatalf("AutoMigrate diff: %v", err)
	}
	cols, err = m.ColumnTypes(ctx, "mig_users")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, col := range cols {
		if col.Name == "email" {
			found = true
		}
	}
	if !found {
		t.Errorf("email column missing: %+v", cols)
	}
	var indexes int
	if err := db.Statement.ConnPool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'mig_users'`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes == 0 {
		t.Error("unique index was not created")
	}
}

func TestVersionedMigrations(t *testing.T) {
	db := setupMigration(t)
	m, err := db.Migrator(migrator.WithTableName("fusion_test_migrations"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.Register(migrator.Migration{
		Version: 1,
		Name:    "create_widgets",
		Up: func(pool *sql.DB) error {
			_, err := pool.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY)`)
			return err
		},
		Down: func(pool *sql.DB) error {
			_, err := pool.Exec(`DROP TABLE widgets`)
			return err
		},
	})
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	exists, err := m.HasTable(ctx, "widgets")
	if err != nil || !exists {
		t.Fatalf("widgets exists = %v, %v", exists, err)
	}
	var applied int
	if err := db.Statement.ConnPool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fusion_test_migrations WHERE version = 1`).Scan(&applied); err != nil || applied != 1 {
		t.Errorf("lock row = %d, %v", applied, err)
	}
	if err := m.Down(ctx, 1); err != nil {
		t.Fatalf("Down: %v", err)
	}
	exists, err = m.HasTable(ctx, "widgets")
	if err != nil || exists {
		t.Errorf("widgets after Down = %v, %v", exists, err)
	}
}

// migUserV3 adds a column with a non-constant default to the existing
// mig_users table, exercising the SQLite table-rebuild path.
type migUserV3 struct {
	ID        int64     `fusion:"pk,autoincrement"`
	Name      string    `fusion:"notnull"`
	CreatedAt time.Time `fusion:"default:CURRENT_TIMESTAMP"`
}

func (migUserV3) TableName() string { return "mig_users" }

func TestAutoMigrateAddColumnNonConstantDefault(t *testing.T) {
	db := setupMigration(t)
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.AutoMigrate(ctx, &migUserV1{}); err != nil {
		t.Fatalf("AutoMigrate v1: %v", err)
	}
	if _, err := db.Statement.ConnPool.ExecContext(ctx,
		`INSERT INTO "mig_users" ("name") VALUES ('Alice')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if err := m.AutoMigrate(ctx, &migUserV3{}); err != nil {
		t.Fatalf("AutoMigrate v3: %v", err)
	}
	cols, err := m.ColumnTypes(ctx, "mig_users")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, col := range cols {
		if col.Name == "created_at" {
			found = true
		}
	}
	if !found {
		t.Fatalf("created_at column missing: %+v", cols)
	}
	// Pre-rebuild row must survive and receive the default via the copy step.
	var name string
	var createdAt any
	if err := db.Statement.ConnPool.QueryRowContext(ctx,
		`SELECT "name", "created_at" FROM "mig_users" WHERE "id" = 1`).Scan(&name, &createdAt); err != nil {
		t.Fatalf("row after rebuild: %v", err)
	}
	if name != "Alice" {
		t.Errorf("name after rebuild = %q", name)
	}
	if createdAt == nil {
		t.Error("created_at not backfilled for copied row")
	}
	// Raw INSERT bypasses ORM auto-timestamps: the DB default must fill it.
	if _, err := db.Statement.ConnPool.ExecContext(ctx,
		`INSERT INTO "mig_users" ("name") VALUES ('Bob')`); err != nil {
		t.Fatalf("raw insert: %v", err)
	}
	if err := db.Statement.ConnPool.QueryRowContext(ctx,
		`SELECT "created_at" FROM "mig_users" WHERE "name" = 'Bob'`).Scan(&createdAt); err != nil {
		t.Fatalf("row after raw insert: %v", err)
	}
	if createdAt == nil {
		t.Error("created_at default not applied on insert")
	}
}

func TestLoadSQLFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"1_init.up.sql":   `CREATE TABLE sql_widgets (id INTEGER PRIMARY KEY);`,
		"1_init.down.sql": `DROP TABLE sql_widgets;`,
		"README.md":       "ignored",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	migrations, err := migrator.LoadSQLFiles(dir)
	if err != nil || len(migrations) != 1 {
		t.Fatalf("LoadSQLFiles = %+v, %v", migrations, err)
	}
	if migrations[0].Version != 1 || !strings.Contains(migrations[0].Name, "init") ||
		migrations[0].Up == nil || migrations[0].Down == nil {
		t.Errorf("migration = %+v", migrations[0])
	}
}
