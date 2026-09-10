package server

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestBudgetSourceEditsPreserveOmittedPayer(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	p := &models.Person{Name: "Payer"}
	if err := s.store.CreatePerson(ctx, p); err != nil {
		t.Fatal(err)
	}
	cost := 20.0
	lo := &models.Lodging{VacationID: v.ID, Name: "Hotel", CheckIn: v.StartDate, CheckOut: v.EndDate, Cost: &cost, PaidBy: &p.ID}
	if err := s.store.CreateLodging(ctx, lo); err != nil {
		t.Fatal(err)
	}
	ts := &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, Cost: &cost, PaidBy: &p.ID}
	if err := s.store.CreateTravelSegment(ctx, ts); err != nil {
		t.Fatal(err)
	}
	lodgingForm := url.Values{
		"name": {"Edited hotel"}, "checkin_date": {"2026-08-01"}, "checkin_time": {"15:00"},
		"checkout_date": {"2026-08-10"}, "checkout_time": {"11:00"}, "cost": {"0"},
	}
	travelForm := url.Values{
		"kind": {"arrival"}, "step_order": {"0"}, "segment_id": {ts.ID.String()}, "cost": {"0"},
	}
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/lodging/" + lo.ID.String(), lodgingForm},
		{"/vacations/" + v.ID.String() + "/travel", travelForm},
	} {
		rec := postAISettings(s, tc.path, tc.form, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
		}
		tc.form.Set("paid_by", "malformed")
		rec = postAISettings(s, tc.path, tc.form, true)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid payer accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	hotel, err := s.store.GetLodging(ctx, lo.ID)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := s.store.ListTravelSegments(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hotel.PaidBy == nil || *hotel.PaidBy != p.ID || hotel.Cost == nil || *hotel.Cost != 0 ||
		len(segments) != 1 || segments[0].PaidBy == nil || *segments[0].PaidBy != p.ID || segments[0].Cost == nil || *segments[0].Cost != 0 {
		t.Fatal("omitted or invalid payer cleared metadata, or zero cost was lost")
	}
	lodgingForm.Set("paid_by", "")
	travelForm.Set("paid_by", "")
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/lodging/" + lo.ID.String(), lodgingForm},
		{"/vacations/" + v.ID.String() + "/travel", travelForm},
	} {
		if rec := postAISettings(s, tc.path, tc.form, true); rec.Code != http.StatusOK {
			t.Fatalf("explicit unassignment failed: %d", rec.Code)
		}
	}
	hotel, err = s.store.GetLodging(ctx, lo.ID)
	if err != nil {
		t.Fatal(err)
	}
	segments, err = s.store.ListTravelSegments(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hotel.PaidBy != nil || len(segments) != 1 || segments[0].PaidBy != nil {
		t.Fatal("explicit payer removal was ignored")
	}
}

func TestBudgetDeletingPayerKeepsBookingCost(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	p := &models.Person{Name: "Former payer"}
	if err := s.store.CreatePerson(ctx, p); err != nil {
		t.Fatal(err)
	}
	cost := 100.0
	lo := &models.Lodging{VacationID: v.ID, Name: "Hotel", CheckIn: v.StartDate, CheckOut: v.EndDate, Cost: &cost, PaidBy: &p.ID}
	if err := s.store.CreateLodging(ctx, lo); err != nil {
		t.Fatal(err)
	}
	if err := s.store.DeletePerson(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.GetLodging(ctx, lo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost == nil || *got.Cost != cost || got.PaidBy != nil {
		t.Fatalf("deleting a person must not delete their booked expense: %+v", got)
	}
	v.Lodgings = []models.Lodging{*got}
	b := newBudgetView(v, budgetInput{})
	if b.Spent != cost || b.Unassigned != cost || b.AttributedTotal != 0 || b.ExpenseCount != 1 {
		t.Fatalf("deleted payer misrepresented in budget: %+v", b)
	}
}

func TestBudgetTravelIdentityCannotTargetAnotherVacation(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"kind": {"arrival"}, "step_order": {"0"}, "segment_id": {uuid.NewString()}, "cost": {"99"}}
	rec := postAISettings(s, "/vacations/"+v.ID.String()+"/travel", form, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown source ID accepted: %d", rec.Code)
	}
}

func TestBudgetFreeBookingDoesNotEnrollFallbackParticipant(t *testing.T) {
	alice := models.Person{ID: uuid.New(), Name: "Alice"}
	bob := models.Person{ID: uuid.New(), Name: "Bob"}
	cost, free := 100.0, 0.0
	b := newBudgetView(&models.Vacation{}, budgetInput{
		Items:     []models.Item{{Cost: &cost, PaidBy: &alice.ID}, {Cost: &free, PaidBy: &bob.ID}},
		AllPeople: []models.Person{alice, bob},
	})
	if len(b.Transfers) != 0 {
		t.Fatalf("a free booking must not fabricate a debt for a nonparticipant: %+v", b.Transfers)
	}
}

func TestBudgetMultistopToggleNeverDeletesBookings(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	cost := 30.0
	for step := range 2 {
		seg := &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, StepOrder: step, Cost: &cost, Notes: "Keep booking"}
		if err := s.store.CreateTravelSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	rec := postAISettings(s, "/vacations/"+v.ID.String()+"/travel/multistop?kind=arrival", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle: %d %s", rec.Code, rec.Body.String())
	}
	segments, err := s.store.ListTravelSegments(ctx, v.ID)
	if err != nil || len(segments) != 2 {
		t.Fatalf("toggle deleted source bookings: %+v, %v", segments, err)
	}
	totals, err := s.store.SpendByVacation(ctx)
	if err != nil || totals[v.ID] != 60 {
		t.Fatalf("toggle lost expense data: %v, %v", totals, err)
	}
}

func TestBudgetTravelEditsUseSourceUUIDNotDuplicateSlot(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	cost := 100.0
	first := &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, Cost: &cost, Notes: "First booking"}
	second := &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, Cost: &cost, Notes: "Second booking"}
	for _, seg := range []*models.TravelSegment{first, second} {
		if err := s.store.CreateTravelSegment(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	form := url.Values{
		"kind": {"arrival"}, "step_order": {"0"}, "segment_id": {second.ID.String()},
		"cost": {"150"}, "notes": {"Edited second booking"},
	}
	rec := postAISettings(s, "/vacations/"+v.ID.String()+"/travel", form, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	segments, err := s.store.ListTravelSegments(ctx, v.ID)
	if err != nil || len(segments) != 2 {
		t.Fatalf("independent bookings must be retained: %+v, %v", segments, err)
	}
	for _, seg := range segments {
		switch seg.ID {
		case first.ID:
			if seg.Cost == nil || *seg.Cost != 100 || seg.Notes != first.Notes {
				t.Fatalf("editing another UUID overwrote first booking: %+v", seg)
			}
		case second.ID:
			if seg.Cost == nil || *seg.Cost != 150 || seg.Notes != "Edited second booking" {
				t.Fatalf("selected source UUID was not updated: %+v", seg)
			}
		default:
			t.Fatalf("unexpected source identity: %s", seg.ID)
		}
	}
}
