package fusion

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/schema"
)

type relUser struct {
	ID      int64 `fusion:"pk,autoincrement"`
	Name    string
	Profile *relProfile `fusion:"rel:has-one,foreignKey:UserID"`
	Posts   []relPost   `fusion:"rel:has-many,foreignKey:UserID"`
	TeamID  int64
	Team    *relTeam `fusion:"rel:belongs-to,foreignKey:TeamID"`
	Tags    []relTag `fusion:"rel:many2many,many2many:user_tags"`
}

type relProfile struct {
	ID     int64 `fusion:"pk,autoincrement"`
	UserID int64
	Bio    string
}

type relPost struct {
	ID       int64 `fusion:"pk,autoincrement"`
	UserID   int64
	Title    string
	Comments []relComment `fusion:"rel:has-many,foreignKey:PostID"`
}

type relComment struct {
	ID     int64 `fusion:"pk,autoincrement"`
	PostID int64
	Body   string
}

type relTeam struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

type relTag struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

type countingLogger struct {
	logger.Interface
	mu   sync.Mutex
	n    int
	sqls []string
}

func (c *countingLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	c.mu.Lock()
	c.n++
	c.sqls = append(c.sqls, sql)
	c.mu.Unlock()
}

func (c *countingLogger) reset() {
	c.mu.Lock()
	c.n = 0
	c.sqls = nil
	c.mu.Unlock()
}

func (c *countingLogger) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *countingLogger) statements() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sqls...)
}

func setupRelations(t *testing.T) (*DB, *countingLogger) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "relations.db")
	counter := &countingLogger{Interface: logger.Discard}
	db, err := Open(sqlite.Open(dsn), &Config{Logger: counter})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	for _, ddl := range []string{
		`CREATE TABLE rel_teams (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`CREATE TABLE rel_users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, team_id INTEGER)`,
		`CREATE TABLE rel_profiles (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, bio TEXT)`,
		`CREATE TABLE rel_posts (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, title TEXT)`,
		`CREATE TABLE rel_comments (id INTEGER PRIMARY KEY AUTOINCREMENT, post_id INTEGER, body TEXT)`,
		`CREATE TABLE rel_tags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`CREATE TABLE user_tags (rel_user_id INTEGER, rel_tag_id INTEGER)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&[]relTeam{
		{ID: 1, Name: "core"}, {ID: 2, Name: "ops"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]relUser{
		{ID: 1, Name: "alice", TeamID: 1},
		{ID: 2, Name: "bob", TeamID: 1},
		{ID: 3, Name: "carol", TeamID: 2},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]relProfile{
		{ID: 1, UserID: 1, Bio: "a"}, {ID: 2, UserID: 2, Bio: "b"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]relPost{
		{ID: 1, UserID: 1, Title: "p1"},
		{ID: 2, UserID: 1, Title: "p2"},
		{ID: 3, UserID: 2, Title: "p3"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]relComment{
		{ID: 1, PostID: 1, Body: "c1"}, {ID: 2, PostID: 2, Body: "c2"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]relTag{
		{ID: 1, Name: "go"}, {ID: 2, Name: "sql"}, {ID: 3, Name: "orm"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO user_tags (rel_user_id, rel_tag_id) VALUES (1,1), (1,3), (2,2)`).Error; err != nil {
		t.Fatal(err)
	}
	counter.reset()
	return db, counter
}

func TestPreloadAllRelationKinds(t *testing.T) {
	db, counter := setupRelations(t)

	q := db.Model(&relUser{}).Preload("Profile").Preload("Posts.Comments").Preload("Team").Preload("Tags").Order("id")
	var users []relUser
	res := q.Find(&users)
	if res.Error != nil {
		t.Fatalf("Find: %v", res.Error)
	}
	if len(users) != 3 {
		t.Fatalf("users = %d", len(users))
	}
	if users[0].Profile == nil || users[0].Profile.Bio != "a" {
		t.Errorf("profile = %+v", users[0].Profile)
	}
	if users[2].Profile != nil {
		t.Errorf("unexpected profile for carol: %+v", users[2].Profile)
	}
	if len(users[0].Posts) != 2 || len(users[0].Posts[0].Comments) != 1 {
		t.Errorf("nested posts/comments = %+v", users[0].Posts)
	}
	if users[0].Team == nil || users[0].Team.Name != "core" {
		t.Errorf("team = %+v", users[0].Team)
	}
	if len(users[0].Tags) != 2 || users[1].Tags[0].Name != "sql" {
		t.Errorf("tags = %+v / %+v", users[0].Tags, users[1].Tags)
	}

	if got := counter.count(); got != 7 {
		t.Errorf("queries = %d, want 7 (root + 4 relations + nested + m2m junction)", got)
	}
	postQueries := 0
	for _, sql := range counter.statements() {
		if strings.Contains(sql, `FROM "rel_posts"`) {
			postQueries++
		}
	}
	if postQueries != 1 {
		t.Errorf("post queries = %d, want 1 (N+1 detected): %v", postQueries, counter.statements())
	}
}

