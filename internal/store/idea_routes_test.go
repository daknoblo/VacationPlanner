package store

import (
	"errors"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

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
