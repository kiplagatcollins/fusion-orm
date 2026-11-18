package fusion

import (
	"errors"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
)

// setupUsers creates a users table in a fresh SQLite database.
func setupUsers(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	if tx := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"); tx.Error != nil {
		t.Fatalf("create table: %v", tx.Error)
	}
	return db
}

// countUsers returns the number of rows in users via the root pool.
func countUsers(t *testing.T, db *DB) int {
	t.Helper()
	sqldb, err := db.DB()
	if err != nil {
		t.Fatalf("DB(): %v", err)
	}
	var n int
	if err := sqldb.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// countUsersIn runs COUNT(*) on the statement's own pool, so it can observe
// uncommitted rows inside a transaction.
func countUsersIn(t *testing.T, tx *DB) int {
	t.Helper()
	var n int
	err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT COUNT(*) FROM users").Scan(&n)
	if err != nil {
		t.Fatalf("count in tx: %v", err)
	}
	return n
}

func TestTransactionCommit(t *testing.T) {
	db := setupUsers(t)

	err := db.Transaction(func(tx *DB) error {
		return tx.Exec("INSERT INTO users (name) VALUES (?)", "alice").Error
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	if got := countUsers(t, db); got != 1 {
		t.Errorf("rows after commit = %d, want 1", got)
	}
}

func TestTransactionRollbackOnError(t *testing.T) {
	db := setupUsers(t)

	boom := errors.New("boom")
	err := db.Transaction(func(tx *DB) error {
		if e := tx.Exec("INSERT INTO users (name) VALUES (?)", "alice").Error; e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Transaction err = %v, want %v", err, boom)
	}
	if got := countUsers(t, db); got != 0 {
		t.Errorf("rows after rollback = %d, want 0", got)
	}
	// The root must remain usable after a failed transaction.
	if tx := db.Exec("INSERT INTO users (name) VALUES (?)", "bob"); tx.Error != nil {
		t.Fatalf("post-rollback Exec: %v", tx.Error)
	}
	if got := countUsers(t, db); got != 1 {
		t.Errorf("rows after recovery = %d, want 1", got)
	}
}

func TestTransactionRollbackOnPanic(t *testing.T) {
	db := setupUsers(t)

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("panic did not propagate")
			}
		}()
		_ = db.Transaction(func(tx *DB) error {
			if e := tx.Exec("INSERT INTO users (name) VALUES (?)", "alice").Error; e != nil {
				return e
			}
			panic("mid-transaction failure")
		})
	}()

	if got := countUsers(t, db); got != 0 {
		t.Errorf("rows after panic rollback = %d, want 0", got)
	}
}

func TestTransactionNestingUsesSavepoints(t *testing.T) {
	db := setupUsers(t)

	err := db.Transaction(func(outer *DB) error {
		if e := outer.Exec("INSERT INTO users (name) VALUES (?)", "outer").Error; e != nil {
			return e
		}

		innerErr := outer.Transaction(func(inner *DB) error {
			if e := inner.Exec("INSERT INTO users (name) VALUES (?)", "inner").Error; e != nil {
				return e
			}
			return errors.New("inner fails")
		})
		if innerErr == nil {
			t.Error("inner Transaction succeeded, want error")
		}

		// The inner failure rolled back to the savepoint only: the outer
		// insert must still be live in this transaction.
		if got := countUsersIn(t, outer); got != 1 {
			t.Errorf("rows inside outer tx = %d, want 1", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("outer Transaction: %v", err)
	}

	if got := countUsers(t, db); got != 1 {
		t.Errorf("rows = %d, want 1 (outer kept, inner rolled back)", got)
	}
}

func TestTransactionNestedSuccessReleases(t *testing.T) {
	db := setupUsers(t)

	for i, name := range []string{"nested1", "nested2"} {
		err := db.Transaction(func(outer *DB) error {
			return outer.Transaction(func(inner *DB) error {
				return inner.Exec("INSERT INTO users (name) VALUES (?)", name).Error
			})
		})
		if err != nil {
			t.Fatalf("round %d Transaction: %v", i, err)
		}
	}
	// Two rounds prove savepoints were released and names stay reusable.
	if got := countUsers(t, db); got != 2 {
		t.Errorf("rows = %d, want 2", got)
	}
}

func TestDisableNestedTransactionRunsInline(t *testing.T) {
	db := setupUsers(t)
	db.Config.DisableNestedTransaction = true

	err := db.Transaction(func(outer *DB) error {
		if e := outer.Exec("INSERT INTO users (name) VALUES (?)", "outer").Error; e != nil {
			return e
		}
		// Inline: no savepoint, so the inner failure fails the outer call
		// too and everything rolls back.
		return outer.Transaction(func(inner *DB) error {
			if e := inner.Exec("INSERT INTO users (name) VALUES (?)", "inner").Error; e != nil {
				return e
			}
			return errors.New("inner fails")
		})
	})
	if err == nil {
		t.Fatal("want inner error to surface")
	}
	if got := countUsers(t, db); got != 0 {
		t.Errorf("rows = %d, want 0", got)
	}
}

func TestBeginCommitRollback(t *testing.T) {
	db := setupUsers(t)

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin: %v", tx.Error)
	}
	if e := tx.Exec("INSERT INTO users (name) VALUES (?)", "alice").Error; e != nil {
		t.Fatalf("insert in tx: %v", e)
	}
	if e := tx.Commit().Error; e != nil {
		t.Fatalf("Commit: %v", e)
	}
	if got := countUsers(t, db); got != 1 {
		t.Errorf("rows after commit = %d, want 1", got)
	}

	tx = db.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin: %v", tx.Error)
	}
	if e := tx.Exec("INSERT INTO users (name) VALUES (?)", "bob").Error; e != nil {
		t.Fatalf("insert in tx: %v", e)
	}
	if e := tx.Rollback().Error; e != nil {
		t.Fatalf("Rollback: %v", e)
	}
	if got := countUsers(t, db); got != 1 {
		t.Errorf("rows after rollback = %d, want 1", got)
	}
}

func TestCommitOutsideTransactionErrors(t *testing.T) {
	db := setupUsers(t)

	if err := db.Commit().Error; !errors.Is(err, ErrInvalidTransaction) {
		t.Errorf("Commit outside tx = %v, want ErrInvalidTransaction", err)
	}
	if err := db.Rollback().Error; !errors.Is(err, ErrInvalidTransaction) {
		t.Errorf("Rollback outside tx = %v, want ErrInvalidTransaction", err)
	}
}

func TestSavePointManual(t *testing.T) {
	db := setupUsers(t)

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin: %v", tx.Error)
	}

	if err := tx.SavePoint("sp1").Error; err != nil {
		t.Fatalf("SavePoint: %v", err)
	}
	if e := tx.Exec("INSERT INTO users (name) VALUES (?)", "kept").Error; e != nil {
		t.Fatalf("insert: %v", e)
	}
	if err := tx.SavePoint("sp2").Error; err != nil {
		t.Fatalf("SavePoint sp2: %v", err)
	}
	if e := tx.Exec("INSERT INTO users (name) VALUES (?)", "undone").Error; e != nil {
		t.Fatalf("insert: %v", e)
	}
	if err := tx.RollbackTo("sp2").Error; err != nil {
		t.Fatalf("RollbackTo: %v", err)
	}
	if e := tx.Commit().Error; e != nil {
		t.Fatalf("Commit: %v", e)
	}

	if got := countUsers(t, db); got != 1 {
		t.Errorf("rows = %d, want 1 (only pre-sp2 insert)", got)
	}
}

func TestSavePointRejectsInvalidName(t *testing.T) {
	db := setupUsers(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin: %v", tx.Error)
	}
	defer tx.Rollback()

	if err := tx.SavePoint("bad name; DROP TABLE users").Error; !errors.Is(err, ErrInvalidValue) {
		t.Errorf("SavePoint with hostile name = %v, want ErrInvalidValue", err)
	}
	if err := tx.SavePoint("").Error; !errors.Is(err, ErrInvalidValue) {
		t.Errorf("SavePoint with empty name = %v, want ErrInvalidValue", err)
	}
}

func TestBeginInsideTransactionErrors(t *testing.T) {
	db := setupUsers(t)

	err := db.Transaction(func(tx *DB) error {
		// Begin on a fresh derivative: the error must land on that
		// instance's slot, not pollute the live transaction's slot.
		inner := tx.Session(&Session{NewDB: true}).Begin()
		if !errors.Is(inner.Error, ErrInvalidTransaction) {
			t.Errorf("nested Begin = %v, want ErrInvalidTransaction", inner.Error)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
}

// noSavepointDialector wraps a real dialector with feature.Savepoint cleared.
type noSavepointDialector struct {
	dialect.Dialector
}

func (d noSavepointDialector) Features() feature.Feature {
	return d.Dialector.Features().Remove(feature.Savepoint)
}

// TestSavePointRequiresFeature proves the feature gate: a dialector without
// feature.Savepoint records ErrUnsupportedFeature instead of executing.
func TestSavePointRequiresFeature(t *testing.T) {
	db := setupUsers(t)
	orig := db.Config.Dialector
	t.Cleanup(func() { db.Config.Dialector = orig })

	db.Config.Dialector = noSavepointDialector{orig}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin: %v", tx.Error)
	}
	defer tx.Rollback()

	err := tx.SavePoint("sp1").Error
	if !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("SavePoint without feature = %v, want ErrUnsupportedFeature", err)
	}
	var target *UnsupportedFeatureError
	if !errors.As(err, &target) || target.Feature != "Savepoint" {
		t.Errorf("feature name = %+v", target)
	}
}

func TestTransactionSwapsPoolAndContext(t *testing.T) {
	db := setupUsers(t)

	err := db.Transaction(func(tx *DB) error {
		if tx.Statement.Context == nil {
			t.Error("transaction statement lost its context")
		}
		if tx.Statement.ConnPool == db.Statement.ConnPool {
			t.Error("transaction did not swap in a tx pool")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
}

func TestShippedDialectsDeclareSavepoint(t *testing.T) {
	// Both shipped dialects must support savepoints — the nested
	// transaction path depends on it.
	if !sqlite.Open("x").Features().Has(feature.Savepoint) {
		t.Error("sqlite missing feature.Savepoint")
	}
	if !postgres.Open("x").Features().Has(feature.Savepoint) {
		t.Error("postgres missing feature.Savepoint")
	}
}

func TestReusableChainAfterImplicitTransaction(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(`CREATE TABLE chain_users (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	chain := db.Table("chain_users")
	if err := chain.Create(&struct {
		ID   int64 `fusion:"pk,autoincrement"`
		Name string
	}{Name: "ada"}).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	var count int64
	if err := chain.Model(&struct {
		ID int64 `fusion:"pk"`
	}{}).Count(&count).Error; err != nil {
		t.Fatalf("Count on reused chain: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
