package fusion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// openTestDB opens a SQLite database on a temp file (integration tests use
// real files per project policy — no daemons, no shared :memory: pools).
// Traces default to Discard so error-path tests stay quiet; pass a Config
// with a Logger to observe output.
func openTestDB(t *testing.T, opts ...Option) *DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	opts = append([]Option{&Config{Logger: logger.Discard}}, opts...)
	db, err := Open(sqlite.Open(dsn), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if sqldb, err := db.DB(); err == nil {
			_ = sqldb.Close()
		}
	})
	return db
}

func TestOpenPingsSQLite(t *testing.T) {
	db := openTestDB(t)
	if db.Dialector.Name() != dialect.SQLite {
		t.Errorf("Dialector.Name() = %s", db.Dialector.Name())
	}
	if db.Error != nil {
		t.Errorf("root Error = %v", db.Error)
	}
	if db.Statement.Context == nil {
		t.Fatal("Open must initialize Statement with a context")
	}
}

func TestOpenRequiresDialector(t *testing.T) {
	if _, err := Open(nil); err == nil {
		t.Fatal("Open(nil) succeeded, want error")
	}
}

func TestOpenBadDSNFails(t *testing.T) {
	// A database file whose parent directory does not exist cannot open;
	// Open's automatic ping surfaces the failure.
	dir := t.TempDir()
	if _, err := Open(sqlite.Open(filepath.Join(dir, "missing", "test.db"))); err == nil {
		t.Fatal("Open with unopenable DSN succeeded, want error")
	}
}

func TestExecRawAndRowsAffected(t *testing.T) {
	db := openTestDB(t)

	tx := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
	if tx.Error != nil {
		t.Fatalf("CREATE: %v", tx.Error)
	}

	tx = db.Exec("INSERT INTO users (name) VALUES (?), (?)", "alice", "bob")
	if tx.Error != nil {
		t.Fatalf("INSERT: %v", tx.Error)
	}
	if tx.RowsAffected != 2 {
		t.Errorf("RowsAffected = %d, want 2", tx.RowsAffected)
	}

	// Root instance stays clean: Exec never mutates the receiver.
	if db.Error != nil || db.RowsAffected != 0 {
		t.Errorf("root mutated: Error=%v RowsAffected=%d", db.Error, db.RowsAffected)
	}
}

func TestExecErrorRecordedNotPanicked(t *testing.T) {
	db := openTestDB(t)
	tx := db.Exec("SELECT * FROM missing_table")
	if tx.Error == nil {
		t.Fatal("expected error for missing table")
	}
	if db.Error != nil {
		t.Errorf("root Error = %v, want nil", db.Error)
	}
}

func TestChainInstancesAreIsolated(t *testing.T) {
	db := openTestDB(t)

	tx1 := db.Exec("SELECT 1")
	tx2 := db.Exec("SELECT 2")

	if tx1 == tx2 || &tx1.Statement == &tx2.Statement {
		t.Fatal("chains must not share a Statement")
	}
	if string(tx1.Statement.SQL) != "" || string(tx2.Statement.SQL) != "" {
		t.Fatal("non-dry-run Exec must not capture SQL")
	}
}

func TestDryRunCapturesSQL(t *testing.T) {
	db := openTestDB(t)

	tx := db.Session(&Session{DryRun: true}).Exec("SELECT * FROM users WHERE id = ?", 7)
	if tx.Error != nil {
		t.Fatalf("dry-run Exec: %v", tx.Error)
	}
	if got := string(tx.Statement.SQL); got != "SELECT * FROM users WHERE id = ?" {
		t.Errorf("captured SQL = %q", got)
	}
	if len(tx.Statement.Vars) != 1 || tx.Statement.Vars[0] != 7 {
		t.Errorf("captured vars = %v", tx.Statement.Vars)
	}

	// DryRun must not leak back to the root config.
	if db.DryRun {
		t.Error("Session(DryRun) mutated the root Config")
	}
}