func TestJoinsRelationAndRaw(t *testing.T) {
	db, _ := setupRelations(t)

	var users []relUser
	if err := db.Model(&relUser{}).
		Joins("Profile").Joins("Team").
		Order("rel_users.id").
		Find(&users).Error; err != nil {
		t.Fatalf("relation joins: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("users = %d", len(users))
	}
	if users[0].Profile == nil || users[0].Profile.Bio != "a" || users[0].Team == nil {
		t.Errorf("joined row = %+v", users[0])
	}
	if users[2].Profile != nil {
		t.Errorf("unexpected joined profile: %+v", users[2].Profile)
	}

	var raw []relUser
	if err := db.Model(&relUser{}).
		Joins(`LEFT JOIN rel_profiles AS p ON p.user_id = rel_users.id`).
		Where("p.bio = ?", "a").
		Find(&raw).Error; err != nil {
		t.Fatalf("raw join: %v", err)
	}
	if len(raw) != 1 || raw[0].Name != "alice" {
		t.Errorf("raw join = %+v", raw)
	}
}

func TestAssociationAPI(t *testing.T) {
	db, _ := setupRelations(t)

	var carol relUser
	if err := db.First(&carol, 3).Error; err != nil {
		t.Fatal(err)
	}
	assoc := db.Model(&carol).Association("Profile")
	if err := assoc.Append(&relProfile{Bio: "carol bio"}); err != nil {
		t.Fatalf("Append profile: %v", err)
	}
	var profile relProfile
	if err := db.Model(&carol).Association("Profile").Find(&profile); err != nil {
		t.Fatalf("Find profile: %v", err)
	}
	if profile.Bio != "carol bio" || profile.UserID != 3 {
		t.Errorf("profile = %+v", profile)
	}
	if n, err := db.Model(&carol).Association("Profile").Count(); err != nil || n != 1 {
		t.Errorf("profile count = %d, %v", n, err)
	}

	posts := db.Model(&carol).Association("Posts")
	if err := posts.Append(&relPost{Title: "p4"}, &relPost{Title: "p5"}); err != nil {
		t.Fatalf("Append posts: %v", err)
	}
	if n, err := db.Model(&carol).Association("Posts").Count(); err != nil || n != 2 {
		t.Errorf("posts count = %d, %v", n, err)
	}
	if err := db.Model(&carol).Association("Posts").Replace(&relPost{Title: "only"}); err != nil {
		t.Fatalf("Replace posts: %v", err)
	}
	if n, err := db.Model(&carol).Association("Posts").Count(); err != nil || n != 1 {
		t.Errorf("posts count after replace = %d, %v", n, err)
	}

	var tag relTag
	if err := db.First(&tag, 3).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&carol).Association("Tags").Append(&tag); err != nil {
		t.Fatalf("Append tag: %v", err)
	}
	if n, err := db.Model(&carol).Association("Tags").Count(); err != nil || n != 1 {
		t.Errorf("tags count = %d, %v", n, err)
	}
	if err := db.Model(&carol).Association("Tags").Delete(&tag); err != nil {
		t.Fatalf("Delete tag: %v", err)
	}
	if n, err := db.Model(&carol).Association("Tags").Count(); err != nil || n != 0 {
		t.Errorf("tags count after delete = %d, %v", n, err)
	}

	if err := db.Model(&carol).Association("Profile").Clear(); err != nil {
		t.Fatalf("Clear profile: %v", err)
	}
	if n, err := db.Model(&carol).Association("Profile").Count(); err != nil || n != 0 {
		t.Errorf("profile count after clear = %d, %v", n, err)
	}

	var team relTeam
	if err := db.Model(&carol).Association("Team").Find(&team); err != nil || team.Name != "ops" {
		t.Errorf("belongs-to Find = %+v, %v", team, err)
	}
	if err := db.Model(&carol).Association("Team").Clear(); err != nil {
		t.Fatalf("Clear team: %v", err)
	}
	if carol.TeamID != 0 {
		t.Errorf("TeamID after clear = %d", carol.TeamID)
	}
}

func TestSetupJoinTable(t *testing.T) {
	db, _ := setupRelations(t)
	type userTag struct {
		UserID int64 `fusion:"column:rel_user_id"`
		TagID  int64 `fusion:"column:rel_tag_id"`
	}
	if err := db.SetupJoinTable(&relUser{}, "Tags", &userTag{}); err != nil {
		t.Fatal(err)
	}
	spec := db.m2mSpec(relationFor(mustParse(t, db, &relUser{}), "Tags"))
	if spec.table != "user_tags" || spec.fkCol != "rel_user_id" || spec.refCol != "rel_tag_id" {
		t.Errorf("spec = %+v", spec)
	}
}

