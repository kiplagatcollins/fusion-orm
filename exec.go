package fusion

import (
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
		tx.Logger.Trace(ctx, begin, func() (string, int64) {
			s, p := sql, args
			if filter, ok := tx.Logger.(logger.ParamsFilter); ok {
				s, p = filter.ParamsFilter(ctx, sql, args...)
			}
			if p == nil {
				// ParameterizedQueries mode (or no args): log as-is.
				return s, tx.RowsAffected
			}
			return dialect.Explain(tx.Dialector, s, p...), tx.RowsAffected
		}, err)
	}
	return tx
}
