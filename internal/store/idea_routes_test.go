package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestActivityRoutePersistenceSourcesAndIdeaCacheReuse(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	day := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	vacation := &models.Vacation{Title: "Trip", StartDate: day, EndDate: day}
	if err := store.CreateVacation(ctx, vacation); err != nil {
		t.Fatal(err)
	}
	latitude, longitude := 55.0, 8.0
	item := &models.Item{VacationID: vacation.ID, Title: "Stop", Day: &day, Latitude: &latitude, Longitude: &longitude}
	if err := store.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	source := &models.ActivityRoute{VacationID: vacation.ID, ItemID: item.ID,
		FromLat: 54, FromLng: 8, ToLat: latitude, ToLng: longitude, Status: "ready", DistanceM: 1000, DurationS: 120}
	if saved, err := store.PutActivityRoute(ctx, source); err != nil || !saved {
		t.Fatal(saved, err)
	}
	path := store.path
	store.Close()
	var err error
	store, err = NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if result, err := store.GetActivityRoute(ctx, source); err != nil || result.DistanceM != 1000 || result.DurationS != 120 {
		t.Fatal("saved route did not survive reopen", result, err)
	}
	changed := *source
	changed.FromLat++
	if _, err := store.GetActivityRoute(ctx, &changed); !errors.Is(err, ErrNotFound) {
		t.Fatal("old origin cache matched new coordinates", err)
	}
	changed = *source
	changed.Provider = "https://different.example"
	if _, err := store.GetActivityRoute(ctx, &changed); !errors.Is(err, ErrNotFound) {
		t.Fatal("old provider cache matched new provider", err)
	}
	if saved, err := store.PutActivityRoute(ctx, &changed); err != nil || saved {
		t.Fatal("stale provider write succeeded", saved, err)
	}
	latitude = 56
	if err := store.UpdateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if saved, err := store.PutActivityRoute(ctx, source); err != nil || saved {
		t.Fatal("stale destination write succeeded", saved, err)
	}
	if _, err := store.GetActivityRoute(ctx, source); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale destination was readable", err)
	}
	source.ToLat = latitude
	lodging := &models.Lodging{VacationID: vacation.ID, Name: "Stay", CheckIn: day, CheckOut: day,
		Latitude: &source.FromLat, Longitude: &source.FromLng}
	if err := store.CreateLodging(ctx, lodging); err != nil {
		t.Fatal(err)
	}
	ideaRoute := &models.IdeaRoute{VacationID: vacation.ID, LodgingID: lodging.ID, ItemID: item.ID,
		FromLat: source.FromLat, FromLng: source.FromLng, ToLat: latitude, ToLng: longitude,
		Status: "ready", DistanceM: 2000, DurationS: 240}
	if saved, err := store.PutIdeaRoute(ctx, ideaRoute); err != nil || !saved {
		t.Fatal(saved, err)
	}
	if result, err := store.GetActivityRoute(ctx, source); err != nil || result.DistanceM != 2000 {
		t.Fatal("existing idea route was not reused", result, err)
	}
	item.Day = nil
	if err := store.UpdateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetActivityRoute(ctx, source); !errors.Is(err, ErrNotFound) {
		t.Fatal("unscheduled item retained an active day route", err)
	}
	if err := store.DeleteItem(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if saved, err := store.PutActivityRoute(ctx, source); err != nil || saved {
		t.Fatal("deleted item was restored by routing", saved, err)
	}
}

func TestOverviewIdeaRoutesUseCurrentShortestSavedPairs(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	lat, lng := 55.0, 8.0
	v := &models.Vacation{Title: "Trip", Destination: "Test", StartDate: time.Now(), EndDate: time.Now().Add(72 * time.Hour)}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	items := make([]models.Item, 2)
	for n := range items {
		items[n] = models.Item{VacationID: v.ID, Title: "Idea", Latitude: &lat, Longitude: &lng}
		if err := st.CreateItem(ctx, &items[n]); err != nil {
			t.Fatal(err)
		}
	}
	lodgings := make([]models.Lodging, 3)
	for n := range lodgings {
		lodgings[n] = models.Lodging{VacationID: v.ID, Name: "Stay", Latitude: &lat, Longitude: &lng,
			CheckIn: v.StartDate.Add(time.Duration(n) * 24 * time.Hour), CheckOut: v.EndDate}
		if err := st.CreateLodging(ctx, &lodgings[n]); err != nil {
			t.Fatal(err)
		}
		for j, item := range items {
			distance, duration, status := 9000.0, 800.0, "ready"
			if n == 0 {
				duration = 900
			}
			if j == 1 {
				distance = float64(n+1) * 5000
				if n == 2 {
					distance, status = 0, "unavailable"
				}
			}
			job := &models.IdeaRoute{VacationID: v.ID, LodgingID: lodgings[n].ID, ItemID: item.ID,
				FromLat: lat, FromLng: lng, ToLat: lat, ToLng: lng,
				Status: status, DistanceM: distance, DurationS: duration, Geometry: [][2]float64{{lat, lng}, {lat, lng}}}
			if saved, err := st.PutIdeaRoute(ctx, job); err != nil || !saved {
				t.Fatal(saved, err)
			}
		}
	}
	check := func(first uuid.UUID) {
		t.Helper()
		result, err := st.ListIdeaRoutes(ctx, "", v.ID, uuid.Nil)
		if err != nil || len(result) != 2 {
			t.Fatal(result, err)
		}
		for _, value := range result {
			want := lodgings[0].ID
			if value.ItemID == items[0].ID {
				want = first
			}
			if value.Status != "ready" || value.LodgingID != want || len(value.Geometry) != 0 {
				t.Fatalf("incorrect shortest route or unnecessary geometry: %+v", value)
			}
		}
	}
	check(lodgings[1].ID)
	lodgings[2].CheckIn = lodgings[1].CheckIn
	if err := st.UpdateLodging(ctx, &lodgings[2]); err != nil {
		t.Fatal(err)
	}
	want := lodgings[1].ID
	if lodgings[2].ID.String() < want.String() {
		want = lodgings[2].ID
	}
	check(want)
	newLat := 56.0
	lodgings[1].Latitude = &newLat
	if err := st.UpdateLodging(ctx, &lodgings[1]); err != nil {
		t.Fatal(err)
	}
	check(lodgings[2].ID)
	if err := st.DeleteLodging(ctx, lodgings[2].ID); err != nil {
		t.Fatal(err)
	}
	check(lodgings[0].ID)
	if result, err := st.ListIdeaRoutes(ctx, "changed-provider", v.ID, uuid.Nil); err != nil || len(result) != 0 {
		t.Fatal("overview reused another provider's cache", result, err)
	}
}

