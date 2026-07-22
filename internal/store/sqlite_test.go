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

func TestItemRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}

	day := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	lat, lng, cost := 1.5, 2.5, 42.0
	it := &models.Item{
		VacationID: v.ID, Day: &day, Title: "Museum", Category: "Culture",
		StartMin: 600, EndMin: 720, Description: "Visit", Location: "Center",
		Latitude: &lat, Longitude: &lng, Cost: &cost,
	}
	if err := st.CreateItem(ctx, it); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	list, err := st.ListItems(ctx, v.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListItems: err=%v len=%d", err, len(list))
	}
	got := list[0]
	if got.Title != "Museum" || got.StartMin != 600 || got.EndMin != 720 || !got.OnDay(day) {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.HasCoords() || got.Cost == nil || *got.Cost != 42.0 || !got.Timed() {
		t.Fatalf("coords/cost/timed round-trip mismatch: %+v", got)
	}

	got.StartMin = 630
	got.Title = "Museum Tour"
	if err := st.UpdateItem(ctx, &got); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	again, err := st.GetItem(ctx, it.ID)
	if err != nil || again.StartMin != 630 || again.Title != "Museum Tour" {
		t.Fatalf("update not persisted: %+v err=%v", again, err)
	}

	if err := st.DeleteItem(ctx, it.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if l, _ := st.ListItems(ctx, v.ID); len(l) != 0 {
		t.Fatalf("expected 0 items after delete, got %d", len(l))
	}
}

func TestCascadeDelete(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	day := time.Now().UTC()
	if err := st.CreateItem(ctx, &models.Item{VacationID: v.ID, Title: "Tower", Visited: true}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	if err := st.CreateTravelSegment(ctx, &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, Mode: "flight"}); err != nil {
		t.Fatalf("CreateTravelSegment: %v", err)
	}
	if err := st.CreateItem(ctx, &models.Item{VacationID: v.ID, Day: &day, Title: "Walk", StartMin: 540, EndMin: 600}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Deleting the vacation must cascade (requires foreign_keys=ON).
	if err := st.DeleteVacation(ctx, v.ID); err != nil {
		t.Fatalf("DeleteVacation: %v", err)
	}
	if items, _ := st.ListItems(ctx, v.ID); len(items) != 0 {
		t.Fatalf("cascade failed: %d items remain", len(items))
	}
	if travel, _ := st.ListTravelSegments(ctx, v.ID); len(travel) != 0 {
		t.Fatalf("cascade failed: %d travel segments remain", len(travel))
	}
}

func TestItemAndTravelRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}

	planned := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	lat, lng := 1.5, 2.5
	it := &models.Item{VacationID: v.ID, Title: "T", Category: "c", Visited: true, Day: &planned, Latitude: &lat, Longitude: &lng}
	if err := st.CreateItem(ctx, it); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}
	items, _ := st.ListItems(ctx, v.ID)
	if len(items) != 1 || !items[0].Visited || items[0].Day == nil || !items[0].HasCoords() {
		t.Fatalf("item round-trip: %+v", items)
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
	it := &models.Item{VacationID: v.ID, Title: "T"}
	_ = st.CreateItem(ctx, it)

	it.Visited = true
	if err := st.UpdateItem(ctx, it); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	got, err := st.GetItem(ctx, it.ID)
	if err != nil || !got.Visited {
		t.Fatalf("visited not persisted: err=%v visited=%v", err, got.Visited)
	}
}

