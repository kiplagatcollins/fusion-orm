// Package migrator implements schema migration: AutoMigrate (inspect, diff,
// ALTER) and versioned Go/SQL migrations guarded by a lock table.
package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// Column describes one existing database column.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	Primary  bool
	Default  string
}

// Migration is a versioned pair of up/down operations.
type Migration struct {
	Version int64
	Name    string
	Up      func(*sql.DB) error
	Down    func(*sql.DB) error
}

// Option configures a Migrator.
type Option func(*Migrator)

// WithTableName overrides the version lock table name.
func WithTableName(name string) Option {
	return func(m *Migrator) { m.lockTable = name }
}

// WithMarkAppliedOnSuccess controls whether successful migrations are
// recorded in the lock table.
func WithMarkAppliedOnSuccess(enabled bool) Option {
	return func(m *Migrator) { m.markApplied = enabled }
}

// Migrator performs schema operations on one pool.
type Migrator struct {
	db          *sql.DB
	dialector   dialect.Dialector
	tables      *schema.Tables
	lockTable   string
	markApplied bool
	mu          sync.Mutex
	migrations  []Migration
}

// New builds a Migrator over pool using d and the shared schema cache.
func New(pool *sql.DB, d dialect.Dialector, tables *schema.Tables, opts ...Option) *Migrator {
	if tables == nil {
		tables = schema.NewTables(nil, false, nil)
	}
	m := &Migrator{
		db: pool, dialector: d, tables: tables,
		lockTable: "fusion_migrations", markApplied: true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

func (m *Migrator) quote(ident string) string {
	return string(m.dialector.QuoteTo(nil, ident))
}

func (m *Migrator) sqlType(field *schema.Field) string {
	if field.SQLType != "" {
		return field.SQLType
	}
	pg := m.dialector.Name() == dialect.Postgres
	switch field.DataType {
	case schema.DataTypeBool:
		if pg {
			return "BOOLEAN"
		}
		return "INTEGER"
	case schema.DataTypeInt, schema.DataTypeUint:
		if pg {
			return "BIGINT"
		}
		return "INTEGER"
	case schema.DataTypeFloat:
		if pg {
			return "DOUBLE PRECISION"
		}
		return "REAL"
	case schema.DataTypeBytes:
		if pg {
			return "BYTEA"
		}
		return "BLOB"
	case schema.DataTypeJSON:
		if pg {
			return "JSONB"
		}
		return "TEXT"
	case schema.DataTypeTime:
		return "TIMESTAMP"
	default:
		return "TEXT"
	}
}

func (m *Migrator) columnDefinition(field *schema.Field) string {
	typ := m.sqlType(field)
	pg := m.dialector.Name() == dialect.Postgres
	primary := field.PrimaryKey
	if primary && field.AutoIncrement && pg {
		typ = "BIGSERIAL"
	}
	var b strings.Builder
	b.WriteString(m.quote(field.DBName))
	b.WriteByte(' ')
	b.WriteString(typ)
	if field.NotNull {
		b.WriteString(" NOT NULL")
	}
	if field.DefaultValue != "" {
		b.WriteString(" DEFAULT ")
		b.WriteString(field.DefaultValue)
	}
	if primary && !field.AutoIncrement {
		b.WriteString(" PRIMARY KEY")
	}
	if primary && field.AutoIncrement && !pg {
		b.WriteString(" PRIMARY KEY AUTOINCREMENT")
	}
	return b.String()
}

// HasTable reports whether name exists.
func (m *Migrator) HasTable(ctx context.Context, name string) (bool, error) {
	var query string
	if m.dialector.Name() == dialect.Postgres {
		query = `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1 AND table_schema = current_schema())`
	} else {
		query = `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`
	}
	var exists bool
	err := m.db.QueryRowContext(ctx, query, name).Scan(&exists)
	return exists, err
}

// CreateTable creates a table for model.
func (m *Migrator) CreateTable(ctx context.Context, model any) error {
	table, err := m.tables.Parse(model)
	if err != nil {
		return err
	}
	definitions := make([]string, 0, len(table.Fields)+1)
	for _, field := range table.Fields {
		definitions = append(definitions, m.columnDefinition(field))
	}
	if len(table.PKs) > 1 {
		keys := make([]string, 0, len(table.PKs))
		for _, field := range table.PKs {
			keys = append(keys, m.quote(field.DBName))
		}
		definitions = append(definitions, "PRIMARY KEY ("+strings.Join(keys, ", ")+")")
	}
	stmt := fmt.Sprintf("CREATE TABLE %s (%s)", m.quote(table.Name), strings.Join(definitions, ", "))
	if _, err := m.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("create table %s: %w", table.Name, err)
	}
	return m.createIndexes(ctx, table)
}

func (m *Migrator) createIndexes(ctx context.Context, table *schema.Table) error {
	for _, field := range table.Fields {
		for _, spec := range []struct {
			name   string
			unique bool
		}{
			{field.IndexName, false}, {field.UniqueName, true},
		} {
			if spec.name == "" {
				continue
			}
			unique := ""
			if spec.unique {
				unique = "UNIQUE "
			}
			stmt := fmt.Sprintf("CREATE %sINDEX IF NOT EXISTS %s ON %s (%s)",
				unique, m.quote(spec.name), m.quote(table.Name), m.quote(field.DBName))
			if _, err := m.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("create index %s: %w", spec.name, err)
			}
		}
	}
	return nil
}

