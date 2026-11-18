package fusion

// The hook interfaces below are the compile-time-safe forms of the methods
// schema detects by name at parse time. A model may implement any subset.

// BeforeCreater runs before a record is created.
type BeforeCreater interface {
	BeforeCreate(tx *DB) error
}

// AfterCreater runs after a record is created.
type AfterCreater interface {
	AfterCreate(tx *DB) error
}

// BeforeUpdater runs before a record is updated.
type BeforeUpdater interface {
	BeforeUpdate(tx *DB) error
}

// AfterUpdater runs after a record is updated.
type AfterUpdater interface {
	AfterUpdate(tx *DB) error
}

// BeforeDeleter runs before a record is deleted.
type BeforeDeleter interface {
	BeforeDelete(tx *DB) error
}

// AfterDeleter runs after a record is deleted.
type AfterDeleter interface {
	AfterDelete(tx *DB) error
}

// BeforeFinder runs before a query.
type BeforeFinder interface {
	BeforeFind(tx *DB) error
}

// AfterFinder runs after a query.
type AfterFinder interface {
	AfterFind(tx *DB) error
}
