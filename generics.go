package fusion

import (
	"context"
	"database/sql"

	"github.com/kiplagatcollins/fusion-orm/clause"
)

// ChainInterface is the checked chainable surface of the generics facade.
type ChainInterface[T any] interface {
	Model(any) *Generic[T]
	Table(string) *Generic[T]
	Select(...string) *Generic[T]
	Distinct() *Generic[T]
	Where(any, ...any) *Generic[T]
	Or(any, ...any) *Generic[T]
	Order(string) *Generic[T]
	Limit(int) *Generic[T]
	Offset(int) *Generic[T]
	Joins(string, ...any) *Generic[T]
	Preload(string, ...any) *Generic[T]
	Scopes(...func(*DB) *DB) *Generic[T]
	Clauses(...clause.Interface) *Generic[T]
	Unscoped() *Generic[T]
	WithContext(context.Context) *Generic[T]
	WithResult(T) *Generic[T]
	Session(*Session) *Generic[T]
	Debug() *Generic[T]
}

// ExecInterface is the checked finisher surface of the generics facade.
type ExecInterface[T any] interface {
	Create(*T) error
	Save(*T) error
	Find(...any) ([]T, error)
	First(...any) (T, error)
	Take(...any) (T, error)
	Last(...any) (T, error)
	Update(string, any) error
	Updates(any) error
	Delete(...any) error
	Count(*int64) error
	Pluck(string, any) error
	Scan(any) error
	FindInBatches([]T, int, func(*Generic[T], int) error) error
	FirstOrInit(...any) (T, error)
	FirstOrCreate(...any) (T, error)
	Row() *sql.Row
	Rows() (*sql.Rows, error)
	ToSQL(func(*DB) *DB) string
}

// Interface is the complete checked generics surface.
type Interface[T any] interface {
	ChainInterface[T]
	ExecInterface[T]
}

// Generic is the typed facade over a *DB chain.
type Generic[T any] struct {
	db        *DB
	result    T
	hasResult bool
}

var _ Interface[struct{}] = (*Generic[struct{}])(nil)

// G creates a typed facade. Options are applied to a fresh derivative without
// mutating the original session.
func G[T any](db *DB, opts ...Option) *Generic[T] {
	tx := db.Session(&Session{NewDB: db.clone == 1})
	for _, opt := range opts {
		if opt != nil {
			opt.Apply(tx.Config)
		}
	}
	return &Generic[T]{db: tx}
}

func (g *Generic[T]) chain(tx *DB) *Generic[T] {
	return &Generic[T]{db: tx, result: g.result, hasResult: g.hasResult}
}

func (g *Generic[T]) modelDB() *DB {
	tx := g.db.getInstance()
	if tx.Statement.Model == nil {
		tx.Statement.Model = new(T)
	}
	return tx
}

// Model sets the model used to derive metadata.
func (g *Generic[T]) Model(value any) *Generic[T] { return g.chain(g.db.Model(value)) }

// Table overrides the table name.
func (g *Generic[T]) Table(name string) *Generic[T] { return g.chain(g.db.Table(name)) }

// Select restricts selected columns.
func (g *Generic[T]) Select(columns ...string) *Generic[T] { return g.chain(g.db.Select(columns...)) }

// Distinct adds DISTINCT.
func (g *Generic[T]) Distinct() *Generic[T] { return g.chain(g.db.Distinct()) }

// Where adds an AND condition.
func (g *Generic[T]) Where(query any, args ...any) *Generic[T] {
	return g.chain(g.db.Where(query, args...))
}

// Or adds an OR condition.
func (g *Generic[T]) Or(query any, args ...any) *Generic[T] {
	return g.chain(g.db.Or(query, args...))
}

// Order adds ORDER BY columns.
func (g *Generic[T]) Order(value string) *Generic[T] {
	return g.chain(g.db.Order(value))
}

// Limit adds a LIMIT.
func (g *Generic[T]) Limit(n int) *Generic[T] { return g.chain(g.db.Limit(n)) }

// Offset adds an OFFSET.
func (g *Generic[T]) Offset(n int) *Generic[T] { return g.chain(g.db.Offset(n)) }

// Joins adds a raw or relation join.
func (g *Generic[T]) Joins(query string, args ...any) *Generic[T] {
	return g.chain(g.db.Joins(query, args...))
}

// Preload requests a batched relation load.
func (g *Generic[T]) Preload(query string, args ...any) *Generic[T] {
	return g.chain(g.db.Preload(query, args...))
}

// Scopes applies query scopes.
func (g *Generic[T]) Scopes(funcs ...func(*DB) *DB) *Generic[T] {
	return g.chain(g.db.Scopes(funcs...))
}

