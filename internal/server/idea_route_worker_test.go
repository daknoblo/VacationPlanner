package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func seedActivityRoute(t *testing.T, server *Server) (*models.Vacation, *models.Item) {
	t.Helper()
	vacation, _ := dayRouteFixture()
	vacation.Items = vacation.Items[1:2]
	if err := server.store.CreateVacation(t.Context(), vacation); err != nil {
		t.Fatal(err)
	}
	item := &vacation.Items[0]
	item.VacationID = vacation.ID
	item.Notes = "Keep the booking notes"
	item.Cost = fptr(42)
	if err := server.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	return vacation, item
}

func TestActivityWorkerDoesNotBlockPagesAndStopsCleanly(t *testing.T) {
	server := newIntegrationServer(t)
	vacation, item := seedActivityRoute(t, server)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	defer provider.Close()
	defer close(release)
	server.routing = route.New("test")
	if err := server.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := server.StartIdeaRouteWorker(ctx)
	defer stop()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled route did not start without a page visit")
	}
	status, err := server.backgroundStatus(t.Context())
	if err != nil || !status.Active || !strings.Contains(status.Detail, "Driving routes") {
		t.Fatal("activity worker is missing from background status", status, err)
	}
	for _, suffix := range []string{"", "/api/overview", "/api/dayroute?day=2026-08-03", "/api/daycards?day=2026-08-03"} {
		readCtx, cancelRead := context.WithTimeout(t.Context(), time.Second)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequestWithContext(readCtx, http.MethodGet, "/vacations/"+vacation.ID.String()+suffix, nil))
		cancelRead()
		if response.Code != http.StatusOK || calls.Load() != 1 {
			t.Fatalf("page blocked on active worker: path=%s status=%d calls=%d", suffix, response.Code, calls.Load())
		}
	}
	cancel()
	stop()
	sources, err := server.activityRouteSources(t.Context(), vacation.ID, provider.URL)
	if err != nil || len(sources) != 1 {
		t.Fatal(sources, err)
	}
	if _, err := server.store.GetActivityRoute(t.Context(), &sources[0]); !isNotFound(err) {
		t.Fatal("shutdown lost pending work", err)
	}
	stored, err := server.store.GetItem(t.Context(), item.ID)
	if err != nil || stored.Notes != item.Notes || stored.Cost == nil || *stored.Cost != 42 {
		t.Fatal("background work changed booking details", stored, err)
	}
}

func TestActivityWorkerFailureRequiresExplicitRetry(t *testing.T) {
	server := newIntegrationServer(t)
	vacation, _ := seedActivityRoute(t, server)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write([]byte(`{"routes":[{"summary":{"distance":1000,"duration":120}}]}`))
	}))
	defer provider.Close()
	server.routing = route.New("test")
	if err := server.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	if worked, err := server.prepareNextActivityRoute(t.Context()); !worked || err == nil {
		t.Fatal("failed attempt was not recorded", worked, err)
	}
	server.routing = route.New("test")
	for range 2 {
		if worked, err := server.prepareNextActivityRoute(t.Context()); worked || err != nil || calls.Load() != 1 {
			t.Fatal("failure was retried automatically", worked, err, calls.Load())
		}
	}
	if err := server.store.RetryIdeaRoutes(t.Context(), vacation.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := server.prepareNextActivityRoute(t.Context()); !worked || err != nil || calls.Load() != 2 {
		t.Fatal("explicit retry did not complete", worked, err, calls.Load())
	}
	if err := server.store.RetryIdeaRoutes(t.Context(), vacation.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := server.prepareNextActivityRoute(t.Context()); worked || err != nil || calls.Load() != 2 {
		t.Fatal("retry discarded a successful route", worked, err, calls.Load())
	}
	if server.routeRevision.Load() != 2 {
		t.Fatal("saved completions did not notify readers")
	}
}

func TestActivityWorkerDiscardsChangedOrigin(t *testing.T) {
	server := newIntegrationServer(t)
	vacation, _ := seedActivityRoute(t, server)
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		vacation.Latitude = fptr(49)
		if err := server.store.UpdateVacation(request.Context(), vacation); err != nil {
			t.Error(err)
		}
		_, _ = response.Write([]byte(`{"routes":[{"summary":{"distance":1000,"duration":120}}]}`))
	}))
	defer provider.Close()
	server.routing = route.New("test")
	if err := server.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	original, err := server.activityRouteSources(t.Context(), vacation.ID, provider.URL)
	if err != nil || len(original) != 1 {
		t.Fatal(original, err)
	}
	if worked, err := server.prepareNextActivityRoute(t.Context()); !worked || err != nil {
		t.Fatal(worked, err)
	}
	if _, err := server.store.GetActivityRoute(t.Context(), &original[0]); !isNotFound(err) {
		t.Fatal("changed origin accepted a stale result", err)
	}
	if server.routeRevision.Load() != 0 {
		t.Fatal("stale completion was reported as saved")
	}
}

