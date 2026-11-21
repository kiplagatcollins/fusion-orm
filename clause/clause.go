// Package clause contains pure SQL clause builders. Builders never perform
// I/O and never import the schema or session layers; they only append bytes
// through Builder, which binds vars and quotes identifiers via the dialect.
package clause

import (
	"strings"

	"github.com/kiplagatcollins/fusion-orm/dialect"
)

// Interface is a named, orderable SQL clause.
type Interface interface {
	Name() string
	Build(Builder)
}

// Builder is the append/bind surface given to clauses.
type Builder interface {
	WriteString(string)
	AppendByte(byte)
	WriteQuoted(string)
	AddVar(v any)
	Dialector() dialect.Dialector
}

// Expression is a SQL fragment with positional ? placeholders and vars.
type Expression struct {
	SQL  string
	Vars []any
}

// Expr creates an expression. Each ? in sql consumes the next var.
func Expr(sql string, vars ...any) Expression {
	return Expression{SQL: sql, Vars: vars}
}

// Build appends the expression, replacing ? with dialect placeholders.
func (e Expression) Build(b Builder) {
	parts := strings.Split(e.SQL, "?")
	for i, part := range parts {
		b.WriteString(part)
		if i < len(e.Vars) && i < len(parts)-1 {
			b.AddVar(e.Vars[i])
		}
	}
}

// Condition is one joined group of a WHERE clause.
type Condition struct {
	Join string // "AND" (default) or "OR"
	// Conds renders a parenthesized nested group — used to keep OR groups
	// intact when another predicate is appended after them. When set, Exprs
	// is ignored.
	Conds []Condition
	Exprs []Expression
}

// Build appends the condition: a nested Conds group as "(...)", otherwise
// Exprs joined by the condition's own Join (AND by default).
func (c Condition) Build(b Builder) {
	if len(c.Conds) > 0 {
		b.AppendByte('(')
		buildConditions(b, c.Conds)
		b.AppendByte(')')
		return
	}
	join := c.Join
	if join == "" {
		join = "AND"
	}
	for i, expr := range c.Exprs {
		if i > 0 {
			b.WriteString(" ")
			b.WriteString(join)
			b.WriteString(" ")
		}
		expr.Build(b)
	}
}
