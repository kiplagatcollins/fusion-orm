package fusion

import (
	"os"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

func TestPostgresRelations(t *testing.T) {
	dsn := os.Getenv("FUSION_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FUSION_TEST_PG_DSN is not set")
	}
	type pgRelPost struct {
		ID     int64 `fusion:"pk,autoincrement"`
		UserID int64
		Title  string
	}
	type pgRelProfile struct {
		ID     int64 `fusion:"pk,autoincrement"`
		UserID int64
		Bio    string
	}
	type pgRelUser struct {
		ID      int64         `fusion:"pk,autoincrement"`
		Name    string        `fusion:"type:varchar(64)"`
		Profile *pgRelProfile `fusion:"rel:has-one,foreignKey:UserID"`
		Posts   []pgRelPost   `fusion:"rel:has-many,foreignKey:UserID"`
	}

	db, err := Open(postgres.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS pg_rel_users (id BIGSERIAL PRIMARY KEY, name VARCHAR(64))`,
		`CREATE TABLE IF NOT EXISTS pg_rel_profiles (id BIGSERIAL PRIMARY KEY, user_id BIGINT, bio TEXT)`,
		`CREATE TABLE IF NOT EXISTS pg_rel_posts (id BIGSERIAL PRIMARY KEY, user_id BIGINT, title TEXT)`,
		`TRUNCATE pg_rel_users, pg_rel_profiles, pg_rel_posts RESTART IDENTITY CASCADE`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	t.Cleanup(func() {
		for _, ddl := range []string{
			`DROP TABLE IF EXISTS pg_rel_posts`,
			`DROP TABLE IF EXISTS pg_rel_profiles`,
			`DROP TABLE IF EXISTS pg_rel_users`,
		} {
			_ = db.Exec(ddl).Error
		}
	})

	u := pgRelUser{Name: "pg"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := db.Create(&[]pgRelProfile{{UserID: u.ID, Bio: "bio"}}).Error; err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if err := db.Create(&[]pgRelPost{{UserID: u.ID, Title: "p1"}, {UserID: u.ID, Title: "p2"}}).Error; err != nil {
		t.Fatalf("Create posts: %v", err)
	}

	var got pgRelUser
	if err := db.Model(&pgRelUser{}).
		Preload("Profile").Preload("Posts").
		First(&got, u.ID).Error; err != nil {
		t.Fatalf("Preload: %v", err)
	}
	if got.Profile == nil || got.Profile.Bio != "bio" || len(got.Posts) != 2 {
		t.Errorf("relations = %+v", got)
	}

	var joined []pgRelUser
	if err := db.Model(&pgRelUser{}).Joins("Profile").Find(&joined).Error; err != nil {
		t.Fatalf("Joins: %v", err)
	}
	if len(joined) != 1 || joined[0].Profile == nil || joined[0].Profile.Bio != "bio" {
		t.Errorf("joined = %+v", joined)
	}
}
