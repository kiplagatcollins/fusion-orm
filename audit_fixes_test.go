// Regression tests promoted from the audit suite (docs/AUDIT.md): each test
// previously failed under `go test -tags=audit` and now guards the fix in the
// default suite.
package fusion

import "testing"

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
