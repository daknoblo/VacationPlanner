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
	budget := 1500.0
	v := &models.Vacation{
		Title:       "Lisbon",
		Destination: "Portugal",
		StartDate:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Latitude:    &lat,
		Longitude:   &lng,
		Notes:       "budget 1500",
		Budget:      &budget,
		People:      3,
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
	if got.Budget == nil || *got.Budget != 1500 || got.People != 3 {
		t.Fatalf("budget/people round-trip mismatch: budget=%v people=%d", got.Budget, got.People)
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

func TestActivityRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}

	day := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	a := &models.Activity{
		VacationID: v.ID, Day: day, Title: "Museum", Category: "Culture",
		StartMin: 600, EndMin: 720, Description: "Visit", Location: "Center",
	}
	if err := st.CreateActivity(ctx, a); err != nil {
		t.Fatalf("CreateActivity: %v", err)
	}

	list, err := st.ListActivities(ctx, v.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListActivities: err=%v len=%d", err, len(list))
	}
	got := list[0]
	if got.Title != "Museum" || got.StartMin != 600 || got.EndMin != 720 || !got.OnDay(day) {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	got.StartMin = 630
	got.Title = "Museum Tour"
	if err := st.UpdateActivity(ctx, &got); err != nil {
		t.Fatalf("UpdateActivity: %v", err)
	}
	again, err := st.GetActivity(ctx, a.ID)
	if err != nil || again.StartMin != 630 || again.Title != "Museum Tour" {
		t.Fatalf("update not persisted: %+v err=%v", again, err)
	}

	if err := st.DeleteActivity(ctx, a.ID); err != nil {
		t.Fatalf("DeleteActivity: %v", err)
	}
	if l, _ := st.ListActivities(ctx, v.ID); len(l) != 0 {
		t.Fatalf("expected 0 activities after delete, got %d", len(l))
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
	if err := st.CreateActivity(ctx, &models.Activity{VacationID: v.ID, Day: time.Now().UTC(), Title: "Walk"}); err != nil {
		t.Fatalf("CreateActivity: %v", err)
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
	if acts, _ := st.ListActivities(ctx, v.ID); len(acts) != 0 {
		t.Fatalf("cascade failed: %d activities remain", len(acts))
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

func TestSettings(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	m, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("expected no settings, got %v", m)
	}

	if err := st.PutSetting(ctx, "ai.model", "gpt-4o"); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	if err := st.PutSetting(ctx, "ai.model", "gpt-4o-mini"); err != nil { // upsert
		t.Fatalf("PutSetting (upsert): %v", err)
	}
	if err := st.PutSetting(ctx, "ai.base_url", "https://example.com/v1"); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	m, err = st.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if m["ai.model"] != "gpt-4o-mini" {
		t.Fatalf("upsert failed: %q", m["ai.model"])
	}
	if m["ai.base_url"] != "https://example.com/v1" {
		t.Fatalf("unexpected base_url: %q", m["ai.base_url"])
	}
}

func TestCategories(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Migration 0005 seeds three default categories.
	cats, err := st.ListCategories(ctx)
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(cats) != 3 {
		t.Fatalf("expected 3 seeded categories, got %d", len(cats))
	}

	c := &models.Category{Name: "Museum", Icon: "🏛️", SortOrder: 4}
	if err := st.CreateCategory(ctx, c); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if c.ID == uuid.Nil {
		t.Fatal("category ID was not assigned")
	}
	if cats, _ = st.ListCategories(ctx); len(cats) != 4 {
		t.Fatalf("expected 4 categories after insert, got %d", len(cats))
	}

	// The unique index is case-insensitive.
	if err := st.CreateCategory(ctx, &models.Category{Name: "museum"}); err == nil {
		t.Fatal("expected error creating a case-insensitive duplicate category")
	}

	if err := st.DeleteCategory(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}
	if cats, _ = st.ListCategories(ctx); len(cats) != 3 {
		t.Fatalf("expected 3 categories after delete, got %d", len(cats))
	}
	if err := st.DeleteCategory(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting missing category, got %v", err)
	}
}

func TestStatsBackupRestore(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{
		Title: "Trip", Destination: "X",
		StartDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), // 3 inclusive days
		People:    2,
	}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	if err := st.CreateSight(ctx, &models.Sight{VacationID: v.ID, Name: "A"}); err != nil {
		t.Fatalf("CreateSight: %v", err)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Vacations != 1 || stats.Days != 3 || stats.Sights != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := st.BackupTo(ctx, backup); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	if !ValidSQLiteFile(backup) {
		t.Fatal("backup is not a valid SQLite file")
	}

	if err := st.DeleteVacation(ctx, v.ID); err != nil {
		t.Fatalf("DeleteVacation: %v", err)
	}
	if list, _ := st.ListVacations(ctx); len(list) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(list))
	}

	if err := st.Restore(ctx, backup); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	list, err := st.ListVacations(ctx)
	if err != nil || len(list) != 1 || list[0].Title != "Trip" {
		t.Fatalf("restore failed: err=%v list=%+v", err, list)
	}
	if _, err := st.GetSettings(ctx); err != nil {
		t.Fatalf("GetSettings after restore: %v", err)
	}
}
