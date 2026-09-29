package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func TestRoutingEnrichesMissingLocationsBeforePreparingSavedRoutes(t *testing.T) {
	var lookups, routes atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		lookups.Add(1)
		_, _ = w.Write([]byte(`[{"name":"Blue Museum","type":"museum","lat":"55.1","lon":"9.1","address":{"state":"Region","country":"Norway"}}]`))
	})
	v.Latitude, v.Longitude = fptr(55), fptr(9)
	if err := st.UpdateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	lodging := &models.Lodging{VacationID: v.ID, Name: "Stay", Latitude: fptr(55), Longitude: fptr(9), Region: "Region, Norway", CheckIn: v.StartDate, CheckOut: v.EndDate}
	if err := st.CreateLodging(t.Context(), lodging); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Blue Museum", Cost: fptr(25), Notes: "Keep this"}
	ambiguous := &models.Item{VacationID: v.ID, Title: "Museum"}
	for _, idea := range []*models.Item{item, ambiguous} {
		if err := st.CreateItem(t.Context(), idea); err != nil {
			t.Fatal(err)
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		routes.Add(1)
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":1234,"duration":567},"geometry":"_eunI_y|u@_pR_pR"}]}`))
	}))
	defer provider.Close()
	s.routing = route.New("test")
	if err := st.PutSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !s.geographyStatus(v.ID).Pending || routes.Load() != 0 || lookups.Load() != 0 {
		t.Fatal("routing must queue background geocoding before an unlocated route can be requested")
	}
	s.geography.run(t.Context(), <-s.geography.queue)
	got, err := st.GetItem(t.Context(), item.ID)
	if err != nil || !got.HasCoords() || got.Title != item.Title || got.Notes != item.Notes || got.Cost == nil || *got.Cost != 25 {
		t.Fatal("background lookup must safely preserve the idea", got, err)
	}
	unlocated, err := st.GetItem(t.Context(), ambiguous.ID)
	if err != nil || unlocated.HasCoords() {
		t.Fatal("ambiguous place was guessed", unlocated, err)
	}
	s.geography.mu.Lock()
	s.geography.states[v.ID].finished = time.Now().Add(-2 * geographyCooldown)
	s.geography.mu.Unlock()
	for range 3 {
		if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := st.ListIdeaRoutes(t.Context(), provider.URL, v.ID, lodging.ID)
	if err != nil || len(saved) != 1 || saved[0].Status != "ready" || saved[0].DistanceM != 1234 || len(saved[0].Geometry) != 2 {
		t.Fatal("enriched idea did not become a persisted route", saved, err)
	}
	if routes.Load() != 1 || lookups.Load() != 2 || s.geographyStatus(v.ID).Pending {
		t.Fatal("completed routes or ambiguous places were retried automatically", routes.Load(), lookups.Load())
	}
	s.retryGeography(v.ID, "en")
	if !s.geographyStatus(v.ID).Pending {
		t.Fatal("an explicit location retry must remain available")
	}
}

func TestMissingLocationWarningsAndCleanPlannerHeader(t *testing.T) {
	s := newIntegrationServer(t)
	v, _, located := seedIdeaRoute(t, s)
	missing := &models.Item{VacationID: v.ID, Title: "Missing place", Region: "Manual region", RegionManual: true}
	scheduled := &models.Item{VacationID: v.ID, Title: "Scheduled missing place", Day: &v.StartDate, StartMin: 600, EndMin: 660}
	for _, item := range []*models.Item{missing, scheduled} {
		if err := s.store.CreateItem(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+v.ID.String(), nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, body)
	}
	for _, item := range []*models.Item{missing, scheduled} {
		if !strings.Contains(body, `href="#idea-location-`+item.ID.String()+`"`) ||
			!strings.Contains(body, `data-idea-location-edit="`+item.ID.String()+`"`) {
			t.Fatal("missing location must link to its original editor", item.ID)
		}
	}
	if strings.Contains(body, `class="idea-location-warning" href="#idea-location-`+located.ID.String()+`"`) {
		t.Fatal("located idea incorrectly shows a location warning")
	}
	planner := strings.SplitN(strings.SplitN(body, `data-tab-panel="tagesplan"`, 2)[1], `data-tab-panel=`, 2)[0]
	if strings.Contains(planner, "data-geography-refresh") || strings.Contains(planner, "data-planner-geography-status") ||
		strings.Contains(planner, "planner-region-tools") {
		t.Fatal("obsolete location controls remain above the planner")
	}
	if !strings.Contains(body, `aria-label="Location missing`) || !strings.Contains(body, `class="idea-location-warning"`) {
		t.Fatal("location warning needs an accessible label and visible circle")
	}
}
