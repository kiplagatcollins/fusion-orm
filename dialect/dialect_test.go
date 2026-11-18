package dialect_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
)

// dialects returns one instance of each shipped dialector. Open never
// touches the network, so this is safe without a live database.
func dialects() []dialect.Dialector {
	return []dialect.Dialector{
		sqlite.Open("file:test?mode=memory"),
		postgres.Open("postgres://user@localhost/db"),
	}
}

// TestGoldenQuotes pins identifier quoting per dialect — these outputs are
// the SQL reference for the clause builders (M3). A failing case is a
// design change: discuss before editing.
func TestGoldenQuotes(t *testing.T) {
	tests := []struct {
		name  string
		ident string
		want  string
	}{
		{"simple", "users", `"users"`},
		{"dotted", "public.users", `"public"."users"`},
		{"star", "*", "*"},
		{"table-star", "users.*", `"users".*`},
		{"quote-escape", `weird"name`, `"weird""name"`},
		{"mixed-case", "UserID", `"UserID"`},
		{"empty", "", `""`},
		{"numbered", "t1", `"t1"`},
	}

	for _, d := range dialects() {
		for _, tc := range tests {
			t.Run(string(d.Name())+"/"+tc.name, func(t *testing.T) {
				got := string(d.QuoteTo(nil, tc.ident))
				if got != tc.want {
					t.Errorf("QuoteTo(%q) = %q, want %q", tc.ident, got, tc.want)
				}
			})
		}
	}
}

func TestBindVarTo(t *testing.T) {
	tests := []struct {
		d     dialect.Dialector
		index int
		want  string
	}{
		{sqlite.Open("x"), 1, "?"},
		{sqlite.Open("x"), 12, "?"},
		{postgres.Open("x"), 1, "$1"},
		{postgres.Open("x"), 12, "$12"},
	}
	for _, tc := range tests {
		got := string(tc.d.BindVarTo(nil, tc.index))
		if got != tc.want {
			t.Errorf("%s BindVarTo(%d) = %q, want %q", tc.d.Name(), tc.index, got, tc.want)
		}
	}
}

func TestAppendStringGolden(t *testing.T) {
	for _, d := range dialects() {
		tests := []struct {
			in   string
			want string
		}{
			{"plain", `'plain'`},
			{"it's", `'it''s'`},
			{"", `''`},
			{"line\nbreak", "'line\nbreak'"},
			{"héllo", "'héllo'"},
		}
		for _, tc := range tests {
			got := string(d.AppendString(nil, tc.in))
			if got != tc.want {
				t.Errorf("%s AppendString(%q) = %q, want %q", d.Name(), tc.in, got, tc.want)
			}
		}
	}
}

// TestAppendStringRejectsNUL: NUL bytes must fail closed (never silently
// stripped) — PostgreSQL rejects NUL in text outright, and stripping would
// store a different value than the caller supplied.
func TestAppendStringRejectsNUL(t *testing.T) {
	for _, d := range dialects() {
		got := string(d.AppendString(nil, "a\x00b"))
		want := `?!(fusion: string contains a NUL byte (0x00))`
		if got != want {
			t.Errorf("%s AppendString with NUL = %q, want %q", d.Name(), got, want)
		}
	}
}

// TestLineCommentGuard: a negative literal rendered directly after '-' would
// form "--", a SQL line comment (CVE-2024-44906 class).
func TestLineCommentGuard(t *testing.T) {
	for _, d := range dialects() {
		got := string(d.AppendInt64([]byte("col-"), -1))
		if got != "col- -1" {
			t.Errorf("%s AppendInt64 after '-' = %q, want %q", d.Name(), got, "col- -1")
		}

		// No guard when the buffer does not end with '-'.
		got = string(d.AppendInt64([]byte("x = "), -1))
		if got != "x = -1" {
			t.Errorf("%s AppendInt64 normal = %q", d.Name(), got)
		}

		got = string(d.AppendFloat64([]byte("col-"), -2.5))
		if got != "col- -2.5" {
			t.Errorf("%s AppendFloat64 after '-' = %q", d.Name(), got)
		}

		// Positive values never trigger the guard.
		got = string(d.AppendInt64([]byte("col-"), 1))
		if got != "col-1" {
			t.Errorf("%s AppendInt64 positive = %q", d.Name(), got)
		}
	}
}

func TestAppendJSON(t *testing.T) {
	for _, d := range dialects() {
		tests := []struct {
			name string
			in   string
			want string
		}{
			{"plain", `{"a":1}`, `'{"a":1}'`},
			{"quote-doubled", `{"a":"it's"}`, `'{"a":"it''s"}'`},
			{"escaped-quote", `{"a":"x\"y"}`, `'{"a":"x\"y"}'`},
			{"u0000-kept", `{"a":"\u0000"}`, `'{"a":"\\u0000"}'`},
			{"raw-nul-stripped", "{\"a\":\"\x00\"}", `'{"a":""}'`},
		}
		for _, tc := range tests {
			got := string(d.AppendJSON(nil, []byte(tc.in)))
			if got != tc.want {
				t.Errorf("%s AppendJSON(%s) = %q, want %q", d.Name(), tc.name, got, tc.want)
			}
		}
	}
}

