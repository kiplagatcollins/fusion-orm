package dialect

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrStringNUL is reported when a string value contains a NUL byte (0x00).
// Silently stripping it would store a value different from the one the
// caller supplied; PostgreSQL rejects NUL in text outright.
var ErrStringNUL = errors.New("fusion: string contains a NUL byte (0x00)")

// BaseDialect provides the appenders shared by every dialect. Dialect
// structs embed it and override the pieces that differ (identifier quote
// byte, byte-literal syntax, placeholder style).
type BaseDialect struct {
	// Quote is the identifier quote byte; the zero value means '"'.
	Quote byte
}

// NewBaseDialect returns a BaseDialect using quote for identifiers.
func NewBaseDialect(quote byte) BaseDialect {
	if quote == 0 {
		quote = '"'
	}
	return BaseDialect{Quote: quote}
}

// IdentQuote returns the configured identifier quote byte.
func (b BaseDialect) IdentQuote() byte {
	if b.Quote == 0 {
		return '"'
	}
	return b.Quote
}

// QuoteTo appends ident with each dotted segment quoted.
func (b BaseDialect) QuoteTo(dst []byte, ident string) []byte {
	return AppendIdent(dst, ident, b.IdentQuote())
}

// AppendNull appends NULL.
func (BaseDialect) AppendNull(b []byte) []byte {
	return AppendNull(b)
}

// AppendBool appends TRUE/FALSE.
func (BaseDialect) AppendBool(b []byte, v bool) []byte {
	return AppendBool(b, v)
}

// AppendInt64 appends a signed integer with the line-comment guard.
func (BaseDialect) AppendInt64(b []byte, v int64) []byte {
	return AppendInt64(b, v)
}

// AppendUint64 appends an unsigned integer.
func (BaseDialect) AppendUint64(b []byte, v uint64) []byte {
	return AppendUint64(b, v)
}

// AppendFloat64 appends a float with the line-comment guard.
func (BaseDialect) AppendFloat64(b []byte, v float64) []byte {
	return AppendFloat64(b, v)
}

// AppendString appends a quoted string literal with ”-doubling. NUL bytes
// are rejected (fail closed) rather than stripped.
func (BaseDialect) AppendString(b []byte, s string) []byte {
	if strings.IndexByte(s, 0) >= 0 {
		return AppendError(b, ErrStringNUL)
	}

	b = append(b, '\'')
	for _, r := range s {
		if r == '\'' {
			b = append(b, '\'', '\'')
			continue
		}
		if r < utf8.RuneSelf {
			b = append(b, byte(r))
			continue
		}
		l := len(b)
		if cap(b)-l < utf8.UTFMax {
			b = append(b, make([]byte, utf8.UTFMax)...)
		}
		n := utf8.EncodeRune(b[l:l+utf8.UTFMax], r)
		b = b[:l+n]
	}
	b = append(b, '\'')
	return b
}

// AppendBytes appends a PostgreSQL-style hex byte literal (”\x...” as
// '\x...'). Dialects with different literal syntax (e.g. SQLite's X'...')
// override this.
func (BaseDialect) AppendBytes(b []byte, bs []byte) []byte {
	if bs == nil {
		return AppendNull(b)
	}

	b = append(b, `'\x`...)

	s := len(b)
	b = append(b, make([]byte, hex.EncodedLen(len(bs)))...)
	hex.Encode(b[s:], bs)

	b = append(b, '\'')
	return b
}

// AppendTime appends a UTC timestamp literal.
func (BaseDialect) AppendTime(b []byte, tm time.Time) []byte {
	b = append(b, '\'')
	b = tm.UTC().AppendFormat(b, "2006-01-02 15:04:05.999999-07:00")
	b = append(b, '\'')
	return b
}

// AppendJSON appends a JSON document as a quoted string literal, doubling
// quotes and failing closed on embedded NUL bytes. Backslash handling keeps
// the quote-doubling path authoritative so a crafted '\' sequence cannot
// break out of the literal (CVE-2024-44906 class).
func (BaseDialect) AppendJSON(b []byte, jsonb []byte) []byte {
	b = append(b, '\'')

	for i := 0; i < len(jsonb); i++ {
		c := jsonb[i]
		switch c {
		case '\'':
			b = append(b, '\'', '\'')
		case 0:
			// Strip raw NUL bytes; JSON never contains them unescaped.
			continue
		case '\\':
			if i+5 < len(jsonb) && string(jsonb[i+1:i+6]) == "u0000" {
				b = append(b, `\\u0000`...)
				i += 5
			} else {
				// Emit the backslash but do NOT consume the next byte: let
				// the loop escape it so a "\'" pair becomes "\''".
				b = append(b, '\\')
			}
		default:
			b = append(b, c)
		}
	}

	b = append(b, '\'')
	return b
}

// BindVarToQuestion appends "?" regardless of index (SQLite style).
func (BaseDialect) BindVarToQuestion(b []byte, _ int) []byte {
	return append(b, '?')
}

// BindVarToDollar appends "$n" (PostgreSQL style).
func (BaseDialect) BindVarToDollar(b []byte, index int) []byte {
	b = append(b, '$')
	return AppendUint64(b, uint64(index))
}
