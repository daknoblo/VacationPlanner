package server

import (
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestWeatherAvailabilityCountdown(t *testing.T) {
	for _, tt := range []struct {
		name, now, zone, day string
		days                 int
	}{
		{"trip begins on ninth", "2026-10-02T09:39:00Z", "Europe/Berlin", "2026-10-09", 2},
		{"following trip day", "2026-10-02T09:39:00Z", "Europe/Berlin", "2026-10-10", 3},
		{"tomorrow", "2026-10-03T09:39:00Z", "Europe/Berlin", "2026-10-09", 1},
		{"window entered", "2026-10-04T09:39:00Z", "Europe/Berlin", "2026-10-09", 0},
		{"local date ahead", "2026-10-02T23:30:00Z", "Europe/Berlin", "2026-10-09", 1},
		{"local date behind", "2026-10-03T01:00:00Z", "America/Los_Angeles", "2026-10-09", 2},
		{"autumn DST boundary", "2026-10-23T22:30:00Z", "Europe/Berlin", "2026-10-29", 1},
		{"spring DST boundary", "2026-03-27T22:30:00Z", "Europe/Berlin", "2026-04-02", 0},
		{"month boundary", "2026-10-29T10:00:00Z", "Europe/Berlin", "2026-11-05", 2},
		{"year boundary", "2026-12-29T10:00:00Z", "Europe/Berlin", "2027-01-05", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			if err != nil {
				t.Fatal(err)
			}
			tz, err := time.LoadLocation(tt.zone)
			if err != nil {
				t.Fatal(err)
			}
			for _, lang := range []i18n.Lang{i18n.LangEN, i18n.LangDE} {
				loc := i18n.NewLocalizer(lang)
				want := loc.T("weather.empty")
				if tt.days == 1 {
					want = loc.T("weather.available_one")
				} else if tt.days > 1 {
					want = loc.T("weather.available_days", tt.days)
				}
				got := weatherSummary(models.WeatherCache{}, tt.day, tz, loc, now)
				if got.Notice != want || got.Summary != "" {
					t.Fatalf("%s: got %q, want %q", lang, got.Notice, want)
				}
				for _, status := range []string{"queued", "running", "error"} {
					c := models.WeatherCache{Status: status, ErrorCode: "auth"}
					want := loc.T("weather.pending")
					if status == "error" {
						want = loc.T("weather.error.auth")
					}
					if e := weatherSummary(c, tt.day, tz, loc, now); e.Notice != want {
						t.Fatal("countdown hid request state", e)
					}
				}
			}
		})
	}
}

func TestWeatherCountdownSharedWithCalendars(t *testing.T) {
	s, _, v := weatherTestServer(t)
	now := time.Date(2026, 10, 2, 9, 39, 0, 0, time.UTC)
	v.StartDate = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	v.EndDate = v.StartDate.AddDate(0, 0, 1)
	ctx := i18n.NewContext(t.Context(), i18n.NewLocalizer(i18n.LangDE))
	view, err := s.weatherView(ctx, v, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Wetterdaten voraussichtlich in 2 Tagen verfügbar.", "Wetterdaten voraussichtlich in 3 Tagen verfügbar."}
	for i, day := range view.Days {
		if day.Entries[0].Notice != want[i] || view.ByDate[day.Date][0].Notice != want[i] {
			t.Fatal("tab and calendar countdown differ", view)
		}
	}
	v.Latitude = nil
	view, err = s.weatherView(ctx, v, now)
	if err != nil || view.Days[0].Entries[0].Notice != i18n.NewLocalizer(i18n.LangDE).T("weather.no_location") {
		t.Fatal("countdown hid missing coordinates", view, err)
	}
}
