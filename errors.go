// Package fusion is a full-featured Go ORM that pairs bun's SQL-first query
// engine with gorm's fluent chaining and callback pipeline.
package fusion

import (
	"errors"
	"fmt"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/logger"
)

// ErrorTranslator maps driver constraint errors onto fusion sentinels. It
// is the optional Dialector capability behind Config.TranslateError.
type ErrorTranslator = dialect.ErrorTranslator

// Sentinel errors for every condition the caller must branch on.
// Never string-match these; use errors.Is.
var (
	// ErrRecordNotFound is returned by First/Take/Last when no row matches.
	// Find returns an empty slice instead of this error.
	ErrRecordNotFound = logger.ErrRecordNotFound

	// ErrMissingWhereClause guards global Update/Delete: a statement without
	// WHERE conditions errors unless Config.AllowGlobalUpdate is set.
	ErrMissingWhereClause = errors.New("WHERE conditions required")

	// ErrDuplicatedKey is returned (after error translation) on unique
	// constraint violations.
	ErrDuplicatedKey = errors.New("duplicated key not allowed")

	// ErrForeignKeyViolated is returned (after error translation) on foreign
	// key constraint violations.
	ErrForeignKeyViolated = errors.New("violates foreign key constraint")

	// ErrCheckConstraintViolated is returned (after error translation) on
	// check constraint violations.
	ErrCheckConstraintViolated = errors.New("violates check constraint")

	// ErrInvalidValue reports a destination or argument of the wrong type
	// (e.g. Find on a non-pointer). It is a programmer error.
	ErrInvalidValue = errors.New("invalid value, should be pointer to struct or slice")

	// ErrInvalidTransaction is returned by Commit/Rollback outside a
	// transaction.
	ErrInvalidTransaction = errors.New("invalid transaction")

	// ErrUnsupportedFeature is returned when the active dialect lacks a
	// feature flag required by the operation. The message names the feature.
	ErrUnsupportedFeature = errors.New("unsupported feature")

	// ErrDryRunModeUnsupported is returned when a finisher that requires
	// execution is used while Config.DryRun is set.
	ErrDryRunModeUnsupported = errors.New("dry run mode unsupported")

	// ErrRegistered is returned by Use when a plugin name is already taken.
	ErrRegistered = errors.New("registered")

	// ErrInvalidDB is returned when the underlying connection pool cannot be
	// resolved to a *sql.DB.
	ErrInvalidDB = errors.New("invalid db")
)

// UnsupportedFeatureError names the specific feature flag that a dialect
// lacks, so callers can branch on ErrUnsupportedFeature while still seeing
// which feature was required.
type UnsupportedFeatureError struct {
	Feature string
}

func (e *UnsupportedFeatureError) Error() string {
	return fmt.Sprintf("unsupported feature: %s", e.Feature)
}

func (e *UnsupportedFeatureError) Unwrap() error { return ErrUnsupportedFeature }

// NewUnsupportedFeatureError returns an error that matches
// ErrUnsupportedFeature and names the missing feature.
func NewUnsupportedFeatureError(feature string) error {
	return &UnsupportedFeatureError{Feature: feature}
}

// AddError records err on db's single error slot using wrap-chain
// semantics: the first error is kept as the wrap head so errors.Is continues
// to match it, and subsequent errors are appended with "; %w" so every cause
// stays branchable. Returns the full chain (nil when err is nil).
//
// When Config.TranslateError is set and the dialector implements
// ErrorTranslator, err is translated before being recorded.
func (db *DB) AddError(err error) error {
	if err != nil {
		if db.Config.TranslateError {
			if translator, ok := db.Dialector.(ErrorTranslator); ok {
				err = translator.Translate(err)
			}
		}

		switch {
		case db.Error == nil:
			db.Error = err
		default:
			// Both causes stay branchable: the first remains the wrap head
			// (errors.Is matches it) while later errors chain on too.
			db.Error = fmt.Errorf("%w; %w", db.Error, err)
		}
	}
	return db.Error
}
