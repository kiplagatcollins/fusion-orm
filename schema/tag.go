package schema

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidTag reports an unknown or malformed fusion tag in strict mode.
var ErrInvalidTag = errors.New("schema: invalid fusion tag")

// Tag is a parsed fusion tag: an optional bare column name plus key/value
// options. Option values are case-preserving; keys are normalized to lower
// case with spaces removed.
type Tag struct {
	Name    string
	Options map[string]string
}

// Has reports whether the tag carries key.
func (t Tag) Has(key string) bool {
	_, ok := t.Options[normalizeKey(key)]
	return ok
}

// Get returns the value of key (empty when absent).
func (t Tag) Get(key string) string {
	v, _ := t.Lookup(key)
	return v
}

// Lookup returns the value of key and whether it was present.
func (t Tag) Lookup(key string) (string, bool) {
	v, ok := t.Options[normalizeKey(key)]
	return v, ok
}

func normalizeKey(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "")
}

var knownKeys = map[string]bool{
	"column": true, "type": true, "pk": true, "autoincrement": true,
	"notnull": true, "nullzero": true, "default": true, "index": true,
	"unique": true, "->": true, "<-": true, "-": true, "embed": true,
	"rel": true, "foreignkey": true, "references": true,
	"many2many": true, "polymorphic": true, "polymorphicvalue": true,
	"serializer": true, "json": true, "constraint": true,
	"ondelete": true, "onupdate": true,
}

// gormTagAliases documents the 1:1 migration from gorm:"..." keys.
var gormTagAliases = map[string]string{
	"primarykey":    "pk",
	"autoincrement": "autoincrement",
	"notnull":       "notnull",
	"uniqueindex":   "unique",
	"embedded":      "embed",
	"foreignkey":    "foreignkey",
	"references":    "references",
	"many2many":     "many2many",
	"polymorphic":   "polymorphic",
	"serializer":    "serializer",
	"column":        "column",
	"type":          "type",
	"default":       "default",
	"index":         "index",
	"size":          "type",
}

// ParseTag parses a fusion tag. Both "," and ";" separate tokens so gorm tags
// migrate unchanged. Unknown keys warn (when warn is non-nil) unless strict is
// set, in which case they return ErrInvalidTag.
func ParseTag(raw string, strict bool, warn func(string)) (Tag, error) {
	var tag Tag
	if raw == "" {
		return tag, nil
	}

	tokens := splitTokens(raw)
	for _, token := range tokens {
		key, value, hasValue := strings.Cut(token, ":")
		rawKey := key
		key = normalizeKey(key)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if key == "" {
			continue
		}
		if alias, ok := gormTagAliases[key]; ok {
			key = alias
		}
		if !knownKeys[key] {
			if !hasValue && tag.Name == "" {
				tag.Name = rawKey
				continue
			}
			if strict {
				return tag, fmt.Errorf("%w: unknown key %q", ErrInvalidTag, key)
			}
			if warn != nil {
				warn(key)
			}
			continue
		}
		if tag.Options == nil {
			tag.Options = make(map[string]string, 4)
		}
		tag.Options[key] = value
	}
	return tag, nil
}

func splitTokens(s string) []string {
	var out []string
	var cur strings.Builder
	var quoted byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted != 0:
			if c == quoted {
				quoted = 0
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quoted = c
			cur.WriteByte(c)
		case c == ',' || c == ';':
			if cur.Len() > 0 {
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}
