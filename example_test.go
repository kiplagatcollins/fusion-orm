package fusion_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kiplagatcollins/fusion-orm"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

type exampleUser struct {
	ID      int64           `fusion:"pk,autoincrement"`
	Name    string          `fusion:"notnull"`
	Profile *exampleProfile `fusion:"rel:has-one,foreignKey:UserID"`
	Posts   []examplePost   `fusion:"rel:has-many,foreignKey:UserID"`
}

func (exampleUser) TableName() string { return "example_users" }

type exampleProfile struct {
	ID     int64 `fusion:"pk,autoincrement"`
	UserID int64
	Bio    string
}

func (exampleProfile) TableName() string { return "example_profiles" }

type examplePost struct {
	ID     int64 `fusion:"pk,autoincrement"`
	UserID int64
	Title  string
}

func (examplePost) TableName() string { return "example_posts" }

type exampleSoftUser struct {
	ID        int64 `fusion:"pk,autoincrement"`
	Name      string
	DeletedAt fusion.DeletedAt
}

func (exampleSoftUser) TableName() string { return "example_soft_users" }

type exampleHandle struct {
	session *fusion.DB
	dir     string
}

func newExample() exampleHandle {
	dir, err := os.MkdirTemp("", "fusion-example-")
	if err != nil {
		panic(err)
	}
	db, err := fusion.Open(sqlite.Open(filepath.Join(dir, "example.db")), &fusion.Config{Logger: logger.Discard})
	if err != nil {
		_ = os.RemoveAll(dir)
		panic(err)
	}
	return exampleHandle{session: db, dir: dir}
}

func (e exampleHandle) close() {
	if pool, err := e.session.DB(); err == nil {
		_ = pool.Close()
	}
	_ = os.RemoveAll(e.dir)
}

func exampleMust(err error) {
	if err != nil {
		panic(err)
	}
}

func ExampleDB_Create() {
	e := newExample()
	defer e.close()
	exampleMust(e.AutoMigrateExample(&exampleUser{}))

	user := exampleUser{Name: "Ada"}
	exampleMust(e.session.Create(&user).Error)

	var got exampleUser
	exampleMust(e.session.First(&got, user.ID).Error)
	fmt.Println(got.Name)
	// Output: Ada
}

func ExampleDB_Preload() {
	e := newExample()
	defer e.close()
	exampleMust(e.AutoMigrateExample(&exampleUser{}, &exampleProfile{}, &examplePost{}))

	user := exampleUser{Name: "Ada"}
	exampleMust(e.session.Create(&user).Error)
	exampleMust(e.session.Create(&exampleProfile{UserID: user.ID, Bio: "mathematician"}).Error)
	exampleMust(e.session.Create(&[]examplePost{
		{UserID: user.ID, Title: "notes"},
		{UserID: user.ID, Title: "analysis"},
	}).Error)

	var got exampleUser
	exampleMust(e.session.Model(&exampleUser{}).
		Preload("Profile").Preload("Posts").
		First(&got, user.ID).Error)
	fmt.Println(got.Profile.Bio, len(got.Posts))
	// Output: mathematician 2
}

func ExampleDB_ToSQL() {
	e := newExample()
	defer e.close()

	sql := e.session.ToSQL(func(tx *fusion.DB) *fusion.DB {
		return tx.Model(&exampleUser{}).Where("name = ?", "Ada").Find(&[]exampleUser{})
	})
	fmt.Println(sql)
	// Output: SELECT * FROM "example_users" WHERE name = 'Ada'
}

func ExampleG() {
	e := newExample()
	defer e.close()
	exampleMust(e.AutoMigrateExample(&exampleUser{}))

	users := fusion.G[exampleUser](e.session)
	exampleMust(users.Create(&exampleUser{Name: "Ada"}))
	found, err := users.Where("name = ?", "Ada").Find()
	exampleMust(err)
	fmt.Println(len(found), found[0].Name)
	// Output: 1 Ada
}

func ExampleDB_Unscoped() {
	e := newExample()
	defer e.close()
	exampleMust(e.AutoMigrateExample(&exampleSoftUser{}))

	user := exampleSoftUser{Name: "Ada"}
	exampleMust(e.session.Create(&user).Error)
	exampleMust(e.session.Delete(&exampleSoftUser{}, user.ID).Error)

	var visible, all int64
	exampleMust(e.session.Model(&exampleSoftUser{}).Count(&visible).Error)
	exampleMust(e.session.Unscoped().Model(&exampleSoftUser{}).Count(&all).Error)
	fmt.Println(visible, all)
	// Output: 0 1
}

func ExampleDB_QueryHook() {
	e := newExample()
	defer e.close()
	exampleMust(e.AutoMigrateExample(&exampleUser{}))

	hook := &countingQueryHook{}
	db := e.session.QueryHook(hook)
	exampleMust(db.Create(&exampleUser{Name: "Ada"}).Error)
	fmt.Println(hook.calls > 0)
	// Output: true
}

type countingQueryHook struct {
	calls int
}

func (h *countingQueryHook) BeforeQuery(ctx context.Context, _ *fusion.QueryEvent) context.Context {
	return ctx
}

func (h *countingQueryHook) AfterQuery(context.Context, *fusion.QueryEvent) {
	h.calls++
}

func (e exampleHandle) AutoMigrateExample(values ...any) error {
	m, err := e.session.Migrator()
	if err != nil {
		return err
	}
	return m.AutoMigrate(context.Background(), values...)
}
