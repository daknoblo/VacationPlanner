package server

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/weather"
)

const settingWeatherInterval = "weather.interval"
const settingWeatherBlockedUntil = "weather.blocked_until"

var weatherIntervals = map[string]time.Duration{"off": 0, "3": 3 * time.Hour, "6": 6 * time.Hour, "12": 12 * time.Hour}

type weatherProvider interface {
	Enabled() bool
	Forecast(context.Context, float64, float64) ([]models.WeatherSample, error)
}

type weatherPlace struct {
	Name     string
	Fallback bool
	Located  bool
	Lat, Lng float64
}

func weatherPlaces(v *models.Vacation, day string, tz *time.Location) []weatherPlace {
	lodgings := append([]models.Lodging(nil), v.Lodgings...)
	sort.Slice(lodgings, func(i, j int) bool {
		if !lodgings[i].CheckIn.Equal(lodgings[j].CheckIn) {
			return lodgings[i].CheckIn.Before(lodgings[j].CheckIn)
		}
		return lodgings[i].ID.String() < lodgings[j].ID.String()
	})
	var places []weatherPlace
	fallback := func(name string) weatherPlace {
		p := weatherPlace{Name: name, Fallback: true}
		if v.HasCoords() && weather.ValidCoordinates(*v.Latitude, *v.Longitude) {
			p.Located, p.Lat, p.Lng = true, *v.Latitude, *v.Longitude
		}
		return p
	}
	for _, l := range lodgings {
		if !l.CheckOut.After(l.CheckIn) || day < l.CheckIn.In(tz).Format("2006-01-02") ||
			day > l.CheckOut.In(tz).Format("2006-01-02") {
			continue
		}
		p := fallback(l.Name)
		if l.HasCoords() && weather.ValidCoordinates(*l.Latitude, *l.Longitude) {
			p = weatherPlace{Name: l.Name, Located: true, Lat: *l.Latitude, Lng: *l.Longitude}
		}
		places = append(places, p)
	}
	if len(places) == 0 {
		places = append(places, fallback(v.Destination))
	}
	return places
}

func (s *Server) weatherEnabled() bool { return s.weather != nil && s.weather.Enabled() }

// Only the intersection of the trip and the provider's next 120 hours is eligible.
func (s *Server) eligibleWeather(ctx context.Context, vacationID uuid.UUID, now time.Time) (map[string]models.WeatherCache, error) {
	vacations, err := s.store.ListVacations(ctx)
	if err != nil {
		return nil, err
	}
	_, tz := s.regionSettings(ctx)
	today := now.In(tz).Format("2006-01-02")
	last := now.Add(5 * 24 * time.Hour).In(tz).Format("2006-01-02")
	points := make(map[string]models.WeatherCache)
	for _, v := range vacations {
		if vacationID != uuid.Nil && v.ID != vacationID || v.Archived ||
			v.EndDate.Format("2006-01-02") < today || v.StartDate.Format("2006-01-02") > last {
			continue
		}
		v.Lodgings, err = s.store.ListLodgings(ctx, v.ID)
		if err != nil {
			return nil, err
		}
		first := max(today, v.StartDate.Format("2006-01-02"))
		end := min(last, v.EndDate.Format("2006-01-02"))
		start, err := time.Parse("2006-01-02", first)
		if err != nil {
			return nil, err
		}
		for d := start; d.Format("2006-01-02") <= end; d = d.AddDate(0, 0, 1) {
			for _, p := range weatherPlaces(&v, d.Format("2006-01-02"), tz) {
				if p.Located {
					key := weather.Key(p.Lat, p.Lng)
					points[key] = models.WeatherCache{Key: key, Latitude: p.Lat, Longitude: p.Lng}
				}
			}
		}
	}
	return points, nil
}

func (s *Server) queueWeather(ctx context.Context, id uuid.UUID, now time.Time, interval time.Duration) (int, error) {
	points, err := s.eligibleWeather(ctx, id, now)
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(points))
	for key := range points {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	count := 0
	for _, key := range keys {
		queued, err := s.store.QueueWeather(ctx, points[key], now.Add(-interval), now)
		if err != nil {
			return count, err
		}
		if queued {
			count++
		}
		if count == 100 {
			break
		}
	}
	return count, nil
}

func (s *Server) StartWeatherWorker(parent context.Context) func() {
	s.weatherStart.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		done := make(chan struct{})
		s.weatherStop = func() { cancel(); <-done }
		go func() {
			defer close(done)
			if err := s.store.InterruptWeather(ctx); err != nil {
				s.log.Error("interrupting old weather requests", "err", err)
				return
			}
			timer := time.NewTimer(0)
			defer timer.Stop()
			var nextRequest time.Time
			for {
				select {
				case <-ctx.Done():
					return
				case <-s.weatherWake:
				case <-timer.C:
				}
				if remaining := time.Until(nextRequest); remaining > 0 {
					timer.Reset(remaining)
					continue
				}
				delay := time.Minute
				if s.weatherEnabled() {
					busy, err := s.weatherStep(ctx, time.Now())
					if err != nil && ctx.Err() == nil {
						s.log.Error("weather worker failed", "err", err)
					}
					if busy {
						delay = 2 * time.Second
						nextRequest = time.Now().Add(delay)
					}
				}
				timer.Reset(delay)
			}
		}()
	})
	return s.weatherStop
}

func (s *Server) weatherStep(ctx context.Context, now time.Time) (bool, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	if raw := settings[settingWeatherBlockedUntil]; raw != "" {
		until, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return false, err
		}
		if now.Before(until) {
			return false, nil
		}
	}
	if interval := weatherIntervals[settings[settingWeatherInterval]]; interval > 0 {
		if _, err := s.queueWeather(ctx, uuid.Nil, now, interval); err != nil {
			return false, err
		}
	}
	key, err := s.store.ClaimWeather(ctx)
	if err != nil || key == "" {
		return false, err
	}
	caches, err := s.store.ListWeather(ctx)
	if err != nil {
		return false, errors.Join(err, s.store.InterruptWeather(ctx))
	}
	c := caches[key]
	points, err := s.eligibleWeather(ctx, uuid.Nil, now)
	if err != nil {
		c.Status, c.ErrorCode = "error", "request"
		return false, errors.Join(err, s.store.FinishWeather(ctx, c))
	}
	c.Status, c.ErrorCode = "error", "obsolete"
	if _, current := points[key]; current {
		samples, callErr := s.weather.Forecast(ctx, c.Latitude, c.Longitude)
		if callErr == nil {
			if len(samples) == 0 || samples[0].Time.Before(now.Add(-6*time.Hour)) ||
				samples[len(samples)-1].Time.After(now.Add(6*24*time.Hour)) {
				callErr = weather.ErrData
			}
		}
		if callErr != nil {
			c.ErrorCode = weather.ErrorCode(callErr)
			// Log only a fixed code, never a URL or a provider response containing credentials.
			s.log.Warn("weather forecast failed", "code", c.ErrorCode)
		} else {
			c.Status, c.ErrorCode, c.Samples, c.UpdatedAt = "ready", "", samples, now
		}
	}
	finishCtx := ctx
	if errors.Is(ctx.Err(), context.Canceled) {
		var cancel context.CancelFunc
		finishCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		c.Status, c.ErrorCode = "error", "interrupted"
	}
	finishErr := s.store.FinishWeather(finishCtx, c)
	if c.ErrorCode == "quota" {
		finishErr = errors.Join(finishErr, s.store.PutSetting(finishCtx, settingWeatherBlockedUntil,
			now.Add(time.Hour).UTC().Format(time.RFC3339)))
	}
	return true, finishErr
}