func TestDocuments(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatalf("CreateVacation: %v", err)
	}
	it := &models.Item{VacationID: v.ID, Title: "Ferry"}
	if err := st.CreateItem(ctx, it); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	// Item document round-trip: listing loads metadata only, reading loads bytes.
	idoc := &models.Document{ItemID: &it.ID, Filename: "ticket.pdf", ContentType: "application/pdf", Size: 5, Data: []byte("%PDF-")}
	if err := st.CreateDocument(ctx, idoc); err != nil {
		t.Fatalf("CreateDocument(item): %v", err)
	}
	idocs, err := st.ListItemDocuments(ctx, it.ID)
	if err != nil || len(idocs) != 1 || idocs[0].Filename != "ticket.pdf" || idocs[0].ItemID == nil {
		t.Fatalf("ListItemDocuments: err=%v docs=%+v", err, idocs)
	}
	if len(idocs[0].Data) != 0 {
		t.Fatalf("list should not load file bytes, got %d", len(idocs[0].Data))
	}
	full, err := st.ReadDocument(ctx, idoc.ID)
	if err != nil || string(full.Data) != "%PDF-" {
		t.Fatalf("ReadDocument: err=%v data=%q", err, full.Data)
	}

	// Travel document keyed by (vacation, kind, step).
	tdoc := &models.Document{VacationID: &v.ID, TravelKind: models.TravelArrival, TravelStep: 1, Filename: "boarding.png", ContentType: "image/png", Size: 3, Data: []byte("PNG")}
	if err := st.CreateDocument(ctx, tdoc); err != nil {
		t.Fatalf("CreateDocument(travel): %v", err)
	}
	tdocs, err := st.ListTravelDocuments(ctx, v.ID, models.TravelArrival, 1)
	if err != nil || len(tdocs) != 1 || !tdocs[0].IsImage() || tdocs[0].VacationID == nil {
		t.Fatalf("ListTravelDocuments: err=%v docs=%+v", err, tdocs)
	}
	if other, _ := st.ListTravelDocuments(ctx, v.ID, models.TravelArrival, 0); len(other) != 0 {
		t.Fatalf("expected 0 docs for step 0, got %d", len(other))
	}

	// DeleteTravelStepDocuments removes only that leg's documents.
	if err := st.DeleteTravelStepDocuments(ctx, v.ID, models.TravelArrival, 1); err != nil {
		t.Fatalf("DeleteTravelStepDocuments: %v", err)
	}
	if left, _ := st.ListTravelDocuments(ctx, v.ID, models.TravelArrival, 1); len(left) != 0 {
		t.Fatalf("travel docs not deleted: %d", len(left))
	}

	if err := st.DeleteDocument(ctx, idoc.ID); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if _, err := st.GetDocument(ctx, idoc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDocumentCascade(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	v := &models.Vacation{Title: "x", Destination: "y", StartDate: time.Now().UTC(), EndDate: time.Now().UTC()}
	_ = st.CreateVacation(ctx, v)
	it := &models.Item{VacationID: v.ID, Title: "T"}
	_ = st.CreateItem(ctx, it)

	idoc := &models.Document{ItemID: &it.ID, Filename: "a.pdf", ContentType: "application/pdf", Size: 1, Data: []byte("x")}
	if err := st.CreateDocument(ctx, idoc); err != nil {
		t.Fatalf("CreateDocument(item): %v", err)
	}
	tdoc := &models.Document{VacationID: &v.ID, TravelKind: models.TravelDeparture, TravelStep: 0, Filename: "b.pdf", ContentType: "application/pdf", Size: 1, Data: []byte("y")}
	if err := st.CreateDocument(ctx, tdoc); err != nil {
		t.Fatalf("CreateDocument(travel): %v", err)
	}

	// Deleting the item cascades its documents.
	if err := st.DeleteItem(ctx, it.ID); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}
	if _, err := st.GetDocument(ctx, idoc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("item document not cascaded: %v", err)
	}

	// Deleting the vacation cascades the travel document.
	if err := st.DeleteVacation(ctx, v.ID); err != nil {
		t.Fatalf("DeleteVacation: %v", err)
	}
	if _, err := st.GetDocument(ctx, tdoc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("travel document not cascaded: %v", err)
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
	if err := st.CreateItem(ctx, &models.Item{VacationID: v.ID, Title: "A"}); err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Vacations != 1 || stats.Days != 3 || stats.Items != 1 {
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