func seedIdeaRoute(t *testing.T, s *Server) (*models.Vacation, *models.Lodging, *models.Item) {
	t.Helper()
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	l := &models.Lodging{VacationID: v.ID, Name: "Stay", CheckIn: v.StartDate, CheckOut: v.EndDate, Latitude: fptr(38.5), Longitude: fptr(-120.2)}
	i := &models.Item{VacationID: v.ID, Title: "Idea", Latitude: fptr(43.252), Longitude: fptr(-126.453)}
	if err := s.store.CreateLodging(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateItem(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	return v, l, i
}

func TestBackgroundRoutesProgressGeometryAndReadOnlyPolling(t *testing.T) {
	s := newIntegrationServer(t)
	v, l, i := seedIdeaRoute(t, s)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Geometry     bool  `json:"geometry"`
			Instructions *bool `json:"instructions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !body.Geometry || body.Instructions == nil || *body.Instructions {
			t.Errorf("background request must include geometry and omit turn instructions: %+v %v", body, err)
		}
		_, _ = w.Write([]byte("{\"routes\":[{\"geometry\":\"_p~iF~ps|U_ulLnnqC_mqNvxq`@\",\"summary\":{\"distance\":12345,\"duration\":4567}}]}"))
	}))
	defer provider.Close()
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	status, err := s.backgroundStatus(t.Context())
	if err != nil || !status.Active || !status.Determinate || status.Total != 1 || !strings.Contains(status.Detail, "Driving routes") {
		t.Fatal(status, err)
	}
	var markers ideasMapPayload
	path := "/vacations/" + v.ID.String() + "/api/ideas-map?lodging=" + l.ID.String()
	readTripJSON(t, s, path, &markers)
	if len(markers.Routes) != 0 || calls.Load() != 0 {
		t.Fatal("GET performed routing")
	}
	if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
		t.Fatal(err)
	}
	readTripJSON(t, s, path, &markers)
	got := markers.Routes[i.ID.String()]
	if got.Status != "ready" || got.Distance != "12.3 km" || len(got.Geometry) != 3 || got.Geometry[1] != [2]float64{40.7, -120.95} {
		t.Fatalf("missing cached provider road geometry: %+v", got)
	}
	status, err = s.backgroundStatus(t.Context())
	if err != nil || status.Active || calls.Load() != 1 {
		t.Fatal(status, err)
	}
	other := &models.Item{VacationID: v.ID, Title: "Added after cache", Latitude: fptr(43), Longitude: fptr(-126)}
	if err := s.store.CreateItem(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	status, err = s.backgroundStatus(t.Context())
	if err != nil || !status.Active || status.Completed != 1 || status.Total != 2 {
		t.Fatal(status, err)
	}
	retryPath := "/vacations/" + v.ID.String() + "/ideas-routes/retry"
	if rec := postAISettings(s, retryPath, url.Values{}, false); rec.Code != http.StatusForbidden {
		t.Fatal("retry lacks CSRF")
	}
	if rec := postAISettings(s, retryPath, url.Values{}, true); rec.Code != http.StatusOK {
		t.Fatal("retry failed", rec.Code)
	}
	if calls.Load() != 1 {
		t.Fatal("retry handler made provider call")
	}
}

func TestIdeaRouteWorkerStartsWithoutReadsAndStopsCleanly(t *testing.T) {
	s := newIntegrationServer(t)
	seedIdeaRoute(t, s)
	started := make(chan struct{})
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer provider.Close()
	defer close(release)
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := s.StartIdeaRouteWorker(ctx)
	stopAgain := s.StartIdeaRouteWorker(ctx)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not process saved records automatically")
	}
	cancel()
	stop()
	stopAgain()
	job, err := s.store.NextIdeaRoute(t.Context(), provider.URL)
	if err != nil || job == nil {
		t.Fatal("shutdown lost pending work", err)
	}
}

