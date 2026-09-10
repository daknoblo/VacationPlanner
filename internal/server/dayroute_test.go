package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func dayRouteFixture() (*models.Vacation, time.Time) {
	day := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	return &models.Vacation{
		ID: uuid.New(), Title: "Trip", Destination: "Base",
		StartDate: day, EndDate: day.AddDate(0, 0, 1),
		Latitude: fptr(48), Longitude: fptr(16),
		Items: []models.Item{
			{ID: uuid.New(), Title: "Second", Day: &day, StartMin: 720, EndMin: 780, Latitude: fptr(48.02), Longitude: fptr(16.02)},
			{ID: uuid.New(), Title: "First", Day: &day, StartMin: 540, EndMin: 600, Latitude: fptr(48.01), Longitude: fptr(16.01)},
			{ID: uuid.New(), Title: "Idea", Latitude: fptr(50), Longitude: fptr(20)},
		},
	}, day
}

func TestDayCardsPreserveItemLinks(t *testing.T) {
	s := &Server{routing: route.New("")}
	v, day := dayRouteFixture()
	link := models.ItemLink{Kind: "website", URL: "https://example.com/visit"}
	v.Items[1].Links = []models.ItemLink{link}
	cards := s.dayCards(context.Background(), i18n.NewLocalizer(i18n.LangEN), time.UTC, day, v, v.Items[:2])
	if len(cards[0].Links) != 1 || cards[0].Links[0] != link {
		t.Fatalf("day cards must preserve item references: %+v", cards[0])
	}
}

func TestDayRouteEstimatesOrderingAndNoReturn(t *testing.T) {
	s := &Server{routing: route.New("")}
	v, day := dayRouteFixture()
	v.Items[0].OriginRef = "hotel"
	loc := i18n.NewLocalizer(i18n.LangEN)
	got := s.dayRoute(context.Background(), loc, time.UTC, v, day)
	if len(got.Legs) != 2 || got.Legs[0].Title != "First" || got.Legs[1].Title != "Second" {
		t.Fatalf("must include only ordered assigned stops, no return journey: %+v", got)
	}
	if got.Legs[0].VisitTime != "09:00–10:00" || got.Legs[1].VisitTime != "12:00–13:00" {
		t.Fatalf("saved visit times changed: %+v", got.Legs)
	}
	if !got.Legs[1].Override || got.Legs[1].Origin != got.Base {
		t.Fatalf("explicit hotel leg not represented: %+v", got.Legs[1])
	}
	wantDistance := route.Haversine(route.Point{Lat: 48, Lng: 16}, *itemPoint(v.Items[1])) +
		route.Haversine(route.Point{Lat: 48, Lng: 16}, *itemPoint(v.Items[0]))
	if got.Distance != formatDistance(wantDistance) || got.Duration != "" || got.Approx != 2 || got.Routed != 0 {
		t.Fatalf("estimates must not imply driving time or return mileage: %+v", got)
	}
}

func TestDayRouteMissingCoordinatesAndEmptyDay(t *testing.T) {
	s := &Server{routing: route.New("")}
	v, day := dayRouteFixture()
	v.Items[1].Latitude = nil
	got := s.dayRoute(context.Background(), i18n.NewLocalizer(i18n.LangEN), time.UTC, v, day)
	if got.Missing != 2 || got.Distance != "" || got.Duration != "" || got.Legs[1].Origin != "First" {
		t.Fatalf("missing intermediate coordinates must not fabricate direct routes: %+v", got)
	}
	empty := s.dayRoute(context.Background(), i18n.NewLocalizer(i18n.LangEN), time.UTC, v, day.AddDate(0, 0, 1))
	if len(empty.Legs) != 0 || empty.Distance != "" || empty.Duration != "" {
		t.Fatalf("empty route must have no fake-zero totals: %+v", empty)
	}
}

