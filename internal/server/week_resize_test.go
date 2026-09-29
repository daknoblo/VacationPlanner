package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestWeeklyActivityResizePersistence(t *testing.T) {
	s := newIntegrationServer(t)
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	cost, lat, lng := 25.0, 55.0, 8.0
	item := &models.Item{VacationID: v.ID, Title: "Museum", Description: "Keep description",
		Day: &v.StartDate, StartMin: 540, EndMin: 600, Notes: "Keep notes", Cost: &cost,
		Latitude: &lat, Longitude: &lng, Location: "Ribe", Region: "Manual region", RegionManual: true,
		Links: []models.ItemLink{{Kind: "website", URL: "https://example.com/"}}}
	if err := s.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	original, err := s.store.GetItem(t.Context(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/items/" + item.ID.String() + "/schedule"
	form := url.Values{"day": {v.StartDate.Format("2006-01-02")}, "start": {"08:00"}, "end": {"10:00"}}
	if rec := postAISettings(s, path, form, false); rec.Code != http.StatusForbidden {
		t.Fatal("missing CSRF accepted", rec.Code)
	}
	for _, times := range [][2]int{{480, 600}, {480, 720}, {690, 720}, {330, 420}, {0, 30}, {1410, 1440}} {
		form.Set("start", fmt.Sprint(times[0]))
		form.Set("end", fmt.Sprint(times[1]))
		rec := postAISettings(s, path, form, true)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`data-start="%d"`, times[0])) ||
			!strings.Contains(rec.Body.String(), fmt.Sprintf(`data-end="%d"`, times[1])) {
			t.Fatal("saved planner fragment differs from requested times", rec.Code, rec.Body.String())
		}
		got, err := s.store.GetItem(t.Context(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.StartMin != times[0] || got.EndMin != times[1] {
			t.Fatal("times were not persisted", got)
		}
		got.StartMin, got.EndMin, got.UpdatedAt = original.StartMin, original.EndMin, original.UpdatedAt
		if !reflect.DeepEqual(got, original) {
			t.Fatalf("resizing modified source fields: got %#v want %#v", got, original)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+v.ID.String(), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-week-resize="start"`) ||
		!strings.Contains(rec.Body.String(), `data-week-resize="end"`) ||
		!strings.Contains(rec.Body.String(), `data-start="1410" data-end="1440"`) {
		t.Fatal("reloaded week does not expose saved range and handles", rec.Code)
	}
	form.Set("day", v.EndDate.AddDate(0, 0, 1).Format("2006-01-02"))
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusConflict {
		t.Fatal("out-of-trip date accepted", rec.Code)
	}
}
