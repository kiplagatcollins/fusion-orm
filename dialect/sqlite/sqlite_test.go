package sqlite

import (
	"testing"

	"github.com/kiplagatcollins/fusion-orm/dialect"
	"github.com/kiplagatcollins/fusion-orm/dialect/feature"
)

var _ dialect.Dialector = (*Dialector)(nil)

func TestFeatures(t *testing.T) {
	f := Open("x").Features()

	for _, flag := range []feature.Feature{
		feature.CTE,
		feature.Returning,
		feature.InsertOnConflict,
		feature.InsertReturning,
		feature.DeleteReturning,
		feature.AutoIncrement,
		feature.CompositeIn,
		feature.RowValues,
		feature.TableNotExists,
		feature.CreateIndexIfNotExists,
		feature.Savepoint,
	} {
		if !f.Has(flag) {
			t.Errorf("sqlite missing feature %s", flag.Name())
		}
	}

	for _, flag := range []feature.Feature{
		feature.DoubleColonCast,
		feature.Merge,
		feature.InsertOnDuplicateKey,
		feature.UpdateFromTable,
		feature.TableTruncate,
		feature.GeneratedIdentity,
	} {
		if f.Has(flag) {
			t.Errorf("sqlite unexpectedly has feature %s", flag.Name())
		}
	}
}

func TestNameAndQuote(t *testing.T) {
	d := Open("x")
	if d.Name() != dialect.SQLite {
		t.Errorf("Name() = %s", d.Name())
	}
	if d.IdentQuote() != '"' {
		t.Errorf("IdentQuote() = %q", d.IdentQuote())
	}
}

func TestBindVarIsQuestion(t *testing.T) {
	got := string(Open("x").BindVarTo(nil, 3))
	if got != "?" {
		t.Errorf("BindVarTo(3) = %q, want ?", got)
	}
}