func TestIdeaRouteWorkerPacesNewPairsAndPersistsWithoutPageReads(t *testing.T) {
	s := newIntegrationServer(t)
	v, _, _ := seedIdeaRoute(t, s)
	arrivals := make(chan time.Time, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		arrivals <- time.Now()
		_, _ = w.Write([]byte("{\"routes\":[{\"geometry\":\"_p~iF~ps|U_ulLnnqC_mqNvxq`@\",\"segments\":[{\"distance\":12345,\"duration\":4567}]}]}"))
	}))
	defer provider.Close()
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	stop := s.StartIdeaRouteWorker(t.Context())
	defer stop()
	var first time.Time
	select {
	case first = <-arrivals:
	case <-time.After(3 * time.Second):
		t.Fatal("initial route was not prepared")
	}
	idea := &models.Item{VacationID: v.ID, Title: "Added while worker is running", Latitude: fptr(43), Longitude: fptr(-126)}
	if err := s.store.CreateItem(t.Context(), idea); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-arrivals:
		if second.Sub(first) < 2*time.Second {
			t.Fatal("provider requests were not paced")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("new idea required a page visit to begin routing")
	}
	deadline := time.Now().Add(time.Second)
	for {
		progress, err := s.store.IdeaRouteProgress(t.Context(), provider.URL, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		if progress.Completed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background results were not persisted", progress)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRetryIncompleteRouteReachesProviderAgain(t *testing.T) {
	s := newIntegrationServer(t)
	v, _, _ := seedIdeaRoute(t, s)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":1000}}]}`))
			return
		}

		_, _ = w.Write([]byte("{\"routes\":[{\"geometry\":\"_p~iF~ps|U_ulLnnqC_mqNvxq`@\",\"segments\":[{\"distance\":1000,\"duration\":600}]}]}"))
	}))
	defer provider.Close()
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareNextIdeaRoute(t.Context()); err == nil {
		t.Fatal("incomplete result accepted")
	}
	if err := s.store.RetryIdeaRoutes(t.Context(), v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareNextIdeaRoute(t.Context()); err != nil || calls.Load() != 2 {
		t.Fatal("retry reused an incomplete in-memory result", err, calls.Load())
	}
}

func TestRetryRepairsSavedFailureAndKeepsSuccessfulSummaryRoutes(t *testing.T) {
	s := newIntegrationServer(t)
	v, lodging, idea := seedIdeaRoute(t, s)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("{\"routes\":[{\"geometry\":\"_p~iF~ps|U_ulLnnqC_mqNvxq`@\",\"summary\":{\"distance\":12345,\"duration\":4567}}]}"))
	}))
	defer provider.Close()
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	job, err := s.store.NextIdeaRoute(t.Context(), provider.URL)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = "unavailable"
	if stored, err := s.store.PutIdeaRoute(t.Context(), job); err != nil || !stored {
		t.Fatal(stored, err)
	}
	path := "/vacations/" + v.ID.String() + "/api/ideas-map?lodging=" + lodging.ID.String()
	var data ideasMapPayload
	readTripJSON(t, s, path, &data)
	if data.Progress.Failed != 1 || !strings.Contains(data.ProgressLabel, "failed: 1") {
		t.Fatal("failure hidden as success", data.ProgressLabel)
	}
	if err := s.prepareNextIdeaRoute(t.Context()); err != nil || calls.Load() != 0 {
		t.Fatal("failure retried automatically", err)
	}
	retry := "/vacations/" + v.ID.String() + "/ideas-routes/retry"
	if rec := postAISettings(s, retry, url.Values{}, true); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.routing = route.New("test") // Discard the in-memory cache, keeping SQLite.
	for range 3 {
		readTripJSON(t, s, path, &data)
		drive := data.Routes[idea.ID.String()]
		if drive.Status != "ready" || drive.Distance != "12.3 km" || drive.Duration != "1 h 16 min" || len(drive.Geometry) != 3 {
			t.Fatalf("summary-only provider result did not reach the saved map/table: %+v", drive)
		}
		if data.Progress.Failed != 0 || !strings.Contains(data.ProgressLabel, "Saved routes: 1") {
			t.Fatal(data.ProgressLabel)
		}
		if rec := postAISettings(s, retry, url.Values{}, true); rec.Code != http.StatusOK {
			t.Fatal(rec.Code)
		}
		if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("reads/retries repeated successful provider calls: %d", calls.Load())
	}
}
