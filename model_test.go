package fusion

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

type postWithUUID struct {
	ID        uuid.UUID `fusion:"pk,type:uuid"`
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (postWithUUID) TableName() string { return "uuid_posts" }

func setupUUIDModelDB(t *testing.T) *DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "uuidmodel.db")
	db, err := Open(sqlite.Open(dsn), &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	return db
}

func TestCreateFillsUUIDv7ID(t *testing.T) {
	db := setupUUIDModelDB(t)
	ctx := context.Background()
	if err := db.WithContext(ctx).Exec(`CREATE TABLE uuid_posts (id TEXT PRIMARY KEY, title TEXT, created_at DATETIME, updated_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	post := postWithUUID{Title: "hello"}
	if err := db.WithContext(ctx).Create(&post).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if post.ID == (uuid.UUID{}) {
		t.Fatal("id not generated")
	}
	if post.ID[6]>>4 != 7 {
		t.Errorf("version nibble = %d, want 7", post.ID[6]>>4)
	}
	var found postWithUUID
	if err := db.WithContext(ctx).First(&found, "id = ?", post.ID).Error; err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if found.Title != "hello" {
		t.Errorf("round trip title = %q", found.Title)
	}
}

func TestCreateRespectsPresetUUID(t *testing.T) {
	db := setupUUIDModelDB(t)
	ctx := context.Background()
	if err := db.WithContext(ctx).Exec(`CREATE TABLE uuid_posts (id TEXT PRIMARY KEY, title TEXT, created_at DATETIME, updated_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	preset := uuid.Must(uuid.NewV7())
	post := postWithUUID{ID: preset, Title: "preset"}
	if err := db.WithContext(ctx).Create(&post).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if post.ID != preset {
		t.Errorf("preset id overwritten: got %v want %v", post.ID, preset)
	}
}

func TestCreateBatchUniqueUUIDs(t *testing.T) {
	db := setupUUIDModelDB(t)
	ctx := context.Background()
	if err := db.WithContext(ctx).Exec(`CREATE TABLE uuid_posts (id TEXT PRIMARY KEY, title TEXT, created_at DATETIME, updated_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	posts := make([]postWithUUID, 5)
	for i := range posts {
		posts[i].Title = "batch"
	}
	if err := db.WithContext(ctx).Create(&posts).Error; err != nil {
		t.Fatalf("batch create: %v", err)
	}
	seen := make(map[uuid.UUID]bool, len(posts))
	for _, p := range posts {
		if p.ID == (uuid.UUID{}) {
			t.Fatal("id not generated in batch row")
		}
		if seen[p.ID] {
			t.Fatalf("duplicate id %v", p.ID)
		}
		seen[p.ID] = true
	}
}
