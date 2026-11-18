// Package schema provides cached reflection metadata for models: tables,
// fields, relations and precompiled per-field closures. It depends on neither
// the session layer nor the query engine.
package schema

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/jinzhu/inflection"
)

// Tabler lets a model override its table name.
type Tabler interface {
	TableName() string
}

// Replacer rewrites model identifiers before naming (e.g. acronyms).
type Replacer interface {
	Replace(string) string
}

// Namer maps Go identifiers to database identifiers. NamingStrategy is the
// default implementation; each Tables cache is bound to one Namer.
type Namer interface {
	TableName(string) string
	ColumnName(table, column string) string
	JoinTableName(string) string
	FKName(table, column string) string
	IndexName(table, column string) string
	UniqueName(table, column string) string
}

var _ Namer = (*NamingStrategy)(nil)

// NamingStrategy derives pluralized snake_case database names, gorm-style.
type NamingStrategy struct {
	TablePrefix         string
	SingularTable       bool
	NameReplacer        Replacer
	NoLowerCase         bool
	IdentifierMaxLength int
}

// TableName pluralizes model unless SingularTable is set.
func (ns NamingStrategy) TableName(model string) string {
	if ns.SingularTable {
		return ns.TablePrefix + ns.toDBName(model)
	}
	return ns.TablePrefix + inflection.Plural(ns.toDBName(model))
}

// ColumnName converts a Go field name to a column name.
func (ns NamingStrategy) ColumnName(_, column string) string {
	return ns.toDBName(column)
}

// JoinTableName names an explicit or inferred many-to-many junction table.
func (ns NamingStrategy) JoinTableName(name string) string {
	if !ns.NoLowerCase && strings.ToLower(name) == name {
		return ns.TablePrefix + name
	}
	if ns.SingularTable {
		return ns.TablePrefix + ns.toDBName(name)
	}
	return ns.TablePrefix + inflection.Plural(ns.toDBName(name))
}

// FKName names a foreign-key constraint.
func (ns NamingStrategy) FKName(table, column string) string {
	return ns.formatName("fk", table, column)
}

// IndexName names a non-unique index.
func (ns NamingStrategy) IndexName(table, column string) string {
	return ns.formatName("idx", table, column)
}

// UniqueName names a unique index or constraint.
func (ns NamingStrategy) UniqueName(table, column string) string {
	return ns.formatName("uni", table, column)
}

func (ns NamingStrategy) formatName(prefix, table, name string) string {
	formatted := strings.ReplaceAll(prefix+"_"+table+"_"+ns.toDBName(name), ".", "_")
	limit := ns.IdentifierMaxLength
	if limit == 0 {
		limit = 64
	}
	if utf8.RuneCountInString(formatted) <= limit {
		return formatted
	}
	sum := sha1.Sum([]byte(formatted))
	return formatted[:limit-9] + "_" + hex.EncodeToString(sum[:])[:8]
}

var commonInitialisms = []string{
	"API", "ASCII", "CPU", "CSS", "DNS", "EOF", "GUID", "HTML", "HTTP",
	"HTTPS", "ID", "IP", "JSON", "LHS", "QPS", "RAM", "RHS", "RPC", "SLA",
	"SMTP", "SSH", "TLS", "TTL", "UID", "UI", "UUID", "URI", "URL", "UTF8",
	"VM", "XML", "XSRF", "XSS",
}

func titleInitialism(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

var initialismReplacer = func() *strings.Replacer {
	pairs := make([]string, 0, len(commonInitialisms)*2)
	for _, s := range commonInitialisms {
		pairs = append(pairs, s, titleInitialism(s))
	}
	return strings.NewReplacer(pairs...)
}()

// toDBName converts CamelCase (including common initialisms) to snake_case.
func (ns NamingStrategy) toDBName(name string) string {
	if name == "" {
		return ""
	}
	if ns.NameReplacer != nil {
		name = ns.NameReplacer.Replace(name)
	}
	if ns.NoLowerCase {
		return name
	}
	name = initialismReplacer.Replace(name)

	var buf strings.Builder
	buf.Grow(len(name) + 8)
	lastUpper := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			if i > 0 && !lastUpper && name[i-1] != '_' {
				buf.WriteByte('_')
			}
			buf.WriteByte(c + ('a' - 'A'))
			lastUpper = true
		} else {
			buf.WriteByte(c)
			lastUpper = false
		}
	}
	return buf.String()
}