func TestIdeaRoutesDurableDerivedQueueAndCoordinateCAS(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	lat, lng, to := 55.0, 8.0, 56.0
	v := &models.Vacation{Title: "Trip", Destination: "Test", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	l := &models.Lodging{VacationID: v.ID, Name: "House", CheckIn: v.StartDate, CheckOut: v.EndDate, Latitude: &lat, Longitude: &lng}
	i := &models.Item{VacationID: v.ID, Title: "Idea", Latitude: &to, Longitude: &lng, Notes: "Preserve me"}
	if err := st.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateItem(ctx, i); err != nil {
		t.Fatal(err)
	}
	progress, err := st.IdeaRouteProgress(ctx, "", v.ID)
	if err != nil || progress.Total != 1 || progress.Completed != 0 {
		t.Fatal(progress, err)
	}
	job, err := st.NextIdeaRoute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if job.LodgingID != l.ID || job.ItemID != i.ID {
		t.Fatal("wrong pair")
	}
	job.Status, job.DistanceM, job.DurationS = "ready", 12000, 900
	job.Geometry = [][2]float64{{55, 8}, {55.5, 8.2}, {56, 8}}
	if saved, err := st.PutIdeaRoute(ctx, job); err != nil || !saved {
		t.Fatal(saved, err)
	}
	path := st.path
	st.Close()
	st, err = NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.NextIdeaRoute(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("ready pair queued again", err)
	}
	results, err := st.ListIdeaRoutes(ctx, "", v.ID, l.ID)
	if err != nil || len(results) != 1 || len(results[0].Geometry) != 3 {
		t.Fatal(results, err)
	}
	if err := st.RetryIdeaRoutes(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.NextIdeaRoute(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("retry erased successful results")
	}
	nextLat := 57.0
	i.Latitude = &nextLat
	if err := st.UpdateItem(ctx, i); err != nil {
		t.Fatal(err)
	}
	if saved, err := st.PutIdeaRoute(ctx, job); err != nil || saved {
		t.Fatal("stale coordinates committed", saved, err)
	}
	results, err = st.ListIdeaRoutes(ctx, "", v.ID, l.ID)
	if err != nil || len(results) != 0 {
		t.Fatal("stale metrics served", results, err)
	}
	job, err = st.NextIdeaRoute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	job.Status = "unavailable"
	if saved, err := st.PutIdeaRoute(ctx, job); err != nil || !saved {
		t.Fatal(saved, err)
	}
	progress, err = st.IdeaRouteProgress(ctx, "", v.ID)
	if err != nil || progress.Completed != 1 || progress.Failed != 1 {
		t.Fatal(progress, err)
	}
	if _, err := st.NextIdeaRoute(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed pair retried automatically")
	}
	if err := st.RetryIdeaRoutes(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.NextIdeaRoute(ctx, ""); err != nil {
		t.Fatal("failed pair not retried", err)
	}
	if err := st.PutSetting(ctx, "route.base_url", "https://example.test"); err != nil {
		t.Fatal(err)
	}
	if saved, err := st.PutIdeaRoute(ctx, job); err != nil || saved {
		t.Fatal("old provider committed", saved, err)
	}
	job, err = st.NextIdeaRoute(ctx, "https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteItem(ctx, i.ID); err != nil {
		t.Fatal(err)
	}
	job.Status = "unavailable"
	if saved, err := st.PutIdeaRoute(ctx, job); err != nil || saved {
		t.Fatal("deleted idea revived", saved, err)
	}
	progress, err = st.IdeaRouteProgress(ctx, "https://example.test", v.ID)
	if err != nil || progress.Total != 0 {
		t.Fatal(progress, err)
	}
}
