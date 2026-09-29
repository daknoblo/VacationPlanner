package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func TestIdeasMapIncludesEveryAccommodationAndItemWithoutProviderWork(t *testing.T) {
	s := newIntegrationServer(t)
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		lodging := models.Lodging{VacationID: v.ID, Name: "Stay", CheckIn: v.StartDate, CheckOut: v.EndDate}
		if i != 2 {
			lodging.Latitude, lodging.Longitude = fptr(55), fptr(float64(8+i))
		}
		if err := s.store.CreateLodging(t.Context(), &lodging); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		item := models.Item{VacationID: v.ID, Title: "Idea <script>", Category: "Hotel"}
		if i != 2 {
			item.Latitude, item.Longitude = fptr(54), fptr(9)
		}
		if i == 1 {
			item.Day = &v.StartDate
		}
		if err := s.store.CreateItem(t.Context(), &item); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	var data ideasMapPayload
	readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/ideas-map", &data)
	if len(data.Lodgings) != 3 || len(data.Ideas) != 3 || !data.Routing || calls.Load() != 0 {
		t.Fatalf("incomplete map or unexpected routing: %+v, calls=%d", data, calls.Load())
	}
	var scheduled, missing int
	for _, item := range data.Ideas {
		if item.Day != "" {
			scheduled++
		}
		if item.Lat == nil {
			missing++
		}
	}
	if scheduled != 1 || missing != 1 || data.Lodgings[0].DateRange == "" {
		t.Fatalf("scheduled/unlocated entries or dates lost: %+v", data)
	}
	if state := s.geographyStatus(v.ID); state.Pending {
		t.Fatal("marker reads must not start geography work")
	}
}

func TestIdeaDriveUsesSelectedCurrentAccommodationAndSharedCache(t *testing.T) {
	s := newIntegrationServer(t)
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	lodging := models.Lodging{VacationID: v.ID, Name: "Apartment", Latitude: fptr(55), Longitude: fptr(8), CheckIn: v.StartDate, CheckOut: v.EndDate}
	item := models.Item{VacationID: v.ID, Title: "Idea", Latitude: fptr(56), Longitude: fptr(9)}
	if err := s.store.CreateLodging(t.Context(), &lodging); err != nil {
		t.Fatal(err)
	}
	second := models.Lodging{VacationID: v.ID, Name: "Campsite", Latitude: fptr(57), Longitude: fptr(10), CheckIn: v.StartDate, CheckOut: v.EndDate}
	if err := s.store.CreateLodging(t.Context(), &second); err != nil {
		t.Fatal(err)
	}
	if err := s.store.CreateItem(t.Context(), &item); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Coordinates [][2]float64 `json:"coordinates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/v2/directions/driving-car" || len(body.Coordinates) != 2 ||
			(body.Coordinates[0] != [2]float64{8, 55} && body.Coordinates[0] != [2]float64{10, 57}) || body.Coordinates[1] != [2]float64{9, 56} {
			t.Errorf("not a directed saved accommodation-to-idea leg: %+v", body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body.Coordinates[0] == [2]float64{10, 57} {
			_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":24690,"duration":1800},"segments":[{"distance":24690,"duration":1800}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":12345,"duration":4567},"segments":[{"distance":12345,"duration":4567}]}]}`))
	}))
	defer provider.Close()
	s.routing = route.New("test")
	if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
		t.Fatal(err)
	}
	path := "/vacations/" + v.ID.String() + "/api/ideas-route?lodging=" + lodging.ID.String() + "&item=" + item.ID.String()
	var initial ideaDrive
	readTripJSON(t, s, path, &initial)
	if initial.Status != "pending" || calls.Load() != 0 {
		t.Fatal("GET started provider work")
	}
	for range 2 {
		if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	s.routing = route.New("test")
	if err := s.prepareNextIdeaRoute(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var data ideaDrive
		readTripJSON(t, s, path, &data)
		if data.Status != "ready" || data.Distance != "12.3 km" || data.Duration != "1 h 16 min" {
			t.Fatalf("wrong metrics: %+v", data)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("background results were not persisted for both accommodations")
	}
	var selected ideaDrive
	readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/ideas-route?lodging="+second.ID.String()+"&item="+item.ID.String(), &selected)
	if selected.Distance != "24.7 km" || selected.Duration != "30 min" || calls.Load() != 2 {
		t.Fatalf("selected accommodation ignored: %+v", selected)
	}
	other := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+other.ID.String()+"/api/ideas-route?lodging="+lodging.ID.String()+"&item="+item.ID.String(), nil))
	if rec.Code != http.StatusNotFound || calls.Load() != 2 {
		t.Fatal("accepted cross-trip sources")
	}
	lodging.Latitude = nil
	if err := s.store.UpdateLodging(t.Context(), &lodging); err != nil {
		t.Fatal(err)
	}
	var data ideaDrive
	readTripJSON(t, s, path, &data)
	if data.Status != "missing" || data.Distance != "" || data.Duration != "" || calls.Load() != 2 {
		t.Fatalf("stale coordinates used: %+v", data)
	}
}

func TestIdeaDriveDisabledFailuresAndMalformedMetrics(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		enabled          bool
		status           int
	}{
		{"disabled", "", "disabled", false, 200},
		{"failure", "", "unavailable", true, 503},
		{"empty", `{"routes":[]}`, "unavailable", true, 200},
		{"missing metrics", `{"routes":[{"segments":[{}]}]}`, "unavailable", true, 200},
		{"negative metrics", `{"routes":[{"segments":[{"distance":-1,"duration":30}]}]}`, "unavailable", true, 200},
		{"missing leg", `{"routes":[{"summary":{"distance":30,"duration":10}}]}`, "unavailable", true, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newIntegrationServer(t)
			v := sampleVacation()
			if err := s.store.CreateVacation(t.Context(), v); err != nil {
				t.Fatal(err)
			}
			l := models.Lodging{VacationID: v.ID, Name: "Stay", Latitude: fptr(55), Longitude: fptr(8), CheckIn: v.StartDate, CheckOut: v.EndDate}
			i := models.Item{VacationID: v.ID, Title: "Idea", Latitude: fptr(56), Longitude: fptr(9)}
			if err := s.store.CreateLodging(t.Context(), &l); err != nil {
				t.Fatal(err)
			}
			if err := s.store.CreateItem(t.Context(), &i); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer provider.Close()
			if test.enabled {
				s.routing = route.New("test")
			}
			if err := s.putSetting(t.Context(), settingRouteBaseURL, provider.URL); err != nil {
				t.Fatal(err)
			}
			if err := s.prepareNextIdeaRoute(t.Context()); test.enabled && err == nil {
				t.Fatal("provider failure was not reported")
			} else if !test.enabled && err != nil {
				t.Fatal(err)
			}
			var data ideaDrive
			readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/ideas-route?lodging="+l.ID.String()+"&item="+i.ID.String(), &data)
			if data.Status != test.want || data.Distance != "" || data.Duration != "" {
				t.Fatalf("invented driving values: %+v", data)
			}
			if !test.enabled && calls.Load() != 0 {
				t.Fatal("disabled routing made a provider call")
			}
			before := calls.Load()
			if err := s.prepareNextIdeaRoute(t.Context()); err != nil || calls.Load() != before {
				t.Fatal("failed attempts must not retry automatically", err)
			}
		})
	}
}
