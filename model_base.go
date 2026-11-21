package fusion

// Base models: gorm.Model parity for fusion-orm. Embed by value to inherit
// the primary key, automatic timestamps, and soft-delete DeletedAt.

import (
	"time"

	"github.com/google/uuid"
)

// Model is the numeric base model, mirroring gorm.Model: an autoincrement
// primary key backfilled after insert, automatic CreatedAt/UpdatedAt, and a
// nullable DeletedAt that enables soft delete.
type Model struct {
	ID        uint64 `fusion:"pk,autoincrement"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt DeletedAt `fusion:"index"`
}

// UUIDModel replaces Model's numeric ID with a UUIDv7 primary key generated
// by the ORM at create time whenever the field is zero. Pre-set IDs are
// respected.
type UUIDModel struct {
	ID        uuid.UUID `fusion:"pk,type:uuid"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt DeletedAt `fusion:"index"`
}
