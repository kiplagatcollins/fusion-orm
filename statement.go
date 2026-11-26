package fusion

import (
	"context"
	"sync"

	"github.com/kiplagatcollins/fusion-orm/clause"
	"github.com/kiplagatcollins/fusion-orm/dialect"
)

// Statement carries per-chain state: clauses, model/destination, vars, the
// connection pool, execution context, SQL captured for raw/DryRun execution,
// and the plugin settings store. A Statement belongs to exactly one chain
// instance; chainable methods clone it before mutating.
type Statement struct {
	// DB is the chain instance that owns this statement.
	DB *DB
	// ConnPool is the pool queries run against: the root pool, or the
	// current transaction once Begin has swapped it in.
	ConnPool ConnPool
	// Context is propagated to every I/O call made through this statement.
	Context context.Context
	// SQL holds the assembled or raw statement text.
	SQL []byte
	// Vars holds the bound variables for SQL.
	Vars []any
	// Settings is a per-statement key/value store for plugins. It is
	// copied entry-by-entry on clone so chains never share settings.
	Settings *sync.Map

	// Model is the struct/slice used to derive table and columns.
	Model any
	// Dest is the destination passed to a finisher.
	Dest any
	// Table overrides the model-derived table name.
	Table string
	// slots holds the standard clauses indexed by canonical build order
	// (slotIndex); slotMask records which are present. Storing them inline
	// makes Add/Remove/Clause and clone allocation-free for the hot names.
	slots    [numSlots]clause.Interface
	slotMask uint32
	// Clauses holds custom (non-standard) clause builders by name,
	// allocated only when a plugin or Clauses() call registers one.
	Clauses map[string]clause.Interface
	// Order is the insertion order of custom clauses only.
	Order []string

	// RaiseErrorOnNotFound makes First/Take/Last return ErrRecordNotFound.
	RaiseErrorOnNotFound bool
	// SkipHooks suppresses model hooks for this statement.
	SkipHooks bool
	// SkipTimestamps suppresses automatic UpdatedAt maintenance
	// (UpdateColumn/UpdateColumns).
	SkipTimestamps bool
	// CreateBatchSize bounds rows per INSERT statement (0 = all rows).
	CreateBatchSize int
	// Preloads holds requested relation paths; each path is loaded in one
	// batched query, with nested paths loaded after their parent.
	Preloads []*preloadEntry
	// Joins holds explicit raw joins and relation names to LEFT JOIN.
	Joins []joinEntry
}

func statementContext(stmt *Statement) context.Context {
	if stmt != nil && stmt.Context != nil {
		return stmt.Context
	}
	return context.TODO()
}

// slotNames is the canonical clause build order. Index i in this array is
// the slot index for that clause; custom clauses follow after the slots.
var slotNames = [...]string{
	"with", "select", "insert", "update", "delete", "from", "join", "set", "values",
	"on_conflict", "where", "order", "limit", "offset", "locking", "returning",
}

const numSlots = len(slotNames)

// slotIndex maps a standard clause name to its slot. The switch is lowered
// by the compiler to a binary search, cheaper than a map probe. The
// statement_slots_match_order test guards the index constants against
// slotNames drift.
func slotIndex(name string) (uint, bool) {
	switch name {
	case "with":
		return 0, true
	case "select":
		return 1, true
	case "insert":
		return 2, true
	case "update":
		return 3, true
	case "delete":
		return 4, true
	case "from":
		return 5, true
	case "join":
		return 6, true
	case "set":
		return 7, true
	case "values":
		return 8, true
	case "on_conflict":
		return 9, true
	case "where":
		return 10, true
	case "order":
		return 11, true
	case "limit":
		return 12, true
	case "offset":
		return 13, true
	case "locking":
		return 14, true
	case "returning":
		return 15, true
	}
	return 0, false
}

// AddClause registers or replaces c. Standard clause names land in fixed
// slots (no allocation); custom names use the fallback map and keep their
// first-seen build order after the standard clauses.
func (stmt *Statement) AddClause(c clause.Interface) {
	name := c.Name()
	if i, ok := slotIndex(name); ok {
		stmt.slotMask |= 1 << i
		stmt.slots[i] = c
		return
	}
	if stmt.Clauses == nil {
		stmt.Clauses = make(map[string]clause.Interface, 4)
	}
	if _, exists := stmt.Clauses[name]; !exists {
		stmt.Order = append(stmt.Order, name)
	}
	stmt.Clauses[name] = c
}

// RemoveClause drops the named clause from the statement.
func (stmt *Statement) RemoveClause(name string) {
	if i, ok := slotIndex(name); ok {
		stmt.slotMask &^= 1 << i
		stmt.slots[i] = nil
		return
	}
	delete(stmt.Clauses, name)
	for i, n := range stmt.Order {
		if n == name {
			stmt.Order = append(stmt.Order[:i], stmt.Order[i+1:]...)
			return
		}
	}
}

// Clause returns the named clause, if present.
func (stmt *Statement) Clause(name string) clause.Interface {
	if i, ok := slotIndex(name); ok {
		if stmt.slotMask&(1<<i) == 0 {
			return nil
		}
		return stmt.slots[i]
	}
	return stmt.Clauses[name]
}

// QuoteTo quotes an identifier through the active dialect.
func (stmt *Statement) QuoteTo(ident string) string {
	return string(stmt.dialect().QuoteTo(nil, ident))
}

// Set stores a plugin/step value on the statement settings.
func (stmt *Statement) Set(key string, value any) {
	if stmt.Settings == nil {
		stmt.Settings = &sync.Map{}
	}
	stmt.Settings.Store(key, value)
}

// Get retrieves a statement setting.
func (stmt *Statement) Get(key string) (any, bool) {
	if stmt.Settings == nil {
		return nil, false
	}
	return stmt.Settings.Load(key)
}

