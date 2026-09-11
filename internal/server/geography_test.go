package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

func TestGeographyLodgingConfidence(t *testing.T) {
	hotel := geo.Result{Name: "Blue Harbour Hotel", Type: "hotel", Street: "Quay", HouseNumber: "12", City: "Brighton", Country: "United Kingdom", Lat: 50, Lng: -1}
	for _, tt := range []struct {
		name    string
		lodging models.Lodging
		results []geo.Result
		want    bool
	}{
		{"explicit address", models.Lodging{Name: "Arrival hotel", Location: "12 Quay, Brighton, United Kingdom"}, []geo.Result{hotel}, true},
		{"exact named local hotel", models.Lodging{Name: hotel.Name, Location: "Blue Harbour Hotel, Brighton"}, []geo.Result{hotel}, true},
		{"distinctive name only", models.Lodging{Name: hotel.Name}, []geo.Result{hotel}, true},
		{"ambiguous name", models.Lodging{Name: hotel.Name}, []geo.Result{hotel, {Name: hotel.Name, Type: "hotel", City: "Auckland", Lat: -37, Lng: 175}}, false},
		{"ambiguous address", models.Lodging{Location: "Quay 12, Brighton"}, []geo.Result{hotel, {Street: "Quay", HouseNumber: "12", City: "Brighton", Type: "house", Lat: 51, Lng: -2}}, false},
		{"city", models.Lodging{Name: "Brighton", Location: "Brighton"}, []geo.Result{{Name: "Brighton", Type: "city", Lat: 50, Lng: -1}}, false},
		{"country", models.Lodging{Name: "Norway"}, []geo.Result{{Name: "Norway", Type: "country", Lat: 60, Lng: 10}}, false},
		{"generic hotel name", models.Lodging{Name: "Hotel Central"}, []geo.Result{{Name: "Hotel Central", Type: "hotel", City: "Brighton", Lat: 50, Lng: -1}}, false},
		{"different hotel name", models.Lodging{Name: "Green Harbour Hotel"}, []geo.Result{hotel}, false},
		{"wrong street", models.Lodging{Location: "Other Road 12, Brighton"}, []geo.Result{hotel}, false},
		{"wrong house", models.Lodging{Location: "Quay 112, Brighton"}, []geo.Result{hotel}, false},
		{"no locality", models.Lodging{Location: "Quay 12"}, []geo.Result{hotel}, false},
		{"no label parsing", models.Lodging{Location: "Quay 12, Brighton"}, []geo.Result{{DisplayName: "Hotel, Quay 12, Brighton", Type: "hotel", Lat: 50, Lng: -1}}, false},
		{"duplicate same point", models.Lodging{Location: "Quay 12, Brighton"}, []geo.Result{hotel, hotel}, true},
		{"truncated results", models.Lodging{Name: hotel.Name}, make([]geo.Result, 10), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, got := matchLodging(&tt.lodging, tt.results)
			if got != tt.want {
				t.Fatalf("match = %v, want %v", got, tt.want)
			}
		})
	}
}

