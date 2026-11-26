package fusion

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.TODO()
	}
	return ctx
}

func (db *DB) beforeQuery(ctx context.Context, event *QueryEvent) context.Context {
	for _, hook := range db.QueryHooks {
		if hook != nil {
			ctx = hook.BeforeQuery(ctx, event)
		}
	}
	return nonNilContext(ctx)
}

func finishQuery(db *DB, ctx context.Context, event *QueryEvent, begin time.Time, rows int64, err error) {
	event.Context = ctx
	event.RowsAffected = rows
	event.Error = err
	event.Duration = time.Since(begin)
	trace(db, ctx, begin, event.SQL, event.Vars, err)
	for _, hook := range db.QueryHooks {
		if hook != nil {
			hook.AfterQuery(ctx, event)
		}
	}
}

func (db *DB) execContext(ctx context.Context, query string, vars ...any) (sql.Result, error) {
	ctx = nonNilContext(ctx)
	begin := time.Now()
	event := &QueryEvent{Context: ctx, SQL: query, Vars: vars, StartTime: begin, Statement: &db.Statement}
	ctx = db.beforeQuery(ctx, event)
	result, err := db.Statement.ConnPool.ExecContext(ctx, query, vars...)
	rows := int64(-1)
	if result != nil {
		if affected, e := result.RowsAffected(); e == nil {
			rows = affected
		}
	}
	finishQuery(db, ctx, event, begin, rows, err)
	return result, err
}

func (db *DB) queryContext(ctx context.Context, query string, vars ...any) (*sql.Rows, error) {
	ctx = nonNilContext(ctx)
	begin := time.Now()
	event := &QueryEvent{Context: ctx, SQL: query, Vars: vars, StartTime: begin, Statement: &db.Statement}
	ctx = db.beforeQuery(ctx, event)
	rows, err := db.Statement.ConnPool.QueryContext(ctx, query, vars...)
	finishQuery(db, ctx, event, begin, -1, err)
	return rows, err
}

func (db *DB) queryRowContext(ctx context.Context, query string, vars ...any) *sql.Row {
	ctx = nonNilContext(ctx)
	begin := time.Now()
	event := &QueryEvent{Context: ctx, SQL: query, Vars: vars, StartTime: begin, Statement: &db.Statement}
	ctx = db.beforeQuery(ctx, event)
	row := db.Statement.ConnPool.QueryRowContext(ctx, query, vars...)
	finishQuery(db, ctx, event, begin, -1, nil)
	return row
}

// SlogQueryHook logs completed statements through log/slog without recording
// bound parameter values.
type SlogQueryHook struct {
	Logger *slog.Logger
	Level  slog.Level
}

// BeforeQuery implements QueryHook.
func (h *SlogQueryHook) BeforeQuery(ctx context.Context, _ *QueryEvent) context.Context { return ctx }

// AfterQuery implements QueryHook.
func (h *SlogQueryHook) AfterQuery(ctx context.Context, event *QueryEvent) {
	if h == nil || h.Logger == nil || event == nil {
		return
	}
	attrs := []any{
		"sql", event.SQL,
		"duration_ms", float64(event.Duration.Microseconds()) / 1000,
		"rows", event.RowsAffected,
	}
	if event.Error != nil {
		attrs = append(attrs, "error", event.Error.Error())
	}
	h.Logger.Log(nonNilContext(ctx), h.Level, "query", attrs...)
}

// QueryHook registers an observability hook on a derivative session.
func (db *DB) QueryHook(hook QueryHook) *DB {
	tx := db.getInstance()
	config := *tx.Config
	config.QueryHooks = append(append([]QueryHook(nil), config.QueryHooks...), hook)
	tx.Config = &config
	return tx
}
