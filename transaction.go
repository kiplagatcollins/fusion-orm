package fusion

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sync/atomic"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
)

// savepointSeq generates savepoint names unique within the process.
var savepointSeq atomic.Uint64

// Transaction runs fc inside a transaction: commit on nil error, rollback on
// error or panic (the panic still propagates after rollback). When the
// receiver is already inside a transaction, fc runs against a savepoint
// instead — RELEASE on success, ROLLBACK TO on failure — unless
// Config.DisableNestedTransaction runs it inline.
func (db *DB) Transaction(fc func(tx *DB) error, opts ...*sql.TxOptions) (err error) {
	panicked := true

	if committer, ok := db.Statement.ConnPool.(TxCommitter); ok && committer != nil && !isNil(committer) {
		if db.DisableNestedTransaction {
			err = fc(db.Session(&Session{NewDB: db.clone == 1}))
			panicked = false
			return err
		}

		spName := fmt.Sprintf("sp%d", savepointSeq.Add(1))
		if err = db.SavePoint(spName).Error; err != nil {
			panicked = false
			return err
		}
		defer func() {
			if panicked || err != nil {
				// Failure or panic: undo to the savepoint, then free it.
				_ = db.RollbackTo(spName).Error
				_ = db.releaseSavepoint(spName).Error
			} else if e := db.releaseSavepoint(spName).Error; e != nil {
				err = e
			}
		}()

		err = fc(db.Session(&Session{NewDB: db.clone == 1}))
		panicked = false
		return err
	}

	tx := db.Begin(opts...)
	if tx.Error != nil {
		panicked = false
		return tx.Error
	}
	defer func() {
		if panicked || err != nil {
			tx.Rollback()
		}
	}()

	if err = fc(tx); err == nil {
		panicked = false
		return tx.Commit().Error
	}
	panicked = false
	return err
}

// Begin starts a transaction on a derived instance; the returned instance's
// Statement.ConnPool is the *sql.Tx, so subsequent statements run inside it.
// Calling Begin inside an active transaction returns ErrInvalidTransaction —
// use Transaction, which nests through savepoints.
func (db *DB) Begin(opts ...*sql.TxOptions) *DB {
	tx := db.getInstance()

	ctx := tx.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var opt *sql.TxOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	switch beginner := tx.Statement.ConnPool.(type) {
	case TxBeginner:
		sqlTx, err := beginner.BeginTx(ctx, opt)
		if err != nil {
			tx.AddError(err)
		} else {
			tx.Statement.ConnPool = sqlTx
		}
	case ConnPoolBeginner:
		conn, err := beginner.BeginTx(ctx, opt)
		if err != nil {
			tx.AddError(err)
		} else {
			tx.Statement.ConnPool = conn
		}
	default:
		tx.AddError(ErrInvalidTransaction)
	}
	return tx
}

// Commit commits the active transaction, or records ErrInvalidTransaction
// when the instance is not inside one.
func (db *DB) Commit() *DB {
	if committer, ok := db.Statement.ConnPool.(TxCommitter); ok && committer != nil && !isNil(committer) {
		db.AddError(committer.Commit())
	} else {
		db.AddError(ErrInvalidTransaction)
	}
	return db
}

// Rollback rolls back the active transaction, or records
// ErrInvalidTransaction when the instance is not inside one. It is safe to
// call on an already-finished transaction.
func (db *DB) Rollback() *DB {
	if committer, ok := db.Statement.ConnPool.(TxCommitter); ok && committer != nil && !isNil(committer) {
		db.AddError(committer.Rollback())
	} else {
		db.AddError(ErrInvalidTransaction)
	}
	return db
}

// SavePoint creates a savepoint on the current transaction. The name must
// be a plain identifier; it is quoted through the dialector. Requires
// feature.Savepoint, else records ErrUnsupportedFeature.
func (db *DB) SavePoint(name string) *DB {
	return db.savepoint(dialect.AppendSavepoint, name)
}

// RollbackTo undoes statements back to the named savepoint (the savepoint
// itself stays active).
func (db *DB) RollbackTo(name string) *DB {
	return db.savepoint(dialect.AppendRollbackTo, name)
}

// releaseSavepoint frees the named savepoint (SAVEPOINT -> RELEASE).
func (db *DB) releaseSavepoint(name string) *DB {
	return db.savepoint(dialect.AppendReleaseSavepoint, name)
}

// savepoint renders and executes one savepoint statement on a derived
// instance.
func (db *DB) savepoint(appendSQL func([]byte, dialect.Dialector, string) []byte, name string) *DB {
	tx := db.getInstance()

	if tx.Dialector == nil || !tx.Dialector.Features().Has(feature.Savepoint) {
		tx.AddError(NewUnsupportedFeatureError(feature.Savepoint.Name()))
		return tx
	}
	if !validIdentifier(name) {
		tx.AddError(fmt.Errorf("fusion: invalid savepoint name %q: %w", name, ErrInvalidValue))
		return tx
	}

	query := string(appendSQL(nil, tx.Dialector, name))
	_, err := tx.Statement.ConnPool.ExecContext(tx.Statement.Context, query)
	tx.AddError(err)
	return tx
}

// validIdentifier reports whether name is a plain SQL identifier
// ([A-Za-z_][A-Za-z0-9_]*), so generated and caller-supplied savepoint
// names are safe once quoted.
func validIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// isNil reports whether an interface holds a nil pointer (or is nil).
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
		return rv.IsNil()
	}
	return false
}