func TestToSQLInterpolates(t *testing.T) {
	db := openTestDB(t)

	got := db.ToSQL(func(tx *DB) *DB {
		return tx.Exec("SELECT * FROM users WHERE name = ?", "O'Brien")
	})
	want := "SELECT * FROM users WHERE name = 'O''Brien'"
	if got != want {
		t.Errorf("ToSQL = %q, want %q", got, want)
	}

	if got := db.ToSQL(func(tx *DB) *DB { return tx }); got != "" {
		t.Errorf("ToSQL of empty chain = %q, want empty", got)
	}

	// A queryFn that returns nil must not panic (Statement is a value field;
	// the guard lives on the *DB, not on &Statement).
	if got := db.ToSQL(func(*DB) *DB { return nil }); got != "" {
		t.Errorf("ToSQL of nil-returning fn = %q, want empty", got)
	}
}

func TestSessionFlagsDoNotLeak(t *testing.T) {
	db := openTestDB(t)

	sess := db.Session(&Session{
		DryRun:                   true,
		AllowGlobalUpdate:        true,
		SkipDefaultTransaction:   true,
		DisableNestedTransaction: true,
	})

	if !sess.DryRun || !sess.AllowGlobalUpdate || !sess.SkipDefaultTransaction || !sess.DisableNestedTransaction {
		t.Fatal("session flags not applied to derivative")
	}
	if db.DryRun || db.AllowGlobalUpdate || db.SkipDefaultTransaction || db.DisableNestedTransaction {
		t.Error("session flags leaked to the root Config")
	}
	// The derivative owns its Config copy rather than sharing the pointer.
	if sess.Config == db.Config {
		t.Error("Session shared the root Config pointer")
	}
	sess.Config.DryRun = false
	if db.Config.DryRun {
		t.Error("mutating the session Config leaked to the root Config")
	}
}

func TestSessionContextIsolatesStatement(t *testing.T) {
	db := openTestDB(t)
	type ctxKey struct{}

	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	sess := db.WithContext(ctx)

	if &sess.Statement == &db.Statement {
		t.Fatal("WithContext must clone the Statement")
	}
	if sess.Statement.Context != ctx {
		t.Fatal("WithContext did not set the context")
	}
	if db.Statement.Context == ctx {
		t.Error("context leaked to the root Statement")
	}
	if got := sess.Statement.Context.Value(ctxKey{}); got != "v" {
		t.Errorf("context value = %v", got)
	}

	// nil context falls back to TODO rather than panicking later.
	if got := db.WithContext(context.TODO()).Statement.Context; got != context.TODO() {
		t.Errorf("WithContext(nil) = %v, want context.TODO()", got)
	}
}

// captureWriter records log output for assertions.
type captureWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *captureWriter) Printf(format string, v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = fmt.Fprintf(&w.buf, format, v...)
}

