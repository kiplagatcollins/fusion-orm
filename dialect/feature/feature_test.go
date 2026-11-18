package feature

import "testing"

func TestHasSetRemove(t *testing.T) {
	var f Feature
	if f.Has(CTE) {
		t.Error("zero feature reports Has(CTE)")
	}

	f = f.Set(CTE).Set(Returning)
	if !f.Has(CTE) || !f.Has(Returning) {
		t.Fatalf("f = %s, want CTE and Returning", f)
	}
	if f.Has(InsertOnConflict) {
		t.Error("f reports unset InsertOnConflict")
	}

	f = f.Remove(CTE)
	if f.Has(CTE) {
		t.Error("Remove(CTE) did not clear the bit")
	}
	if !f.Has(Returning) {
		t.Error("Remove(CTE) cleared an unrelated bit")
	}
}

func TestStringDeterministic(t *testing.T) {
	f := CTE | Returning | Savepoint
	want := "CTE|Returning|Savepoint"
	for i := 0; i < 20; i++ {
		if got := f.String(); got != want {
			t.Fatalf("String() = %q, want %q", got, want)
		}
	}
}

func TestStringZeroAndUnknown(t *testing.T) {
	if got := Feature(0).String(); got != "none" {
		t.Errorf("zero String() = %q", got)
	}
	// A bit outside the declared set renders numerically.
	unknown := Feature(1) << 60
	got := unknown.String()
	if got == "" || got == "none" {
		t.Errorf("unknown String() = %q", got)
	}
}

func TestName(t *testing.T) {
	if got := CompositeIn.Name(); got != "CompositeIn" {
		t.Errorf("CompositeIn.Name() = %q", got)
	}
	if got := Feature(0).Name(); got != "unknown(0)" {
		t.Errorf("zero Name() = %q", got)
	}
}

func TestDeclaredFlagCount(t *testing.T) {
	// The roadmap budgets ~35 flags; guard against accidental drop or
	// runaway growth of the vocabulary.
	if n := len(ordered); n < 30 || n > 40 {
		t.Fatalf("declared flags = %d, want 30..40", n)
	}
	seen := map[Feature]bool{}
	for _, e := range ordered {
		if seen[e.flag] {
			t.Fatalf("duplicate flag %s", e.name)
		}
		seen[e.flag] = true
	}
}
