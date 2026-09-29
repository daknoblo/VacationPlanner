package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func TestSettingsRouteRetryIsScopedAndPreservesCompleteCache(t *testing.T) {
	s := newIntegrationServer(t)
	v, lodging, failed := seedIdeaRoute(t, s)
	other, otherLodging, otherItem := seedIdeaRoute(t, s)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	s.routing = route.New("test")
	s.cfg.RouterAPIKey = "test"
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	complete := &models.Item{VacationID: v.ID, Title: "Complete", Latitude: fptr(44), Longitude: fptr(-125)}
	incomplete := &models.Item{VacationID: v.ID, Title: "Missing geometry", Latitude: fptr(45), Longitude: fptr(-124)}
	for _, item := range []*models.Item{complete, incomplete} {
		if err := s.store.CreateItem(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct {
		lodging *models.Lodging
		item    *models.Item
		status  string
	}{
		{lodging, failed, "unavailable"},
		{lodging, complete, "ready"},
		{lodging, incomplete, "ready"},
		{otherLodging, otherItem, "unavailable"},
	} {
		job := &models.IdeaRoute{
			VacationID: entry.item.VacationID, LodgingID: entry.lodging.ID, ItemID: entry.item.ID,
			Provider: provider.URL, Status: entry.status,
			FromLat: *entry.lodging.Latitude, FromLng: *entry.lodging.Longitude,
			ToLat: *entry.item.Latitude, ToLng: *entry.item.Longitude,
			DistanceM: 1234, DurationS: 567,
		}
		if entry.item == complete {
			job.Geometry = [][2]float64{{job.FromLat, job.FromLng}, {job.ToLat, job.ToLng}}
		}
		if saved, err := s.store.PutIdeaRoute(t.Context(), job); err != nil || !saved {
			t.Fatal(saved, err)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/settings", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="vacation_id" required`) ||
		!strings.Contains(rec.Body.String(), `value="`+v.ID.String()+`"`) ||
		!strings.Contains(rec.Body.String(), `value="`+other.ID.String()+`"`) {
		t.Fatal("Settings must render the explicit trip selector", rec.Code)
	}
	path := "/settings/route/retry"
	values := url.Values{"vacation_id": {v.ID.String()}}
	if got := postAISettings(s, path, values, false); got.Code != http.StatusForbidden {
		t.Fatal("retry must require CSRF", got.Code)
	}
	for _, id := range []string{"", "invalid", uuid.NewString()} {
		got := postAISettings(s, path, url.Values{"vacation_id": {id}}, true)
		if got.Code != http.StatusUnprocessableEntity || got.Header().Get("HX-Retarget") != "#route-retry-status" {
			t.Fatal("invalid or deleted trips need a visible validation error", id, got.Code)
		}
	}
	before, err := s.store.ListIdeaRoutes(t.Context(), provider.URL, v.ID, lodging.ID)
	if err != nil || len(before) != 3 {
		t.Fatal("rejected requests modified the cache", before, err)
	}
	for range 2 {
		got := postAISettings(s, path, values, true)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "will be prepared in the background") {
			t.Fatal("missing queued confirmation", got.Code, got.Body.String())
		}
		if !s.geographyStatus(v.ID).Pending || s.geographyStatus(other.ID).Pending {
			t.Fatal("refresh must queue geography only for the selected trip")
		}
		saved, err := s.store.ListIdeaRoutes(t.Context(), provider.URL, v.ID, lodging.ID)
		if err != nil || len(saved) != 1 || saved[0].ItemID != complete.ID || saved[0].DistanceM != 1234 || len(saved[0].Geometry) != 2 {
			t.Fatal("complete cache must remain unchanged", saved, err)
		}

		untouched, err := s.store.ListIdeaRoutes(t.Context(), provider.URL, other.ID, otherLodging.ID)
		if err != nil || len(untouched) != 1 || untouched[0].Status != "unavailable" {
			t.Fatal("another trip was retried", untouched, err)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", s.newCSRFToken())
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings#settings-route-retry" {
		t.Fatal("non-HTMX fallback must return to Settings", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatal("Settings requests must not call the provider", calls.Load())
	}
}

func TestSettingsRefreshLocationsWithoutRoutingAndQueueFailure(t *testing.T) {
	s := newIntegrationServer(t)
	v, _, _ := seedIdeaRoute(t, s)
	s.routing = route.New("")
	values := url.Values{"vacation_id": {v.ID.String()}}
	got := postAISettings(s, "/settings/route/retry", values, true)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Driving routes are disabled") || !s.geographyStatus(v.ID).Pending {
		t.Fatal("location refresh must work without a routing key", got.Code, got.Body.String())
	}
	s.geography = newGeographyWorker(s)
	s.geography.stopped = true
	got = postAISettings(s, "/settings/route/retry", values, true)
	if got.Code != http.StatusServiceUnavailable {
		t.Fatal("failed geography scheduling must not report success", got.Code)
	}
}
