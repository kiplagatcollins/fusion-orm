package fusion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/migrator"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

// ConnPool is the execution interface every statement runs through: the
// root *sql.DB, a *sql.Tx, or a wrapper (e.g. the prepared-statement cache
// added in M5).
type ConnPool interface {
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// TxBeginner begins a transaction returning the concrete *sql.Tx.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// ConnPoolBeginner begins a transaction on a custom ConnPool wrapper.
type ConnPoolBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (ConnPool, error)
}

// TxCommitter commits or rolls back an active transaction.
type TxCommitter interface {
	Commit() error
	Rollback() error
}

// Plugin extends the session. Register with DB.Use; plugins registered
// through Config.Plugins are initialized during Open.
type Plugin interface {
	Name() string
	Initialize(*DB) error
}

// Config holds session-wide settings. It is copied (shallowly) whenever a
// session derivative is made, so session flags never leak back to the root.
type Config struct {
	// SkipDefaultTransaction disables the implicit transaction wrapping
	// single create/update/delete operations (honored by M3 callbacks).
	SkipDefaultTransaction bool
	// Logger receives info/warn/error messages and SQL traces.
	Logger logger.Interface
	// NowFunc produces timestamps; defaults to time.Now().Local().
	NowFunc func() time.Time
	// DryRun captures SQL on Statement instead of executing it.
	DryRun bool
	// TranslateError maps driver constraint errors through the dialector's
	// ErrorTranslator before they reach the error slot.
	TranslateError bool
	// AllowGlobalUpdate permits Update/Delete statements without WHERE
	// clauses (guarded by ErrMissingWhereClause otherwise, M3).
	AllowGlobalUpdate bool
	// DisableAutomaticPing skips the Ping performed by Open.
	DisableAutomaticPing bool
	// DisableNestedTransaction runs nested Transaction calls inline instead
	// of via savepoints.
	DisableNestedTransaction bool
	// NamingStrategy maps Go model/field names to table/column names.
	NamingStrategy schema.Namer
	// StrictTags rejects unknown fusion tag keys instead of warning.
	StrictTags bool
	// PrepareStmt enables the LRU+TTL prepared-statement cache.
	PrepareStmt bool
	// PreparedStmtCacheSize bounds cached statements (default 1000).
	PreparedStmtCacheSize int
	// PreparedStmtTTL expires cached statements (0 = no expiry).
	PreparedStmtTTL time.Duration
	// QueryHooks observes statements around execution.
	QueryHooks []QueryHook

	// ConnPool, when set before Open, is used instead of opening a pool
	// through the dialector.
	ConnPool ConnPool
	// Dialector renders SQL for the session. Set by Open from its argument.
	Dialector dialect.Dialector
	// Plugins registered before Open; each is initialized during Open.
	Plugins map[string]Plugin
	// tables is the per-session schema parse cache (shared by derivatives).
	tables *schema.Tables
	// callbacks holds the CRUD processors (shared by derivatives).
	callbacks *CallbackManager
	// joinTables holds SetupJoinTable overrides (shared by derivatives).
	joinTables *joinTableRegistry
	// FullSaveAssociations makes association saves use Save (not Create) for
	// records that already carry a primary key.
	FullSaveAssociations bool
}

// Option configures Open. *Config implements Option, so a full config can be
// passed directly:
//
//	db, err := fusion.Open(sqlite.Open("app.db"), &fusion.Config{Logger: l})
type Option interface {
	Apply(*Config)
}

// Apply copies c onto the working config. It implements Option.
func (c *Config) Apply(target *Config) {
	if c != nil && c != target {
		*target = *c
	}
}

// Session configures a derivative created by DB.Session.
type Session struct {
	// DryRun turns the derivative into a SQL-capturing session.
	DryRun bool
	// NewDB resets the chain: the derivative starts with a fresh Statement
	// instead of inheriting the receiver's clauses.
	NewDB bool
	// Initialized forces the derivative through getInstance immediately.
	Initialized bool
	// SkipDefaultTransaction / DisableNestedTransaction / AllowGlobalUpdate
	// override the corresponding Config flags for this session.
	SkipDefaultTransaction   bool
	DisableNestedTransaction bool
	AllowGlobalUpdate        bool
	// Context replaces the derivative's execution context.
	Context context.Context
	// Logger replaces the derivative's logger (used by Debug).
	Logger logger.Interface
	// NowFunc replaces the derivative's timestamp source.
	NowFunc func() time.Time
	// FullSaveAssociations saves associated records with Save instead of
	// Create on the derivative.
	FullSaveAssociations bool
}

