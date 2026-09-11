package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func dashboardHTML(t *testing.T, s *Server) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.Header.Set("Accept-Language", "de")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestDashboardArchiveIsManualAndPastOnly(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	past := &models.Vacation{Title: "Past trip", Destination: "Paris", StartDate: today.AddDate(0, 0, -5), EndDate: today.AddDate(0, 0, -1)}
	ongoing := &models.Vacation{Title: "Ongoing trip", Destination: "Rome", StartDate: today.AddDate(0, 0, -1), EndDate: today}
	future := &models.Vacation{Title: "Future trip", Destination: "London", StartDate: today.AddDate(0, 0, 5), EndDate: today.AddDate(0, 0, 10)}
	for _, v := range []*models.Vacation{past, ongoing, future} {
		if err := s.store.CreateVacation(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	html := dashboardHTML(t, s)
	archiveSection := strings.Index(html, `<section id="past-vacations"`)
	if archiveSection < 0 || !strings.Contains(html, "Deine geplanten Urlaube") || !strings.Contains(html, "Vergangene Urlaube") {
		t.Fatal("dashboard sections missing")
	}
	if !strings.Contains(html[:archiveSection], `id="vacation-`+past.ID.String()+`"`) {
		t.Fatal("past trip was automatically moved")
	}
	for _, v := range []*models.Vacation{ongoing, future} {
		if strings.Contains(html, `action="/vacations/`+v.ID.String()+`/archive"`) {
			t.Fatal("active trip offered archiving")
		}
	}
	path := "/vacations/" + past.ID.String() + "/archive"
	if !strings.Contains(html, `action="`+path+`"`) {
		t.Fatal("past trip has no archive action")
	}
	if rec := postAISettings(s, path, nil, false); rec.Code != http.StatusForbidden {
		t.Fatal("archive bypassed CSRF")
	}
	for _, v := range []*models.Vacation{ongoing, future} {
		if rec := postAISettings(s, "/vacations/"+v.ID.String()+"/archive", nil, true); rec.Code != http.StatusUnprocessableEntity {
			t.Fatal("active trip archived")
		}
	}
	for range 2 {
		rec := postAISettings(s, path, nil, true)
		if rec.Code != http.StatusOK || rec.Header().Get("HX-Redirect") != "/" {
			t.Fatalf("archive: %d %s", rec.Code, rec.Body.String())
		}
	}
	html = dashboardHTML(t, s)
	archiveSection = strings.Index(html, `<section id="past-vacations"`)
	if strings.Contains(html[:archiveSection], `id="vacation-`+past.ID.String()+`"`) || !strings.Contains(html[archiveSection:], `id="vacation-`+past.ID.String()+`"`) {
		t.Fatal("archive did not move the card to the lower section")
	}
	if strings.Contains(html, `action="`+path+`"`) {
		t.Fatal("archived card still offers archiving")
	}
	if _, err := s.store.GetVacation(ctx, past.ID); err != nil {
		t.Fatal("archived trip data disappeared", err)
	}
}
