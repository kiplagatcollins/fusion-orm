// Package postgres implements the fusion-orm Dialector for PostgreSQL using
// the pgx/v5 stdlib adapter (registered as "pgx").
package postgres

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
)

// driverName is the database/sql driver registered by pgx/v5/stdlib.
const driverName = "pgx"

// Dialector implements dialect.Dialector for PostgreSQL.
type Dialector struct {
	dialect.BaseDialect
	dsn string
}

// Open returns a Dialector that will open dsn (a URL such as
// "postgres://user:pass@localhost/db" or a keyword/value string).
func Open(dsn string) dialect.Dialector {
	return &Dialector{
		BaseDialect: dialect.NewBaseDialect('"'),
		dsn:         dsn,
	}
}

// Name implements dialect.Dialector.
func (d *Dialector) Name() dialect.Name { return dialect.Postgres }

// Features declares PostgreSQL's capabilities.
func (d *Dialector) Features() feature.Feature {
	return feature.CTE |
		feature.WithValues |
		feature.Returning |
		feature.InsertReturning |
		feature.DefaultPlaceholder |
		feature.DoubleColonCast |
		feature.InsertTableAlias |
		feature.UpdateTableAlias |
		feature.DeleteTableAlias |
		feature.TableCascade |
		feature.TableIdentity |
		feature.TableTruncate |
		feature.TableNotExists |
		feature.InsertOnConflict |
		feature.SelectExists |
		feature.GeneratedIdentity |
		feature.CompositeIn |
		feature.FKDefaultOnAction |
		feature.DeleteReturning |
		feature.Merge |
		feature.MergeReturning |
		feature.AlterColumnExists |
		feature.CreateIndexIfNotExists |
		feature.Savepoint |
		feature.PostgreSQLTypes |
		feature.InformationSchema |
		feature.DollarPlaceholder |
		feature.SerialColumn |
		feature.AlterColumnType |
		feature.AddColumnNonConstantDefault
}

// Initialize opens the underlying database/sql pool.
func (d *Dialector) Initialize() (*sql.DB, error) {
	return sql.Open(driverName, d.dsn)
}

// BindVarTo renders the "$n" placeholder.
func (d *Dialector) BindVarTo(b []byte, index int) []byte {
	return d.BindVarToDollar(b, index)
}

// Translate maps PostgreSQL SQLSTATE codes onto the shared sentinels.
func (d *Dialector) Translate(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	var sentinel error
	switch pgErr.Code {
	case "23505":
		sentinel = dialect.ErrDuplicatedKey
	case "23503":
		sentinel = dialect.ErrForeignKeyViolated
	case "23514":
		sentinel = dialect.ErrCheckConstraintViolated
	}
	if sentinel == nil {
		return err
	}
	return fmt.Errorf("%w: %w", sentinel, err)
}
