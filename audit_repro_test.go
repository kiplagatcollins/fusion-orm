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