func newGeographyTestServer(t *testing.T, handler http.HandlerFunc) (*Server, *store.SQLite, *models.Vacation) {
	t.Helper()
	provider := httptest.NewServer(handler)
	t.Cleanup(provider.Close)
	st, err := store.NewSQLite(context.Background(), filepath.Join(t.TempDir(), "geography.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(context.Background(), settingGeoBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	v := &models.Vacation{Title: "Norway trip", Destination: "Norway", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, geo: geo.New("private-provider-key")}
	s.geography = newGeographyWorker(s)
	return s, st, v
}

func waitGeography(t *testing.T, s *Server, id uuid.UUID) geographyStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := s.geographyStatus(id)
		if !status.Pending {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("geography job did not finish")
	return geographyStatus{}
}

func TestGeographyWorkerEnrichesWithoutDestinationBias(t *testing.T) {
	var calls atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if q.Has("lat") || q.Has("lon") || q.Get("q") != "12 Quay, Brighton, United Kingdom" {
			t.Errorf("arrival accommodation biased to destination: %v", q)
		}
		_, _ = w.Write([]byte(`[{"display_name":"Blue Harbour Hotel, Brighton, United Kingdom","name":"Blue Harbour Hotel","type":"hotel","lat":"50","lon":"-1","address":{"road":"Quay","house_number":"12","city":"Brighton","state":"Sussex","country":"United Kingdom"}}]`))
	})
	ctx := context.Background()
	l := &models.Lodging{VacationID: v.ID, Name: "Arrival hotel", Location: "12 Quay, Brighton, United Kingdom", CheckIn: time.Now(), CheckOut: time.Now()}
	if err := st.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	lat, lng := 50.0, -1.0
	manual := &models.Item{VacationID: v.ID, Title: "Manual idea", Latitude: &lat, Longitude: &lng, Region: "", RegionManual: true}
	if err := st.CreateItem(ctx, manual); err != nil {
		t.Fatal(err)
	}
	stop := s.StartGeographyWorker(ctx)
	t.Cleanup(stop)
	s.queueGeography(v.ID, "de")
	status := waitGeography(t, s, v.ID)
	if status.Error || status.Pending || status.UpdatedCount != 1 || len(status.Unresolved) != 0 || status.UnknownRegions != 1 || calls.Load() != 1 {
		t.Fatalf("unexpected status: %+v, calls=%d", status, calls.Load())
	}
	got, err := st.GetLodging(ctx, l.ID)
	if err != nil || !got.HasCoords() || *got.Latitude != lat || *got.Longitude != lng {
		t.Fatalf("lodging not enriched: %+v %v", got, err)
	}
	s.queueGeography(v.ID, "de")
	if s.geographyStatus(v.ID).Pending {
		t.Fatal("cooldown allowed polling to enqueue another job")
	}
}

