// Regression tests promoted from the audit suite (docs/AUDIT.md): each test
// previously failed under `go test -tags=audit` and now guards the fix in the
// default suite.
package fusion

import (
	"errors"
	"strings"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/clause"
)

// ---------------------------------------------------------------- class A

type atxUser struct {
	ID    int64  `fusion:"pk,autoincrement"`
	Email string `fusion:"unique"`
}

// TestAuditImplicitTxReleasedOnInsertError: a failed INSERT inside the
// implicit default transaction must not leave the transaction (and its
// checked-out connection) open. GORM #7897 class.
func TestAuditImplicitTxReleasedOnInsertError(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(`CREATE TABLE atx_users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT UNIQUE)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&atxUser{Email: "a@x"}).Error; err != nil {
		t.Fatal(err)
	}
	dup := db.Create(&atxUser{Email: "a@x"})
	if dup.Error == nil {
		t.Fatal("duplicate insert must fail")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.Stats().InUse; got != 0 {
		t.Errorf("failed create inside implicit tx leaked a connection: InUse=%d (want 0)", got)
	}
}

type panicUser struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

func (u *panicUser) BeforeCreate(_ *DB) error {
	panic("hook boom")
}

// TestAuditImplicitTxReleasedOnHookPanic: a panicking model hook inside the
// implicit default transaction must roll back and return the connection;
// the panic still propagates. GORM #7897 class.
func TestAuditImplicitTxReleasedOnHookPanic(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(`CREATE TABLE panic_users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("hook panic must propagate to the caller")
			}
		}()
		db.Create(&panicUser{Name: "x"})
	}()
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.Stats().InUse; got != 0 {
		t.Errorf("panicking create inside implicit tx leaked a connection: InUse=%d (want 0)", got)
	}
	var n int64
	if err := db.Model(&panicUser{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("panicking insert must roll back, rows=%d (want 0)", n)
	}
}

// ---------------------------------------------------------------- class H

// TestAuditGlobalSoftDeleteRequiresWhere: Delete without any caller
// conditions on a soft-deletable model must fail with ErrMissingWhereClause
// instead of soft-deleting every live row (the injected soft-delete filter
// must not satisfy the global-delete guard). GORM #6338 class.
func TestAuditGlobalSoftDeleteRequiresWhere(t *testing.T) {
	db := setupSoftDelete(t) // seeds two live rows
	err := db.Delete(&softUser{}).Error
	if !errors.Is(err, ErrMissingWhereClause) {
		t.Errorf("Delete without WHERE = %v, want ErrMissingWhereClause", err)
	}
	var live int64
	if countErr := db.Model(&softUser{}).Count(&live).Error; countErr == nil && live != 2 {
		t.Errorf("rows after guarded delete = %d, want 2", live)
	}
}

