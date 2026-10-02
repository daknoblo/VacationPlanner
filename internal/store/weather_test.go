package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/weather"
)

func TestWeatherPersistentAttemptsAndPreservedForecast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weather.db")
	ctx := t.Context()
	s, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	c := models.WeatherCache{Key: weather.Key(0, 1), Longitude: 1}
	if queued, err := s.QueueWeather(ctx, c, now.Add(-time.Hour), now); err != nil || !queued {
		t.Fatal(queued, err)
	}
	if queued, err := s.QueueWeather(ctx, c, now, now); err != nil || queued {
		t.Fatal("duplicate queued", queued, err)
	}
	s.Close()
	s, err = NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	key, err := s.ClaimWeather(ctx)
	if err != nil || key != c.Key {
		t.Fatal("queue did not survive restart", key, err)
	}
	if key, err := s.ClaimWeather(ctx); err != nil || key != "" {
		t.Fatal("duplicate claim", key, err)
	}
	caches, err := s.ListWeather(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c = caches[key]
	c.Status, c.UpdatedAt = "ready", now
	c.Samples = []models.WeatherSample{{Time: now, Temperature: 18, Code: 800}}
	if err := s.FinishWeather(ctx, c); err != nil {
		t.Fatal(err)
	}
	if queued, err := s.QueueWeather(ctx, c, now.Add(-time.Minute), now); err != nil || queued {
		t.Fatal("cooldown bypassed", queued, err)
	}
	if queued, err := s.QueueWeather(ctx, c, now.Add(time.Minute), now.Add(time.Hour)); err != nil || !queued {
		t.Fatal(queued, err)
	}
	if _, err := s.ClaimWeather(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishWeather(ctx, c); err != nil {
		t.Fatal(err)
	}
	caches, _ = s.ListWeather(ctx)
	if caches[key].Status != "running" {
		t.Fatal("stale completion overwrote new attempt")
	}
	if err := s.InterruptWeather(ctx); err != nil {
		t.Fatal(err)
	}
	caches, _ = s.ListWeather(ctx)
	if caches[key].Status != "error" || len(caches[key].Samples) != 1 || !caches[key].UpdatedAt.Equal(now) {
		t.Fatal("interruption lost prior forecast", caches[key])
	}
	for i := range 110 {
		next := models.WeatherCache{Key: weather.Key(1, float64(i)), Latitude: 1, Longitude: float64(i)}
		if _, err := s.QueueWeather(ctx, next, now, now); err != nil {
			t.Fatal(err)
		}
	}
	caches, _ = s.ListWeather(ctx)
	pending := 0
	for _, entry := range caches {
		if entry.Status == "queued" {
			pending++
		}
	}
	if pending != 100 {
		t.Fatal("queue is not bounded", pending)
	}
}
