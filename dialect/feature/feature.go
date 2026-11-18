// Package feature defines bit flags representing optional SQL dialect
// capabilities. Each dialect declares which features it supports by
// combining flags with the bitwise OR operator; query builders branch on
// capability instead of sniffing dialect names.
package feature

import "fmt"

// Feature is a bit flag representing an optional SQL dialect capability.
type Feature uint64

const (
	// Common query features.

	// CTE enables Common Table Expressions (WITH ... AS ...) syntax.
	CTE Feature = 1 << iota
	// WithValues enables WITH ... (VALUES ...) syntax for inline value lists.
	WithValues
	// Returning enables the RETURNING clause to return rows affected by DML.
	Returning
	// Output enables the OUTPUT clause (MSSQL's RETURNING equivalent).
	Output
	// DefaultPlaceholder enables the DEFAULT keyword in INSERT value lists.
	DefaultPlaceholder
	// DoubleColonCast enables PostgreSQL-style :: type cast syntax.
	DoubleColonCast
	// ValuesRow enables VALUES ROW(...) syntax.
	ValuesRow
	// CompositeIn enables WHERE (A, B) IN ((N, NN), ...) comparison syntax.
	CompositeIn

	// SELECT features.

	// OffsetFetch enables OFFSET ... FETCH NEXT syntax.
	OffsetFetch
	// SelectExists enables EXISTS subquery expressions.
	SelectExists

	// INSERT features.

	// InsertReturning enables INSERT ... RETURNING syntax.
	InsertReturning
	// InsertTableAlias enables table alias support in INSERT statements.
	InsertTableAlias
	// InsertOnConflict enables INSERT ... ON CONFLICT syntax.
	InsertOnConflict
	// InsertOnDuplicateKey enables INSERT ... ON DUPLICATE KEY syntax.
	InsertOnDuplicateKey
	// InsertIgnore enables INSERT IGNORE syntax.
	InsertIgnore

	// UPDATE features.

	// UpdateFromTable enables UPDATE ... FROM ... syntax.
	UpdateFromTable
	// UpdateMultiTable enables multi-table UPDATE syntax.
	UpdateMultiTable
	// UpdateTableAlias enables table alias support in UPDATE statements.
	UpdateTableAlias
	// UpdateOrderLimit enables UPDATE ... ORDER BY ... LIMIT syntax.
	UpdateOrderLimit

	// DELETE features.

	// DeleteReturning enables DELETE ... RETURNING syntax.
	DeleteReturning
	// DeleteTableAlias enables table alias support in DELETE statements.
	DeleteTableAlias
	// DeleteOrderLimit enables DELETE ... ORDER BY ... LIMIT syntax.
	DeleteOrderLimit

	// MERGE features.

	// Merge enables MERGE ... USING ... ON ... WHEN syntax.
	Merge
	// MergeReturning enables MERGE ... RETURNING syntax.
	MergeReturning

	// Table DDL features.

	// TableCascade enables CASCADE for DROP TABLE and related operations.
	TableCascade
	// TableIdentity enables table-level IDENTITY property.
	TableIdentity
	// TableTruncate enables TRUNCATE TABLE support.
	TableTruncate
	// TableNotExists enables IF NOT EXISTS / IF EXISTS DDL syntax.
	TableNotExists
	// AlterColumnExists enables ADD/DROP COLUMN IF [NOT] EXISTS syntax.
	AlterColumnExists
	// CreateIndexIfNotExists enables CREATE INDEX IF NOT EXISTS syntax.
	CreateIndexIfNotExists

	// Column definition features.

	// AutoIncrement enables AUTO_INCREMENT column syntax.
	AutoIncrement
	// Identity enables IDENTITY column syntax.
	Identity
	// GeneratedIdentity enables GENERATED ALWAYS AS IDENTITY syntax.
	GeneratedIdentity

	// Dialect-specific features.

	// FKDefaultOnAction indicates FK ON UPDATE/ON DELETE defaults to NO ACTION.
	FKDefaultOnAction
	// Savepoint enables SAVEPOINT / ROLLBACK TO / RELEASE statements used
	// for nested transactions.
	Savepoint
)

// Has reports whether all bits of flag are present in f.
func (f Feature) Has(flag Feature) bool {
	return f&flag == flag
}

// Set returns f with flag added.
func (f Feature) Set(flag Feature) Feature {
	return f | flag
}

// Remove returns f with flag cleared.
func (f Feature) Remove(flag Feature) Feature {
	return f &^ flag
}

// String renders the flag names contained in f in declaration order, e.g.
// "CTE|Returning". Unknown bits render as their numeric value.
func (f Feature) String() string {
	if f == 0 {
		return "none"
	}
	var (
		s    []byte
		seen bool
	)
	for _, e := range ordered {
		if f.Has(e.flag) {
			if seen {
				s = append(s, '|')
			}
			s = append(s, e.name...)
			seen = true
			f = f.Remove(e.flag)
		}
	}
	if f != 0 {
		if seen {
			s = append(s, '|')
		}
		s = append(s, fmt.Sprintf("%#x", uint64(f))...)
	}
	return string(s)
}

// Name returns the canonical name of a single-bit flag, or
// "unknown(<value>)" for zero or composite values.
func (f Feature) Name() string {
	if name, ok := flag2str[f]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", uint64(f))
}

type entry struct {
	flag Feature
	name string
}

// ordered mirrors flag2str in iota declaration order for stable output.
var ordered = []entry{
	{CTE, "CTE"},
	{WithValues, "WithValues"},
	{Returning, "Returning"},
	{Output, "Output"},
	{DefaultPlaceholder, "DefaultPlaceholder"},
	{DoubleColonCast, "DoubleColonCast"},
	{ValuesRow, "ValuesRow"},
	{CompositeIn, "CompositeIn"},
	{OffsetFetch, "OffsetFetch"},
	{SelectExists, "SelectExists"},
	{InsertReturning, "InsertReturning"},
	{InsertTableAlias, "InsertTableAlias"},
	{InsertOnConflict, "InsertOnConflict"},
	{InsertOnDuplicateKey, "InsertOnDuplicateKey"},
	{InsertIgnore, "InsertIgnore"},
	{UpdateFromTable, "UpdateFromTable"},
	{UpdateMultiTable, "UpdateMultiTable"},
	{UpdateTableAlias, "UpdateTableAlias"},
	{UpdateOrderLimit, "UpdateOrderLimit"},
	{DeleteReturning, "DeleteReturning"},
	{DeleteTableAlias, "DeleteTableAlias"},
	{DeleteOrderLimit, "DeleteOrderLimit"},
	{Merge, "Merge"},
	{MergeReturning, "MergeReturning"},
	{TableCascade, "TableCascade"},
	{TableIdentity, "TableIdentity"},
	{TableTruncate, "TableTruncate"},
	{TableNotExists, "TableNotExists"},
	{AlterColumnExists, "AlterColumnExists"},
	{CreateIndexIfNotExists, "CreateIndexIfNotExists"},
	{AutoIncrement, "AutoIncrement"},
	{Identity, "Identity"},
	{GeneratedIdentity, "GeneratedIdentity"},
	{FKDefaultOnAction, "FKDefaultOnAction"},
	{Savepoint, "Savepoint"},
}

var flag2str = func() map[Feature]string {
	m := make(map[Feature]string, len(ordered))
	for _, e := range ordered {
		m[e.flag] = e.name
	}
	return m
}()
