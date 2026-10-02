package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/weather"
)

type weatherEntry struct {
	Place    string
	Icon     string
	Summary  string
	Compact  string
	Detail   string
	Notice   string
	Metrics  []weatherMetric
	Coverage string
}

type weatherMetric struct {
	Label string
	Value string
	Hint  string
}

type weatherDay struct {
	Date    string
	Label   string
	Entries []weatherEntry
}

type weatherView struct {
	Days    []weatherDay
	ByDate  map[string][]weatherEntry
	Updated string
}

func weatherCondition(code int) (string, string) {
	switch {
	case code < 300:
		return "⛈", "storm"
	case code < 600:
		return "🌧", "rain"
	case code < 700:
		return "❄", "snow"
	case code < 800:
		return "🌫", "fog"
	case code == 800:
		return "☀", "clear"
	default:
		return "☁", "clouds"
	}
}

func weatherSummary(c models.WeatherCache, day string, tz *time.Location, loc *i18n.Localizer, now time.Time) weatherEntry {
	entry := weatherEntry{}
	var samples []models.WeatherSample
	for _, sample := range c.Samples {
		if sample.Time.In(tz).Format("2006-01-02") == day {
			samples = append(samples, sample)
		}
	}
	switch c.Status {
	case "error":
		code := c.ErrorCode
		switch code {
		case "auth", "quota", "data", "request", "interrupted", "obsolete":
		default:
			code = "request"
		}
		entry.Notice = loc.T("weather.error." + code)
	case "queued", "running":
		entry.Notice = loc.T("weather.pending")
	}
	if len(samples) == 0 {
		if entry.Notice == "" {
			switch {
			case day < now.In(tz).Format("2006-01-02"):
				entry.Notice = loc.T("weather.past")
			case day > now.Add(5*24*time.Hour).In(tz).Format("2006-01-02"):
				target, err := time.Parse("2006-01-02", day)
				if err != nil {
					entry.Notice = loc.T("weather.error.data")
					break
				}
				horizon := now.Add(5 * 24 * time.Hour).In(tz)
				year, month, date := horizon.Date()
				// Count calendar dates, not 24-hour periods across DST changes.
				horizonDate := time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
				days := int(target.Sub(horizonDate) / (24 * time.Hour))
				if days == 1 {
					entry.Notice = loc.T("weather.available_one")
				} else {
					entry.Notice = loc.T("weather.available_days", days)
				}
			case len(c.Samples) > 0 && day > c.Samples[len(c.Samples)-1].Time.In(tz).Format("2006-01-02"):
				entry.Notice = loc.T("weather.future")
			default:
				entry.Notice = loc.T("weather.empty")
			}
		}
		entry.Compact = "—"
		return entry
	}
	minT, maxT := samples[0].Temperature, samples[0].Temperature
	var rain, snow, chance, wind float64
	nearest := samples[0]
	for _, p := range samples {
		minT, maxT = min(minT, p.Temperature), max(maxT, p.Temperature)
		rain, snow = rain+p.RainMM, snow+p.SnowMM
		chance, wind = max(chance, p.RainChance), max(wind, p.WindMS)
		if math.Abs(float64(p.Time.In(tz).Hour()-12)) < math.Abs(float64(nearest.Time.In(tz).Hour()-12)) {
			nearest = p
		}
	}
	icon, condition := weatherCondition(nearest.Code)
	entry.Icon = icon
	entry.Compact = fmt.Sprintf("%.0f–%.0f °C", minT, maxT)
	entry.Summary = loc.T("weather.condition."+condition) + " · " + entry.Compact
	entry.Metrics = []weatherMetric{
		{Label: loc.T("weather.metric.chance"), Value: loc.T("weather.value.percent", chance*100), Hint: loc.T("weather.metric.maximum_hint")},
		{Label: loc.T("weather.metric.rain"), Value: loc.T("weather.value.mm", rain), Hint: loc.T("weather.metric.total_hint")},
		{Label: loc.T("weather.metric.snow"), Value: loc.T("weather.value.mm", snow), Hint: loc.T("weather.metric.total_hint")},
		{Label: loc.T("weather.metric.wind"), Value: loc.T("weather.value.wind", wind*3.6), Hint: loc.T("weather.metric.maximum_hint")},
	}
	entry.Coverage = loc.T("weather.coverage", samples[0].Time.In(tz).Format("15:04"),
		samples[len(samples)-1].Time.In(tz).Format("15:04"), len(samples))
	entry.Detail = loc.T("weather.metrics", chance*100, rain, snow, wind*3.6) + " · " +
		entry.Coverage
	if now.Sub(c.UpdatedAt) > 6*time.Hour {
		entry.Notice = strings.TrimSpace(entry.Notice + " " + loc.T("weather.stale"))
	}
	if day < now.In(tz).Format("2006-01-02") {
		entry.Notice = strings.TrimSpace(entry.Notice + " " + loc.T("weather.saved_forecast"))
	}
	return entry
}

