package fusion

import (
	"context"
	"time"
)

// The hook interfaces below are the compile-time-safe forms of the methods
// schema detects by name at parse time. A model may implement any subset.

// BeforeCreater runs before a record is created.
type BeforeCreater interface {
	BeforeCreate(tx *DB) error
}

// AfterCreater runs after a record is created.
type AfterCreater interface {
	AfterCreate(tx *DB) error
}

// BeforeUpdater runs before a record is updated.
type BeforeUpdater interface {
	BeforeUpdate(tx *DB) error
}

// AfterUpdater runs after a record is updated.
type AfterUpdater interface {
	AfterUpdate(tx *DB) error
}

// BeforeDeleter runs before a record is deleted.
type BeforeDeleter interface {
	BeforeDelete(tx *DB) error
}

// AfterDeleter runs after a record is deleted.
type AfterDeleter interface {
	AfterDelete(tx *DB) error
}

// BeforeFinder runs before a query.
type BeforeFinder interface {
	BeforeFind(tx *DB) error
}

// AfterFinder runs after a query.
type AfterFinder interface {
	AfterFind(tx *DB) error
}

// QueryEvent carries the details of one executed statement to QueryHooks.
// Vars are the bound parameters; RowsAffected is -1 when unknown.
type QueryEvent struct {
	Context      context.Context
	SQL          string
	Vars         []any
	RowsAffected int64
	Error        error
	StartTime    time.Time
	Duration     time.Duration
	Statement    *Statement
}

// QueryHook observes statements around execution. BeforeQuery may replace the
// context used for the I/O call; AfterQuery receives the completed event.
type QueryHook interface {
	BeforeQuery(ctx context.Context, event *QueryEvent) context.Context
	AfterQuery(ctx context.Context, event *QueryEvent)
}
