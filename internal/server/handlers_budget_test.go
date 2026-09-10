package server

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestBudgetSourceIdentityAndTotals(t *testing.T) {
	cost := 20.0
	zero := 0.0
	v := &models.Vacation{
		ID: uuid.New(),
		Lodgings: []models.Lodging{
			{ID: uuid.New(), Name: "Same booking name", Cost: &cost},
			{ID: uuid.New(), Name: "Same booking name", Cost: &cost},
		},
		TravelSegments: []models.TravelSegment{
			{ID: uuid.New(), Kind: models.TravelArrival, Cost: &cost},
			{ID: uuid.New(), Kind: models.TravelDeparture, Cost: &cost},
		},
	}
	items := []models.Item{
		{ID: uuid.New(), Title: "Same booking name", Cost: &cost},
		{ID: uuid.New(), Title: "Free entry", Cost: &zero},
		{ID: uuid.New(), Title: "Unknown cost"},
	}
	b := newBudgetView(v, budgetInput{Items: items, LodgingLabel: "Hotels", TravelLabel: "Travel"})
	if b.Spent != 100 || b.ExpenseCount != 6 || b.Unassigned != 100 || b.UnassignedCount != 6 {
		t.Fatalf("source costs not preserved: %+v", b)
	}
	seen := map[string]bool{}
	for _, e := range b.Expenses {
		key := e.Source + ":" + e.SourceID
		if e.Source == "" || e.SourceID == "" || seen[key] {
			t.Fatalf("invalid or repeated source identity: %+v", e)
		}
		seen[key] = true
	}
}

func TestBudgetUnresolvedPayerDoesNotFabricateAttribution(t *testing.T) {
	missing := uuid.New()
	known := models.Person{ID: uuid.New(), Name: "Known"}
	cost := 30.0
	v := &models.Vacation{Participants: []models.Person{known}}
	b := newBudgetView(v, budgetInput{Items: []models.Item{{Cost: &cost, PaidBy: &missing}}})
	if b.Spent != 30 || b.Unassigned != 30 || b.AttributedTotal != 0 || b.UnassignedCount != 1 {
		t.Fatalf("unresolved payer must remain unassigned: %+v", b)
	}
	if b.Persons[0].Balance != 0 || len(b.Transfers) != 0 || b.Expenses[0].PayerID != "" {
		t.Fatalf("unresolved payer affected balances: %+v", b)
	}
}

func TestBudgetExternalPayerRetainsCredit(t *testing.T) {
	alice := models.Person{ID: uuid.New(), Name: "Alice"}
	bob := models.Person{ID: uuid.New(), Name: "Bob"}
	sponsor := models.Person{ID: uuid.New(), Name: "Sponsor"}
	cost := 120.0
	v := &models.Vacation{People: 8, Participants: []models.Person{alice, bob}}
	b := newBudgetView(v, budgetInput{
		Items:     []models.Item{{Cost: &cost, PaidBy: &sponsor.ID}},
		AllPeople: []models.Person{alice, bob, sponsor},
	})
	if b.People != 2 || b.SpentPerPerson != 60 || len(b.Persons) != 3 || len(b.Transfers) != 2 {
		t.Fatalf("incorrect split for external payer: %+v", b)
	}
	var total float64
	for _, p := range b.Persons {
		total += p.Balance
		if p.Name == sponsor.Name && (p.Share != 0 || p.Balance != 120) {
			t.Fatalf("external payer was enrolled or lost credit: %+v", p)
		}
	}
	if math.Abs(total) > 0.0001 {
		t.Fatalf("balances do not sum to zero: %v", total)
	}
}

func TestBudgetPayerOptionsPreserveNonparticipants(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	participant := &models.Person{Name: "Participant"}
	payer := &models.Person{Name: "Previous payer"}
	for _, p := range []*models.Person{participant, payer} {
		if err := s.store.CreatePerson(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.SetVacationParticipants(ctx, v.ID, []uuid.UUID{participant.ID}); err != nil {
		t.Fatal(err)
	}
	people := s.formPayerOptions(ctx, v.ID)
	if len(people) != 2 {
		t.Fatalf("nonparticipant payer hidden: %+v", people)
	}
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		people   []models.Person
		selected string
	}{
		{"known", people, payer.ID.String()},
		{"unresolved", []models.Person{*participant}, payer.ID.String()},
		{"no-options", nil, payer.ID.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := r.fragment(rec, "paid_by_select", i18n.NewLocalizer(i18n.LangEN),
				map[string]any{"People": tc.people, "Selected": tc.selected}, time.UTC, "€")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rec.Body.String(), `value="`+tc.selected+`" selected`) {
				t.Fatalf("previous payer would silently reset: %s", rec.Body.String())
			}
		})
	}
}

