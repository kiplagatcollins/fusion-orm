package clause

import "github.com/kiplagatcollins/fusion-orm/dialect/feature"

// Insert renders INSERT INTO table (columns).
type Insert struct {
	Table   string
	Columns []string
}

// Name implements Interface.
func (Insert) Name() string { return "insert" }

// Build implements Interface.
func (i Insert) Build(b Builder) {
	b.WriteString("INSERT INTO ")
	b.WriteQuoted(i.Table)
	if len(i.Columns) > 0 {
		b.WriteString(" (")
		for n, col := range i.Columns {
			if n > 0 {
				b.WriteString(", ")
			}
			b.WriteQuoted(col)
		}
		b.AppendByte(')')
	}
}

// Values renders VALUES (...), (...).
type Values struct {
	Rows []Expression
}

// Name implements Interface.
func (Values) Name() string { return "values" }

// Build implements Interface.
func (v Values) Build(b Builder) {
	if len(v.Rows) == 0 {
		return
	}
	b.WriteString("VALUES ")
	for i := range v.Rows {
		if i > 0 {
			b.WriteString(", ")
		}
		b.AppendByte('(')
		v.Rows[i].Build(b)
		b.AppendByte(')')
	}
}

// Assignment is one SET pair.
type Assignment struct {
	Column string
	Value  Expression
}

// Set renders SET col = expr, ...
type Set struct {
	Assignments []Assignment
}

// Name implements Interface.
func (Set) Name() string { return "set" }

// Build implements Interface.
func (s Set) Build(b Builder) {
	if len(s.Assignments) == 0 {
		return
	}
	b.WriteString("SET ")
	for i, a := range s.Assignments {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteQuoted(a.Column)
		b.WriteString(" = ")
		a.Value.Build(b)
	}
}

// OnConflict renders ON CONFLICT (cols) DO NOTHING / DO UPDATE SET ...
type OnConflict struct {
	Columns       []string
	DoNothing     bool
	UpdateColumns []string
}

// Name implements Interface.
func (OnConflict) Name() string { return "on_conflict" }

// Build implements Interface.
func (o OnConflict) Build(b Builder) {
	if !b.Dialector().Features().Has(feature.InsertOnConflict) {
		return
	}
	b.WriteString("ON CONFLICT (")
	for i, col := range o.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteQuoted(col)
	}
	if o.DoNothing {
		b.WriteString(") DO NOTHING")
		return
	}
	b.WriteString(") DO UPDATE SET ")
	for i, col := range o.UpdateColumns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteQuoted(col)
		b.WriteString(" = EXCLUDED.")
		b.WriteQuoted(col)
	}
}

// Returning renders RETURNING columns on dialects with the feature.
type Returning struct {
	Columns []string
}

// Name implements Interface.
func (Returning) Name() string { return "returning" }

// Build implements Interface.
func (r Returning) Build(b Builder) {
	if len(r.Columns) == 0 || !b.Dialector().Features().Has(feature.Returning) {
		return
	}
	b.WriteString("RETURNING ")
	for i, col := range r.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteQuoted(col)
	}
}

// Update renders UPDATE table.
type Update struct {
	Table string
}

// Name implements Interface.
func (Update) Name() string { return "update" }

// Build implements Interface.
func (u Update) Build(b Builder) {
	b.WriteString("UPDATE ")
	b.WriteQuoted(u.Table)
}

// Delete renders DELETE FROM table.
type Delete struct {
	Table string
}

// Name implements Interface.
func (Delete) Name() string { return "delete" }

// Build implements Interface.
func (d Delete) Build(b Builder) {
	b.WriteString("DELETE FROM ")
	b.WriteQuoted(d.Table)
}