func (w *captureWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestDebugTracesAtInfoButRootStaysQuiet(t *testing.T) {
	w := &captureWriter{}
	db := openTestDB(t, &Config{Logger: logger.New(w, logger.Config{LogLevel: logger.Warn})})

	// Root logger is Warn: a successful trace must not be printed.
	if tx := db.Exec("SELECT 1"); tx.Error != nil {
		t.Fatalf("root Exec: %v", tx.Error)
	}
	if got := w.String(); got != "" {
		t.Fatalf("root Exec at Warn logged %q", got)
	}

	// Debug installs an Info-gated derivative logger.
	if tx := db.Debug().Exec("SELECT 2"); tx.Error != nil {
		t.Fatalf("Debug Exec: %v", tx.Error)
	}
	got := w.String()
	if !strings.Contains(got, "SELECT 2") {
		t.Fatalf("Debug Exec did not trace, logged %q", got)
	}
}

type testPlugin struct {
	name  string
	init  func(*DB) error
	calls int
}

func (p *testPlugin) Name() string { return p.name }
func (p *testPlugin) Initialize(db *DB) error {
	p.calls++
	if p.init != nil {
		return p.init(db)
	}
	return nil
}

func TestUsePlugin(t *testing.T) {
	db := openTestDB(t)

	p := &testPlugin{name: "metrics"}
	if err := db.Use(p); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if p.calls != 1 {
		t.Errorf("Initialize calls = %d, want 1", p.calls)
	}
	if err := db.Use(&testPlugin{name: "metrics"}); !errors.Is(err, ErrRegistered) {
		t.Errorf("duplicate Use = %v, want ErrRegistered", err)
	}

	failing := &testPlugin{name: "bad", init: func(*DB) error { return errors.New("init failed") }}
	if err := db.Use(failing); err == nil {
		t.Fatal("Use of failing plugin succeeded")
	}
	if _, ok := db.Plugins["bad"]; ok {
		t.Error("failed plugin must not be registered")
	}
}

func TestConfigNowFuncDefaultAndOverride(t *testing.T) {
	db := openTestDB(t)
	if db.NowFunc == nil {
		t.Fatal("NowFunc default not set")
	}

	custom := func() time.Time { return time.Unix(0, 0).UTC() }
	db2 := openTestDB(t, &Config{NowFunc: custom})
	if !db2.NowFunc().Equal(custom()) {
		t.Error("NowFunc override not applied")
	}
}

func TestDBReturnsSQLDB(t *testing.T) {
	db := openTestDB(t)
	sqldb, err := db.DB()
	if err != nil {
		t.Fatalf("DB(): %v", err)
	}
	if err := sqldb.Ping(); err != nil {
		t.Fatalf("ping via DB(): %v", err)
	}
}

func TestStatementCloneIsolatesSettingsAndSQL(t *testing.T) {
	db := openTestDB(t)
	stmt := &Statement{
		DB:       db,
		ConnPool: db.Statement.ConnPool,
		Context:  db.Statement.Context,
		SQL:      []byte("SELECT 1"),
		Vars:     []any{1},
		Settings: &sync.Map{},
	}
	stmt.Settings.Store("k", "v")

	clone := stmt.clone()
	if &clone == stmt {
		t.Fatal("clone returned the same pointer")
	}
	if string(clone.SQL) != "SELECT 1" {
		t.Errorf("clone SQL = %q", clone.SQL)
	}
	clone.SQL[0] = 'X'
	if string(stmt.SQL) != "SELECT 1" {
		t.Error("clone shares the SQL buffer")
	}
	clone.Settings.Store("k", "other")
	clone.Settings.Store("extra", 1)
	if v, _ := stmt.Settings.Load("k"); v != "v" {
		t.Error("clone shares Settings entries")
	}
	if _, ok := clone.Settings.Load("k"); !ok {
		t.Error("clone did not copy Settings entries")
	}
	if stmt.Settings == clone.Settings {
		t.Fatal("clone shares the Settings map")
	}
}

func TestSessionSchemaCacheAndStrictTags(t *testing.T) {
	type article struct {
		ID    int64  `fusion:"pk"`
		Title string `fusion:"headline"`
	}
	db := openTestDB(t, &Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true, TablePrefix: "app_"},
		StrictTags:     true,
	})

	table, err := db.Parse(&article{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if table.Name != "app_article" || table.Column("headline") == nil {
		t.Errorf("table = %s, columns = %v", table.Name, table.DBNames)
	}
	again, err := db.Session(&Session{NewDB: true}).Parse(&article{})
	if err != nil {
		t.Fatalf("cached Parse: %v", err)
	}
	if again != table {
		t.Error("sessions did not share the schema cache")
	}

	type bad struct {
		Name string `fusion:"name,bogus:value"`
	}
	if _, err := db.Parse(&bad{}); !errors.Is(err, schema.ErrInvalidTag) {
		t.Errorf("strict tag error = %v", err)
	}
}

func TestNilStatementContextUsesTODO(t *testing.T) {
	db := openTestDB(t)
	db.Statement.Context = nil
	ctx := statementContext(&db.Statement)
	if ctx == nil {
		t.Fatal("statementContext returned nil")
	}
	if err := db.Exec(`SELECT 1`).Error; err != nil {
		t.Fatalf("Exec with nil statement context: %v", err)
	}
}
