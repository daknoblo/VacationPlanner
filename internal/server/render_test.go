package server

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func sampleVacation() *models.Vacation {
	lat, lng := 38.7223, -9.1393
	depart := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	planned := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	id := uuid.New()
	return &models.Vacation{
		ID:          id,
		Title:       "Sommer in Lissabon",
		Destination: "Lissabon, Portugal",
		StartDate:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Latitude:    &lat,
		Longitude:   &lng,
		Notes:       "Budget: 1500€",
		TravelSegments: []models.TravelSegment{{
			ID: uuid.New(), VacationID: id, Kind: models.TravelArrival,
			Mode: "flight", FromLocation: "BER", ToLocation: "LIS", DepartAt: &depart,
		}},
		Sights: []models.Sight{{
			ID: uuid.New(), VacationID: id, Name: "Torre de Belém",
			Category: "Wahrzeichen", Description: "UNESCO-Turm", Latitude: &lat,
			Longitude: &lng, PlannedDate: &planned, Notes: "früh hin",
		}},
	}
}

func TestNewRendererParses(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}
	if _, ok := r.pages["index"]; !ok {
		t.Fatal("index page not registered")
	}
	if _, ok := r.pages["vacation"]; !ok {
		t.Fatal("vacation page not registered")
	}
}

func TestRenderPages(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}

	v := sampleVacation()

	cases := []struct {
		name string
		data viewData
	}{
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{*v}}}},
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{}}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": true}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": false}}},
	}
	loc := i18n.NewLocalizer(i18n.LangEN)
	for _, c := range cases {
		rec := httptest.NewRecorder()
		if err := r.page(rec, c.name, loc, c.data); err != nil {
			t.Fatalf("render page %q: %v", c.name, err)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("render page %q produced empty output", c.name)
		}
	}
}

func TestRenderFragments(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}
	v := sampleVacation()

	fragments := []struct {
		name string
		data any
	}{
		{"vacation_card", v},
		{"sight_item", v.Sights[0]},
		{"travel_item", v.TravelSegments[0]},
		{"form_error", "etwas ist schiefgelaufen"},
		{"ai_suggestions", aiSuggestionsView{
			VacationID:  v.ID.String(),
			Suggestions: []ai.Suggestion{{Name: "Castelo", Category: "Burg", Description: "d", Reason: "r"}},
		}},
		{"ai_suggestions", aiSuggestionsView{VacationID: v.ID.String(), Error: "deaktiviert"}},
	}
	loc := i18n.NewLocalizer(i18n.LangEN)
	for _, f := range fragments {
		rec := httptest.NewRecorder()
		if err := r.fragment(rec, f.name, loc, f.data); err != nil {
			t.Fatalf("render fragment %q: %v", f.name, err)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("render fragment %q produced empty output", f.name)
		}
	}
}