func TestDayRouteSharesCardRoutingCache(t *testing.T) {
	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":1250,"duration":600},"segments":[{"distance":1250,"duration":600}]}]}`))
	}))
	defer mock.Close()
	s := newIntegrationServer(t)
	s.routing = route.New("test-key")
	if err := s.putSetting(context.Background(), settingRouteBaseURL, mock.URL); err != nil {
		t.Fatal(err)
	}
	v, day := dayRouteFixture()
	loc := i18n.NewLocalizer(i18n.LangEN)
	got := s.dayRoute(context.Background(), loc, time.UTC, v, day)
	cards := s.dayCards(context.Background(), loc, time.UTC, day, v, v.Items[:2])
	if calls.Load() != 2 {
		t.Fatalf("route and cards must share two-point routing cache; calls = %d", calls.Load())
	}
	if got.Distance != "2.5 km" || got.Duration != "20 min" || got.Routed != 2 || got.Approx != 0 {
		t.Fatalf("bad live route totals: %+v", got)
	}
	for i, leg := range got.Legs {
		if leg.Distance != cards[i].DistanceLabel || leg.Duration != cards[i].DurationLabel || leg.Origin != cards[i].OriginLabel {
			t.Fatalf("timeline and card disagree: %+v vs %+v", leg, cards[i])
		}
	}
}

func TestDayRouteKeepsUnlocatedExplicitOriginOption(t *testing.T) {
	v, _ := dayRouteFixture()
	v.Items[1].Latitude = nil
	v.Items[0].OriginRef = v.Items[1].ID.String()
	options := originOptionsFor(i18n.NewLocalizer(i18n.LangEN), v.Items, v.Items[0], "Hotel")
	for _, option := range options {
		if option.Value == v.Items[1].ID.String() && option.Selected {
			return
		}
	}
	t.Fatal("an explicit origin whose coordinates were removed must remain selected")
}

func TestActivityRouteCoalescesConcurrentRequests(t *testing.T) {
	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"routes":[{"summary":{"distance":1000,"duration":120}}]}`))
	}))
	defer mock.Close()
	key := activityRouteKey{client: route.New("test-key"), baseURL: mock.URL, from: route.Point{Lat: 1, Lng: 2}, to: route.Point{Lat: 3, Lng: 4}}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		workers.Go(func() {
			<-start
			result, err := activityRoute(context.Background(), key)
			if err != nil || result.TotalDistanceM != 1000 {
				t.Errorf("shared route: result=%+v, err=%v", result, err)
			}
		})
	}
	close(start)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("simultaneous timeline/cards must make only one provider request, got %d", calls.Load())
	}
}

func TestDayRouteProviderFailureIsNotDrivingTime(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer mock.Close()
	s := newIntegrationServer(t)
	s.routing = route.New("test-key")
	if err := s.putSetting(context.Background(), settingRouteBaseURL, mock.URL); err != nil {
		t.Fatal(err)
	}
	v, day := dayRouteFixture()
	got := s.dayRoute(context.Background(), i18n.NewLocalizer(i18n.LangEN), time.UTC, v, day)
	if got.Approx != 2 || got.Routed != 0 || got.Duration != "" || got.Distance == "" {
		t.Fatalf("provider failure must show explicit Haversine-only estimates: %+v", got)
	}
}

func TestDayRouteCancelledOperationUsesExplicitEstimates(t *testing.T) {
	// No store or HTTP transport is needed: an exhausted operation must never
	// start another network request (including one against the default URL).
	s := &Server{routing: route.New("test-key")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, day := dayRouteFixture()
	got := s.dayRoute(ctx, i18n.NewLocalizer(i18n.LangEN), time.UTC, v, day)
	if got.Approx != 2 || got.Duration != "" {
		t.Fatalf("cancelled operation must fall back without fabricated drive time: %+v", got)
	}
}

func TestHandleDayRouteValidationRenderingAndNoWrites(t *testing.T) {
	s := newIntegrationServer(t)
	v, day := dayRouteFixture()
	if err := s.store.CreateVacation(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "<script>alert(1)</script>", Day: &day, StartMin: 540, EndMin: 600}
	if err := s.store.CreateItem(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		day    string
		status int
	}{
		{"", http.StatusBadRequest},
		{"2026-99-99", http.StatusBadRequest},
		{"2026-08-02", http.StatusBadRequest},
		{"2026-08-03", http.StatusOK},
		{"2026-08-04", http.StatusOK},
	} {
		t.Run(tc.day, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?day="+tc.day, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("vacationID", v.ID.String())
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			s.handleDayRoute(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.day == "2026-08-03" {
				body := rec.Body.String()
				if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") ||
					!strings.Contains(body, "day-journey__stop--missing") {
					t.Fatalf("expected escaped stop and missing-coordinate status: %s", body)
				}
			}
		})
	}
	items, err := s.store.ListItems(context.Background(), v.ID)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("route reads must not duplicate source bookings: %+v, %v", items, err)
	}
}