// DB is a session handle: the root instance returned by Open, or a
// derivative produced by chaining, Session, WithContext, Debug, or
// Begin. A *DB is safe for concurrent use; every chainable method returns
// a derivative and never mutates the receiver.
type DB struct {
	*Config
	// Error is the single error slot for the chain (see AddError).
	Error error
	// RowsAffected is set by finishers that execute DML.
	RowsAffected int64
	// Statement holds the chain's per-statement state.
	Statement *Statement
	// clone selects getInstance's behavior: 1 = fresh statement on next
	// use (root/pristine), 2 = clone the current statement (mid-chain).
	clone int
}

// Open initializes a session over d. Options apply before defaults: any nil
// Config field falls back to its default (Warn-level logger, time.Now,
// empty plugin map), then the dialector opens its pool and Open pings it
// (unless DisableAutomaticPing).
func Open(d dialect.Dialector, opts ...Option) (*DB, error) {
	if d == nil {
		return nil, errors.New("fusion: Open requires a dialector")
	}

	config := &Config{}
	for _, opt := range opts {
		if opt != nil {
			opt.Apply(config)
		}
	}

	config.Dialector = d
	if config.NamingStrategy == nil {
		config.NamingStrategy = schema.NamingStrategy{}
	}
	if config.Logger == nil {
		config.Logger = logger.Default
	}
	config.tables = schema.NewTables(config.NamingStrategy, config.StrictTags, func(key string) {
		config.Logger.Warn(context.TODO(), "unknown fusion tag key %q", key)
	})
	config.callbacks = newCallbackManager()
	config.joinTables = &joinTableRegistry{}
	if config.NowFunc == nil {
		config.NowFunc = func() time.Time { return time.Now().Local() }
	}
	if config.Plugins == nil {
		config.Plugins = map[string]Plugin{}
	}

	if config.ConnPool == nil {
		pool, err := d.Initialize()
		if err != nil {
			return nil, fmt.Errorf("fusion: open %s: %w", d.Name(), err)
		}
		config.ConnPool = pool
	}
	if config.PrepareStmt {
		config.ConnPool = newPreparedPool(config.ConnPool, config.PreparedStmtCacheSize, config.PreparedStmtTTL)
	}

	db := &DB{
		Config: config,
		clone:  1,
		Statement: &Statement{
			DB:       nil, // set below; Config is shared, DB pointer is not
			ConnPool: config.ConnPool,
			Context:  context.TODO(),
			Settings: &sync.Map{},
		},
	}
	db.Statement.DB = db

	if config.TranslateError {
		if _, ok := d.(ErrorTranslator); !ok {
			config.Logger.Warn(context.TODO(),
				"TranslateError is enabled, but dialector %s does not implement ErrorTranslator", d.Name())
		}
	}

	for name, p := range config.Plugins {
		if err := p.Initialize(db); err != nil {
			closePool(config.ConnPool)
			return nil, fmt.Errorf("fusion: initialize plugin %s: %w", name, err)
		}
	}

	if !config.DisableAutomaticPing {
		if pinger, ok := config.ConnPool.(interface{ Ping() error }); ok {
			if err := pinger.Ping(); err != nil {
				closePool(config.ConnPool)
				return nil, fmt.Errorf("fusion: ping %s: %w", d.Name(), err)
			}
		}
	}

	return db, nil
}

// Session returns a derivative configured per config. Flags on config are
// one-way overrides onto a copied Config; the receiver's Config is never
// mutated. With NewDB false (default) the derivative inherits the current
// chain's statement; with Context set the statement is cloned first so the
// new context cannot leak back.
func (db *DB) Session(config *Session) *DB {
	txConfig := *db.Config
	tx := &DB{
		Config:    &txConfig,
		Statement: db.Statement,
		Error:     db.Error,
		clone:     1,
	}

	if config.SkipDefaultTransaction {
		txConfig.SkipDefaultTransaction = true
	}
	if config.AllowGlobalUpdate {
		txConfig.AllowGlobalUpdate = true
	}
	if config.DisableNestedTransaction {
		txConfig.DisableNestedTransaction = true
	}
	if config.DryRun {
		txConfig.DryRun = true
	}
	if config.Logger != nil {
		txConfig.Logger = config.Logger
	}
	if config.NowFunc != nil {
		txConfig.NowFunc = config.NowFunc
	}
	if config.FullSaveAssociations {
		txConfig.FullSaveAssociations = true
	}

	if config.Context != nil {
		tx.Statement = tx.Statement.clone()
		tx.Statement.DB = tx
		tx.Statement.Context = config.Context
	}

	if !config.NewDB {
		tx.clone = 2
	}
	if config.Initialized {
		tx = tx.getInstance()
	}
	return tx
}

