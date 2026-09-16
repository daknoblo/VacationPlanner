package server

import (
	"context"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestGeographyLodgingRegionLookupAndPartialCoordinates(t *testing.T) {
	var calls atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/reverse" || r.URL.Query().Get("accept-language") != "en" {
			t.Errorf("unexpected lookup: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"display_name":"Hotel","lat":"50","lon":"-1","address":{"state":"Sussex","country":"United Kingdom"}}`))
	})
	ctx := context.Background()
	lat, lng := 50.0, -1.0
	entries := []*models.Lodging{
		{Name: "Located", Latitude: &lat, Longitude: &lng},
		{Name: "Cached", Latitude: &lat, Longitude: &lng, Region: "Existing region"},
		{Name: "Partial", Latitude: &lat},
	}
	for _, l := range entries {
		l.VacationID, l.CheckIn, l.CheckOut = v.ID, v.StartDate, v.EndDate
		if err := st.CreateLodging(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	stop := s.StartGeographyWorker(ctx)
	t.Cleanup(stop)
	s.queueGeography(v.ID, "de")
	status := waitGeography(t, s, v.ID)
	if status.Error || status.UnknownRegions != 0 || status.UnknownLodgingRegions != 1 ||
		status.UpdatedCount != 1 || status.Completed != 1 || calls.Load() != 1 ||
		len(status.Unresolved) != 1 || status.Unresolved[0] != "Partial" {
		t.Fatalf("wrong lodging progress: %+v, calls=%d", status, calls.Load())
	}
	for i, want := range []string{"Sussex, United Kingdom", "Existing region", ""} {
		got, err := st.GetLodging(ctx, entries[i].ID)
		if err != nil || got.Region != want {
			t.Fatalf("lodging %d: %+v %v", i, got, err)
		}
		if i == 2 && (got.Latitude == nil || got.Longitude != nil) {
			t.Fatalf("partial coordinates altered: %+v", got)
		}
	}
}

func TestGeographyLodgingRegionBoundedFollowup(t *testing.T) {
	var searches, reverses atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reverse" {
			reverses.Add(1)
			_, _ = w.Write([]byte(`{"display_name":"Hotel","lat":"50","lon":"-1","address":{"state":"Sussex","country":"United Kingdom"}}`))
			return
		}
		searches.Add(1)
		_, _ = w.Write([]byte(`[{"name":"Blue Harbour Hotel","type":"hotel","lat":"50","lon":"-1","address":{"road":"Quay","house_number":"12","city":"Brighton"}}]`))
	})
	ctx := context.Background()
	for range geographyLookupLimit {
		l := &models.Lodging{VacationID: v.ID, Name: "Blue Harbour Hotel", Location: "Quay 12, Brighton",
			CheckIn: v.StartDate, CheckOut: v.EndDate}
		if err := st.CreateLodging(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	// Run each queued batch directly to inspect the boundary without timing races.
	s.queueGeography(v.ID, "de")
	s.geography.run(ctx, <-s.geography.queue)
	if status := s.geographyStatus(v.ID); !status.Pending || status.UnknownRegions != 0 ||
		status.UnknownLodgingRegions != geographyLookupLimit || status.UpdatedCount != geographyLookupLimit {
		t.Fatalf("missing bounded follow-up: %+v", status)
	}
	if len(s.geography.queue) != 1 || reverses.Load() != 0 {
		t.Fatalf("first batch exceeded its budget: queue=%d reverse=%d", len(s.geography.queue), reverses.Load())
	}
	lodgings, err := st.ListLodgings(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lodgings {
		if !l.HasCoords() || l.Region != "" {
			t.Fatalf("first batch should only locate accommodations: %+v", l)
		}
	}
	s.geography.run(ctx, <-s.geography.queue)
	status := s.geographyStatus(v.ID)
	if status.Error || status.Pending || status.Limited || status.UnknownLodgingRegions != 0 ||
		status.Completed != geographyLookupLimit || status.Total != geographyLookupLimit || len(s.geography.queue) != 0 {
		t.Fatalf("follow-up did not finish within budget: %+v", status)
	}
	if searches.Load() != 1 || reverses.Load() != 1 {
		t.Fatal("identical lookups did not use the geocoder cache")
	}
}

func TestGeographyLodgingFailedRegionDoesNotAutoRetry(t *testing.T) {
	var calls atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
	})
	ctx := context.Background()
	lat, lng := 50.0, -1.0
	l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Latitude: &lat, Longitude: &lng,
		CheckIn: v.StartDate, CheckOut: v.EndDate}
	if err := st.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	s.queueGeography(v.ID, "en")
	s.geography.run(ctx, <-s.geography.queue)
	status := s.geographyStatus(v.ID)
	if !status.Error || status.Pending || status.UnknownLodgingRegions != 1 || status.UnknownRegions != 0 {
		t.Fatalf("wrong failed region status: %+v", status)
	}
	s.queueGeography(v.ID, "en")
	if len(s.geography.queue) != 0 || calls.Load() != 1 {
		t.Fatal("failed reverse lookup automatically retried")
	}
}

func TestGeographyLodgingEditPreservesAndRefreshesRegion(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	lat, lng := 50.0, -1.0
	l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Location: "Quay", Latitude: &lat, Longitude: &lng,
		Region: "Sussex, United Kingdom", CheckIn: v.StartDate, CheckOut: v.EndDate}
	if err := s.store.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"name": {l.Name}, "location": {l.Location}, "latitude": {"50"}, "longitude": {"-1"}, "cost": {"250"},
		"checkin_date": {l.CheckIn.Format("2006-01-02")}, "checkin_time": {"15:00"},
		"checkout_date": {l.CheckOut.Add(24 * time.Hour).Format("2006-01-02")}, "checkout_time": {"10:00"},
	}
	if rec := postAISettings(s, "/lodging/"+l.ID.String(), form, true); rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	got, err := s.store.GetLodging(ctx, l.ID)
	if err != nil || got.Region != l.Region || got.Cost == nil || *got.Cost != 250 {
		t.Fatalf("cached region or cost lost: %+v %v", got, err)
	}
	if s.geographyStatus(v.ID).Pending {
		t.Fatal("booking-only edit unnecessarily queued geography")
	}
	form.Set("latitude", "51")
	if rec := postAISettings(s, "/lodging/"+l.ID.String(), form, true); rec.Code != http.StatusOK {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	got, err = s.store.GetLodging(ctx, l.ID)
	if err != nil || got.Region != "" || !s.geographyStatus(v.ID).Pending {
		t.Fatalf("moved lodging not queued for region refresh: %+v %v", got, err)
	}
}