func mustParse(t *testing.T, db *DB, model any) *schema.Table {
	t.Helper()
	table, err := db.resolveTable(model)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestAssociationSavingInCallbacks(t *testing.T) {
	db, _ := setupRelations(t)

	dave := &relUser{
		Name:    "dave",
		Profile: &relProfile{Bio: "d"},
		Posts:   []relPost{{Title: "d1"}, {Title: "d2"}},
		Tags:    []relTag{{Name: "fresh"}},
	}
	if err := db.Create(dave).Error; err != nil {
		t.Fatalf("Create with associations: %v", err)
	}
	if dave.ID == 0 || dave.Profile.UserID != dave.ID || dave.Posts[0].UserID != dave.ID {
		t.Fatalf("FK write-back = %+v", dave)
	}

	var loaded relUser
	if err := db.Model(&relUser{}).Preload("Profile").Preload("Posts").Preload("Tags").
		First(&loaded, dave.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Profile == nil || loaded.Profile.Bio != "d" || len(loaded.Posts) != 2 || len(loaded.Tags) != 1 {
		t.Errorf("loaded = %+v", loaded)
	}

	eve := &relUser{Name: "eve", Team: &relTeam{Name: "newteam"}}
	if err := db.Create(eve).Error; err != nil {
		t.Fatalf("Create belongs-to: %v", err)
	}
	if eve.TeamID == 0 {
		t.Fatal("belongs-to FK was not written back")
	}
	var team relTeam
	if err := db.First(&team, eve.TeamID).Error; err != nil || team.Name != "newteam" {
		t.Errorf("created team = %+v, %v", team, err)
	}

	if err := db.Delete(&relUser{}, dave.ID).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var count int64
	if err := db.Model(&relPost{}).Where("user_id = ?", dave.ID).Count(&count).Error; err != nil || count != 0 {
		t.Errorf("dependent posts = %d, %v", count, err)
	}
}

type polyOwner struct {
	ID    int64 `fusion:"pk,autoincrement"`
	Name  string
	Notes []polyNote `fusion:"rel:has-many,polymorphic:owner"`
}

type polyNote struct {
	ID        int64 `fusion:"pk,autoincrement"`
	OwnerID   int64
	OwnerType string
	Body      string
}

func TestPolymorphicPreloadAndApply(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "poly.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	for _, ddl := range []string{
		`CREATE TABLE poly_owners (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`,
		`CREATE TABLE poly_notes (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_id INTEGER, owner_type TEXT, body TEXT)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&polyOwner{ID: 1, Name: "o"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]polyNote{
		{ID: 1, OwnerID: 1, OwnerType: "owner", Body: "go note"},
		{ID: 2, OwnerID: 1, OwnerType: "owner", Body: "sql note"},
		{ID: 3, OwnerID: 1, OwnerType: "other", Body: "foreign note"},
	}).Error; err != nil {
		t.Fatal(err)
	}

	var owner polyOwner
	if err := db.Model(&polyOwner{}).Preload("Notes").First(&owner, 1).Error; err != nil {
		t.Fatal(err)
	}
	if len(owner.Notes) != 2 {
		t.Fatalf("polymorphic notes = %+v", owner.Notes)
	}

	var filtered polyOwner
	if err := db.Model(&polyOwner{}).
		Preload("Notes", func(tx *DB) *DB { return tx.Where("body LIKE ?", "%go%") }).
		First(&filtered, 1).Error; err != nil {
		t.Fatal(err)
	}
	if len(filtered.Notes) != 1 || filtered.Notes[0].Body != "go note" {
		t.Errorf("filtered notes = %+v", filtered.Notes)
	}

	var conditional polyOwner
	if err := db.Model(&polyOwner{}).
		Preload("Notes", "body = ?", "sql note").
		First(&conditional, 1).Error; err != nil {
		t.Fatal(err)
	}
	if len(conditional.Notes) != 1 || conditional.Notes[0].Body != "sql note" {
		t.Errorf("conditional notes = %+v", conditional.Notes)
	}
}

func TestFullSaveAssociations(t *testing.T) {
	db, _ := setupRelations(t)
	frank := &relUser{Name: "frank", Tags: []relTag{{ID: 1, Name: "renamed"}}}
	if err := db.Session(&Session{FullSaveAssociations: true}).Create(frank).Error; err != nil {
		t.Fatal(err)
	}
	var tag relTag
	if err := db.First(&tag, 1).Error; err != nil || tag.Name != "renamed" {
		t.Errorf("tag = %+v, %v", tag, err)
	}
	var count int64
	if err := db.Model(&relTag{}).Count(&count).Error; err != nil || count != 3 {
		t.Errorf("tag count = %d, want 3 (no duplicate insert)", count)
	}
}