// WithContext returns a derivative that runs every statement under ctx.
func (db *DB) WithContext(ctx context.Context) *DB {
	if ctx == nil {
		ctx = context.TODO()
	}
	return db.Session(&Session{Context: ctx})
}

// Debug returns a derivative that logs every SQL trace at Info level.
func (db *DB) Debug() *DB {
	tx := db.getInstance()
	return tx.Session(&Session{Logger: db.Logger.LogMode(logger.Info)})
}

// Use registers a plugin on the session. The plugin name must be unique;
// Initialize runs before the plugin is stored.
func (db *DB) Use(plugin Plugin) error {
	name := plugin.Name()
	if _, ok := db.Plugins[name]; ok {
		return ErrRegistered
	}
	if err := plugin.Initialize(db); err != nil {
		return err
	}
	db.Plugins[name] = plugin
	return nil
}

// ToSQL renders the SQL that queryFn would produce, without executing it.
// queryFn runs in a DryRun session; the captured statement text is
// interpolated through the dialector for display:
//
//	sql := db.ToSQL(func(tx *fusion.DB) *fusion.DB {
//	    return tx.Exec("SELECT * FROM users WHERE id = ?", 1)
//	})
func (db *DB) ToSQL(queryFn func(tx *DB) *DB) string {
	tx := queryFn(db.Session(&Session{DryRun: true}).getInstance())
	stmt := tx.Statement
	if stmt == nil || len(stmt.SQL) == 0 {
		return ""
	}
	return dialect.Explain(db.Dialector, string(stmt.SQL), stmt.Vars...)
}

// Parse returns the cached schema metadata for model, using this session's
// naming strategy and tag strictness.
func (db *DB) Parse(model any) (*schema.Table, error) {
	if db.tables == nil {
		return nil, schema.ErrInvalidModel
	}
	return db.tables.Parse(model)
}

// DB returns the underlying *sql.DB. On a transaction instance it returns
// the root pool the transaction was begun from.
func (db *DB) DB() (*sql.DB, error) {
	unwrap := func(pool ConnPool) ConnPool {
		for {
			wrapper, ok := pool.(interface{ Underlying() ConnPool })
			if !ok {
				return pool
			}
			pool = wrapper.Underlying()
		}
	}
	if db.Statement != nil {
		if pool, ok := unwrap(db.Statement.ConnPool).(*sql.DB); ok {
			return pool, nil
		}
	}
	if pool, ok := unwrap(db.Config.ConnPool).(*sql.DB); ok {
		return pool, nil
	}
	return nil, ErrInvalidDB
}

// Migrator returns a migration runner over the session's pool.
func (db *DB) Migrator(opts ...migrator.Option) (*migrator.Migrator, error) {
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	return migrator.New(pool, db.Dialector, db.tables, opts...), nil
}

// getInstance materializes the copy-on-write chain. clone == 1 (root or
// NewDB session) builds a fresh Statement sharing the pool and context;
// clone == 2 (mid-chain) clones the current Statement so clauses, SQL, and
// settings survive while the receiver stays untouched. clone == 0 (a
// statement after getInstance) is returned as-is.
func (db *DB) getInstance() *DB {
	if db.clone == 0 {
		return db
	}

	tx := &DB{Config: db.Config, Error: db.Error}
	if db.clone == 1 {
		tx.Statement = &Statement{
			DB:       tx,
			ConnPool: db.Statement.ConnPool,
			Context:  db.Statement.Context,
			Settings: &sync.Map{},
		}
	} else {
		tx.Statement = db.Statement.clone()
		tx.Statement.DB = tx
	}
	return tx
}

// closePool releases a pool opened by Open when initialization fails.
func closePool(pool ConnPool) {
	if closer, ok := pool.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