func TestGeographyWorkerReverseRegionAndManualOverride(t *testing.T) {
	var calls atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/reverse" || r.URL.Query().Get("accept-language") != "en" {
			t.Errorf("unexpected reverse lookup: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"display_name":"Hotel","lat":"50","lon":"-1","address":{"state":"Sussex","country":"United Kingdom"}}`))
	})
	ctx := context.Background()
	lat, lng := 50.0, -1.0
	item := &models.Item{VacationID: v.ID, Title: "Automatic", Latitude: &lat, Longitude: &lng}
	manual := &models.Item{VacationID: v.ID, Title: "Manual", Latitude: &lat, Longitude: &lng, Region: "My region", RegionManual: true}
	for _, entry := range []*models.Item{item, manual} {
		if err := st.CreateItem(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	stop := s.StartGeographyWorker(ctx)
	t.Cleanup(stop)
	s.queueGeography(v.ID, "de")
	status := waitGeography(t, s, v.ID)
	if status.Error || status.UnknownRegions != 0 || status.UpdatedCount != 1 || calls.Load() != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	got, err := st.GetItem(ctx, item.ID)
	if err != nil || got.Region != "Sussex, United Kingdom" {
		t.Fatalf("region not persisted: %+v %v", got, err)
	}
	got, err = st.GetItem(ctx, manual.ID)
	if err != nil || got.Region != "My region" || !got.RegionManual {
		t.Fatalf("manual region changed: %+v %v", got, err)
	}
}

func TestGeographyWorkerBoundedAndDeduplicatedQueue(t *testing.T) {
	s := &Server{}
	s.geography = newGeographyWorker(s)
	for range 100 {
		s.queueGeography(uuid.MustParse("11111111-1111-1111-1111-111111111111"), "en")
	}
	if len(s.geography.queue) != 1 {
		t.Fatal("same vacation was not deduplicated")
	}
	var last uuid.UUID
	for range geographyQueueSize {
		last = uuid.New()
		s.queueGeography(last, "en")
	}
	if len(s.geography.queue) != geographyQueueSize {
		t.Fatal("queue size limit not enforced")
	}
	if status := s.geographyStatus(last); !status.Error || !status.Limited || status.Pending {
		t.Fatalf("queue overflow hidden: %+v", status)
	}
}

func TestGeographyWorkerCancellationJoins(t *testing.T) {
	started := make(chan struct{})
	s, st, v := newGeographyTestServer(t, func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	l := &models.Lodging{VacationID: v.ID, Name: "Blue Harbour Hotel", CheckIn: time.Now(), CheckOut: time.Now()}
	if err := st.CreateLodging(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	stop := s.StartGeographyWorker(context.Background())
	t.Cleanup(stop)
	s.queueGeography(v.ID, "en")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not start")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not cancel and join")
	}
	status := s.geographyStatus(v.ID)
	if status.Pending || !status.Error {
		t.Fatalf("cancelled job status not visible: %+v", status)
	}
	s.retryGeography(v.ID, "en")
	if s.geographyStatus(v.ID).Pending {
		t.Fatal("stopped worker accepted a job")
	}
}

func TestGeographyWorkerBatchLimitAndProgress(t *testing.T) {
	var calls atomic.Int32
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"display_name":"Hotel","lat":"50","lon":"-1","address":{"state":"Sussex","country":"United Kingdom"}}`))
	})
	ctx := context.Background()
	lat, lng := 50.0, -1.0
	for range geographyLookupLimit + 1 {
		item := &models.Item{VacationID: v.ID, Title: "Idea", Latitude: &lat, Longitude: &lng}
		if err := st.CreateItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	stop := s.StartGeographyWorker(ctx)
	t.Cleanup(stop)
	s.queueGeography(v.ID, "en")
	status := waitGeography(t, s, v.ID)
	if status.Error || !status.Limited || status.UpdatedCount != geographyLookupLimit || status.UnknownRegions != 1 {
		t.Fatalf("batch limit not visible: %+v", status)
	}
	s.retryGeography(v.ID, "en")
	status = waitGeography(t, s, v.ID)
	if status.Error || status.Limited || status.UnknownRegions != 0 || status.UpdatedCount != 1 || calls.Load() != 1 {
		t.Fatalf("remaining work did not progress / cache not reused: %+v calls=%d", status, calls.Load())
	}
}

func TestGeographyNilWorkerSafe(t *testing.T) {
	s := &Server{}
	s.queueGeography(uuid.New(), "en")
	s.retryGeography(uuid.New(), "en")
	s.StartGeographyWorker(context.Background())()
	if status := s.geographyStatus(uuid.New()); !status.Error {
		t.Fatal("missing worker silently appeared healthy")
	}
}

func TestGeographyWorkerUnresolvedAndProviderErrorVisible(t *testing.T) {
	for _, providerError := range []bool{false, true} {
		t.Run(map[bool]string{false: "city-only", true: "provider-error"}[providerError], func(t *testing.T) {
			var calls atomic.Int32
			s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if providerError {
					http.Error(w, "private-provider-key", http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte(`[{"display_name":"Brighton","name":"Brighton","type":"city","lat":"50","lon":"-1"}]`))
			})
			l := &models.Lodging{VacationID: v.ID, Name: "Hotel Central", Location: "Brighton", CheckIn: time.Now(), CheckOut: time.Now()}
			if err := st.CreateLodging(context.Background(), l); err != nil {
				t.Fatal(err)
			}
			stop := s.StartGeographyWorker(context.Background())
			t.Cleanup(stop)
			s.queueGeography(v.ID, "en")
			status := waitGeography(t, s, v.ID)
			if status.Error != providerError || status.Pending || status.UpdatedCount != 0 || len(status.Unresolved) != 1 || status.Unresolved[0] != l.Name {
				t.Fatalf("unresolved/provider failure not visible: %+v", status)
			}
			got, err := st.GetLodging(context.Background(), l.ID)
			if err != nil || got.HasCoords() {
				t.Fatalf("generic city used as lodging: %+v %v", got, err)
			}
			for range 5 {
				s.queueGeography(v.ID, "en")
			}
			if s.geographyStatus(v.ID).Pending || calls.Load() != 1 {
				t.Fatal("failed lodging retried on every poll")
			}
		})
	}
}
