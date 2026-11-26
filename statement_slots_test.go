package fusion

import (
	"testing"

	"github.com/kiplagatcollins/fusion-orm/clause"
)

// testClause is a minimal clause.Interface for exercising slot routing;
// its marker string proves which value a slot holds.
type testClause struct {
	name string
	mark string
}

func (c testClause) Name() string           { return c.name }
func (c testClause) Build(b clause.Builder) { b.WriteString(c.mark) }

func TestSlotIndexMatchesNames(t *testing.T) {
	if len(slotNames) != numSlots {
		t.Fatalf("slotNames length %d != numSlots %d", len(slotNames), numSlots)
	}
	for i, name := range slotNames {
		got, ok := slotIndex(name)
		if !ok {
			t.Fatalf("slotIndex(%q) not found", name)
		}
		if got != uint(i) {
			t.Errorf("slotIndex(%q) = %d, want %d", name, got, i)
		}
	}
	if _, ok := slotIndex("totally_custom"); ok {
		t.Error("custom name must not map to a slot")
	}
}

func TestClauseSlotsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	stmt := &db.Statement

	stmt.AddClause(testClause{"where", "w1"})
	if got, _ := stmt.Clause("where").(testClause); got.mark != "w1" {
		t.Fatalf("Clause(where) = %v, want w1", stmt.Clause("where"))
	}
	// Replace must overwrite, not duplicate.
	stmt.AddClause(testClause{"where", "w2"})
	if got, _ := stmt.Clause("where").(testClause); got.mark != "w2" {
		t.Errorf("after replace Clause(where) = %q, want w2", got.mark)
	}
	if len(stmt.Order) != 0 {
		t.Errorf("standard clauses must not enter Order: %v", stmt.Order)
	}
	if stmt.Clauses != nil {
		t.Errorf("standard clauses must not allocate the map: %v", stmt.Clauses)
	}
	// Custom clause names fall back to the map and track order.
	stmt.AddClause(testClause{"custom_a", "ca"})
	stmt.AddClause(testClause{"custom_b", "cb"})
	if len(stmt.Order) != 2 || stmt.Order[0] != "custom_a" {
		t.Errorf("custom order = %v", stmt.Order)
	}
	stmt.RemoveClause("custom_a")
	if stmt.Clause("custom_a") != nil || len(stmt.Order) != 1 {
		t.Errorf("remove custom: clause=%v order=%v", stmt.Clause("custom_a"), stmt.Order)
	}
	// Remove of a slot clears presence.
	stmt.RemoveClause("where")
	if stmt.Clause("where") != nil {
		t.Error("removed slot still present")
	}
	// Clone carries slots without touching the map path.
	cp := stmt.clone()
	if cp.Clause("custom_b") == nil || cp.Clause("where") != nil {
		t.Errorf("clone state wrong: b=%v where=%v", cp.Clause("custom_b"), cp.Clause("where"))
	}
}
