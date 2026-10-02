package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/weather"
)

type testWeather struct {
	calls atomic.Int32
	fail  error
	now   time.Time
	block chan struct{}
}

func (*testWeather) Enabled() bool { return true }
func (p *testWeather) Forecast(ctx context.Context, _, _ float64) ([]models.WeatherSample, error) {
	p.calls.Add(1)
	if p.block != nil {
		p.block <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if p.fail != nil {
		return nil, p.fail
	}
	return []models.WeatherSample{
		{Time: p.now.Add(time.Hour), Temperature: 12, Code: 800, RainChance: .2, RainMM: 1, WindMS: 4},
		{Time: p.now.Add(4 * time.Hour), Temperature: 17, Code: 501, RainChance: .8, RainMM: 2, WindMS: 6},
	}, nil
}

func weatherTestServer(t *testing.T) (*Server, *testWeather, *models.Vacation) {
	t.Helper()
	s := newIntegrationServer(t)
	now := time.Now().UTC()
	date, _ := time.Parse("2006-01-02", now.Format("2006-01-02"))
	v := &models.Vacation{Title: "Weather trip", Destination: "Rome", Latitude: fptr(41), Longitude: fptr(12),
		StartDate: date, EndDate: date.AddDate(0, 0, 8)}
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	p := &testWeather{now: now}
	s.weather = p
	return s, p, v
}

func TestWeatherReadsNeverFetchAndRefreshIsProtected(t *testing.T) {
	s, provider, v := weatherTestServer(t)
	path := "/vacations/" + v.ID.String() + "/api/weather"
	var view weatherView
	readTripJSON(t, s, path, &view)
	if len(view.Days) != 9 || provider.calls.Load() != 0 {
		t.Fatal("GET fetched weather or hid unavailable days", view)
	}
	if !strings.Contains(view.Days[8].Entries[0].Notice, "Weather data expected to become available in") {
		t.Fatal("future day invented weather", view.Days[8])
	}
	form := url.Values{"vacation_id": {v.ID.String()}}
	if rec := postAISettings(s, "/settings/weather/refresh", form, false); rec.Code != http.StatusForbidden {
		t.Fatal("weather refresh bypasses CSRF", rec.Code)
	}
	for _, id := range []string{"invalid", uuid.NewString()} {
		if rec := postAISettings(s, "/settings/weather/refresh", url.Values{"vacation_id": {id}}, true); rec.Code < 400 {
			t.Fatal("invalid trip accepted", rec.Code)
		}
	}
	if rec := postAISettings(s, "/settings/weather/refresh", form, true); rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if provider.calls.Load() != 0 {
		t.Fatal("POST made a synchronous weather call")
	}
	status, err := s.backgroundStatus(t.Context())
	if err != nil || !status.Active || !strings.Contains(status.Detail, "Weather") {
		t.Fatal(status, err)
	}
	if _, err := s.weatherStep(t.Context(), provider.now); err != nil || provider.calls.Load() != 1 {
		t.Fatal(err, provider.calls.Load())
	}
	for _, lang := range []string{"en", "de"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "°C") {
			t.Fatal(rec.Code, rec.Body.String())
		}
		req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+v.ID.String(), nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-tab="weather"`) ||
			!strings.Contains(rec.Body.String(), `data-weather-day=`) || strings.Contains(rec.Body.String(), "ZgotmplZ") {
			t.Fatal("weather render failed", rec.Code)
		}
	}
	readTripJSON(t, s, path, &view)
	if provider.calls.Load() != 1 {
		t.Fatal("polling refetched forecast")
	}
	if _, err := s.queueWeather(t.Context(), v.ID, provider.now, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.weatherStep(t.Context(), provider.now); err != nil || provider.calls.Load() != 1 {
		t.Fatal("duplicate refresh not throttled", err)
	}
	provider.fail = weather.ErrAuth
	later := provider.now.Add(11 * time.Minute)
	if _, err := s.queueWeather(t.Context(), v.ID, later, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.weatherStep(t.Context(), later); err != nil {
		t.Fatal(err)
	}
	caches, _ := s.store.ListWeather(t.Context())
	cache := caches[weather.Key(*v.Latitude, *v.Longitude)]
	if cache.Status != "error" || cache.ErrorCode != "auth" || len(cache.Samples) != 2 {
		t.Fatal("failed refresh erased data", cache)
	}
	old := *v.Latitude
	v.Latitude = fptr(42)
	if err := s.store.UpdateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	readTripJSON(t, s, path, &view)
	if strings.Contains(view.Days[0].Entries[0].Summary, "°C") {
		t.Fatal("changed location reused old forecast", old, view)
	}
}

func TestWeatherPlacesTimezoneTransferAndAggregation(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	v := &models.Vacation{Destination: "Trip", Latitude: fptr(0), Longitude: fptr(0)}
	v.Lodgings = []models.Lodging{
		{ID: uuid.New(), Name: "First", Latitude: fptr(52), Longitude: fptr(13), CheckIn: day, CheckOut: day.Add(23 * time.Hour)},
		{ID: uuid.New(), Name: "Second", CheckIn: day.Add(22*time.Hour + 30*time.Minute), CheckOut: day.Add(60 * time.Hour)},
	}
	places := weatherPlaces(v, "2026-10-25", tz)
	if len(places) != 2 || places[0].Fallback || !places[1].Fallback || !places[1].Located {
		t.Fatal("transfer/fallback/local dates incorrect", places)
	}
	loc := i18n.NewLocalizer(i18n.LangEN)
	c := models.WeatherCache{Status: "ready", UpdatedAt: day, Samples: []models.WeatherSample{
		{Time: day.Add(22 * time.Hour), Temperature: 8, Code: 800, RainMM: 1, RainChance: .3, WindMS: 2},
		{Time: day.Add(25 * time.Hour), Temperature: 10, Code: 501, RainMM: 2, RainChance: .8, WindMS: 4},
		{Time: day.Add(37 * time.Hour), Temperature: 17, Code: 501, RainMM: 3, RainChance: .5, WindMS: 3},
	}}
	e := weatherSummary(c, "2026-10-25", tz, loc, day.Add(40*time.Hour))
	if e.Compact != "8–17 °C" || !strings.Contains(e.Detail, "80%") ||
		!strings.Contains(e.Detail, "6.0 mm") || !strings.Contains(e.Detail, "3 intervals") ||
		!strings.Contains(e.Notice, "six hours") {
		t.Fatal("incorrect partial-day/DST aggregation", e)
	}
}

func TestWeatherAutomationEligibilityAndCancellation(t *testing.T) {
	s, provider, v := weatherTestServer(t)
	form := url.Values{"interval": {"2"}}
	if rec := postAISettings(s, "/settings/weather", form, true); rec.Code != 422 {
		t.Fatal("invalid cadence accepted", rec.Code)
	}

	t.Run("queued edits, disabled key and quota pause", func(t *testing.T) {
		s, provider, v := weatherTestServer(t)
		form := url.Values{"interval": {"6"}}
		if rec := postAISettings(s, "/settings/weather", form, false); rec.Code != http.StatusForbidden {
			t.Fatal("cadence update bypasses CSRF", rec.Code)
		}
		if rec := postAISettings(s, "/settings/weather", form, true); rec.Code != http.StatusNoContent {
			t.Fatal("cadence not saved", rec.Code, rec.Body.String())
		}
		settings, _ := s.store.GetSettings(t.Context())
		if weatherIntervalSetting(settings) != "6" {
			t.Fatal(settings)
		}
		if err := s.store.PutSetting(t.Context(), settingWeatherInterval, "off"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.queueWeather(t.Context(), v.ID, provider.now, time.Minute); err != nil {
			t.Fatal(err)
		}
		v.Latitude = fptr(42)
		if err := s.store.UpdateVacation(t.Context(), v); err != nil {
			t.Fatal(err)
		}
		if _, err := s.weatherStep(t.Context(), provider.now); err != nil || provider.calls.Load() != 0 {
			t.Fatal("obsolete queued coordinates requested", err)
		}
		provider.fail = weather.ErrQuota
		if _, err := s.queueWeather(t.Context(), v.ID, provider.now, time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := s.weatherStep(t.Context(), provider.now); err != nil || provider.calls.Load() != 1 {
			t.Fatal(err)
		}
		v.Longitude = fptr(13)
		if err := s.store.UpdateVacation(t.Context(), v); err != nil {
			t.Fatal(err)
		}
		if _, err := s.queueWeather(t.Context(), v.ID, provider.now.Add(time.Minute), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := s.weatherStep(t.Context(), provider.now.Add(30*time.Minute)); err != nil || provider.calls.Load() != 1 {
			t.Fatal("quota cooldown ignored", err)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/settings/weather/status", nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "paused until") {
			t.Fatal("quota pause missing from settings", rec.Code, rec.Body.String())
		}
		s.weather = weather.New("")
		if rec := postAISettings(s, "/settings/weather/refresh", url.Values{"vacation_id": {v.ID.String()}}, true); rec.Code != 422 {
			t.Fatal("disabled provider accepted refresh", rec.Code)
		}
	})
	if _, err := s.weatherStep(t.Context(), provider.now); err != nil || provider.calls.Load() != 0 {
		t.Fatal("default off performed work", err)
	}
	if err := s.store.PutSetting(t.Context(), settingWeatherInterval, "3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.weatherStep(t.Context(), provider.now); err != nil || provider.calls.Load() != 1 {
		t.Fatal("auto refresh did not run", err)
	}
	if _, err := s.weatherStep(t.Context(), provider.now.Add(2*time.Hour)); err != nil || provider.calls.Load() != 1 {
		t.Fatal("auto refreshed too early", err)
	}
	if _, err := s.weatherStep(t.Context(), provider.now.Add(3*time.Hour)); err != nil || provider.calls.Load() != 2 {
		t.Fatal("auto refresh interval ignored", err)
	}
	v.StartDate, v.EndDate = v.StartDate.AddDate(0, 0, 30), v.EndDate.AddDate(0, 0, 30)
	if err := s.store.UpdateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	if count, err := s.queueWeather(t.Context(), v.ID, provider.now.Add(6*time.Hour), time.Minute); err != nil || count != 0 {
		t.Fatal("distant trip fetched", count, err)
	}
	s2, blocking, v2 := weatherTestServer(t)
	blocking.block = make(chan struct{}, 1)
	if _, err := s2.queueWeather(t.Context(), v2.ID, blocking.now, time.Minute); err != nil {
		t.Fatal(err)
	}
	stop := s2.StartWeatherWorker(context.Background())
	t.Cleanup(stop)
	select {
	case <-blocking.block:
	case <-time.After(5 * time.Second):
		t.Fatal("worker failed to start")
	}
	stop()
	caches, _ := s2.store.ListWeather(t.Context())
	for _, c := range caches {
		if c.Status != "error" || c.ErrorCode != "interrupted" {
			t.Fatal("shutdown did not persist interrupted attempt", c)
		}
	}
}
