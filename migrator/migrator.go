// Package migrator implements schema migration: AutoMigrate (inspect, diff,
// ALTER) and versioned Go/SQL migrations guarded by a lock table.
package migrator

import (
	"context"
	"database/sql"
	"errors"
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
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
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

// WithCacheInvalidator registers a callback run after AutoMigrate DDL so the
// caller can drop cached prepared statements that may sit on a stale schema
// (GORM #7255 class).
func WithCacheInvalidator(fn func()) Option {
	return func(m *Migrator) {
		if fn != nil {
			m.onDDL = fn
		}
	}
}

// Migrator performs schema operations on one pool.
type Migrator struct {
	db          *sql.DB
	dialector   dialect.Dialector
	tables      *schema.Tables
	lockTable   string
	markApplied bool
	onDDL       func()
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

func (m *Migrator) has(flag feature.Feature) bool {
	return m.dialector.Features().Has(flag)
}

// ErrUnsupportedFeature reports a dialect that lacks a capability required
// by a migration operation.
var ErrUnsupportedFeature = errors.New("unsupported feature")

func missingFeature(flag feature.Feature) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedFeature, flag.Name())
}

func (m *Migrator) sqlType(field *schema.Field) string {
	if field.SQLType != "" {
		return m.mappedSQLType(field.SQLType)
	}
	pgTypes := m.has(feature.PostgreSQLTypes)
	switch field.DataType {
	case schema.DataTypeBool:
		if pgTypes {
			return "BOOLEAN"
		}
		return "INTEGER"
	case schema.DataTypeInt, schema.DataTypeUint:
		if pgTypes {
			return "BIGINT"
		}
		return "INTEGER"
	case schema.DataTypeFloat:
		if pgTypes {
			return "DOUBLE PRECISION"
		}
		return "REAL"
	case schema.DataTypeBytes:
		if pgTypes {
			return "BYTEA"
		}
		return "BLOB"
	case schema.DataTypeJSON:
		if pgTypes {
			return "JSONB"
		}
		return "TEXT"
	case schema.DataTypeTime:
		return "TIMESTAMP"
	default:
		return "TEXT"
	}
}

// mappedSQLType translates known portable type aliases per dialect. Only
// recognized aliases are remapped; everything else passes through verbatim.
func (m *Migrator) mappedSQLType(sqlType string) string {
	if strings.EqualFold(strings.TrimSpace(sqlType), "uuid") {
		if m.has(feature.PostgreSQLTypes) {
			return "uuid"
		}
		return "TEXT"
	}
	return sqlType
}

