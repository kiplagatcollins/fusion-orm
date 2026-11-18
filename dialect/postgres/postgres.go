// Package postgres implements the fusion-orm Dialector for PostgreSQL using
// the pgx/v5 stdlib adapter (registered as "pgx").
package postgres

import (
	"database/sql"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"

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
		feature.Savepoint
}

// Initialize opens the underlying database/sql pool.
func (d *Dialector) Initialize() (*sql.DB, error) {
	return sql.Open(driverName, d.dsn)
}

// BindVarTo renders the "$n" placeholder.
func (d *Dialector) BindVarTo(b []byte, index int) []byte {
	return d.BindVarToDollar(b, index)
}