// ColumnTypes lists the columns of an existing table.
func (m *Migrator) ColumnTypes(ctx context.Context, name string) ([]Column, error) {
	if m.dialector.Name() == dialect.Postgres {
		rows, err := m.db.QueryContext(ctx,
			`SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = $1 AND table_schema = current_schema() ORDER BY ordinal_position`, name)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var cols []Column
		for rows.Next() {
			var col Column
			var nullable string
			if err := rows.Scan(&col.Name, &col.Type, &nullable); err != nil {
				return nil, err
			}
			col.Nullable = nullable == "YES"
			cols = append(cols, col)
		}
		return cols, rows.Err()
	}
	rows, err := m.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", m.quote(name)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []Column
	for rows.Next() {
		var col Column
		var cid, notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &col.Name, &col.Type, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		col.Primary = pk != 0
		col.Nullable = notNull == 0
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// AddColumn adds one column to an existing table.
func (m *Migrator) AddColumn(ctx context.Context, table string, field *schema.Field) error {
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", m.quote(table), m.columnDefinition(field))
	if _, err := m.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, field.DBName, err)
	}
	return nil
}

// DropColumn removes one column.
func (m *Migrator) DropColumn(ctx context.Context, table, column string) error {
	stmt := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", m.quote(table), m.quote(column))
	_, err := m.db.ExecContext(ctx, stmt)
	return err
}

// AlterColumn changes a column's type where the dialect supports it.
func (m *Migrator) AlterColumn(ctx context.Context, table string, field *schema.Field) error {
	if m.dialector.Name() != dialect.Postgres {
		return nil
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s",
		m.quote(table), m.quote(field.DBName), m.sqlType(field))
	_, err := m.db.ExecContext(ctx, stmt)
	return err
}