func (s *Server) weatherView(ctx context.Context, v *models.Vacation, now time.Time) (weatherView, error) {
	caches, err := s.store.ListWeather(ctx)
	if err != nil {
		return weatherView{}, err
	}
	_, tz := s.regionSettings(ctx)
	loc := i18n.FromContext(ctx)
	view := weatherView{ByDate: make(map[string][]weatherEntry)}
	var oldest, newest time.Time
	for _, date := range v.Days() {
		day := date.Format("2006-01-02")
		d := weatherDay{Date: day, Label: date.Format("02.01.2006")}
		for _, p := range weatherPlaces(v, day, tz) {
			cache := caches[weather.Key(p.Lat, p.Lng)]
			e := weatherSummary(cache, day, tz, loc, now)
			e.Place = p.Name
			if p.Fallback {
				fallback := loc.T("weather.destination", v.Destination)
				if p.Name == v.Destination {
					e.Place = fallback
				} else {
					e.Place += " · " + fallback
				}
			}
			if !p.Located {
				e = weatherEntry{Place: p.Name, Compact: "—", Notice: loc.T("weather.no_location")}
			}
			if !s.weatherEnabled() {
				e.Notice = strings.TrimSpace(loc.T("weather.disabled") + " " + e.Notice)
			}
			if e.Summary != "" && !cache.UpdatedAt.IsZero() {
				if oldest.IsZero() || cache.UpdatedAt.Before(oldest) {
					oldest = cache.UpdatedAt
				}
				if cache.UpdatedAt.After(newest) {
					newest = cache.UpdatedAt
				}
			}
			d.Entries = append(d.Entries, e)
		}
		view.Days = append(view.Days, d)
		view.ByDate[day] = d.Entries
	}
	view.Updated = loc.T("weather.data_none")
	if !oldest.IsZero() {
		first, last := oldest.In(tz).Format("02.01.2006 15:04 MST"), newest.In(tz).Format("02.01.2006 15:04 MST")
		if first == last {
			view.Updated = loc.T("weather.data_as_of", first)
		} else {
			view.Updated = loc.T("weather.data_range", first, last)
		}
	}
	return view, nil
}

func (s *Server) handleWeather(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	v.Lodgings, err = s.store.ListLodgings(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view, err := s.weatherView(r.Context(), v, time.Now())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(view); err != nil {
		s.log.Error("encoding weather", "err", err)
	}
}

func weatherIntervalSetting(settings map[string]string) string {
	if _, ok := weatherIntervals[settings[settingWeatherInterval]]; ok {
		return settings[settingWeatherInterval]
	}
	return "off"
}

func (s *Server) handleWeatherSettings(w http.ResponseWriter, r *http.Request) {
	interval := formStr(r, "interval")
	if _, ok := weatherIntervals[interval]; !ok {
		http.Error(w, i18n.FromContext(r.Context()).T("weather.invalid_interval"), http.StatusUnprocessableEntity)
		return
	}
	if err := s.putSetting(r.Context(), settingWeatherInterval, interval); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.wakeWeather()
	s.settingSaved(w, r)
}

func (s *Server) wakeWeather() {
	select {
	case s.weatherWake <- struct{}{}:
	default:
	}
}

func (s *Server) handleWeatherRefresh(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	if !s.weatherEnabled() {
		http.Error(w, loc.T("weather.disabled"), http.StatusUnprocessableEntity)
		return
	}
	id, err := uuid.Parse(formStr(r, "vacation_id"))
	if err != nil {
		http.Error(w, loc.T("weather.choose_trip"), http.StatusUnprocessableEntity)
		return
	}
	if _, err := s.store.GetVacation(r.Context(), id); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	count, err := s.queueWeather(r.Context(), id, time.Now(), 10*time.Minute)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.wakeWeather()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, html.EscapeString(loc.T("weather.queued", count)))
}

func (s *Server) handleWeatherStatus(w http.ResponseWriter, r *http.Request) {
	caches, err := s.store.ListWeather(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var pending, failed int
	var last time.Time
	var latestFailure models.WeatherCache
	for _, c := range caches {
		if c.Status == "queued" || c.Status == "running" {
			pending++
		}
		if c.Status == "error" {
			failed++
			if c.AttemptedAt.After(latestFailure.AttemptedAt) {
				latestFailure = c
			}
		}
		if c.UpdatedAt.After(last) {
			last = c.UpdatedAt
		}
	}
	loc := i18n.FromContext(r.Context())
	status := loc.T("weather.status", pending, failed)
	if latestFailure.Key != "" {
		e := weatherSummary(latestFailure, "", time.UTC, loc, time.Now())
		status += " · " + e.Notice
	}
	settings, err := s.settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if raw := settings[settingWeatherBlockedUntil]; raw != "" {
		until, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if time.Now().Before(until) {
			_, tz := s.regionSettings(r.Context())
			status += " · " + loc.T("weather.paused", until.In(tz).Format("02.01.2006 15:04 MST"))
		}
	}
	if !last.IsZero() {
		_, tz := s.regionSettings(r.Context())
		status += " · " + loc.T("weather.updated", last.In(tz).Format("02.01.2006 15:04 MST"))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, status)
}
