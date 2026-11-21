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

type userWithUUIDModel struct {
	UUIDModel
	Email string
}

func (userWithUUIDModel) TableName() string { return "base_uuid_users" }

type userWithNumericModel struct {
	Model
	Email string
}

func (userWithNumericModel) TableName() string { return "base_users" }

func TestUUIDModelRoundTrip(t *testing.T) {
	db := setupUUIDModelDB(t)
	ctx := context.Background()
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoMigrate(ctx, &userWithUUIDModel{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	user := userWithUUIDModel{Email: "a@example.com"}
	if err := db.WithContext(ctx).Create(&user).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.ID == (uuid.UUID{}) || user.ID[6]>>4 != 7 {
		t.Fatalf("id not uuidv7: %v", user.ID)
	}
	if user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() {
		t.Error("timestamps not set")
	}
	if user.DeletedAt.Valid {
		t.Error("DeletedAt should start NULL")
	}
	var found userWithUUIDModel
	if err := db.WithContext(ctx).First(&found, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("first: %v", err)
	}
	if found.Email != "a@example.com" {
		t.Errorf("email round trip = %q", found.Email)
	}
}

func TestUUIDModelSoftDeleteLifecycle(t *testing.T) {
	db := setupUUIDModelDB(t)
	ctx := context.Background()
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoMigrate(ctx, &userWithUUIDModel{}); err != nil {
		t.Fatal(err)
	}
	user := userWithUUIDModel{Email: "del@example.com"}
	if err := db.WithContext(ctx).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).Delete(&user).Error; err != nil {
		t.Fatalf("delete: %v", err)
	}
	var count int64
	if err := db.WithContext(ctx).Model(&userWithUUIDModel{}).Count(&count).Error; err != nil || count != 0 {
		t.Errorf("soft-deleted row visible: %d, %v", count, err)
	}
	var hidden userWithUUIDModel
	if err := db.WithContext(ctx).Unscoped().First(&hidden, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("unscoped first: %v", err)
	}
	if !hidden.DeletedAt.Valid {
		t.Error("deleted_at not set in DB after soft delete")
	}
	if err := db.WithContext(ctx).Unscoped().Model(&userWithUUIDModel{}).Count(&count).Error; err != nil || count != 1 {
		t.Errorf("unscoped count = %d, %v", count, err)
	}
}

func TestNumericModelBackfill(t *testing.T) {
	db := setupUUIDModelDB(t)
	ctx := context.Background()
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AutoMigrate(ctx, &userWithNumericModel{}); err != nil {
		t.Fatal(err)
	}
	user := userWithNumericModel{Email: "n@example.com"}
	if err := db.WithContext(ctx).Create(&user).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("numeric id not backfilled")
	}
	if user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() {
		t.Error("timestamps not set")
	}
	var found userWithNumericModel
	if err := db.WithContext(ctx).First(&found, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("first: %v", err)
	}
	if found.Email != "n@example.com" {
		t.Errorf("email round trip = %q", found.Email)
	}
}
