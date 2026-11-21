//go:build audit

// Audit repro tests: each test asserts the CORRECT behavior and is expected
// to FAIL on current code — the failure is the evidence recorded in
// docs/AUDIT.md. Run with: go test -tags=audit -run TestAudit ./...
//
// These tests are excluded from default CI (see docs/AUDIT_PLAN.md).
package fusion

import (
	"strings"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/clause"
)

// ---------------------------------------------------------------- class B

// TestAuditStmtCacheEvictionClosesInUseStmt: a statement returned by the
// cache must stay executable for the caller; concurrent LRU eviction must
// not Close it between lookup and use. GORM #7831 class.
func TestAuditStmtCacheEvictionClosesInUseStmt(t *testing.T) {
	db := openTestDB(t)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	c := newStmtCache(1, 0)
	stmtA, err := c.get(t.Context(), pool, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	// Evict A (capacity 1): drops and Closes A while the caller still holds it.
	if _, err := c.get(t.Context(), pool, "SELECT 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := stmtA.ExecContext(t.Context()); err != nil {
		t.Errorf("statement returned by cache.get was closed before use: %v (GORM #7831)", err)
	}
}

// TestAuditPreparedPoolForwardsPing: Open's automatic ping (and callers)
// must work when PrepareStmt wraps the pool. GORM #6181 class.
func TestAuditPreparedPoolForwardsPing(t *testing.T) {
	db := openTestDB(t, &Config{PrepareStmt: true})
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = pool
	raw := newPreparedPool(pool, 10, 0)
	if _, ok := any(raw).(interface{ Ping() error }); !ok {
		t.Error("preparedPool does not implement Ping: automatic ping is silently skipped (GORM #6181)")
	}
}

// ---------------------------------------------------------------- class C

// TestAuditRowNeverNil: Row() must not return nil on build errors — a nil
// *sql.Row panics in the caller's Scan. GORM #7575 class.
func TestAuditRowNeverNil(t *testing.T) {
	db := openTestDB(t)
	row := db.Model(42).Row() // invalid model: build fails
	if row == nil {
		t.Fatal("Row() returned nil on build error; caller Scan panics (GORM #7575)")
	}
	var v any
	_ = row.Scan(&v) // must not panic
}

// ---------------------------------------------------------------- class E

// TestAuditScopeErrorsPropagate: errors raised while applying scopes must
// surface on the finisher chain instead of being dropped. GORM #6283 class.
func TestAuditScopeErrorsPropagate(t *testing.T) {
	db := setupSoftDelete(t)
	var out []softUser
	res := db.Model(&softUser{}).
		Scopes(func(d *DB) *DB { return d.Where(42) }). // invalid condition inside scope
		Find(&out)
	if res.Error == nil {
		t.Error("error raised inside a scope was dropped by applyScopes (GORM #6283 class)")
	}
}

// ---------------------------------------------------------------- class M

type idxModelA struct {
	ID    int64  `fusion:"pk,autoincrement"`
	Email string `fusion:"index:email_idx"`
}

type idxModelB struct {
	ID    int64  `fusion:"pk,autoincrement"`
	Email string `fusion:"index:email_idx"`
}

// TestAuditIndexNameScopedPerTable: an explicit index name shared by two
// tables must produce a usable index on each table; the second table must
// not silently lose its index to CREATE INDEX IF NOT EXISTS. GORM #7346/#7894 class.
func TestAuditIndexNameScopedPerTable(t *testing.T) {
	db := openTestDB(t)
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoMigrate(t.Context(), &idxModelA{}, &idxModelB{}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&idxModelA{}, &idxModelB{}} {
		table, err := db.Parse(model)
		if err != nil {
			t.Fatal(err)
		}
		n := scalarCount(t, db,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql LIKE '%email%'`,
			table.Name)
		if n == 0 {
			t.Errorf("table %s lost its email index to a global name collision (GORM #7346)", table.Name)
		}
	}
}

// ---------------------------------------------------------------- class J

// TestAuditOnConflictRendersNoEmptyTarget: ON CONFLICT with update columns
// but no conflict target must not render the invalid `ON CONFLICT ()`.
func TestAuditOnConflictRendersNoEmptyTarget(t *testing.T) {
	db := openTestDB(t)
	sql := db.ToSQL(func(tx *DB) *DB {
		return tx.Table("audit_plain_users").
			Clauses(clause.OnConflict{UpdateColumns: []string{"name"}}).
			Find(&[]auditPlainUser{})
	})
	if strings.Contains(sql, "ON CONFLICT ()") {
		t.Errorf("OnConflict rendered empty target %q", sql)
	}
}
