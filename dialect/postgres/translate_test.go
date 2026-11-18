package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kiplagatcollins/fusion-orm/dialect"
)

func TestTranslatePgError(t *testing.T) {
	d := Open("")
	tests := []struct {
		code string
		want error
	}{
		{"23505", dialect.ErrDuplicatedKey},
		{"23503", dialect.ErrForeignKeyViolated},
		{"23514", dialect.ErrCheckConstraintViolated},
	}
	for _, tt := range tests {
		err := d.(*Dialector).Translate(&pgconn.PgError{Code: tt.code})
		if !errors.Is(err, tt.want) {
			t.Errorf("code %s = %v, want %v", tt.code, err, tt.want)
		}
	}
	if err := d.(*Dialector).Translate(errors.New("other")); err == nil {
		t.Error("unrelated error was changed")
	}
}
