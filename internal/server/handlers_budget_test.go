package server

import (
	"testing"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestSettleDebts(t *testing.T) {
	persons := []budgetPerson{
		{Name: "A", Balance: 25},
		{Name: "B", Balance: -25},
	}
	tr := settleDebts(persons)
	if len(tr) != 1 {
		t.Fatalf("expected 1 transfer, got %d: %+v", len(tr), tr)
	}
	if tr[0].FromName != "B" || tr[0].ToName != "A" || tr[0].Amount < 24.99 || tr[0].Amount > 25.01 {
		t.Fatalf("unexpected transfer: %+v", tr[0])
	}

	// Balanced group: no transfers.
	if got := settleDebts([]budgetPerson{{Name: "X", Balance: 0}, {Name: "Y", Balance: 0}}); len(got) != 0 {
		t.Fatalf("expected no transfers for a settled group, got %+v", got)
	}
}

func TestNewBudgetViewPerPerson(t *testing.T) {
	alice := models.Person{ID: uuid.New(), Name: "Alice", Color: "#2563eb"}
	bob := models.Person{ID: uuid.New(), Name: "Bob", Color: "#db2777"}
	c100, c50 := 100.0, 50.0
	v := &models.Vacation{
		ID:           uuid.New(),
		Participants: []models.Person{alice, bob},
	}
	items := []models.Item{
		{Title: "Dinner", Category: "Food", Cost: &c100, PaidBy: &alice.ID},
		{Title: "Tickets", Category: "Activity", Cost: &c50, PaidBy: &bob.ID},
	}
	b := newBudgetView(v, items, nil, "€", "Lodging", "Travel")

	if !b.HasPeople || len(b.Persons) != 2 {
		t.Fatalf("expected 2 persons, got HasPeople=%v Persons=%d", b.HasPeople, len(b.Persons))
	}
	if b.AttributedTotal != 150 {
		t.Fatalf("AttributedTotal = %v, want 150", b.AttributedTotal)
	}
	byName := map[string]budgetPerson{}
	for _, p := range b.Persons {
		byName[p.Name] = p
	}
	if byName["Alice"].Balance != 25 {
		t.Errorf("Alice balance = %v, want 25", byName["Alice"].Balance)
	}
	if byName["Bob"].Balance != -25 {
		t.Errorf("Bob balance = %v, want -25", byName["Bob"].Balance)
	}
	if len(b.Transfers) != 1 || b.Transfers[0].FromName != "Bob" || b.Transfers[0].ToName != "Alice" {
		t.Errorf("unexpected transfers: %+v", b.Transfers)
	}
}

func TestNewBudgetViewUnassigned(t *testing.T) {
	alice := models.Person{ID: uuid.New(), Name: "Alice"}
	c40, c60 := 40.0, 60.0
	v := &models.Vacation{ID: uuid.New(), Participants: []models.Person{alice}}
	items := []models.Item{
		{Title: "Paid", Cost: &c40, PaidBy: &alice.ID},
		{Title: "Nobody", Cost: &c60}, // unassigned
	}
	b := newBudgetView(v, items, nil, "€", "Lodging", "Travel")
	if b.Unassigned != 60 {
		t.Fatalf("Unassigned = %v, want 60", b.Unassigned)
	}
	if b.AttributedTotal != 40 {
		t.Fatalf("AttributedTotal = %v, want 40", b.AttributedTotal)
	}
}
