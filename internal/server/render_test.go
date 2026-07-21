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
	budget := 1500.0
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
		Budget:      &budget,
		People:      2,
		TravelSegments: []models.TravelSegment{{
			ID: uuid.New(), VacationID: id, Kind: models.TravelArrival,
			Mode: "flight", FromLocation: "BER", ToLocation: "LIS", DepartAt: &depart,
		}},
		Items: []models.Item{{
			ID: uuid.New(), VacationID: id, Title: "Torre de Belém",
			Category: "Wahrzeichen", Description: "UNESCO-Turm", Latitude: &lat,
			Longitude: &lng, Day: &planned, StartMin: 540, EndMin: 660, Notes: "früh hin",
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

	arrivalEditor := travelEditorView{Seg: &v.TravelSegments[0], VID: v.ID.String(), DistLabel: "1860 km", DurLabel: "2 h 20 min"}
	departureEditor := travelEditorView{Seg: emptyTravelSegment(v.ID, models.TravelDeparture), VID: v.ID.String()}

	cases := []struct {
		name string
		data viewData
	}{
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{*v}}}},
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{}}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": true, "Budget": newBudgetView(v, 0), "Categories": []models.Category{{Name: "Activity", Icon: "🎯"}}, "ArrivalTravel": arrivalEditor, "DepartureTravel": departureEditor}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": false, "Budget": newBudgetView(v, 120), "Categories": []models.Category{}, "ArrivalTravel": arrivalEditor, "DepartureTravel": departureEditor}}},
	}
	loc := i18n.NewLocalizer(i18n.LangEN)
	for _, c := range cases {
		rec := httptest.NewRecorder()
		if err := r.page(rec, c.name, loc, c.data, time.UTC); err != nil {
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
		{"item_row", v.Items[0]},
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
		if err := r.fragment(rec, f.name, loc, f.data, time.UTC); err != nil {
			t.Fatalf("render fragment %q: %v", f.name, err)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("render fragment %q produced empty output", f.name)
		}
	}
}
