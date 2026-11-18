package postgres

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
		feature.DoubleColonCast,
		feature.DefaultPlaceholder,
		feature.Merge,
		feature.TableTruncate,
		feature.TableCascade,
		feature.GeneratedIdentity,
		feature.AlterColumnExists,
		feature.Savepoint,
	} {
		if !f.Has(flag) {
			t.Errorf("postgres missing feature %s", flag.Name())
		}
	}

	for _, flag := range []feature.Feature{
		feature.AutoIncrement,
		feature.InsertOnDuplicateKey,
		feature.InsertIgnore,
	} {
		if f.Has(flag) {
			t.Errorf("postgres unexpectedly has feature %s", flag.Name())
		}
	}
}

func TestNameAndQuote(t *testing.T) {
	d := Open("x")
	if d.Name() != dialect.Postgres {
		t.Errorf("Name() = %s", d.Name())
	}
	if d.IdentQuote() != '"' {
		t.Errorf("IdentQuote() = %q", d.IdentQuote())
	}
}

func TestBindVarIsDollar(t *testing.T) {
	got := string(Open("x").BindVarTo(nil, 3))
	if got != "$3" {
		t.Errorf("BindVarTo(3) = %q, want $3", got)
	}
}
