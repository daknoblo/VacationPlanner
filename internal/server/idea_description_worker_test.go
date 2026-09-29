package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIdeaDescriptionsOnlyRunInWorkerAndRemainCached(t *testing.T) {
	s, backend, v := newCheatsheetTest(t)
	backend.response = []byte(`{"choices":[{"message":{"content":"{\"en\":\"A history museum.\",\"de\":\"Ein Geschichtsmuseum.\"}"}}]}`)
	item := &models.Item{VacationID: v.ID, Title: "Museum", Notes: "Private note"}
	if err := s.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	path := "/vacations/" + v.ID.String() + "/api/ideas-map"
	var result ideasMapPayload
	readTripJSON(t, s, path, &result)
	if backend.calls != 0 || result.Ideas[0].DescriptionStatus != "pending" {
		t.Fatal("GET must not call AI", result)
	}
	if view, err := s.backgroundStatus(t.Context()); err != nil || !view.Active || view.Determinate {
		t.Fatal("pending description missing from progress", view, err)
	}
	if err := s.prepareIdeaDescription(t.Context()); err != nil {
		t.Fatal(err)
	}
	readTripJSON(t, s, path, &result)
	if result.Ideas[0].Description != "A history museum." || backend.calls != 1 {
		t.Fatal("description not cached", result, backend.calls)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Ein Geschichtsmuseum.") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if err := s.prepareIdeaDescription(t.Context()); err != nil || backend.calls != 1 {
		t.Fatal("repeated provider work", err)
	}
	other := &models.Item{VacationID: v.ID, Title: "Unknown"}
	if err := s.store.CreateItem(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	backend.fail = true
	if err := s.prepareIdeaDescription(t.Context()); err == nil {
		t.Fatal("provider failure suppressed")
	}
	if err := s.prepareIdeaDescription(t.Context()); err != nil || backend.calls != 2 {
		t.Fatal("failed description retried automatically", err)
	}
}

func TestIdeaDescriptionWorkerCancellation(t *testing.T) {
	s, _, v := newCheatsheetTest(t)
	backend := &blockingCheatsheetBackend{entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.ai = ai.New(backend)
	item := &models.Item{VacationID: v.ID, Title: "Museum"}
	if err := s.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	stop := s.StartIdeaDescriptionWorker(context.Background())
	t.Cleanup(stop)
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("description worker did not start")
	}
	s.StartIdeaDescriptionWorker(context.Background())
	stop()
	saved, err := s.store.ListIdeaDescriptions(t.Context(), v.ID)
	if err != nil || saved[item.ID].Status != "unavailable" || backend.count.Load() != 1 {
		t.Fatal("cancelled request replayed or unfinished", saved, err)
	}
}

func TestMapDaySchedulingPreservesOriginalIdea(t *testing.T) {
	s := newIntegrationServer(t)
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	cost := 25.0
	item := &models.Item{VacationID: v.ID, Title: "Museum", Description: "A museum. More detail.",
		Cost: &cost, Notes: "Keep notes", Latitude: fptr(55), Longitude: fptr(8),
		Links: []models.ItemLink{{Kind: "website", URL: "https://example.com/"}}}
	if err := s.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	path := "/items/" + item.ID.String() + "/schedule"
	form := url.Values{"day": {v.StartDate.Format("2006-01-02")}, "day_only": {"1"}}
	if rec := postAISettings(s, path, form, false); rec.Code != http.StatusForbidden {
		t.Fatal("missing CSRF accepted", rec.Code)
	}
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	got, err := s.store.GetItem(t.Context(), item.ID)
	if err != nil || got.Day == nil || got.Timed() || got.Description != item.Description ||
		got.Notes != item.Notes || got.Cost == nil || *got.Cost != cost || len(got.Links) != 1 || !got.HasCoords() {
		t.Fatal("date-only schedule lost data or invented time", got, err)
	}
	got.StartMin, got.EndMin = 600, 660
	if err := s.store.UpdateItem(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	form.Set("day", v.EndDate.Format("2006-01-02"))
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `class="planner-block"`) || !strings.Contains(rec.Body.String(), "10:00") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	got, err = s.store.GetItem(t.Context(), item.ID)
	if err != nil || got.StartMin != 600 || got.EndMin != 660 || !got.OnDay(v.EndDate) {
		t.Fatal("existing times changed", got, err)
	}
	form.Set("day", v.EndDate.AddDate(0, 0, 1).Format("2006-01-02"))
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusConflict {
		t.Fatal("out-of-trip day accepted", rec.Code)
	}
	var result ideasMapPayload
	readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/ideas-map", &result)
	if len(result.Ideas) != 1 || result.Ideas[0].Description != "A museum." ||
		result.Ideas[0].ScheduledDay != v.EndDate.Format("2006-01-02") || len(result.Days) != len(v.Days()) {
		t.Fatal("map description or saved day missing", result)
	}
	var counts map[string]int
	readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/daycounts", &counts)
	if counts[v.EndDate.Format("2006-01-02")] != 1 {
		t.Fatal("scheduled original missing from day planner", counts)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/vacations/"+v.ID.String()+"/api/daycards?day="+v.EndDate.Format("2006-01-02"), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), item.ID.String()) {
		t.Fatal("scheduled original missing from rendered day cards", rec.Code, rec.Body.String())
	}
}
