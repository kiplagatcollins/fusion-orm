package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// bufWriter captures Printf output for assertions.
type bufWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *bufWriter) Printf(format string, v ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.buf.WriteString(fmt.Sprintf(format, v...))
}

func (w *bufWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestLogLevelGating(t *testing.T) {
	tests := []struct {
		level     LogLevel
		wantInfo  bool
		wantWarn  bool
		wantError bool
	}{
		{Silent, false, false, false},
		{Error, false, false, true},
		{Warn, false, true, true},
		{Info, true, true, true},
	}
	for _, tc := range tests {
		w := &bufWriter{}
		l := New(w, Config{LogLevel: tc.level})
		l.Info(context.Background(), "info-msg")
		l.Warn(context.Background(), "warn-msg")
		l.Error(context.Background(), "error-msg")
		got := w.String()
		if contains := strings.Contains(got, "info-msg"); contains != tc.wantInfo {
			t.Errorf("level %d Info printed = %v, want %v", tc.level, contains, tc.wantInfo)
		}
		if contains := strings.Contains(got, "warn-msg"); contains != tc.wantWarn {
			t.Errorf("level %d Warn printed = %v, want %v", tc.level, contains, tc.wantWarn)
		}
		if contains := strings.Contains(got, "error-msg"); contains != tc.wantError {
			t.Errorf("level %d Error printed = %v, want %v", tc.level, contains, tc.wantError)
		}
	}
}

func TestLogModeReturnsCopy(t *testing.T) {
	w := &bufWriter{}
	l := New(w, Config{LogLevel: Silent})
	quiet := l.LogMode(Error)
	l.Info(context.Background(), "should-not-print")
	quiet.Error(context.Background(), "should-print")

	got := w.String()
	if strings.Contains(got, "should-not-print") {
		t.Error("original logger was mutated by LogMode")
	}
	if !strings.Contains(got, "should-print") {
		t.Error("LogMode(Error) did not enable Error output")
	}
}

func TestTraceFormat(t *testing.T) {
	w := &bufWriter{}
	l := New(w, Config{LogLevel: Info})

	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 1
	}, nil)

	got := w.String()
	want := regexp.MustCompile(`^\[\d+\.\d{3}ms\] \[rows:1\] SELECT 1\n$`)
	if !want.MatchString(got) {
		t.Fatalf("trace line = %q, want gorm-parity format", got)
	}
}

func TestTraceSlowThreshold(t *testing.T) {
	w := &bufWriter{}
	l := New(w, Config{LogLevel: Warn, SlowThreshold: time.Nanosecond})

	begin := time.Now().Add(-time.Hour)
	l.Trace(context.Background(), begin, func() (string, int64) {
		return "SELECT slow", -1
	}, nil)

	got := w.String()
	if !strings.HasPrefix(got, "SLOW SQL >= 1ns ") {
		t.Fatalf("slow trace = %q", got)
	}
	if !strings.Contains(got, "[rows:-1] SELECT slow") {
		t.Fatalf("slow trace missing rows/sql: %q", got)
	}
}

func TestTraceError(t *testing.T) {
	w := &bufWriter{}
	l := New(w, Config{LogLevel: Error})

	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT boom", -1
	}, errors.New("boom"))

	got := w.String()
	if !strings.HasPrefix(got, "ERROR boom ") || !strings.Contains(got, "SELECT boom") {
		t.Fatalf("error trace = %q", got)
	}
}

func TestTraceIgnoresRecordNotFoundWhenConfigured(t *testing.T) {
	w := &bufWriter{}
	l := New(w, Config{LogLevel: Error, IgnoreRecordNotFoundError: true})

	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 0
	}, ErrRecordNotFound)
	if got := w.String(); got != "" {
		t.Fatalf("expected no output, got %q", got)
	}

	// Without the flag the same trace is printed.
	l2 := New(w, Config{LogLevel: Error})
	l2.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 0
	}, ErrRecordNotFound)
	if got := w.String(); !strings.Contains(got, "record not found") {
		t.Fatalf("expected record-not-found trace, got %q", got)
	}
}

func TestTraceSilent(t *testing.T) {
	w := &bufWriter{}
	l := New(w, Config{LogLevel: Silent})
	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", 1
	}, errors.New("x"))
	if got := w.String(); got != "" {
		t.Fatalf("silent logger printed %q", got)
	}
}

func TestParamsFilter(t *testing.T) {
	sql := "SELECT * FROM users WHERE id = ?"
	params := []any{42}

	l, ok := New(&bufWriter{}, Config{LogLevel: Info}).(ParamsFilter)
	if !ok {
		t.Fatal("text logger must implement ParamsFilter")
	}
	gotSQL, gotParams := l.ParamsFilter(context.Background(), sql, params...)
	if gotSQL != sql {
		t.Errorf("sql = %q", gotSQL)
	}
	if len(gotParams) != 1 {
		t.Errorf("params = %v, want them preserved", gotParams)
	}

	par := New(&bufWriter{}, Config{LogLevel: Info, ParameterizedQueries: true}).(ParamsFilter)
	gotSQL, gotParams = par.ParamsFilter(context.Background(), sql, params...)
	if gotSQL != sql || gotParams != nil {
		t.Errorf("parameterized filter = (%q, %v), want (%q, nil)", gotSQL, gotParams, sql)
	}
}

func TestSlogTrace(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := NewSlogLogger(slog.New(h), Config{LogLevel: Info})

	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 42", 1
	}, nil)

	got := buf.String()
	for _, want := range []string{"SQL executed", "SELECT 42", "rows=1", "duration="} {
		if !strings.Contains(got, want) {
			t.Errorf("slog output missing %q: %s", want, got)
		}
	}
}

func TestSlogTraceError(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	l := NewSlogLogger(slog.New(h), Config{LogLevel: Info})

	l.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "SELECT 1", -1
	}, errors.New("kaboom"))

	got := buf.String()
	if !strings.Contains(got, "level=ERROR") || !strings.Contains(got, "error=kaboom") {
		t.Errorf("slog error trace = %s", got)
	}
}

func TestSlogLogModeCopy(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	base := NewSlogLogger(slog.New(h), Config{LogLevel: Silent})

	base.Info(context.Background(), "hidden")
	base.LogMode(Info).Info(context.Background(), "shown")

	got := buf.String()
	if strings.Contains(got, "hidden") {
		t.Error("original slog logger mutated by LogMode")
	}
	if !strings.Contains(got, "shown") {
		t.Error("LogMode(Info) did not enable output")
	}
}
