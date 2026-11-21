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