// AutoMigrate creates missing tables and adds missing columns and indexes.
func (m *Migrator) AutoMigrate(ctx context.Context, values ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, value := range values {
		table, err := m.tables.Parse(value)
		if err != nil {
			return err
		}
		exists, err := m.HasTable(ctx, table.Name)
		if err != nil {
			return err
		}
		if !exists {
			if err := m.CreateTable(ctx, value); err != nil {
				return err
			}
			continue
		}
		columns, err := m.ColumnTypes(ctx, table.Name)
		if err != nil {
			return err
		}
		present := make(map[string]Column, len(columns))
		for _, col := range columns {
			present[strings.ToLower(col.Name)] = col
		}
		for _, field := range table.Fields {
			existing, ok := present[strings.ToLower(field.DBName)]
			if !ok {
				if err := m.AddColumn(ctx, table.Name, field); err != nil {
					return err
				}
				continue
			}
			if existing.Type != "" && !typeCompatible(existing.Type, m.sqlType(field)) {
				if err := m.AlterColumn(ctx, table.Name, field); err != nil {
					return err
				}
			}
		}
		if err := m.createIndexes(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func typeCompatible(existing, declared string) bool {
	normalize := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		if i := strings.IndexByte(s, '('); i >= 0 {
			s = s[:i]
		}
		switch s {
		case "integer", "bigint", "int", "int8", "serial", "bigserial":
			return "int"
		case "boolean", "bool":
			return "bool"
		case "double precision", "float8", "real", "float", "double":
			return "float"
		case "character varying", "varchar", "text":
			return "text"
		case "timestamp", "timestamp without time zone", "datetime":
			return "time"
		}
		return s
	}
	return normalize(existing) == normalize(declared)
}

func (m *Migrator) ensureLockTable(ctx context.Context) error {
	stmt := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		version BIGINT PRIMARY KEY,
		name TEXT,
		applied_at TIMESTAMP
	)`, m.quote(m.lockTable))
	_, err := m.db.ExecContext(ctx, stmt)
	return err
}

func (m *Migrator) appliedVersions(ctx context.Context) (map[int64]bool, error) {
	if err := m.ensureLockTable(ctx); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx,
		fmt.Sprintf("SELECT version FROM %s ORDER BY version", m.quote(m.lockTable)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func (m *Migrator) record(ctx context.Context, migration Migration) error {
	if !m.markApplied {
		return nil
	}
	stmt := fmt.Sprintf("INSERT INTO %s (version, name, applied_at) VALUES (?, ?, ?)", m.quote(m.lockTable))
	if m.dialector.Name() == dialect.Postgres {
		stmt = fmt.Sprintf("INSERT INTO %s (version, name, applied_at) VALUES ($1, $2, $3)", m.quote(m.lockTable))
	}
	_, err := m.db.ExecContext(ctx, stmt, migration.Version, migration.Name, time.Now().UTC())
	return err
}

func (m *Migrator) forget(ctx context.Context, version int64) error {
	if !m.markApplied {
		return nil
	}
	stmt := fmt.Sprintf("DELETE FROM %s WHERE version = ?", m.quote(m.lockTable))
	if m.dialector.Name() == dialect.Postgres {
		stmt = fmt.Sprintf("DELETE FROM %s WHERE version = $1", m.quote(m.lockTable))
	}
	_, err := m.db.ExecContext(ctx, stmt, version)
	return err
}

// Register adds versioned migrations in arbitrary order.
func (m *Migrator) Register(migrations ...Migration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.migrations = append(m.migrations, migrations...)
	sort.Slice(m.migrations, func(i, j int) bool { return m.migrations[i].Version < m.migrations[j].Version })
}

// Up applies every registered migration that has not been recorded yet.
func (m *Migrator) Up(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return err
	}
	for _, migration := range m.migrations {
		if applied[migration.Version] || migration.Up == nil {
			continue
		}
		if err := migration.Up(m.db); err != nil {
			return fmt.Errorf("migration %d up: %w", migration.Version, err)
		}
		if err := m.record(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

// Down rolls back the most recently applied steps migrations.
func (m *Migrator) Down(ctx context.Context, steps int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if steps <= 0 {
		return nil
	}
	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return err
	}
	versions := make([]int64, 0, len(applied))
	for version := range applied {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	byVersion := make(map[int64]Migration, len(m.migrations))
	for _, migration := range m.migrations {
		byVersion[migration.Version] = migration
	}
	for i := 0; i < steps && i < len(versions); i++ {
		migration, ok := byVersion[versions[i]]
		if !ok || migration.Down == nil {
			continue
		}
		if err := migration.Down(m.db); err != nil {
			return fmt.Errorf("migration %d down: %w", migration.Version, err)
		}
		if err := m.forget(ctx, versions[i]); err != nil {
			return err
		}
	}
	return nil
}

// Migrate registers and applies a single versioned migration.
func (m *Migrator) Migrate(ctx context.Context, version int64, up, down func(*sql.DB) error) error {
	m.Register(Migration{Version: version, Up: up, Down: down})
	return m.Up(ctx)
}

var sqlFilePattern = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

// LoadSQLFiles reads numbered up/down SQL files from dir.
func LoadSQLFiles(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byVersion := make(map[int64]*Migration)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := sqlFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, err
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		migration := byVersion[version]
		if migration == nil {
			migration = &Migration{Version: version, Name: match[2]}
			byVersion[version] = migration
		}
		bodySQL := string(body)
		if match[3] == "up" {
			migration.Up = func(db *sql.DB) error {
				_, err := db.Exec(bodySQL)
				return err
			}
		} else {
			migration.Down = func(db *sql.DB) error {
				_, err := db.Exec(bodySQL)
				return err
			}
		}
	}
	versions := make([]int64, 0, len(byVersion))
	for version := range byVersion {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	out := make([]Migration, 0, len(versions))
	for _, version := range versions {
		out = append(out, *byVersion[version])
	}
	return out, nil
}