// Clauses adds custom clause builders.
func (g *Generic[T]) Clauses(conds ...clause.Interface) *Generic[T] {
	return g.chain(g.db.Clauses(conds...))
}

// Unscoped disables soft-delete handling.
func (g *Generic[T]) Unscoped() *Generic[T] { return g.chain(g.db.Unscoped()) }

// WithContext propagates ctx to the derivative.
func (g *Generic[T]) WithContext(ctx context.Context) *Generic[T] {
	return g.chain(g.db.WithContext(ctx))
}

// WithResult sets the value First/Take/Last write into.
func (g *Generic[T]) WithResult(value T) *Generic[T] {
	next := g.chain(g.db)
	next.result = value
	next.hasResult = true
	return next
}

// Session configures a derivative.
func (g *Generic[T]) Session(config *Session) *Generic[T] {
	return g.chain(g.db.Session(config))
}

// Debug logs every statement at Info level on the derivative.
func (g *Generic[T]) Debug() *Generic[T] { return g.chain(g.db.Debug()) }

// Create inserts value.
func (g *Generic[T]) Create(value *T) error { return g.db.Create(value).Error }

// Save inserts or updates value.
func (g *Generic[T]) Save(value *T) error { return g.db.Save(value).Error }

// Find returns all matching rows.
func (g *Generic[T]) Find(conds ...any) ([]T, error) {
	out := make([]T, 0)
	tx := g.db.Find(&out, conds...)
	return out, tx.Error
}

// First returns the first row by primary key.
func (g *Generic[T]) First(conds ...any) (T, error) {
	var out T
	tx := g.db.First(&out, conds...)
	if tx.Error != nil {
		return out, tx.Error
	}
	if g.hasResult {
		g.result = out
		return g.result, nil
	}
	return out, nil
}

// Take returns the first row without implicit ordering.
func (g *Generic[T]) Take(conds ...any) (T, error) {
	var out T
	tx := g.db.Take(&out, conds...)
	if tx.Error != nil {
		return out, tx.Error
	}
	if g.hasResult {
		g.result = out
		return g.result, nil
	}
	return out, nil
}

// Last returns the last row by primary key.
func (g *Generic[T]) Last(conds ...any) (T, error) {
	var out T
	tx := g.db.Last(&out, conds...)
	if tx.Error != nil {
		return out, tx.Error
	}
	if g.hasResult {
		g.result = out
		return g.result, nil
	}
	return out, nil
}

// Update sets one column on matching rows.
func (g *Generic[T]) Update(column string, value any) error {
	return g.modelDB().Update(column, value).Error
}

// Updates sets columns from a map or struct.
func (g *Generic[T]) Updates(values any) error {
	return g.modelDB().Updates(values).Error
}

// Delete removes matching rows.
func (g *Generic[T]) Delete(conds ...any) error {
	return g.db.Delete(new(T), conds...).Error
}

// Count stores the row count in count.
func (g *Generic[T]) Count(count *int64) error {
	return g.modelDB().Count(count).Error
}

// Pluck scans one column into dest.
func (g *Generic[T]) Pluck(column string, dest any) error {
	return g.modelDB().Pluck(column, dest).Error
}

// Scan scans the statement into dest.
func (g *Generic[T]) Scan(dest any) error { return g.db.Scan(dest).Error }

// FindInBatches loads rows in batches of batchSize.
func (g *Generic[T]) FindInBatches(dest []T, batchSize int, fc func(*Generic[T], int) error) error {
	return g.modelDB().FindInBatches(&dest, batchSize, func(tx *DB, batch int) error {
		return fc(&Generic[T]{db: tx}, batch)
	}).Error
}

// FirstOrInit returns the first matching row or an unsaved value.
func (g *Generic[T]) FirstOrInit(conds ...any) (T, error) {
	var out T
	tx := g.db.FirstOrInit(&out, conds...)
	if tx.Error != nil {
		return out, tx.Error
	}
	return out, nil
}

// FirstOrCreate returns the first matching row or inserts a new one.
func (g *Generic[T]) FirstOrCreate(conds ...any) (T, error) {
	var out T
	tx := g.db.FirstOrCreate(&out, conds...)
	if tx.Error != nil {
		return out, tx.Error
	}
	return out, nil
}

// Row returns a single raw row.
func (g *Generic[T]) Row() *sql.Row { return g.modelDB().Row() }

// Rows returns the raw result set; the caller must close it.
func (g *Generic[T]) Rows() (*sql.Rows, error) { return g.modelDB().Rows() }

// ToSQL renders the statement built by fc without executing it.
func (g *Generic[T]) ToSQL(fc func(*DB) *DB) string { return g.db.ToSQL(fc) }
