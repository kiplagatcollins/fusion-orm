package dialect

import (
	"math"
	"strconv"
)

// AppendError embeds err into the SQL stream as "?!(<message>)". Callers
// render a formatting failure this way so the query fails at execution with
// the original cause visible in the SQL text, instead of silently producing
// a different value than the caller supplied.
func AppendError(b []byte, err error) []byte {
	b = append(b, "?!("...)
	b = append(b, err.Error()...)
	b = append(b, ')')
	return b
}

// AppendNull appends the NULL keyword.
func AppendNull(b []byte) []byte {
	return append(b, "NULL"...)
}

// AppendBool appends TRUE or FALSE.
func AppendBool(b []byte, v bool) []byte {
	if v {
		return append(b, "TRUE"...)
	}
	return append(b, "FALSE"...)
}

// AppendInt64 appends a signed integer, guarding against forming a "--"
// line comment when the buffer ends with '-' (CVE-2024-44906 class).
func AppendInt64(b []byte, v int64) []byte {
	return strconv.AppendInt(guardLineComment(b, v < 0), v, 10)
}

// AppendUint64 appends an unsigned integer.
func AppendUint64(b []byte, v uint64) []byte {
	return strconv.AppendUint(b, v, 10)
}

// AppendFloat32 appends a 32-bit float.
func AppendFloat32(b []byte, v float32) []byte {
	return appendFloat(guardLineComment(b, v < 0), float64(v), 32)
}

// AppendFloat64 appends a 64-bit float, guarding the line-comment case.
func AppendFloat64(b []byte, v float64) []byte {
	return appendFloat(guardLineComment(b, v < 0), v, 64)
}

func appendFloat(b []byte, num float64, bitSize int) []byte {
	switch {
	case math.IsNaN(num):
		return append(b, "'NaN'"...)
	case math.IsInf(num, 1):
		return append(b, "'Infinity'"...)
	case math.IsInf(num, -1):
		return append(b, "'-Infinity'"...)
	default:
		return strconv.AppendFloat(b, num, 'f', -1, bitSize)
	}
}

// guardLineComment inserts a space when a negative numeric literal would be
// appended immediately after a '-'. Without it, "col-?" formatted with -1
// becomes "col--1", where "--" starts a SQL line comment usable for
// injection. Mirrors bun's fix for CVE-2024-44906.
func guardLineComment(b []byte, negative bool) []byte {
	if negative && len(b) > 0 && b[len(b)-1] == '-' {
		return append(b, ' ')
	}
	return b
}

// AppendIdent appends a possibly dotted identifier with each segment quoted.
// "*" passes through unquoted (SELECT *), as does a bare trailing ".*";
// the empty identifier renders as the empty quoted name.
func AppendIdent(b []byte, name string, quote byte) []byte {
	if name == "" {
		return append(b, quote, quote)
	}
	var quoted bool
loop:
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch c {
		case '*':
			if !quoted {
				b = append(b, '*')
				continue loop
			}
		case '.':
			if quoted {
				b = append(b, quote)
				quoted = false
			}
			b = append(b, '.')
			continue loop
		}

		if !quoted {
			b = append(b, quote)
			quoted = true
		}
		if c == quote {
			b = append(b, quote, quote)
		} else {
			b = append(b, c)
		}
	}
	if quoted {
		b = append(b, quote)
	}
	return b
}