func (m *Migrator) columnDefinition(field *schema.Field, inlinePK bool) string {
	typ := m.sqlType(field)
	primary := field.PrimaryKey
	if primary && field.AutoIncrement && m.has(feature.SerialColumn) {
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
	// Composite primary keys are declared table-level by createTableSQL;
	// inlining them per column would emit multiple PRIMARY KEY clauses.
	if primary && (inlinePK || field.AutoIncrement) {
		b.WriteString(" PRIMARY KEY")
		if field.AutoIncrement && m.has(feature.AutoIncrementClause) {
			b.WriteString(" AUTOINCREMENT")
		}
	}
	return b.String()
}

// HasTable reports whether name exists.
func (m *Migrator) HasTable(ctx context.Context, name string) (bool, error) {
	var query string
	switch {
	case m.has(feature.InformationSchema):
		query = `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1 AND table_schema = current_schema())`
	case m.has(feature.PragmaTableInfo):
		query = `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`
	default:
		return false, missingFeature(feature.InformationSchema)
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
	stmt := m.createTableSQL(table, m.quote(table.Name))
	if _, err := m.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("create table %s: %w", table.Name, err)
	}
	return m.createIndexes(ctx, table)
}

// createTableSQL renders the CREATE TABLE statement for table, quoting
// quotedName so the same schema can be materialized under a temp name.
func (m *Migrator) createTableSQL(table *schema.Table, quotedName string) string {
	definitions := make([]string, 0, len(table.Fields)+1)
	inlinePK := len(table.PKs) <= 1
	for _, field := range table.Fields {
		definitions = append(definitions, m.columnDefinition(field, inlinePK))
	}
	if len(table.PKs) > 1 {
		keys := make([]string, 0, len(table.PKs))
		for _, field := range table.PKs {
			keys = append(keys, m.quote(field.DBName))
		}
		definitions = append(definitions, "PRIMARY KEY ("+strings.Join(keys, ", ")+")")
	}
	return fmt.Sprintf("CREATE TABLE %s (%s)", quotedName, strings.Join(definitions, ", "))
}

func (m *Migrator) createIndexes(ctx context.Context, table *schema.Table) error {
	for _, field := range table.Fields {
		for _, spec := range []struct {
			name   string
			unique bool
		}{
			{field.IndexSQLName, false}, {field.UniqueSQLName, true},
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
	if m.has(feature.InformationSchema) {
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
	if !m.has(feature.PragmaTableInfo) {
		return nil, missingFeature(feature.InformationSchema)
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
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", m.quote(table), m.columnDefinition(field, true))
	if _, err := m.db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, field.DBName, err)
	}
	return nil
}

// isNonConstantDefault reports whether the declared default is an expression
// (CURRENT_TIMESTAMP, now(), ...) rather than a literal value. SQLite only
// accepts such defaults inside CREATE TABLE, not ALTER TABLE ADD COLUMN.
func isNonConstantDefault(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "CURRENT_TIMESTAMP", "CURRENT_DATE", "CURRENT_TIME":
		return true
	}
	return strings.Contains(v, "(")
}

// needsRebuild reports whether adding field to an existing table requires a
// full table rebuild because the dialect cannot add the column with a
// non-constant default in place.
func (m *Migrator) needsRebuild(field *schema.Field) bool {
	return field.DefaultValue != "" && isNonConstantDefault(field.DefaultValue) &&
		!m.has(feature.AddColumnNonConstantDefault)
}

// rebuildTable recreates table with the full desired schema and copies the
// surviving rows over. Used when a dialect (SQLite) cannot ALTER ADD a column
// carrying a non-constant DEFAULT: the new table declares the default, and
// copied rows omit the new column so SQLite fills it in.
func (m *Migrator) rebuildTable(ctx context.Context, table *schema.Table) error {
	columns, err := m.ColumnTypes(ctx, table.Name)
	if err != nil {
		return fmt.Errorf("rebuild %s: %w", table.Name, err)
	}
	inModel := make(map[string]bool, len(table.Fields))
	for _, field := range table.Fields {
		inModel[strings.ToLower(field.DBName)] = true
	}
	var copyCols []string
	for _, col := range columns {
		if inModel[strings.ToLower(col.Name)] {
			copyCols = append(copyCols, m.quote(col.Name))
		}
	}
	tmp := m.quote(table.Name + "__fusion_new")
	stmts := []string{
		fmt.Sprintf("DROP TABLE IF EXISTS %s", tmp),
		m.createTableSQL(table, tmp),
	}
	if len(copyCols) > 0 {
		stmts = append(stmts, fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
			tmp, strings.Join(copyCols, ", "), strings.Join(copyCols, ", "), m.quote(table.Name)))
	}
	stmts = append(stmts,
		fmt.Sprintf("DROP TABLE %s", m.quote(table.Name)),
		fmt.Sprintf("ALTER TABLE %s RENAME TO %s", tmp, m.quote(table.Name)),
	)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rebuild %s: %w", table.Name, err)
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("rebuild %s: %w", table.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rebuild %s: %w", table.Name, err)
	}
	if err := m.createIndexes(ctx, table); err != nil {
		return fmt.Errorf("rebuild %s: %w", table.Name, err)
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
	if !m.has(feature.AlterColumnType) {
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
		} else if err := m.migrateExistingTable(ctx, table); err != nil {
			return err
		}
		if err := m.createJunctions(ctx, table); err != nil {
			return err
		}
	}
	// DDL may have changed tables under cached prepared statements
	// (GORM #7255 class).
	if m.onDDL != nil {
		m.onDDL()
	}
	return nil
}

// migrateExistingTable brings an existing table up to the parsed schema:
// missing columns (with a rebuild when required), type changes, and indexes.
func (m *Migrator) migrateExistingTable(ctx context.Context, table *schema.Table) error {
	columns, err := m.ColumnTypes(ctx, table.Name)
	if err != nil {
		return err
	}
	present := make(map[string]Column, len(columns))
	for _, col := range columns {
		present[strings.ToLower(col.Name)] = col
	}
	var missing []*schema.Field
	for _, field := range table.Fields {
		if _, ok := present[strings.ToLower(field.DBName)]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		rebuild := false
		for _, field := range missing {
			if m.needsRebuild(field) {
				rebuild = true
				break
			}
		}
		if rebuild {
			if err := m.rebuildTable(ctx, table); err != nil {
				return err
			}
		} else {
			for _, field := range missing {
				if err := m.AddColumn(ctx, table.Name, field); err != nil {
					return err
				}
			}
		}
	}
	for _, field := range table.Fields {
		existing, ok := present[strings.ToLower(field.DBName)]
		if !ok {
			continue
		}
		if existing.Type != "" && !typeCompatible(existing.Type, m.sqlType(field)) {
			if err := m.AlterColumn(ctx, table.Name, field); err != nil {
				return err
			}
		}
	}
	return m.createIndexes(ctx, table)
}

// createJunctions materializes many-to-many junction tables declared by the
// model's relations. Each junction carries a composite primary key so a
// duplicate pair cannot be inserted at the storage layer.
func (m *Migrator) createJunctions(ctx context.Context, table *schema.Table) error {
	for _, rel := range table.Relations {
		if rel.Kind != schema.ManyToMany || rel.M2MName == "" {
			continue
		}
		if rel.Reference == nil || rel.ForeignKey == nil {
			// A side without a primary key cannot be typed; keep prior
			// behavior and let relation queries surface the problem.
			continue
		}
		exists, err := m.HasTable(ctx, rel.M2MName)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		junction := junctionTable(rel)
		stmt := m.createTableSQL(junction, m.quote(junction.Name))
		if _, err := m.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create join table %s: %w", junction.Name, err)
		}
	}
	return nil
}

// junctionTable builds the schema of a junction table: two key columns typed
// after the related tables' primary keys, both part of the primary key.
func junctionTable(rel *schema.Relation) *schema.Table {
	fk := junctionField(rel.Reference, rel.M2MForeignKey)
	ref := junctionField(rel.ForeignKey, rel.M2MReferences)
	return &schema.Table{
		Name:   rel.M2MName,
		Fields: []*schema.Field{fk, ref},
		PKs:    []*schema.Field{fk, ref},
	}
}

func junctionField(pk *schema.Field, name string) *schema.Field {
	return &schema.Field{
		Name:         name,
		DBName:       name,
		IndirectType: pk.IndirectType,
		IsPtr:        pk.IsPtr,
		DataType:     pk.DataType,
		SQLType:      pk.SQLType,
		PrimaryKey:   true,
		NotNull:      true,
		Creatable:    true,
		Updatable:    true,
		Readable:     true,
	}
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
		case "character varying", "varchar", "text", "uuid":
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
	if m.has(feature.DollarPlaceholder) {
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
	if m.has(feature.DollarPlaceholder) {
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
