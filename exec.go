package fusion

import (
	"context"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

// Exec executes raw SQL with bound arguments and returns a derived
// instance carrying Error and RowsAffected. The statement is traced through
// the session logger; with Config.DryRun set the SQL and args are captured
// on Statement instead of being executed (see ToSQL).
//
//	tx := db.Exec("UPDATE users SET active = ?", true)
//	if tx.Error != nil { ... }
func (db *DB) Exec(sql string, args ...any) *DB {
	tx := db.getInstance()

	if tx.DryRun {
		tx.Statement.SQL = append([]byte(nil), sql...)
		tx.Statement.Vars = append([]any(nil), args...)
		return tx
	}

	ctx := tx.Statement.Context
	begin := time.Now()

	result, err := tx.Statement.ConnPool.ExecContext(ctx, sql, args...)
	if err != nil {
		tx.AddError(err)
	} else if result != nil {
		if n, raErr := result.RowsAffected(); raErr == nil {
			tx.RowsAffected = n
		}
	}

	if tx.Logger != nil {
		trace(tx, ctx, begin, sql, args, err)
	}
	return tx
}

func trace(db *DB, ctx context.Context, begin time.Time, sql string, vars []any, err error) {
	if db.Logger == nil {
		return
	}
	db.Logger.Trace(ctx, begin, func() (string, int64) {
		s, p := sql, vars
		if filter, ok := db.Logger.(logger.ParamsFilter); ok {
			s, p = filter.ParamsFilter(ctx, sql, vars...)
		}
		if p == nil {
			return s, db.RowsAffected
		}
		return dialect.Explain(db.Dialector, s, p...), db.RowsAffected
	}, err)
}
