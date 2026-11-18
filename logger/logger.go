// Package logger defines fusion-orm's logging interface, a stdlib-based
// default implementation, and a log/slog adapter.
package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// ErrRecordNotFound is the sentinel for "no row matched". The root package
// aliases it so callers branch on a single value.
var ErrRecordNotFound = errors.New("record not found")

// LogLevel is the severity gate for log output.
type LogLevel int

const (
	// Silent disables all output, including SQL traces.
	Silent LogLevel = iota + 1
	// Error logs failed SQL traces and Error/Warn/Info calls.
	Error
	// Warn additionally logs slow SQL traces and Warn/Info calls.
	Warn
	// Info additionally logs every SQL trace and Info calls.
	Info
)

// Interface is the logger surface used throughout fusion-orm.
//
// Trace is called once per executed statement: begin is the start time, fc
// lazily produces the SQL text and rows-affected count (pass -1 for
// unknown), and err is the statement error (nil on success).
type Interface interface {
	// LogMode returns a copy of the logger gated at level.
	LogMode(level LogLevel) Interface
	// Info logs an informational message.
	Info(ctx context.Context, msg string, data ...any)
	// Warn logs a warning message.
	Warn(ctx context.Context, msg string, data ...any)
	// Error logs an error message.
	Error(ctx context.Context, msg string, data ...any)
	// Trace logs a completed SQL statement.
	Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error)
}

// ParamsFilter is an optional interface a logger can implement to rewrite
// the SQL/params pair before it reaches Trace. With ParameterizedQueries
// enabled the bound variables are dropped so secrets never reach the log.
type ParamsFilter interface {
	ParamsFilter(ctx context.Context, sql string, params ...any) (string, []any)
}

// Config tunes a logger built with New.
type Config struct {
	// SlowThreshold marks traces slower than this as warnings. Zero
	// disables slow-SQL detection.
	SlowThreshold time.Duration
	// LogLevel gates output; the zero value means Warn.
	LogLevel LogLevel
	// IgnoreRecordNotFoundError suppresses traces whose error is
	// ErrRecordNotFound (common and expected on First/Take/Last misses).
	IgnoreRecordNotFoundError bool
	// ParameterizedQueries strips bound variables from logged SQL so
	// user data never reaches the log.
	ParameterizedQueries bool
	// Colorful enables ANSI colors in the stdlib writer output.
	Colorful bool
}

// Writer is the minimal sink the stdlib logger adapts to.
type Writer interface {
	Printf(format string, v ...any)
}

var (
	// Discard drops everything.
	Discard = New(log.New(io.Discard, "", log.LstdFlags), Config{LogLevel: Silent})
	// Default writes to stdout at Warn with a 200ms slow threshold.
	Default = New(log.New(os.Stdout, "", log.LstdFlags), Config{
		LogLevel:      Warn,
		SlowThreshold: 200 * time.Millisecond,
	})
)

// New builds an Interface writing to writer under config.
func New(writer Writer, config Config) Interface {
	if config.LogLevel == 0 {
		config.LogLevel = Warn
	}
	return &textLogger{
		writer: writer,
		config: config,
	}
}

// textLogger renders gorm-parity trace lines:
//
//	[0.123ms] [rows:1] SELECT * FROM users WHERE id = 1
//	SLOW SQL >= 200ms [0.300ms] [rows:1] SELECT ...
//	ERROR record not found [1.000ms] [rows:-1] SELECT ...
type textLogger struct {
	writer Writer
	config Config
}

func (l *textLogger) LogMode(level LogLevel) Interface {
	next := *l
	next.config.LogLevel = level
	return &next
}

func (l *textLogger) Info(_ context.Context, msg string, data ...any) {
	if l.config.LogLevel >= Info {
		l.writer.Printf("[info] "+msg+"\n", data...)
	}
}

func (l *textLogger) Warn(_ context.Context, msg string, data ...any) {
	if l.config.LogLevel >= Warn {
		l.writer.Printf("[warn] "+msg+"\n", data...)
	}
}

func (l *textLogger) Error(_ context.Context, msg string, data ...any) {
	if l.config.LogLevel >= Error {
		l.writer.Printf("[error] "+msg+"\n", data...)
	}
}

func (l *textLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.config.LogLevel <= Silent {
		return
	}

	elapsed := time.Since(begin)
	switch {
	case err != nil && l.config.LogLevel >= Error &&
		(!errors.Is(err, ErrRecordNotFound) || !l.config.IgnoreRecordNotFoundError):
		sql, rows := fc()
		l.writer.Printf("ERROR %s [%sms] [rows:%v] %s\n", err, ms(elapsed), rows, sql)
	case l.config.SlowThreshold != 0 && elapsed > l.config.SlowThreshold && l.config.LogLevel >= Warn:
		sql, rows := fc()
		l.writer.Printf("SLOW SQL >= %v [%sms] [rows:%v] %s\n", l.config.SlowThreshold, ms(elapsed), rows, sql)
	case l.config.LogLevel >= Info:
		sql, rows := fc()
		l.writer.Printf("[%sms] [rows:%v] %s\n", ms(elapsed), rows, sql)
	}
}

// ParamsFilter implements ParamsFilter. With ParameterizedQueries set the
// bound variables are dropped so the caller logs placeholder text only;
// otherwise the params pass through for interpolation.
func (l *textLogger) ParamsFilter(_ context.Context, sql string, params ...any) (string, []any) {
	if l.config.ParameterizedQueries {
		return sql, nil
	}
	return sql, params
}

// ms formats a duration as a fixed 3-decimal millisecond count ("0.123").
func ms(d time.Duration) string {
	return fmt.Sprintf("%.3f", float64(d.Nanoseconds())/1e6)
}
