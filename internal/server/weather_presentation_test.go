package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/weather"
)

func TestWeatherPresentationUsesDisplayedForecastTimestamps(t *testing.T) {
	s, _, v := weatherTestServer(t)
	now := v.StartDate.Add(10 * time.Hour)
	put := func(lat float64, updated time.Time, status string) {
		t.Helper()
		c := models.WeatherCache{Key: weather.Key(lat, 12), Latitude: lat, Longitude: 12}
		if _, err := s.store.QueueWeather(t.Context(), c, now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.store.ClaimWeather(t.Context()); err != nil {
			t.Fatal(err)
		}
		caches, err := s.store.ListWeather(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		c = caches[c.Key]
		c.Status, c.UpdatedAt = "ready", updated
		c.Samples = []models.WeatherSample{{Time: now, Temperature: 15, Code: 800, RainChance: .25, RainMM: .2, WindMS: 3}}
		if err := s.store.FinishWeather(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		if status == "error" {
			if _, err := s.store.QueueWeather(t.Context(), c, now.Add(time.Hour), now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.store.ClaimWeather(t.Context()); err != nil {
				t.Fatal(err)
			}
			caches, err := s.store.ListWeather(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			c = caches[c.Key]
			c.Status, c.ErrorCode, c.UpdatedAt = "error", "auth", now.Add(2*time.Hour)
			if err := s.store.FinishWeather(t.Context(), c); err != nil {
				t.Fatal(err)
			}
		}
	}
	put(41, now.Add(-time.Hour), "error")
	put(42, now, "ready")
	put(43, now.Add(time.Hour), "ready") // Not displayed; must not affect the header.
	for _, lang := range []i18n.Lang{i18n.LangEN, i18n.LangDE} {
		loc := i18n.NewLocalizer(lang)
		ctx := i18n.NewContext(t.Context(), loc)
		v.Lodgings = nil
		view, err := s.weatherView(ctx, v, now)
		if err != nil {
			t.Fatal(err)
		}
		want := loc.T("weather.data_as_of", now.Add(-time.Hour).Format("02.01.2006 15:04 MST"))
		if view.Updated != want {
			t.Fatal("failed refresh/unused cache changed displayed data timestamp", view.Updated, want)
		}
		entry := view.Days[0].Entries[0]
		if entry.Place != loc.T("weather.destination", v.Destination) || len(entry.Metrics) != 4 ||
			entry.Metrics[0].Value != "25%" || entry.Metrics[1].Value != "0.2 mm" ||
			entry.Metrics[2].Value != "0.0 mm" || entry.Metrics[3].Value != "11 km/h" ||
			!strings.Contains(entry.Coverage, "10:00") || strings.Contains(entry.Detail, "09:00") {
			t.Fatal("tile metrics or destination fallback presentation incorrect", entry)
		}
		for _, lat := range []float64{41, 42} {
			v.Lodgings = append(v.Lodgings, models.Lodging{Name: "Stay", Latitude: fptr(lat), Longitude: fptr(12),
				CheckIn: v.StartDate, CheckOut: v.EndDate})
		}
		view, err = s.weatherView(ctx, v, now)
		if err != nil {
			t.Fatal(err)
		}
		want = loc.T("weather.data_range", now.Add(-time.Hour).Format("02.01.2006 15:04 MST"), now.Format("02.01.2006 15:04 MST"))
		if view.Updated != want {
			t.Fatal("mixed forecast age was not disclosed", view.Updated, want)
		}
	}
	v.Lodgings = nil
	v.Latitude = nil
	view, err := s.weatherView(t.Context(), v, now)
	if err != nil || view.Updated != i18n.NewLocalizer(i18n.LangEN).T("weather.data_none") {
		t.Fatal("missing location must not advertise an unrelated timestamp", view, err)
	}
}

func TestWeatherPresentationMarkup(t *testing.T) {
	s, _, v := weatherTestServer(t)
	for _, lang := range []string{"en", "de"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+v.ID.String(), nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: lang})
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		start := strings.Index(body, `class="weather-heading"`)
		if start < 0 {
			t.Fatal("weather header missing")
		}
		end := strings.Index(body[start:], `class="weather-source`)
		if end < 0 {
			t.Fatal("weather attribution footer missing")
		}
		panel := body[start : start+end]
		if !strings.Contains(panel, "data-weather-updated") ||
			strings.Contains(panel, "weather.hint") || strings.Contains(panel, `href="/settings#weather-settings"`) ||
			strings.Contains(panel, "Last successful update") || strings.Contains(panel, "Letzte erfolgreiche Aktualisierung") {
			t.Fatal("weather header still contains removed content")
		}
	}
}
