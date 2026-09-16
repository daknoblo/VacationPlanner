package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

func TestBackgroundStatusUsesActiveJobsNotUnknownRegions(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	id := uuid.New()
	s.geography.states[id] = &geographyState{status: geographyStatus{UnknownRegions: 9, Error: true, Completed: 3, Total: 3}}
	view, err := s.backgroundStatus(ctx)
	if err != nil || view.Active {
		t.Fatal("unresolved regions incorrectly keep the indicator busy", err)
	}
	s.geography.states[id].status = geographyStatus{Pending: true, Completed: 3, Total: 10}
	view, err = s.backgroundStatus(ctx)
	if err != nil || !view.Active || !view.Determinate || view.Completed != 3 || view.Total != 10 {
		t.Fatalf("live geographic progress missing: %+v, %v", view, err)
	}
	s.aiDiscoveries.Add(1)
	view, err = s.backgroundStatus(ctx)
	if err != nil || !view.Active || view.Determinate || !strings.Contains(view.Detail, "deployment") {
		t.Fatalf("mixed work should be indeterminate: %+v, %v", view, err)
	}
}

func TestBackgroundStatusCountsQueuedAndRunningTranslations(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := &models.Vacation{Title: "Test", Destination: "Paris", StartDate: time.Now(), EndDate: time.Now()}
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	key, err := models.CheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	job := &models.CheatsheetJob{VacationID: v.ID, SourceLanguage: "en", DestinationKey: key, Key: "sheet", Status: "queued"}
	if reserved, err := s.store.ReserveCheatsheetJob(ctx, job, false); err != nil || !reserved {
		t.Fatal("job not reserved", err)
	}
	for _, state := range []string{"queued", "running", "ready", "failed"} {
		if err := s.store.SetCheatsheetJobStatus(ctx, job, state); err != nil {
			t.Fatal(err)
		}
		view, err := s.backgroundStatus(ctx)
		if err != nil || view.Active != (state == "queued" || state == "running") {
			t.Fatalf("incorrect status for %s: %+v %v", state, view, err)
		}
	}
}

func TestBackgroundStatusEndpointOnlyReadsLocalState(t *testing.T) {
	s, backend := foundryTestServer(t)
	s.aiDiscoveries.Add(1)
	for range 2 {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/background-status", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<progress") || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status not rendered: %d %s", rec.Code, rec.Body.String())
		}
	}
	if backend.calls != 0 || backend.refreshes != 0 || len(s.geography.states) != 0 {
		t.Fatal("status polling dispatched work")
	}
	s.aiDiscoveries.Add(-1)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/background-status", nil))
	if strings.Contains(rec.Body.String(), "<progress") {
		t.Fatal("idle status must not pretend to be loading")
	}
	if !strings.Contains(rec.Body.String(), `data-state="idle"`) || !strings.Contains(rec.Body.String(), "No active requests") {
		t.Fatal("idle status disappeared")
	}
	if backend.calls != 0 || backend.refreshes != 0 || len(s.geography.states) != 0 {
		t.Fatal("idle status polling dispatched work")
	}
}

func TestBackgroundIdleAndInitialStatusAreLocalized(t *testing.T) {
	s := newIntegrationServer(t)
	for _, language := range []string{"en", "de"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/background-status", nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: language})
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		want, initial := "No active requests", "Checking status"
		if language == "de" {
			want, initial = "Keine aktiven Abfragen", "Status wird geprüft"
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) || strings.Contains(rec.Body.String(), "<progress") {
			t.Fatalf("incorrect %s idle state: %s", language, rec.Body.String())
		}
		req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: language})
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-state="checking"`) ||
			!strings.Contains(rec.Body.String(), initial) {
			t.Fatalf("initial page has an empty status area: %s", rec.Body.String())
		}
	}
}

type brokenBackgroundStore struct{ store.Store }

func (brokenBackgroundStore) CountPendingCheatsheetJobs(context.Context) (int, error) {
	return 0, errors.New("status storage unavailable")
}

func TestBackgroundStatusReadFailureIsVisible(t *testing.T) {
	s := newIntegrationServer(t)
	s.store = brokenBackgroundStore{Store: s.store}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/background-status", nil))
	if !strings.Contains(rec.Body.String(), "Status unavailable") || strings.Contains(rec.Body.String(), "<progress") {
		t.Fatal("status failure was hidden or shown as active work")
	}
}
