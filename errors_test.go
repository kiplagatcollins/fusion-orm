package fusion

import (
	"errors"
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect"
)

// wrapTranslator is a test Dialector that maps a driver-style error onto a
// sentinel, standing in for a real dialect's constraint mapping.
type wrapTranslator struct {
	dialect.Dialector
}

func (wrapTranslator) Translate(err error) error {
	if err.Error() == "driver: unique violation" {
		return ErrDuplicatedKey
	}
	return err
}

func TestAddErrorKeepsFirstAsWrapHead(t *testing.T) {
	db := &DB{Config: &Config{}}

	first := errors.New("first failure")
	second := errors.New("second failure")

	if got := db.AddError(first); !errors.Is(got, first) {
		t.Fatalf("first AddError = %v, want wrapped %v", got, first)
	}
	got := db.AddError(second)
	if !errors.Is(got, first) {
		t.Errorf("chain lost first error: %v", got)
	}
	if !errors.Is(got, second) {
		t.Errorf("chain lost second error: %v", got)
	}
	if got == first || got == second {
		t.Errorf("expected a wrapped chain, got %v", got)
	}
}

func TestAddErrorNilIsNoop(t *testing.T) {
	db := &DB{Config: &Config{}}
	if err := db.AddError(nil); err != nil {
		t.Fatalf("AddError(nil) = %v, want nil", err)
	}
}

func TestAddErrorTranslatesWhenEnabled(t *testing.T) {
	db := &DB{Config: &Config{TranslateError: true}}
	db.Dialector = wrapTranslator{}

	driverErr := errors.New("driver: unique violation")
	got := db.AddError(driverErr)
	if !errors.Is(got, ErrDuplicatedKey) {
		t.Fatalf("AddError = %v, want %v", got, ErrDuplicatedKey)
	}
}

func TestAddErrorNoTranslationByDefault(t *testing.T) {
	db := &DB{Config: &Config{}}
	db.Dialector = wrapTranslator{}

	got := db.AddError(errors.New("driver: unique violation"))
	if errors.Is(got, ErrDuplicatedKey) {
		t.Fatalf("TranslateError off must not translate, got %v", got)
	}
}

func TestUnsupportedFeatureError(t *testing.T) {
	err := NewUnsupportedFeatureError("CompositeIn")
	if !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatal("errors.Is(err, ErrUnsupportedFeature) = false")
	}
	var target *UnsupportedFeatureError
	if !errors.As(err, &target) || target.Feature != "CompositeIn" {
		t.Fatalf("As(*UnsupportedFeatureError) = %+v", target)
	}
	if got := err.Error(); got != "unsupported feature: CompositeIn" {
		t.Errorf("Error() = %q", got)
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrRecordNotFound,
		ErrMissingWhereClause,
		ErrDuplicatedKey,
		ErrForeignKeyViolated,
		ErrCheckConstraintViolated,
		ErrInvalidValue,
		ErrUnsupportedFeature,
		ErrDryRunModeUnsupported,
		ErrInvalidTransaction,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("%v must not match %v", a, b)
			}
		}
	}
}
