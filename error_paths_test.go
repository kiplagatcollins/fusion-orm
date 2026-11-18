package fusion

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
	"github.com/kiplagatcollins/fusion-orm/dialect/sqlite"
	"github.com/kiplagatcollins/fusion-orm/logger"
	"github.com/kiplagatcollins/fusion-orm/migrator"
)

type errPathUser struct {
	ID   int64 `fusion:"pk,autoincrement"`
	Name string
}

func TestErrorPaths(t *testing.T) {
	db := openTestDB(t, &Config{Logger: logger.Discard})
	if err := db.Exec(`CREATE TABLE err_path_users (id INTEGER PRIMARY KEY, name TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		want error
		run  func() error
	}{
		{
			name: "record not found",
			want: ErrRecordNotFound,
			run: func() error {
				var user errPathUser
				return db.First(&user, 999).Error
			},
		},
		{
			name: "global update",
			want: ErrMissingWhereClause,
			run:  func() error { return db.Model(&errPathUser{}).Update("name", "x").Error },
		},
		{
			name: "global delete",
			want: ErrMissingWhereClause,
			run:  func() error { return db.Delete(&errPathUser{}).Error },
		},
		{
			name: "empty update values",
			want: ErrInvalidValue,
			run:  func() error { return db.Model(&errPathUser{}).Updates(nil).Error },
		},
		{
			name: "invalid where value",
			want: ErrInvalidValue,
			run:  func() error { return db.Where(42).Find(&[]errPathUser{}).Error },
		},
		{
			name: "invalid savepoint name",
			want: ErrInvalidValue,
			run:  func() error { return db.SavePoint("bad name!").Error },
		},
		{
			name: "commit without transaction",
			want: ErrInvalidTransaction,
			run:  func() error { return db.Commit().Error },
		},
		{
			name: "rollback without transaction",
			want: ErrInvalidTransaction,
			run:  func() error { return db.Rollback().Error },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestTransactionLifecycleErrors(t *testing.T) {
	db := openTestDB(t, &Config{Logger: logger.Discard})
	tx := db.Begin()
	if err := tx.Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit().Error; !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("second Commit = %v, want sql.ErrTxDone", err)
	}
	if err := tx.Rollback().Error; !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("Rollback after Commit = %v, want sql.ErrTxDone", err)
	}
}

type featurelessDialector struct {
	dialect.Dialector
}

func (featurelessDialector) Features() feature.Feature { return 0 }

func TestFeaturelessDialectErrors(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "featureless.db")
	db, err := Open(featurelessDialector{sqlite.Open(dsn)}, &Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if pool, e := db.DB(); e == nil {
			_ = pool.Close()
		}
	})
	m, err := db.Migrator()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.HasTable(context.Background(), "anything"); !errors.Is(err, migrator.ErrUnsupportedFeature) {
		t.Errorf("HasTable = %v, want migrator.ErrUnsupportedFeature", err)
	}
	if err := db.SavePoint("sp").Error; !errors.Is(err, ErrUnsupportedFeature) {
		t.Errorf("SavePoint = %v, want ErrUnsupportedFeature", err)
	}
}
