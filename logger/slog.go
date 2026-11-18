package logger

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// slogLogger adapts Interface to log/slog, emitting structured trace events.
type slogLogger struct {
	Logger *slog.Logger
	config Config
}

// NewSlogLogger returns an Interface that writes through the given slog
// logger. Trace events are emitted as a "trace" attribute group with
// duration/sql/rows/error keys.
func NewSlogLogger(l *slog.Logger, config Config) Interface {
	if config.LogLevel == 0 {
		config.LogLevel = Warn
	}
	if l == nil {
		l = slog.Default()
	}
	return &slogLogger{Logger: l, config: config}
}

func (l *slogLogger) LogMode(level LogLevel) Interface {
	next := *l
	next.config.LogLevel = level
	return &next
}

func (l *slogLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.config.LogLevel >= Info {
		l.log(ctx, slog.LevelInfo, msg, data...)
	}
}

func (l *slogLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.config.LogLevel >= Warn {
		l.log(ctx, slog.LevelWarn, msg, data...)
	}
}

func (l *slogLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.config.LogLevel >= Error {
		l.log(ctx, slog.LevelError, msg, data...)
	}
}

func (l *slogLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.config.LogLevel <= Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	attrs := []any{
		"duration", ms(elapsed),
		"sql", sql,
	}
	if rows != -1 {
		attrs = append(attrs, "rows", rows)
	}

	switch {
	case err != nil && l.config.LogLevel >= Error &&
		(!errors.Is(err, ErrRecordNotFound) || !l.config.IgnoreRecordNotFoundError):
		l.log(ctx, slog.LevelError, "SQL executed", append(attrs, "error", err.Error())...)
	case l.config.SlowThreshold != 0 && elapsed > l.config.SlowThreshold && l.config.LogLevel >= Warn:
		l.log(ctx, slog.LevelWarn, "SQL executed", attrs...)
	case l.config.LogLevel >= Info:
		l.log(ctx, slog.LevelInfo, "SQL executed", attrs...)
	}
}

// ParamsFilter implements ParamsFilter; see textLogger.ParamsFilter.
func (l *slogLogger) ParamsFilter(_ context.Context, sql string, params ...any) (string, []any) {
	if l.config.ParameterizedQueries {
		return sql, nil
	}
	return sql, params
}

func (l *slogLogger) log(ctx context.Context, level slog.Level, msg string, data ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := make([]slog.Attr, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		key, _ := data[i].(string)
		if key == "" {
			key = "data"
		}
		attrs = append(attrs, slog.Any(key, data[i+1]))
	}
	if len(data)%2 == 1 {
		attrs = append(attrs, slog.Any("data", data[len(data)-1]))
	}
	l.Logger.LogAttrs(ctx, level, msg, attrs...)
}