// TestAuditSoftDeleteNotBypassedByOr: user OR-conditions joined before the
// injected soft-delete filter must keep SQL precedence so deleted rows can
// never reappear. GORM #6998/#5652 class.
func TestAuditSoftDeleteNotBypassedByOr(t *testing.T) {
	db := setupSoftDelete(t)
	var users []softUser
	if err := db.Find(&users).Error; err != nil {
		t.Fatal(err)
	}
	// Soft-delete row "a".
	if err := db.Delete(&softUser{}, users[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	var got []softUser
	if err := db.Model(&softUser{}).
		Where("name = ?", "a").
		Or("name = ?", "b").
		Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	for _, u := range got {
		if u.Name == "a" {
			t.Errorf("deleted row returned through OR condition: %+v", u)
		}
	}
	if len(got) != 1 {
		t.Errorf("rows = %d, want 1 (only live %q)", len(got), "b")
	}
}

// ---------------------------------------------------------------- class F

type auditPlainUser struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

// TestAuditClauseOrRendersOr: clause.Or must render its expressions OR-joined.
func TestAuditClauseOrRendersOr(t *testing.T) {
	db := openTestDB(t)
	sql := db.ToSQL(func(tx *DB) *DB {
		return tx.Table("audit_plain_users").
			Clauses(clause.Where{Conds: []clause.Condition{
				clause.Or(clause.Expr("name = ?", "a"), clause.Expr("name = ?", "b")),
			}}).
			Find(&[]auditPlainUser{})
	})
	if !strings.Contains(sql, " OR ") {
		t.Errorf("clause.Or rendered without OR: %s", sql)
	}
}

// TestAuditMapConditionSQLStable: map-based conditions must produce stable
// SQL so prepared-stmt caches and ToSQL stay deterministic. GORM #5846 class.
func TestAuditMapConditionSQLStable(t *testing.T) {
	db := openTestDB(t)
	cond := map[string]any{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5}
	first := db.ToSQL(func(tx *DB) *DB {
		return tx.Table("audit_plain_users").Where(cond).Find(&[]auditPlainUser{})
	})
	for i := 0; i < 30; i++ {
		got := db.ToSQL(func(tx *DB) *DB {
			return tx.Table("audit_plain_users").Where(cond).Find(&[]auditPlainUser{})
		})
		if got != first {
			t.Fatalf("unstable SQL for identical map condition:\n%s\nvs\n%s (GORM #5846)", first, got)
		}
	}
}

// scalarCount runs a raw scalar COUNT through the underlying pool (the
// facade intentionally has no Raw method). Lives in the default suite so both
// the promoted tests and the remaining audit-tagged tests can use it.
func scalarCount(t *testing.T, db *DB, query string, args ...any) int64 {
	t.Helper()
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := pool.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---------------------------------------------------------------- class H (inline joins)

type jdParent struct {
	ID      int64 `fusion:"pk,autoincrement"`
	Name    string
	Profile *jdProfile `fusion:"rel:has-one,foreignKey:ParentID"`
}

type jdProfile struct {
	ID        int64 `fusion:"pk,autoincrement"`
	ParentID  int64
	Bio       string
	DeletedAt DeletedAt
}

// TestAuditRelationJoinOmitsSoftDeleted: LEFT JOINs on soft-deletable
// relations must exclude soft-deleted rows, matching the preload behavior.
// GORM #6998 class.
func TestAuditRelationJoinOmitsSoftDeleted(t *testing.T) {
	db := openTestDB(t)
	for _, ddl := range []string{
		`CREATE TABLE jd_parents (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`CREATE TABLE jd_profiles (id INTEGER PRIMARY KEY AUTOINCREMENT, parent_id INTEGER, bio TEXT, deleted_at TIMESTAMP)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	parent := jdParent{Name: "p"}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	profile := jdProfile{ParentID: parent.ID, Bio: "live"}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	// Soft-delete the profile directly.
	if err := db.Exec(`UPDATE jd_profiles SET deleted_at = '2024-01-01 00:00:00' WHERE id = ?`, profile.ID).Error; err != nil {
		t.Fatal(err)
	}
	var got []jdParent
	if err := db.Model(&jdParent{}).Joins("Profile").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("parents = %d, want 1", len(got))
	}
	if got[0].Profile != nil {
		t.Errorf("join returned soft-deleted relation: %+v", *got[0].Profile)
	}
}

// ---------------------------------------------------------------- class G

// TestAuditCountStripsOrderBy: ORDER BY must not survive into COUNT(*) —
// PostgreSQL rejects it (column must appear in GROUP BY). GORM #7516 class.
func TestAuditCountStripsOrderBy(t *testing.T) {
	db := openTestDB(t)
	var n int64
	tx := db.Session(&Session{DryRun: true}).
		Model(&auditPlainUser{}).Order("name").Count(&n)
	sql := string(tx.Statement.SQL)
	if strings.Contains(sql, "ORDER BY") {
		t.Errorf("Count kept ORDER BY (invalid on PG): %s", sql)
	}
}

// ---------------------------------------------------------------- class I

type auditSaveUser struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

// TestAuditSaveSliceWithPKs: Save over a slice of persisted models must
// update every row, not fail on the slice type. GORM #7540 class.
func TestAuditSaveSliceWithPKs(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(`CREATE TABLE audit_save_users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]auditSaveUser{{Name: "a"}, {Name: "b"}}).Error; err != nil {
		t.Fatal(err)
	}
	var all []auditSaveUser
	if err := db.Find(&all).Error; err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("rows = %d, want 2", len(all))
	}
	all[0].Name = "a2"
	if err := db.Save(&all).Error; err != nil {
		t.Errorf("Save(slice with PKs) = %v (GORM #7540 class)", err)
	}
	var name string
	if err := db.Model(&auditSaveUser{}).Where("id = ?", all[0].ID).Pluck("name", &name).Error; err != nil {
		t.Fatal(err)
	}
	if name != "a2" {
		t.Errorf("name after Save = %q, want %q", name, "a2")
	}
}
