package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func regionDate(t *testing.T, date string) time.Time {
	t.Helper()
	d, err := time.Parse(time.RFC3339, date)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCalendarRegionsFollowAccommodations(t *testing.T) {
	loc := i18n.NewLocalizer(i18n.LangEN)
	start := regionDate(t, "2026-10-09T00:00:00Z")
	v := &models.Vacation{
		StartDate: start, EndDate: start.AddDate(0, 0, 7),
		Lodgings: []models.Lodging{
			{Name: "Zealand apartment", Region: "Zealand", CheckIn: start.AddDate(0, 0, 3).Add(15 * time.Hour), CheckOut: start.AddDate(0, 0, 6).Add(10 * time.Hour)},
			{Name: "Jutland guesthouse", Region: "Southern Denmark", CheckIn: start.Add(14 * time.Hour), CheckOut: start.AddDate(0, 0, 3).Add(10 * time.Hour)},
		},
		Items: []models.Item{{Title: "Unrelated activity", Region: "Another region", Day: &start}},
	}
	view := calendarRegions(loc, time.UTC, true, v)
	for _, day := range []string{"2026-10-09", "2026-10-10", "2026-10-11"} {
		if got := view.Days[day].Label; got != "Southern Denmark" {
			t.Errorf("%s: region %q", day, got)
		}
	}
	transfer := view.Days["2026-10-12"]
	if transfer.Label != "Southern Denmark · Zealand" || !strings.Contains(transfer.Title, "Jutland guesthouse") || !strings.Contains(transfer.Title, "Zealand apartment") {
		t.Errorf("transfer day must show both chronological lodging regions: %+v", transfer)
	}
	if view.Days["2026-10-15"].Label != "Zealand" || view.Days["2026-10-16"].Label != loc.T("planner.regions.no_lodging") {
		t.Fatal("checkout day or accommodation gap handled incorrectly")
	}
	week := view.Weeks["2026-10-05"]
	if len(week) != 1 || week[0].Start != 4 || week[0].Span != 3 {
		t.Fatalf("partial week must align Friday–Sunday: %+v", week)
	}
	if strings.Count(week[0].Title, "Jutland guesthouse") != 1 {
		t.Fatal("multi-day band repeats the same accommodation in its tooltip")
	}
	next := view.Weeks["2026-10-12"]
	if len(next) != 3 || next[0].Span != 1 || next[1].Start != 1 || next[1].Span != 3 || next[2].Start != 4 {
		t.Fatalf("expected transfer, Zealand, gap spans: %+v", next)
	}
	sunday := calendarRegions(loc, time.UTC, false, v)
	if first := sunday.Weeks["2026-10-04"]; len(first) != 1 || first[0].Start != 5 || first[0].Span != 2 {
		t.Fatalf("Sunday-start partial week: %+v", first)
	}
	weeks := buildWeekCalendar(loc, time.UTC, true, v)
	if weeks[0].Start != "2026-10-05" || weeks[0].Regions[0].Span != 3 {
		t.Fatal("week calendar omitted region spans")
	}
}

func TestCalendarRegionsTimezoneUnknownAndDeduplication(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	start := regionDate(t, "2026-10-24T00:00:00Z")
	v := &models.Vacation{
		StartDate: start, EndDate: start.AddDate(0, 0, 3),
		Lodgings: []models.Lodging{
			{Name: "Late check-in", Region: "Zealand", CheckIn: regionDate(t, "2026-10-24T22:30:00Z"), CheckOut: regionDate(t, "2026-10-25T23:00:00Z")},
			{Name: "Same area", Region: " zealand ", CheckIn: regionDate(t, "2026-10-25T15:00:00Z"), CheckOut: regionDate(t, "2026-10-26T09:00:00Z")},
			{Name: "Unknown hotel", CheckIn: regionDate(t, "2026-10-26T10:00:00Z"), CheckOut: regionDate(t, "2026-10-27T09:00:00Z")},
			{Name: "Invalid reversed stay", Region: "Not a stay", CheckIn: start.AddDate(0, 0, 1), CheckOut: start},
		},
	}
	loc := i18n.NewLocalizer(i18n.LangDE)
	view := calendarRegions(loc, tz, true, v)
	if view.Days["2026-10-24"].Label != loc.T("planner.regions.no_lodging") {
		t.Fatal("UTC check-in leaked into the preceding local calendar day")
	}
	if got := view.Days["2026-10-25"]; got.Label != "Zealand" || !strings.Contains(got.Title, "Same area") {
		t.Fatalf("overlapping bookings in same region must be deduplicated: %+v", got)
	}
	if got := view.Days["2026-10-26"].Label; got != "Zealand · "+loc.T("planner.regions.unknown") {
		t.Fatalf("midnight checkout and unknown next lodging must remain explicit: %s", got)
	}
	if got := view.Days["2026-10-27"].Label; got != loc.T("planner.regions.unknown") {
		t.Fatalf("missing region should not be guessed: %s", got)
	}
}

func TestCalendarRegionsEndpointReadOnlyAndRefresh(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := t.Context()
	start := regionDate(t, "2026-10-09T00:00:00Z")
	v := &models.Vacation{Title: "Regions", StartDate: start, EndDate: start.AddDate(0, 0, 3)}
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	l := &models.Lodging{VacationID: v.ID, Name: "Example lodging", Region: "Southern Denmark", CheckIn: start, CheckOut: v.EndDate}
	if err := s.store.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	base := "/vacations/" + v.ID.String()
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		return rec
	}
	rec := get(base + "/api/calendar-regions")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var view calendarRegionView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Days) != 4 || view.Days["2026-10-09"].Label != "Southern Denmark" {
		t.Fatalf("unexpected saved regions: %+v", view)
	}
	if len(s.geography.queue) != 0 || s.geographyStatus(v.ID).Pending {
		t.Fatal("reading calendar regions started provider work")
	}
	rec = get("/vacations/" + uuid.NewString() + "/api/calendar-regions")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing trip returned %d", rec.Code)
	}
	rec = get(base)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-region-week="2026-10-05"`) ||
		!strings.Contains(rec.Body.String(), `data-region-day="2026-10-09"`) ||
		!strings.Contains(rec.Body.String(), "Southern Denmark") ||
		!strings.Contains(rec.Body.String(), "data-geography-refresh") {
		t.Fatalf("region controls absent from real page: status=%d", rec.Code)
	}
	var csrf *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("page did not set CSRF cookie")
	}
	post := func(token bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, base+"/geography/refresh", nil)
		if token {
			req.AddCookie(csrf)
			req.Header.Set("X-CSRF-Token", csrf.Value)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(false); rec.Code != http.StatusForbidden {
		t.Fatalf("refresh accepted without CSRF: %d", rec.Code)
	}
	if rec := post(true); rec.Code != http.StatusNoContent || !strings.Contains(rec.Header().Get("HX-Trigger"), "itemsChanged") {
		t.Fatalf("refresh did not enqueue/update UI: %d %s", rec.Code, rec.Body.String())
	}
	if !s.geographyStatus(v.ID).Pending {
		t.Fatal("explicit refresh did not queue background work")
	}
	s.geography.mu.Lock()
	s.geography.states[v.ID].status.Pending = false
	s.geography.stopped = true
	s.geography.mu.Unlock()
	if rec := post(true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped worker reported success: %d", rec.Code)
	}
}
