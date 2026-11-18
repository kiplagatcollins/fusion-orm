package clause

// Join renders a JOIN clause. Relation joins set Table/Alias/On; raw joins
// set Raw, in which case the expression is rendered verbatim (with bound
// variables from Expr).
type Join struct {
	Type   string
	Table  string
	Alias  string
	On     []Expression
	Raw    Expression
	HasRaw bool
}

// Joins is a set of JOIN clauses; the registry stores one instance so
// multiple joins keep their position in the canonical build order.
type Joins struct {
	Items []Join
}

// Name implements Interface.
func (Joins) Name() string { return "join" }

// Build implements Interface.
func (j Joins) Build(b Builder) {
	for i, item := range j.Items {
		if i > 0 {
			b.AppendByte(' ')
		}
		item.Build(b)
	}
}

// Name implements Interface.
func (Join) Name() string { return "join" }

// Build implements Interface.
func (j Join) Build(b Builder) {
	if j.HasRaw {
		j.Raw.Build(b)
		return
	}
	if j.Type == "" {
		j.Type = "INNER"
	}
	b.WriteString(j.Type)
	b.AppendByte(' ')
	b.WriteString("JOIN ")
	b.WriteQuoted(j.Table)
	if j.Alias != "" {
		b.AppendByte(' ')
		b.WriteQuoted(j.Alias)
	}
	if len(j.On) > 0 {
		b.WriteString(" ON ")
		for i, expr := range j.On {
			if i > 0 {
				b.WriteString(" AND ")
			}
			expr.Build(b)
		}
	}
}
