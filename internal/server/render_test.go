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
	cost := 25.0
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
			Longitude: &lng, Day: &planned, StartMin: 540, EndMin: 660, Cost: &cost, Notes: "früh hin",
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

	loc := i18n.NewLocalizer(i18n.LangEN)
	arrivalEditor := travelBlockView{Kind: models.TravelArrival, VID: v.ID.String(), Steps: []travelEditorView{{Seg: &v.TravelSegments[0], VID: v.ID.String(), Kind: models.TravelArrival, Number: 1, StepOrder: 0, DepartDate: "2026-08-01", DepartTime: "09:30", DistLabel: "1860 km", DurLabel: "2 h 20 min", ArriveLabel: "01.08.2026 11:50"}}}
	departureEditor := travelBlockView{Kind: models.TravelDeparture, VID: v.ID.String(), Steps: []travelEditorView{{Seg: emptyTravelSegment(v.ID, models.TravelDeparture), VID: v.ID.String(), Kind: models.TravelDeparture, Number: 1, StepOrder: 0, DepartDate: "2026-08-10"}}}
	calTravel := map[string][]calTravelBlock{"2026-08-01": {{StartMin: 570, EndMin: 720, Title: "Arrival · BER → LIS", Label: "09:30–12:00"}}}
	weekCal := buildWeekCalendar(loc, time.UTC, true, v)
	weekHeaders := calWeekdayHeaders(loc, true)
	hourRows := calHourRows()

	cases := []struct {
		name string
		data viewData
	}{
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{*v}}}},
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{}}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": true, "Budget": newBudgetView(v, v.Items, nil), "Categories": []models.Category{{Name: "Activity", Icon: "🎯"}}, "ArrivalTravel": arrivalEditor, "DepartureTravel": departureEditor, "CalTravel": calTravel, "WeekCalendar": weekCal, "WeekHeaders": weekHeaders, "HourRows": hourRows}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": false, "Budget": newBudgetView(v, v.Items, nil), "Categories": []models.Category{}, "ArrivalTravel": arrivalEditor, "DepartureTravel": departureEditor, "CalTravel": calTravel, "WeekCalendar": weekCal, "WeekHeaders": weekHeaders, "HourRows": hourRows}}},
	}
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
		{"vacation_card", dashboardCard{Vacation: *v, HasBudget: true, Percent: 42, Countdown: "in 17 days"}},
		{"item_row", v.Items[0]},
		{"travel_item", v.TravelSegments[0]},
		{"travel_out", travelEditorView{Seg: &v.TravelSegments[0], VID: v.ID.String(), DistLabel: "1860 km", DurLabel: "2 h 20 min", ArriveLabel: "01.08.2026 11:50"}},
		{"travel_step", travelEditorView{Seg: &v.TravelSegments[0], VID: v.ID.String(), Kind: models.TravelArrival, Number: 1, StepOrder: 0, Multi: true, Home: "Hamburg"}},
		{"travel_block", travelBlockView{Kind: models.TravelArrival, VID: v.ID.String(), Multi: true, Steps: []travelEditorView{{Seg: &v.TravelSegments[0], VID: v.ID.String(), Kind: models.TravelArrival, Number: 1, StepOrder: 0, Multi: true}}}},
		{"detail_head", map[string]any{"V": v, "OOB": true}},
		{"budget_panel", newBudgetView(v, v.Items, nil)},
		{"overview_list", []overviewActivity{{WhenLabel: "01.08.2026 · 09:00", Weekday: "Saturday", Title: "Test", Category: "POI"}}},
		{"ideas_backlog", []models.Item{v.Items[0]}},
		{"item_edit", map[string]any{"Item": v.Items[0], "Cats": []models.Category{{Name: "POI", Icon: "📍"}}, "CSRF": "tok"}},
		{"destination_info", destinationInfoView{Destination: "Lisbon", Description: "capital of Portugal", Extract: "Lisbon is the capital of Portugal.", URL: "https://en.wikipedia.org/wiki/Lisbon"}},
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
