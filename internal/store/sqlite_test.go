package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func newTestStore(t *testing.T) *SQLite {
	t.Helper()
	st, err := NewSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestVacationCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	lat, lng := 38.7223, -9.1393
	v := &models.Vacation{
		Title:       "Lisbon",
		Destination: "Portugal",
		StartDate:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Latitude:    &lat,
		Longitude:   &lng,
		Notes:       "budget 1500",
	}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	if v.ID == uuid.Nil {
		t.Fatal("ID was not assigned")
	}

	got, err := st.GetVacation(ctx, v.ID)
	if err != nil {
		t.Fatalf("GetVacation: %v", err)
	}
	if got.Title != "Lisbon" || !got.HasCoords() || got.Nights() != 9 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.StartDate.Equal(v.StartDate) {
		t.Fatalf("start date mismatch: %v != %v", got.StartDate, v.StartDate)
	}

	got.Title = "Lisboa"
	if err := st.UpdateVacation(ctx, got); err != nil {
		t.Fatalf("UpdateVacation: %v", err)
	}
	again, _ := st.GetVacation(ctx, v.ID)
	if again.Title != "Lisboa" {
		t.Fatalf("update not persisted: %q", again.Title)
	}

	list, err := st.ListVacations(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListVacations: err=%v len=%d", err, len(list))
	}
}

func TestCascadeDelete(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	if err := st.CreateSight(ctx, &models.Sight{VacationID: v.ID, Name: "Tower", Visited: true}); err != nil {
		t.Fatalf("CreateSight: %v", err)
	}
	if err := st.CreateTravelSegment(ctx, &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, Mode: "flight"}); err != nil {
		t.Fatalf("CreateTravelSegment: %v", err)
	}

	// Deleting the vacation must cascade (requires foreign_keys=ON).
	if err := st.DeleteVacation(ctx, v.ID); err != nil {
		t.Fatalf("DeleteVacation: %v", err)
	}
	if sights, _ := st.ListSights(ctx, v.ID); len(sights) != 0 {
		t.Fatalf("cascade failed: %d sights remain", len(sights))
	}
	if travel, _ := st.ListTravelSegments(ctx, v.ID); len(travel) != 0 {
		t.Fatalf("cascade failed: %d travel segments remain", len(travel))
	}
}

func TestSightAndTravelRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}

	planned := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	lat, lng := 1.5, 2.5
	sight := &models.Sight{VacationID: v.ID, Name: "T", Category: "c", Visited: true, PlannedDate: &planned, Latitude: &lat, Longitude: &lng}
	if err := st.CreateSight(ctx, sight); err != nil {
		t.Fatalf("CreateSight: %v", err)
	}
	sights, _ := st.ListSights(ctx, v.ID)
	if len(sights) != 1 || !sights[0].Visited || sights[0].PlannedDate == nil || !sights[0].HasCoords() {
		t.Fatalf("sight round-trip: %+v", sights)
	}

	depart := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	tr := &models.TravelSegment{VacationID: v.ID, Kind: models.TravelDeparture, Mode: "train", DepartAt: &depart}
	if err := st.CreateTravelSegment(ctx, tr); err != nil {
		t.Fatalf("CreateTravelSegment: %v", err)
	}
	travel, _ := st.ListTravelSegments(ctx, v.ID)
	if len(travel) != 1 || travel[0].Kind != models.TravelDeparture || travel[0].DepartAt == nil {
		t.Fatalf("travel round-trip: %+v", travel)
	}
	if !travel[0].DepartAt.Equal(depart) {
		t.Fatalf("depart time mismatch: %v != %v", *travel[0].DepartAt, depart)
	}
}

func TestGetVacationNotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetVacation(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestToggleVisited(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	_ = st.CreateVacation(ctx, v)
	sight := &models.Sight{VacationID: v.ID, Name: "T"}
	_ = st.CreateSight(ctx, sight)

	sight.Visited = true
	if err := st.UpdateSight(ctx, sight); err != nil {
		t.Fatalf("UpdateSight: %v", err)
	}
	got, err := st.GetSight(ctx, sight.ID)
	if err != nil || !got.Visited {
		t.Fatalf("visited not persisted: err=%v visited=%v", err, got.Visited)
	}
}