// Delete removes a statement setting.
func (stmt *Statement) Delete(key string) {
	if stmt.Settings != nil {
		stmt.Settings.Delete(key)
	}
}

func (stmt *Statement) dialect() dialect.Dialector { return stmt.DB.Dialector }

// BuildClauses renders all registered clauses in order, binds vars, and
// stores the result in SQL. Each build starts with a fresh var list.
func (stmt *Statement) BuildClauses() []byte {
	stmt.normalizeOnConflict()
	b := &statementBuilder{stmt: stmt, buf: make([]byte, 0, 128)}
	stmt.Vars = stmt.Vars[:0]
	for i := 0; i < numSlots; i++ {
		if stmt.slotMask&(1<<uint(i)) == 0 {
			continue
		}
		if len(b.buf) > 0 && b.buf[len(b.buf)-1] != ' ' {
			b.buf = append(b.buf, ' ')
		}
		stmt.slots[i].Build(b)
	}
	// Custom clauses only: standard names always route to slots.
	for _, name := range stmt.Order {
		c := stmt.Clauses[name]
		if c == nil {
			continue
		}
		if len(b.buf) > 0 && b.buf[len(b.buf)-1] != ' ' {
			b.buf = append(b.buf, ' ')
		}
		c.Build(b)
	}
	stmt.SQL = b.buf
	return b.buf
}

// normalizeOnConflict fills an ON CONFLICT clause from the model's schema
// before rendering: UpdateAll expands to every non-primary-key column, and
// a DO UPDATE with no conflict target falls back to the primary key so the
// upsert is portable across dialects (some reject a targetless DO UPDATE).
// Unresolvable models are left untouched — OnConflict.Build never emits
// "ON CONFLICT ()" regardless.
func (stmt *Statement) normalizeOnConflict() {
	raw := stmt.Clause("on_conflict")
	if raw == nil {
		return
	}
	oc, ok := raw.(clause.OnConflict)
	if !ok || oc.DoNothing || stmt.DB == nil || stmt.DB.tables == nil {
		return
	}
	// Create builds from Dest, queries from Model.
	model := stmt.Model
	if model == nil {
		model = stmt.Dest
	}
	if model == nil {
		return
	}
	changed := false
	if oc.UpdateAll && len(oc.UpdateColumns) == 0 {
		if table, err := stmt.DB.tables.Parse(model); err == nil {
			for _, field := range table.Fields {
				if !field.PrimaryKey {
					oc.UpdateColumns = append(oc.UpdateColumns, field.DBName)
				}
			}
			changed = true
		}
	}
	if len(oc.Columns) == 0 && len(oc.UpdateColumns) > 0 {
		if table, err := stmt.DB.tables.Parse(model); err == nil {
			for _, pk := range table.PKs {
				oc.Columns = append(oc.Columns, pk.DBName)
			}
			changed = true
		}
	}
	if changed {
		stmt.AddClause(oc)
	}
}

type statementBuilder struct {
	stmt *Statement
	buf  []byte
}

func (b *statementBuilder) WriteString(s string)         { b.buf = append(b.buf, s...) }
func (b *statementBuilder) AppendByte(c byte)            { b.buf = append(b.buf, c) }
func (b *statementBuilder) Dialector() dialect.Dialector { return b.stmt.dialect() }

func (b *statementBuilder) WriteQuoted(s string) {
	b.buf = b.stmt.dialect().QuoteTo(b.buf, s)
}

func (b *statementBuilder) AddVar(v any) {
	b.buf = b.stmt.dialect().BindVarTo(b.buf, len(b.stmt.Vars)+1)
	b.stmt.Vars = append(b.stmt.Vars, v)
}

// clone returns a deep-enough copy for copy-on-write chaining: SQL/Vars and
// clause state are copied, Settings entries are replayed into a fresh map,
// and the pool and context are carried over (a transaction must survive it).
// It returns a value so the caller can store it inside an embedded DB field
// without a second heap allocation.
func (stmt *Statement) clone() Statement {
	newStmt := Statement{
		DB:                   stmt.DB,
		ConnPool:             stmt.ConnPool,
		Context:              stmt.Context,
		Model:                stmt.Model,
		Dest:                 stmt.Dest,
		Table:                stmt.Table,
		RaiseErrorOnNotFound: stmt.RaiseErrorOnNotFound,
		SkipHooks:            stmt.SkipHooks,
		SkipTimestamps:       stmt.SkipTimestamps,
		CreateBatchSize:      stmt.CreateBatchSize,
		Preloads:             clonePreloads(stmt.Preloads),
		Joins:                append([]joinEntry(nil), stmt.Joins...),
		// Standard clauses ride along inline: an array copy, no map, no
		// allocation. Only custom clauses (rare) touch the fallback map.
		slots:    stmt.slots,
		slotMask: stmt.slotMask,
	}
	if stmt.SQL != nil {
		newStmt.SQL = append([]byte(nil), stmt.SQL...)
	}
	if stmt.Vars != nil {
		newStmt.Vars = append([]any(nil), stmt.Vars...)
	}
	if stmt.Settings != nil {
		// Settings are rare: allocate (and replay) only when the source
		// actually has entries.
		newStmt.Settings = &sync.Map{}
		stmt.Settings.Range(func(k, v any) bool {
			newStmt.Settings.Store(k, v)
			return true
		})
	}
	if stmt.Clauses != nil {
		newStmt.Clauses = make(map[string]clause.Interface, len(stmt.Clauses))
		for name, c := range stmt.Clauses {
			newStmt.Clauses[name] = c
		}
	}
	if stmt.Order != nil {
		newStmt.Order = append([]string(nil), stmt.Order...)
	}
	return newStmt
}
