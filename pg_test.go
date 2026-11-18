package fusion

import (
	"os"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect/postgres"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

func TestPostgresCRUD(t *testing.T) {
	dsn := os.Getenv("FUSION_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FUSION_TEST_PG_DSN is not set")
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

	type pgUser struct {
		ID   int64  `fusion:"pk,autoincrement"`
		Name string `fusion:"type:varchar(64)"`
	}
	table := db.Table("fusion_pg_users")
	if err := table.Exec(`CREATE TABLE IF NOT EXISTS fusion_pg_users (id BIGSERIAL PRIMARY KEY, name VARCHAR(64))`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_ = table.Exec(`DROP TABLE fusion_pg_users`).Error
	})

	u := pgUser{Name: "pg"}
	if err := table.Create(&u).Error; err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == 0 {
		t.Error("id not backfilled by RETURNING")
	}
	var got pgUser
	if err := table.First(&got, u.ID).Error; err != nil || got.Name != "pg" {
		t.Errorf("First = %v, %+v", err, got)
	}
	if err := table.Model(&pgUser{}).Where("name = ?", "pg").Update("name", "changed").Error; err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := table.Delete(&got).Error; err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
