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

// TestAuditHasManyResaveNoDuplicate: creating a parent whose has-many child
// carries an existing primary key must not insert a duplicate child row.
// GORM #6119 class.
func TestAuditHasManyResaveNoDuplicate(t *testing.T) {
	db, _ := setupRelations(t)
	// Existing child post (id 7) owned by user 1.
	if err := db.Exec(`INSERT INTO rel_posts (id, user_id, title) VALUES (7, 1, 'seed')`).Error; err != nil {
		t.Fatal(err)
	}
	// A new parent claims the existing child through the relation.
	newUser := relUser{ID: 40, Name: "dave", Posts: []relPost{{ID: 7, Title: "seed"}}}
	if err := db.Create(&newUser).Error; err != nil {
		t.Fatalf("Create parent with existing child: %v", err)
	}
	n := scalarCount(t, db, `SELECT COUNT(*) FROM rel_posts WHERE title = 'seed'`)
	if n != 1 {
		t.Errorf("child rows with title 'seed' = %d, want 1 (no duplicate; GORM #6119)", n)
	}
}

// TestAuditM2MAppendIdempotent: re-appending an existing relation must not
// insert a duplicate junction row. GORM #5579/#7365 class.
func TestAuditM2MAppendIdempotent(t *testing.T) {
	db, _ := setupRelations(t) // user 1 already linked to tags 1 and 3
	u := &relUser{}
	u.ID = 1
	if err := db.Model(u).Association("Tags").Append(&relTag{ID: 1}); err != nil {
		t.Fatalf("Append existing tag: %v", err)
	}
	n := scalarCount(t, db, `SELECT COUNT(*) FROM user_tags WHERE rel_user_id = 1 AND rel_tag_id = 1`)
	if n != 1 {
		t.Errorf("junction rows for (1,1) = %d, want 1 (idempotent append; GORM #5579)", n)
	}
}

// ---------------------------------------------------------------- class K

type m2mAUser struct {
	ID    int64      `fusion:"pk,autoincrement"`
	Roles []m2mARole `fusion:"rel:many2many,many2many:m2m_a_roles"`
}

type m2mARole struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

// TestAuditAutoMigrateCreatesM2MJunction: AutoMigrate must materialize the
// junction table for many-to-many relations. GORM #7006-adjacent.
func TestAuditAutoMigrateCreatesM2MJunction(t *testing.T) {
	db := openTestDB(t)
	table, err := db.Parse(&m2mAUser{})
	if err != nil {
		t.Fatal(err)
	}
	rel := table.Relations["Roles"]
	if rel == nil {
		t.Fatal("Roles relation not parsed")
	}
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoMigrate(t.Context(), &m2mAUser{}, &m2mARole{}); err != nil {
		t.Fatal(err)
	}
	exists, err := m.HasTable(t.Context(), rel.M2MName)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Errorf("junction table %q not created by AutoMigrate", rel.M2MName)
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
	stmtA, releaseA, err := c.get(t.Context(), pool, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	// Evict A (capacity 1): drops A from the cache while the caller still
	// holds it; Close must wait for releaseA.
	if _, releaseB, err := c.get(t.Context(), pool, "SELECT 2"); err != nil {
		t.Fatal(err)
	} else {
		releaseB()
	}
	if _, err := stmtA.ExecContext(t.Context()); err != nil {
		t.Errorf("statement returned by cache.get was closed before use: %v (GORM #7831)", err)
	}
	releaseA()
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
	m := db.Model(42) // invalid model: build fails
	row := m.Row()
	if row == nil {
		t.Fatal("Row() returned nil on build error; caller Scan panics (GORM #7575)")
	}
	if m.Error == nil {
		t.Error("build error must surface on the chain node (m.Error)")
	}
	var v any
	if err := row.Scan(&v); err == nil {
		t.Error("Scan of the sentinel row must fail with the build error")
	}
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

// TestOnConflictRuntimeUpsert executes real upserts against SQLite so the
// rendered ON CONFLICT SQL is validated by the engine: a manual column set
// (target normalized to the primary key) and UpdateAll.
func TestOnConflictRuntimeUpsert(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec(`CREATE TABLE upsert_users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`).Error; err != nil {
		t.Fatal(err)
	}
	type upsertUser struct {
		ID   int64 `fusion:"pk"`
		Name string
		Age  int
	}
	upsert := clause.OnConflict{UpdateColumns: []string{"name", "age"}}
	if err := db.Clauses(upsert).Create(&upsertUser{ID: 1, Name: "a", Age: 10}).Error; err != nil {
		t.Fatalf("initial insert: %v", err)
	}
	if err := db.Clauses(upsert).Create(&upsertUser{ID: 1, Name: "b", Age: 11}).Error; err != nil {
		t.Fatalf("upsert: %v (engine rejected the rendered ON CONFLICT)", err)
	}
	var got upsertUser
	if err := db.Find(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != "b" || got.Age != 11 {
		t.Errorf("after upsert got %+v, want name=b age=11", got)
	}
	if err := db.Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&upsertUser{ID: 1, Name: "c", Age: 12}).Error; err != nil {
		t.Fatalf("UpdateAll upsert: %v", err)
	}
	if err := db.Find(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if got.Name != "c" || got.Age != 12 {
		t.Errorf("after UpdateAll upsert got %+v, want name=c age=12", got)
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
