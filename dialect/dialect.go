// Package dialect defines the Dialector interface, shared SQL appenders,
// and the Explain formatter. It is the only package outside clause/ that may
// contain dialect-specific SQL strings.
package dialect

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
)

// Constraint sentinels are shared by dialect error translators and aliased by
// the root package so callers only need one import.
var (
	ErrDuplicatedKey           = errors.New("duplicated key not allowed")
	ErrForeignKeyViolated      = errors.New("violates foreign key constraint")
	ErrCheckConstraintViolated = errors.New("violates check constraint")
)

// Name identifies a SQL dialect.
type Name string

const (
	// SQLite is the pure-Go SQLite dialect.
	SQLite Name = "sqlite"
	// Postgres is the PostgreSQL dialect.
	Postgres Name = "postgres"
)

// Dialector is implemented by each supported database dialect.
//
// The value appenders (AppendString and friends) render literals; they must
// never string-concatenate user data — quoting and escaping only. AppendString
// rejects NUL bytes, and the numeric appenders guard against rendering a
// negative literal directly after '-' (which would form a "--" line comment,
// CVE-2024-44906 class).
type Dialector interface {
	// Name returns the dialect name.
	Name() Name
	// Features returns the capability bitset the dialect supports.
	Features() feature.Feature
	// Initialize opens and returns the underlying connection pool. The
	// caller owns the returned pool.
	Initialize() (*sql.DB, error)
	// IdentQuote returns the identifier quote byte (e.g. '"').
	IdentQuote() byte
	// QuoteTo appends ident, quoted for this dialect. Dotted paths are
	// quoted segment by segment and "*" passes through unquoted.
	QuoteTo(b []byte, ident string) []byte
	// BindVarTo appends the placeholder for the 1-based parameter index
	// ("?" or "$1").
	BindVarTo(b []byte, index int) []byte

	// Value appenders.
	AppendNull(b []byte) []byte
	AppendBool(b []byte, v bool) []byte
	AppendInt64(b []byte, v int64) []byte
	AppendUint64(b []byte, v uint64) []byte
	AppendFloat64(b []byte, v float64) []byte
	AppendString(b []byte, v string) []byte
	AppendBytes(b []byte, v []byte) []byte
	AppendTime(b []byte, v time.Time) []byte
	AppendJSON(b []byte, v []byte) []byte
}

// ErrorTranslator is an optional Dialector capability: it maps driver-level
// constraint errors onto the fusion sentinel errors
// (ErrDuplicatedKey / ErrForeignKeyViolated / ErrCheckConstraintViolated).
// Enabled via Config.TranslateError.
type ErrorTranslator interface {
	Translate(err error) error
}

// AppendSavepoint appends "SAVEPOINT <name>" with the name quoted through
// the dialector.
func AppendSavepoint(b []byte, d Dialector, name string) []byte {
	b = append(b, "SAVEPOINT "...)
	return d.QuoteTo(b, name)
}

// AppendRollbackTo appends "ROLLBACK TO SAVEPOINT <name>".
func AppendRollbackTo(b []byte, d Dialector, name string) []byte {
	b = append(b, "ROLLBACK TO SAVEPOINT "...)
	return d.QuoteTo(b, name)
}

// AppendReleaseSavepoint appends "RELEASE SAVEPOINT <name>".
func AppendReleaseSavepoint(b []byte, d Dialector, name string) []byte {
	b = append(b, "RELEASE SAVEPOINT "...)
	return d.QuoteTo(b, name)
}

// Explain interpolates vars into query for logging and ToSQL output. It
// understands both "?" and "$n" placeholders, skips quoted string and
// identifier sections, and renders values through the dialector's appenders
// (including the line-comment guard).
func Explain(d Dialector, query string, vars ...any) string {
	if len(vars) == 0 && !needsExplain(query) {
		return query
	}

	var (
		b      []byte
		argPos int
	)
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch c {
		case '\'', '"':
			// Copy the quoted section verbatim, honoring doubled quotes.
			j := i + 1
			for j < len(query) {
				if query[j] == c {
					if j+1 < len(query) && query[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(query) {
				j = len(query) - 1
			}
			b = append(b, query[i:j+1]...)
			i = j
		case '?':
			if argPos < len(vars) {
				b = appendValue(d, b, vars[argPos])
				argPos++
			} else {
				b = append(b, '?')
			}
		case '$':
			j := i + 1
			for j < len(query) && query[j] >= '0' && query[j] <= '9' {
				j++
			}
			if j > i+1 {
				idx, err := strconv.Atoi(query[i+1 : j])
				if err == nil && idx >= 1 && idx <= len(vars) {
					b = appendValue(d, b, vars[idx-1])
					i = j - 1
					continue
				}
			}
			b = append(b, '$')
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

// needsExplain reports whether query contains any placeholder at all.
func needsExplain(query string) bool {
	return strings.ContainsAny(query, "?$")
}

// appendValue renders a single bound variable through the dialector.
func appendValue(d Dialector, b []byte, v any) []byte {
	switch v := v.(type) {
	case nil:
		return d.AppendNull(b)
	case bool:
		return d.AppendBool(b, v)
	case int:
		return d.AppendInt64(b, int64(v))
	case int8:
		return d.AppendInt64(b, int64(v))
	case int16:
		return d.AppendInt64(b, int64(v))
	case int32:
		return d.AppendInt64(b, int64(v))
	case int64:
		return d.AppendInt64(b, v)
	case uint:
		return d.AppendUint64(b, uint64(v))
	case uint8:
		return d.AppendUint64(b, uint64(v))
	case uint16:
		return d.AppendUint64(b, uint64(v))
	case uint32:
		return d.AppendUint64(b, uint64(v))
	case uint64:
		return d.AppendUint64(b, v)
	case float32:
		return d.AppendFloat64(b, float64(v))
	case float64:
		return d.AppendFloat64(b, v)
	case string:
		return d.AppendString(b, v)
	case []byte:
		return d.AppendBytes(b, v)
	case time.Time:
		return d.AppendTime(b, v)
	case fmt.Stringer:
		return d.AppendString(b, v.String())
	default:
		return d.AppendString(b, fmt.Sprint(v))
	}
}
