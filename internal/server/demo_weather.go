package server

import (
	"context"
	"errors"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/weather"
)

type demoWeather struct{}

func (demoWeather) Enabled() bool { return true }
func (demoWeather) Forecast(context.Context, float64, float64) ([]models.WeatherSample, error) {
	return nil, errors.New("demo: weather requests are forbidden")
}

func (s *Server) seedDemoWeather(ctx context.Context, v *models.Vacation) error {
	lodgings, err := s.store.ListLodgings(ctx, v.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, l := range lodgings {
		if !l.HasCoords() {
			continue
		}
		c := models.WeatherCache{Key: weather.Key(*l.Latitude, *l.Longitude), Latitude: *l.Latitude, Longitude: *l.Longitude}
		if _, err := s.store.QueueWeather(ctx, c, now, now); err != nil {
			return err
		}
		if _, err := s.store.ClaimWeather(ctx); err != nil {
			return err
		}
		caches, err := s.store.ListWeather(ctx)
		if err != nil {
			return err
		}
		c = caches[c.Key]
		c.Status, c.UpdatedAt = "ready", now
		for i := range 40 {
			code := 800
			if i%3 == 0 {
				code = 500
			}
			c.Samples = append(c.Samples, models.WeatherSample{Time: v.StartDate.Add(time.Duration(i) * 3 * time.Hour),
				Temperature: 17 + float64(i%8), Code: code, RainChance: .3, RainMM: .2, WindMS: 3})
		}
		if err := s.store.FinishWeather(ctx, c); err != nil {
			return err
		}
	}
	return nil
}