func TestAppendBytesPerDialect(t *testing.T) {
	in := []byte{0xde, 0xad}

	if got := string(sqlite.Open("x").AppendBytes(nil, in)); got != `X'dead'` {
		t.Errorf("sqlite AppendBytes = %q", got)
	}
	if got := string(postgres.Open("x").AppendBytes(nil, in)); got != `'\xdead'` {
		t.Errorf("postgres AppendBytes = %q", got)
	}
	for _, d := range dialects() {
		if got := string(d.AppendBytes(nil, nil)); got != "NULL" {
			t.Errorf("%s AppendBytes(nil) = %q, want NULL", d.Name(), got)
		}
	}
}

func TestAppendTimeUTC(t *testing.T) {
	tm := time.Date(2026, 11, 17, 15, 4, 5, 123456000, time.UTC)
	want := `'2026-11-17 15:04:05.123456+00:00'`
	for _, d := range dialects() {
		if got := string(d.AppendTime(nil, tm)); got != want {
			t.Errorf("%s AppendTime = %q, want %q", d.Name(), got, want)
		}
	}
}

func TestExplainQuestionPlaceholders(t *testing.T) {
	tests := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{
			name:  "string-escape",
			query: "SELECT * FROM users WHERE name = ?",
			args:  []any{"O'Brien"},
			want:  "SELECT * FROM users WHERE name = 'O''Brien'",
		},
		{
			name:  "negative-after-minus",
			query: "SELECT * FROM t WHERE x-?",
			args:  []any{-1},
			want:  "SELECT * FROM t WHERE x- -1",
		},
		{
			name:  "nil-is-null",
			query: "INSERT INTO t VALUES (?)",
			args:  []any{nil},
			want:  "INSERT INTO t VALUES (NULL)",
		},
		{
			name:  "int-bool",
			query: "SELECT ? , ?",
			args:  []any{7, true},
			want:  "SELECT 7 , TRUE",
		},
		{
			name:  "missing-arg-keeps-placeholder",
			query: "SELECT ?, ?",
			args:  []any{1},
			want:  "SELECT 1, ?",
		},
		{
			name:  "question-inside-quotes-untouched",
			query: "SELECT '?' FROM t WHERE a = ?",
			args:  []any{5},
			want:  "SELECT '?' FROM t WHERE a = 5",
		},
		{
			name: "doubled-quotes",
			// "it''s?" contains a placeholder-looking ? after the closing
			// quote — it must still be interpolated, while the literal is
			// preserved verbatim.
			query: "SELECT 'it''s' FROM t WHERE a = ?",
			args:  []any{"x"},
			want:  "SELECT 'it''s' FROM t WHERE a = 'x'",
		},
		{
			name:  "no-vars-unchanged",
			query: "SELECT 1",
			args:  nil,
			want:  "SELECT 1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dialect.Explain(sqlite.Open("x"), tc.query, tc.args...)
			if got != tc.want {
				t.Errorf("Explain = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExplainDollarPlaceholders(t *testing.T) {
	tests := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{
			name:  "indexed",
			query: "SELECT * FROM users WHERE id = $1 AND name = $2",
			args:  []any{1, "bob"},
			want:  "SELECT * FROM users WHERE id = 1 AND name = 'bob'",
		},
		{
			name:  "out-of-order",
			query: "SELECT $2, $1",
			args:  []any{"a", "b"},
			want:  "SELECT 'b', 'a'",
		},
		{
			name:  "missing-index-kept",
			query: "SELECT $1, $9",
			args:  []any{"a"},
			want:  "SELECT 'a', $9",
		},
		{
			name:  "dollar-inside-string",
			query: "SELECT '$1' FROM t WHERE a = $1",
			args:  []any{3},
			want:  "SELECT '$1' FROM t WHERE a = 3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dialect.Explain(postgres.Open("x"), tc.query, tc.args...)
			if got != tc.want {
				t.Errorf("Explain = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExplainTimeAndBytes(t *testing.T) {
	tm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := dialect.Explain(postgres.Open("x"), "INSERT INTO t VALUES ($1, $2)",
		tm, []byte{0xab})
	want := "INSERT INTO t VALUES ('2026-01-02 03:04:05+00:00', '\\xab')"
	if got != want {
		t.Errorf("Explain = %q, want %q", got, want)
	}
}

func TestSavepointStatements(t *testing.T) {
	pg := postgres.Open("x")
	b := dialect.AppendSavepoint(nil, pg, "sp1")
	if got := string(b); got != `SAVEPOINT "sp1"` {
		t.Errorf("AppendSavepoint = %q", got)
	}
	b = dialect.AppendRollbackTo(nil, pg, "sp1")
	if got := string(b); got != `ROLLBACK TO SAVEPOINT "sp1"` {
		t.Errorf("AppendRollbackTo = %q", got)
	}
	b = dialect.AppendReleaseSavepoint(nil, pg, "sp1")
	if got := string(b); got != `RELEASE SAVEPOINT "sp1"` {
		t.Errorf("AppendReleaseSavepoint = %q", got)
	}

	// Quoting must survive hostile names at the dialect layer (the session
	// layer additionally validates them).
	b = dialect.AppendSavepoint(nil, pg, `we"ird`)
	if !strings.Contains(string(b), `"we""ird"`) {
		t.Errorf("AppendSavepoint did not quote name: %q", b)
	}
}
