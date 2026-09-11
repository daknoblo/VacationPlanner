package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func readTripJSON(t *testing.T, s *Server, path string, target any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatal(err)
	}
}

func TestOverviewMapOnlyUsesRecordedAccommodations(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	v := &models.Vacation{Title: "Trip", Destination: "Denmark", StartDate: start, EndDate: start.AddDate(0, 0, 3)}
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	lat, lng := 55.7, 12.6
	for _, item := range []models.Item{
		{VacationID: v.ID, Title: "Unscheduled idea", Latitude: &lat, Longitude: &lng},
		{VacationID: v.ID, Title: "Planned POI", Day: &start, Latitude: &lat, Longitude: &lng},
		{VacationID: v.ID, Title: "An idea labeled Hotel", Category: "Hotel", Latitude: &lat, Longitude: &lng},
	} {
		if err := s.store.CreateItem(ctx, &item); err != nil {
			t.Fatal(err)
		}
	}
	mapPath := "/vacations/" + v.ID.String() + "/api/overview-map"
	var empty overviewMapPayload
	readTripJSON(t, s, mapPath, &empty)
	if empty.Center != nil || len(empty.Lodgings) != 0 {
		t.Fatal("ideas affected the accommodation-only map or its center")
	}
	for _, name := range []string{"Arrival hotel", "Mid-trip cottage", "Departure hotel"} {
		lodging := &models.Lodging{VacationID: v.ID, Name: name, Latitude: &lat, Longitude: &lng, CheckIn: start, CheckOut: v.EndDate}
		if err := s.store.CreateLodging(ctx, lodging); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.CreateLodging(ctx, &models.Lodging{VacationID: v.ID, Name: "Unlocated stay", CheckIn: start, CheckOut: v.EndDate}); err != nil {
		t.Fatal(err)
	}
	var payload overviewMapPayload
	readTripJSON(t, s, mapPath, &payload)
	if len(payload.Lodgings) != 3 {
		t.Fatalf("want exactly three located stays, got %+v", payload)
	}
	for _, marker := range payload.Lodgings {
		if marker.Title != "Arrival hotel" && marker.Title != "Mid-trip cottage" && marker.Title != "Departure hotel" {
			t.Fatalf("non-accommodation marker: %+v", marker)
		}
	}
	var original itemsPayload
	readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/items", &original)
	if len(original.Items) != 3 {
		t.Fatal("the general item API was stripped of POIs")
	}
}

func TestDayCountsIncludeUntimedItemsAndTrackScheduleChanges(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	first := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	second, third, outside := first.AddDate(0, 0, 1), first.AddDate(0, 0, 2), first.AddDate(0, 0, 3)
	v := &models.Vacation{Title: "Trip", Destination: "Denmark", StartDate: first, EndDate: third}
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	items := []models.Item{
		{VacationID: v.ID, Title: "Timed", Day: &first, StartMin: 600, EndMin: 660},
		{VacationID: v.ID, Title: "Untimed", Day: &first},
		{VacationID: v.ID, Title: "Visited", Day: &second, Visited: true},
		{VacationID: v.ID, Title: "Idea"},
		{VacationID: v.ID, Title: "Outside trip", Day: &outside},
	}
	for i := range items {
		if err := s.store.CreateItem(ctx, &items[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.CreateLodging(ctx, &models.Lodging{VacationID: v.ID, Name: "Stay", CheckIn: first, CheckOut: third}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateTravelSegment(ctx, &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, DepartAt: &first}); err != nil {
		t.Fatal(err)
	}
	path := "/vacations/" + v.ID.String() + "/api/daycounts"
	var counts map[string]int
	readTripJSON(t, s, path, &counts)
	if len(counts) != 3 || counts["2026-10-09"] != 2 || counts["2026-10-10"] != 1 || counts["2026-10-11"] != 0 {
		t.Fatalf("incorrect activity counts: %v", counts)
	}
	v.Items = items
	for _, week := range buildWeekCalendar(i18n.NewLocalizer(i18n.LangEN), time.UTC, true, v) {
		for _, day := range week.Days {
			if day != nil && day.ItemCount != counts[day.Date.Format("2006-01-02")] {
				t.Fatalf("server-rendered calendar has a different count: %+v", day)
			}
		}
	}
	for _, update := range []struct {
		item int
		day  string
	}{{0, "2026-10-10"}, {3, "2026-10-11"}} {
		rec := postAISettings(s, "/items/"+items[update.item].ID.String()+"/schedule", url.Values{"day": {update.day}, "start": {"10:00"}, "end": {"11:00"}}, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("schedule: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, "/items/"+items[1].ID.String(), nil)
	req.Header.Set("X-CSRF-Token", s.newCSRFToken())
	req.Header.Set("HX-Request", "true")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	readTripJSON(t, s, path, &counts)
	if counts["2026-10-09"] != 0 || counts["2026-10-10"] != 2 || counts["2026-10-11"] != 1 {
		t.Fatalf("counts did not follow move/delete: %v", counts)
	}
}
