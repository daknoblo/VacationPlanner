package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func TestIdeasMapDayChoicesIncludeCurrentLocalAccommodationRegions(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := t.Context()
	if err := s.store.PutSetting(ctx, settingTimezone, "Europe/Copenhagen"); err != nil {
		t.Fatal(err)
	}
	start := regionDate(t, "2026-10-24T00:00:00Z")
	v := &models.Vacation{Title: "Regions", StartDate: start, EndDate: start.AddDate(0, 0, 4)}
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	lodgings := []models.Lodging{
		{VacationID: v.ID, Name: "Late arrival", Region: "Zealand", CheckIn: regionDate(t, "2026-10-24T22:30:00Z"), CheckOut: regionDate(t, "2026-10-25T23:00:00Z")},
		{VacationID: v.ID, Name: "Same region", Region: " zealand ", CheckIn: regionDate(t, "2026-10-25T15:00:00Z"), CheckOut: regionDate(t, "2026-10-26T09:00:00Z")},
		{VacationID: v.ID, Name: "Unresolved region", Latitude: fptr(55), Longitude: fptr(8),
			CheckIn: regionDate(t, "2026-10-26T10:00:00Z"), CheckOut: regionDate(t, "2026-10-27T09:00:00Z")},
	}
	for n := range lodgings {
		if err := s.store.CreateLodging(ctx, &lodgings[n]); err != nil {
			t.Fatal(err)
		}
	}
	path := "/vacations/" + v.ID.String() + "/api/ideas-map"
	for _, lang := range []i18n.Lang{i18n.LangEN, i18n.LangDE} {
		loc := i18n.NewLocalizer(lang)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: string(lang)})
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		var data ideasMapPayload
		if rec.Code != http.StatusOK {
			t.Fatalf("day choices failed: %d %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		want := []string{
			loc.T("planner.regions.no_lodging"), "Zealand",
			"Zealand · " + loc.T("planner.regions.unknown"),
			loc.T("planner.regions.unknown"), loc.T("planner.regions.no_lodging"),
		}
		if len(data.Days) != len(want) {
			t.Fatalf("missing trip days: %+v", data.Days)
		}
		for n, region := range want {
			day := start.AddDate(0, 0, n)
			if data.Days[n].Value != day.Format("2006-01-02") || data.Days[n].Label != region+" · "+fmtDate(day) {
				t.Fatalf("wrong local region, transfer day, fallback or date value: %+v", data.Days[n])
			}
		}
	}
	if updated, err := s.store.UpdateLodgingRegion(ctx, &lodgings[2], "Jutland"); err != nil || !updated {
		t.Fatal("could not enrich the saved accommodation region", updated, err)
	}
	var refreshed ideasMapPayload
	readTripJSON(t, s, path, &refreshed)
	if refreshed.Days[2].Label != "Zealand · Jutland · 26.10.2026" || refreshed.Days[3].Label != "Jutland · 27.10.2026" {
		t.Fatalf("day labels retained stale accommodation regions: %+v", refreshed.Days)
	}
	if len(s.geography.queue) != 0 || len(s.geography.states) != 0 {
		t.Fatal("reading day choices started geography work")
	}
}

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
	var overview ideasMapPayload
	overviewPath := "/vacations/" + v.ID.String() + "/api/ideas-map"
	readTripJSON(t, s, overviewPath, &overview)
	nearest := overview.Routes[item.ID.String()]
	if nearest.LodgingID != lodging.ID.String() || nearest.Distance != "12.3 km" ||
		nearest.Duration != "1 h 16 min" || nearest.DistanceM == nil || *nearest.DistanceM != 12345 ||
		nearest.DurationS == nil || *nearest.DurationS != 4567 || len(nearest.Geometry) != 0 || calls.Load() != 2 {
		t.Fatalf("overview must pair the shortest saved road distance with its own duration and origin: %+v", nearest)
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
	var foreign ideasMapPayload
	readTripJSON(t, s, "/vacations/"+other.ID.String()+"/api/ideas-map", &foreign)
	if len(foreign.Routes) != 0 {
		t.Fatal("overview included another trip's routes")
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
	readTripJSON(t, s, overviewPath, &overview)
	if overview.Routes[item.ID.String()].LodgingID != second.ID.String() || calls.Load() != 2 {
		t.Fatal("overview did not replace the now-unlocated starting accommodation")
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
		{"incomplete summary", `{"routes":[{"summary":{"distance":30}}]}`, "unavailable", true, 200},
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
