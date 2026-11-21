package clause

import "strings"

// Select renders SELECT [DISTINCT] columns.
type Select struct {
	Columns  []string
	Distinct bool
}

// Name implements Interface.
func (Select) Name() string { return "select" }

// Build implements Interface.
func (s Select) Build(b Builder) {
	b.WriteString("SELECT ")
	if s.Distinct {
		b.WriteString("DISTINCT ")
	}
	if len(s.Columns) == 0 {
		b.AppendByte('*')
		return
	}
	for i, col := range s.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		if strings.ContainsAny(col, "( )") {
			b.WriteString(col)
		} else {
			b.WriteQuoted(col)
		}
	}
}

// From renders FROM table.
type From struct {
	Table string
}

// Name implements Interface.
func (From) Name() string { return "from" }

// Build implements Interface.
func (f From) Build(b Builder) {
	b.WriteString("FROM ")
	b.WriteQuoted(f.Table)
}

// Where renders WHERE groups joined by AND/OR.
type Where struct {
	Conds []Condition
}

// Name implements Interface.
func (Where) Name() string { return "where" }

// Build implements Interface.
func (w Where) Build(b Builder) {
	if len(w.Conds) == 0 {
		return
	}
	b.WriteString("WHERE ")
	buildConditions(b, w.Conds)
}

// buildConditions renders conds, prefixing each with its join. A condition
// holding multiple expressions is parenthesized unless it is the only one,
// so a list reads left-to-right: appending a predicate to `a OR b` yields
// `(a OR b) AND x`, never `a OR b AND x`.
func buildConditions(b Builder, conds []Condition) {
	for i, cond := range conds {
		if i > 0 {
			join := cond.Join
			if join == "" {
				join = "AND"
			}
			b.AppendByte(' ')
			b.WriteString(join)
			b.AppendByte(' ')
		}
		if len(cond.Conds) == 0 && len(cond.Exprs) > 1 && (i > 0 || len(conds) > 1) {
			b.AppendByte('(')
			cond.Build(b)
			b.AppendByte(')')
			continue
		}
		cond.Build(b)
	}
}

// Or marks the next WHERE group as OR-joined.
func Or(exprs ...Expression) Condition { return Condition{Join: "OR", Exprs: exprs} }

// And marks the next WHERE group as AND-joined.
func And(exprs ...Expression) Condition { return Condition{Join: "AND", Exprs: exprs} }

// OrderBy renders ORDER BY columns (each may carry " DESC" / " ASC").
type OrderBy struct {
	Columns []string
}

// Name implements Interface.
func (OrderBy) Name() string { return "order" }

// Build implements Interface.
func (o OrderBy) Build(b Builder) {
	if len(o.Columns) == 0 {
		return
	}
	b.WriteString("ORDER BY ")
	for i, col := range o.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(col)
	}
}

// Limit renders LIMIT n.
type Limit struct {
	N int
}

// Name implements Interface.
func (Limit) Name() string { return "limit" }

// Build implements Interface.
func (l Limit) Build(b Builder) {
	if l.N < 0 {
		return
	}
	b.WriteString("LIMIT ")
	b.AddVar(l.N)
}

// Offset renders OFFSET n.
type Offset struct {
	N int
}

// Name implements Interface.
func (Offset) Name() string { return "offset" }

// Build implements Interface.
func (o Offset) Build(b Builder) {
	if o.N <= 0 {
		return
	}
	b.WriteString("OFFSET ")
	b.AddVar(o.N)
}

// Locking renders FOR UPDATE when enabled.
type Locking struct {
	Enabled bool
}

// Name implements Interface.
func (Locking) Name() string { return "locking" }

// Build implements Interface.
func (l Locking) Build(b Builder) {
	if l.Enabled {
		b.WriteString("FOR UPDATE")
	}
}

// With renders a WITH cte AS (subquery) prefix.
type With struct {
	CTEName  string
	Subquery string
}

// Name implements Interface.
func (With) Name() string { return "with" }

// Build implements Interface.
func (w With) Build(b Builder) {
	if w.CTEName == "" {
		return
	}
	b.WriteString("WITH ")
	b.WriteQuoted(w.CTEName)
	b.WriteString(" AS (")
	b.WriteString(w.Subquery)
	b.WriteString(") ")
}