func TestBudgetLodgingEditsAndTravelAutosavesStaySourceOnly(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	v.Items, v.Lodgings, v.TravelSegments = nil, nil, nil
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	payer := &models.Person{Name: "Payer"}
	if err := s.store.CreatePerson(ctx, payer); err != nil {
		t.Fatal(err)
	}
	cost := 100.0
	lo := &models.Lodging{VacationID: v.ID, Name: "Hotel", CheckIn: v.StartDate, CheckOut: v.EndDate, Cost: &cost, PaidBy: &payer.ID}
	if err := s.store.CreateLodging(ctx, lo); err != nil {
		t.Fatal(err)
	}
	lodgingForm := url.Values{
		"name": {"Hotel"}, "checkin_date": {"2026-08-01"}, "checkin_time": {"15:00"},
		"checkout_date": {"2026-08-10"}, "checkout_time": {"11:00"}, "cost": {"180"},
		"paid_by": {payer.ID.String()}, "notes": {"Original booking"},
	}
	for range 2 {
		rec := postAISettings(s, "/lodging/"+lo.ID.String(), lodgingForm, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("lodging edit: %d %s", rec.Code, rec.Body.String())
		}
	}
	travelForm := url.Values{"kind": {"arrival"}, "step_order": {"0"}, "mode": {"flight"}, "cost": {"45"}, "paid_by": {payer.ID.String()}}
	for range 2 {
		rec := postAISettings(s, "/vacations/"+v.ID.String()+"/travel", travelForm, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("travel save: %d %s", rec.Code, rec.Body.String())
		}
	}
	var err error
	v.Lodgings, err = s.store.ListLodgings(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	v.TravelSegments, err = s.store.ListTravelSegments(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.store.ListItems(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Lodgings) != 1 || len(v.TravelSegments) != 1 || len(items) != 0 {
		t.Fatalf("editing sources created duplicates: hotels=%d travel=%d items=%d", len(v.Lodgings), len(v.TravelSegments), len(items))
	}
	if v.Lodgings[0].ID != lo.ID || v.Lodgings[0].PaidBy == nil || *v.Lodgings[0].PaidBy != payer.ID {
		t.Fatal("hotel identity or payer lost")
	}
	b := newBudgetView(v, budgetInput{AllPeople: []models.Person{*payer}})
	totals, err := s.store.SpendByVacation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Spent != 225 || totals[v.ID] != b.Spent || b.AttributedTotal != 225 {
		t.Fatalf("dashboard and budget diverge: budget=%+v dashboard=%v", b, totals)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/vacations/"+v.ID.String()+"/api/budget", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "#budget-source-lodging-"+lo.ID.String()) || strings.Contains(rec.Body.String(), "<form") {
		t.Fatalf("budget must link to source, never duplicate entry: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBudgetCostAndPayerValidation(t *testing.T) {
	for _, value := range []string{"-1", "NaN", "+Inf", "-Inf", "bad"} {
		t.Run(value, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(url.Values{"cost": {value}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if _, err := parseCostPtr(req, i18n.NewLocalizer(i18n.LangEN)); err == nil {
				t.Fatal("invalid cost accepted")
			}
		})
	}
	for _, value := range []string{"0", "12,50", ""} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(url.Values{"cost": {value}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		cost, err := parseCostPtr(req, i18n.NewLocalizer(i18n.LangEN))
		if err != nil || (value != "" && cost == nil) {
			t.Fatalf("valid cost %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"invalid", uuid.Nil.String()} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(url.Values{"paid_by": {value}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := parseBudgetPayer(req); err == nil {
			t.Fatal("invalid payer would silently become unassigned")
		}
	}
}
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
	b := newBudgetView(v, budgetInput{Items: items, Currency: "€", LodgingLabel: "Lodging", TravelLabel: "Travel"})

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
	b := newBudgetView(v, budgetInput{Items: items, Currency: "€", LodgingLabel: "Lodging", TravelLabel: "Travel"})
	if b.Unassigned != 60 {
		t.Fatalf("Unassigned = %v, want 60", b.Unassigned)
	}
	if b.AttributedTotal != 40 {
		t.Fatalf("AttributedTotal = %v, want 40", b.AttributedTotal)
	}
}

// When a trip has no explicit participants, the split falls back to the people
// who actually paid something (not every defined person).
func TestNewBudgetViewFallbackToPayers(t *testing.T) {
	alice := models.Person{ID: uuid.New(), Name: "Alice", Color: "#111111"}
	bob := models.Person{ID: uuid.New(), Name: "Bob", Color: "#222222"}
	carol := models.Person{ID: uuid.New(), Name: "Carol"} // defined but never pays
	c100, c60 := 100.0, 60.0
	v := &models.Vacation{ID: uuid.New()} // no participants selected
	items := []models.Item{
		{Title: "A", Cost: &c100, PaidBy: &alice.ID},
		{Title: "B", Cost: &c60, PaidBy: &bob.ID},
	}
	all := []models.Person{alice, bob, carol}
	b := newBudgetView(v, budgetInput{Items: items, AllPeople: all, Currency: "€", LodgingLabel: "Lodging", TravelLabel: "Travel"})

	if !b.HasPeople || len(b.Persons) != 2 {
		t.Fatalf("expected split among the 2 payers, got HasPeople=%v Persons=%d", b.HasPeople, len(b.Persons))
	}
	byName := map[string]budgetPerson{}
	for _, p := range b.Persons {
		byName[p.Name] = p
	}
	if byName["Alice"].Balance != 20 { // paid 100 minus share 80
		t.Errorf("Alice balance = %v, want 20", byName["Alice"].Balance)
	}
	if byName["Bob"].Balance != -20 { // paid 60 minus share 80
		t.Errorf("Bob balance = %v, want -20", byName["Bob"].Balance)
	}
	if _, ok := byName["Carol"]; ok {
		t.Error("Carol should not be in the split — she paid nothing")
	}
}
