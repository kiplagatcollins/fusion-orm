package fusion

import (
	"context"
	"sync"
)

// Statement carries per-chain state: the current connection pool, the
// execution context, SQL captured for raw/DryRun execution, and the
// plugin settings store. A Statement belongs to exactly one chain
// instance; chainable methods clone it before mutating.
type Statement struct {
	// DB is the chain instance that owns this statement.
	DB *DB
	// ConnPool is the pool queries run against: the root pool, or the
	// current transaction once Begin has swapped it in.
	ConnPool ConnPool
	// Context is propagated to every I/O call made through this statement.
	Context context.Context
	// SQL holds raw statement text set by Exec (including DryRun capture).
	SQL []byte
	// Vars holds the bound variables for SQL.
	Vars []any
	// Settings is a per-statement key/value store for plugins. It is
	// copied entry-by-entry on clone so chains never share settings.
	Settings *sync.Map
}

// clone returns a deep-enough copy for copy-on-write chaining: SQL/Vars are
// copied, Settings entries are replayed into a fresh map, and the pool and
// context are carried over (a transaction must survive the clone).
func (stmt *Statement) clone() *Statement {
	newStmt := &Statement{
		DB:       stmt.DB,
		ConnPool: stmt.ConnPool,
		Context:  stmt.Context,
		Settings: &sync.Map{},
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
	return newStmt
}
