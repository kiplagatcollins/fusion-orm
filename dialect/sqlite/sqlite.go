// Package sqlite implements the fusion-orm Dialector for SQLite using the
// pure-Go modernc.org driver (registered as "sqlite").
package sqlite

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"

	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// driverName is the database/sql driver registered by modernc.org/sqlite.
const driverName = "sqlite"

// Dialector implements dialect.Dialector for SQLite.
type Dialector struct {
	dialect.BaseDialect
	dsn string
}

// Open returns a Dialector that will open dsn (a file path, ":memory:", or a
// modernc DSN such as "file:test.db?_pragma=busy_timeout(5000)").
func Open(dsn string) dialect.Dialector {
	return &Dialector{
		BaseDialect: dialect.NewBaseDialect('"'),
		dsn:         dsn,
	}
}

// Name implements dialect.Dialector.
func (d *Dialector) Name() dialect.Name { return dialect.SQLite }

// Features declares SQLite's capabilities.
func (d *Dialector) Features() feature.Feature {
	return feature.CTE |
		feature.WithValues |
		feature.Returning |
		feature.InsertReturning |
		feature.InsertTableAlias |
		feature.UpdateTableAlias |
		feature.DeleteTableAlias |
		feature.InsertOnConflict |
		feature.TableNotExists |
		feature.SelectExists |
		feature.AutoIncrement |
		feature.CompositeIn |
		feature.FKDefaultOnAction |
		feature.DeleteReturning |
		feature.CreateIndexIfNotExists |
		feature.Savepoint
}

// Initialize opens the underlying database/sql pool.
func (d *Dialector) Initialize() (*sql.DB, error) {
	return sql.Open(driverName, d.dsn)
}

// BindVarTo renders the "?" placeholder.
func (d *Dialector) BindVarTo(b []byte, _ int) []byte {
	return d.BindVarToQuestion(b, 1)
}

// AppendBytes renders SQLite's X'...' hex literal.
func (d *Dialector) AppendBytes(b []byte, bs []byte) []byte {
	if bs == nil {
		return d.AppendNull(b)
	}

	b = append(b, `X'`...)
	s := len(b)
	b = append(b, make([]byte, hex.EncodedLen(len(bs)))...)
	hex.Encode(b[s:], bs)
	b = append(b, '\'')
	return b
}

// Translate maps modernc constraint codes onto the shared sentinels.
func (d *Dialector) Translate(err error) error {
	var sqliteErr *sqliteDriver.Error
	if !errors.As(err, &sqliteErr) {
		return err
	}
	var sentinel error
	switch sqliteErr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		sentinel = dialect.ErrDuplicatedKey
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		sentinel = dialect.ErrForeignKeyViolated
	case sqlite3.SQLITE_CONSTRAINT_CHECK:
		sentinel = dialect.ErrCheckConstraintViolated
	}
	if sentinel == nil {
		return err
	}
	return fmt.Errorf("%w: %w", sentinel, err)
}
