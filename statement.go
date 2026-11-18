package fusion

import (
	"context"
	"sync"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/dialect"
)

// Statement carries per-chain state: clauses, model/destination, vars, the
// connection pool, execution context, SQL captured for raw/DryRun execution,
// and the plugin settings store. A Statement belongs to exactly one chain
// instance; chainable methods clone it before mutating.
type Statement struct {
	// DB is the chain instance that owns this statement.
	DB *DB
	// ConnPool is the pool queries run against: the root pool, or the
	// current transaction once Begin has swapped it in.
	ConnPool ConnPool
	// Context is propagated to every I/O call made through this statement.
	Context context.Context
	// SQL holds the assembled or raw statement text.
	SQL []byte
	// Vars holds the bound variables for SQL.
	Vars []any
	// Settings is a per-statement key/value store for plugins. It is
	// copied entry-by-entry on clone so chains never share settings.
	Settings *sync.Map

	// Model is the struct/slice used to derive table and columns.
	Model any
	// Dest is the destination passed to a finisher.
	Dest any
	// Table overrides the model-derived table name.
	Table string
	// Clauses holds the accumulated clause builders by name.
	Clauses map[string]clause.Interface
	// Order is the stable clause build order.
	Order []string

	// RaiseErrorOnNotFound makes First/Take/Last return ErrRecordNotFound.
	RaiseErrorOnNotFound bool
	// SkipHooks suppresses model hooks for this statement.
	SkipHooks bool
	// CreateBatchSize bounds rows per INSERT statement (0 = all rows).
	CreateBatchSize int
	// Preloads holds requested relation paths; each path is loaded in one
	// batched query, with nested paths loaded after their parent.
	Preloads []*preloadEntry
	// Joins holds explicit raw joins and relation names to LEFT JOIN.
	Joins []joinEntry
}

func statementContext(stmt *Statement) context.Context {
	if stmt != nil && stmt.Context != nil {
		return stmt.Context
	}
	return context.TODO()
}

// AddClause registers or replaces c, preserving its first-seen build order.
func (stmt *Statement) AddClause(c clause.Interface) {
	if stmt.Clauses == nil {
		stmt.Clauses = make(map[string]clause.Interface, 8)
	}
	name := c.Name()
	if _, exists := stmt.Clauses[name]; !exists {
		stmt.Order = append(stmt.Order, name)
	}
	stmt.Clauses[name] = c
}

// RemoveClause drops the named clause from the statement.
func (stmt *Statement) RemoveClause(name string) {
	delete(stmt.Clauses, name)
	for i, n := range stmt.Order {
		if n == name {
			stmt.Order = append(stmt.Order[:i], stmt.Order[i+1:]...)
			return
		}
	}
}

// Clause returns the named clause, if present.
func (stmt *Statement) Clause(name string) clause.Interface { return stmt.Clauses[name] }

// QuoteTo quotes an identifier through the active dialect.
func (stmt *Statement) QuoteTo(ident string) string {
	return string(stmt.dialect().QuoteTo(nil, ident))
}

// Set stores a plugin/step value on the statement settings.
func (stmt *Statement) Set(key string, value any) {
	if stmt.Settings == nil {
		stmt.Settings = &sync.Map{}
	}
	stmt.Settings.Store(key, value)
}

// Get retrieves a statement setting.
func (stmt *Statement) Get(key string) (any, bool) {
	if stmt.Settings == nil {
		return nil, false
	}
	return stmt.Settings.Load(key)
}

// Delete removes a statement setting.
func (stmt *Statement) Delete(key string) {
	if stmt.Settings != nil {
		stmt.Settings.Delete(key)
	}
}

func (stmt *Statement) dialect() dialect.Dialector { return stmt.DB.Dialector }

// buildOrder is the canonical clause order. Custom clauses keep their
// insertion position after the standard ones.
var buildOrder = []string{
	"with", "select", "insert", "update", "delete", "from", "join", "set", "values",
	"on_conflict", "where", "order", "limit", "offset", "locking", "returning",
}

// BuildClauses renders all registered clauses in order, binds vars, and
// stores the result in SQL. Each build starts with a fresh var list.
func (stmt *Statement) BuildClauses() []byte {
	b := &statementBuilder{stmt: stmt}
	stmt.Vars = stmt.Vars[:0]
	names := make([]string, 0, len(stmt.Order))
	for _, name := range buildOrder {
		if stmt.Clauses[name] != nil {
			names = append(names, name)
		}
	}
	for _, name := range stmt.Order {
		found := false
		for _, existing := range names {
			if existing == name {
				found = true
				break
			}
		}
		if !found && stmt.Clauses[name] != nil {
			names = append(names, name)
		}
	}
	for _, name := range names {
		c := stmt.Clauses[name]
		if len(b.buf) > 0 && b.buf[len(b.buf)-1] != ' ' {
			b.buf = append(b.buf, ' ')
		}
		c.Build(b)
	}
	stmt.SQL = b.buf
	return b.buf
}

type statementBuilder struct {
	stmt *Statement
	buf  []byte
}

func (b *statementBuilder) WriteString(s string)         { b.buf = append(b.buf, s...) }
func (b *statementBuilder) AppendByte(c byte)            { b.buf = append(b.buf, c) }
func (b *statementBuilder) Dialector() dialect.Dialector { return b.stmt.dialect() }

func (b *statementBuilder) WriteQuoted(s string) {
	b.buf = b.stmt.dialect().QuoteTo(b.buf, s)
}

func (b *statementBuilder) AddVar(v any) {
	b.buf = b.stmt.dialect().BindVarTo(b.buf, len(b.stmt.Vars)+1)
	b.stmt.Vars = append(b.stmt.Vars, v)
}

// clone returns a deep-enough copy for copy-on-write chaining: SQL/Vars and
// clause state are copied, Settings entries are replayed into a fresh map,
// and the pool and context are carried over (a transaction must survive it).
func (stmt *Statement) clone() *Statement {
	newStmt := &Statement{
		DB:                   stmt.DB,
		ConnPool:             stmt.ConnPool,
		Context:              stmt.Context,
		Settings:             &sync.Map{},
		Model:                stmt.Model,
		Dest:                 stmt.Dest,
		Table:                stmt.Table,
		RaiseErrorOnNotFound: stmt.RaiseErrorOnNotFound,
		SkipHooks:            stmt.SkipHooks,
		CreateBatchSize:      stmt.CreateBatchSize,
		Preloads:             clonePreloads(stmt.Preloads),
		Joins:                append([]joinEntry(nil), stmt.Joins...),
	}
	if stmt.SQL != nil {
		newStmt.SQL = append([]byte(nil), stmt.SQL...)
	}
	if stmt.Vars != nil {
		newStmt.Vars = append([]any(nil), stmt.Vars...)
	}
	if stmt.Settings != nil {
		stmt.Settings.Range(func(k, v any) bool {
			newStmt.Settings.Store(k, v)
			return true
		})
	}
	if stmt.Clauses != nil {
		newStmt.Clauses = make(map[string]clause.Interface, len(stmt.Clauses))
		for name, c := range stmt.Clauses {
			newStmt.Clauses[name] = c
		}
	}
	if stmt.Order != nil {
		newStmt.Order = append([]string(nil), stmt.Order...)
	}
	return newStmt
}
