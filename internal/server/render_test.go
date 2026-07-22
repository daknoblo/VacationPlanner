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
	calLodging := map[string][]lodgingStrip{"2026-08-02": {{StartMin: 900, EndMin: 1440, Name: "Hotel Central"}}}
	lodgingList := []lodgingView{{ID: "l1", Name: "Hotel Central", Location: "Center", Nights: 3, CheckIn: "02.08.2026 15:00", CheckOut: "05.08.2026 11:00"}}
	weekCal := buildWeekCalendar(loc, time.UTC, true, v)
	weekHeaders := calWeekdayHeaders(loc, true)
	hourRows := calHourRows()
	sampleCards := map[string][]overviewActivity{
		"2026-08-02": {{
			ItemID: v.Items[0].ID.String(), Weekday: "Sunday", DateLabel: "02.08.2026",
			TimeLabel: "09:00", Title: "Torre de Belém", Category: "Wahrzeichen",
			DistanceLabel: "2.1 km", DurationLabel: "6 min", OriginLabel: "🛏 Hotel Central",
			Approx: true, HasCoords: true, Latitude: v.Latitude, Longitude: v.Longitude,
			Origins: []originOption{{Value: "", Label: "Automatic (previous stop)", Selected: true}, {Value: "hotel", Label: "🛏 Hotel Central"}},
		}},
	}
	sampleWeekCards := []weekCardGroup{{Label: "02.08.2026", Cards: sampleCards["2026-08-02"]}}
	arrivalTotal := travelTotalFor(v, models.TravelArrival, false)
	departureTotal := travelTotalFor(v, models.TravelDeparture, false)

	cases := []struct {
		name string
		data viewData
	}{
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{*v}}}},
		{"index", viewData{Title: "t", Data: map[string]any{"Vacations": []models.Vacation{}}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": true, "Budget": newBudgetView(v, v.Items, nil, "€", "Lodging"), "Categories": []models.Category{{Name: "Activity", Icon: "🎯"}}, "ArrivalTravel": arrivalEditor, "DepartureTravel": departureEditor, "ArrivalTotal": arrivalTotal, "DepartureTotal": departureTotal, "CalTravel": calTravel, "CalLodging": calLodging, "Lodgings": lodgingList, "WeekCalendar": weekCal, "WeekHeaders": weekHeaders, "HourRows": hourRows, "DayCards": sampleCards, "WeekCards": sampleWeekCards}}},
		{"vacation", viewData{Title: "t", Data: map[string]any{"Vacation": v, "AIEnabled": false, "Budget": newBudgetView(v, v.Items, nil, "€", "Lodging"), "Categories": []models.Category{}, "ArrivalTravel": arrivalEditor, "DepartureTravel": departureEditor, "ArrivalTotal": arrivalTotal, "DepartureTotal": departureTotal, "CalTravel": calTravel, "CalLodging": calLodging, "Lodgings": lodgingList, "WeekCalendar": weekCal, "WeekHeaders": weekHeaders, "HourRows": hourRows, "DayCards": sampleCards, "WeekCards": sampleWeekCards}}},
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
		{"budget_panel", newBudgetView(v, v.Items, nil, "€", "Lodging")},
		{"overview_list", []overviewActivity{{DateLabel: "01.08.2026", TimeLabel: "09:00", Weekday: "Saturday", Title: "Test", Category: "POI"}}},
		{"activity_card", overviewActivity{ItemID: v.Items[0].ID.String(), DateLabel: "02.08.2026", TimeLabel: "09:00", Weekday: "Sunday", Title: "Belém", Category: "POI", OriginLabel: "🛏 Hotel", DistanceLabel: "2.1 km", DurationLabel: "6 min", Approx: true, HasCoords: true, Origins: []originOption{{Value: "", Label: "Auto", Selected: true}, {Value: "hotel", Label: "🛏 Hotel"}}}},
		{"activity_card", overviewActivity{DateLabel: "02.08.2026", Weekday: "Sunday", Title: "Arrival · BER → LIS", Category: "flight", DistanceLabel: "1860 km", DurationLabel: "2 h 20 min", IsTravel: true}},
		{"activity_cards", []overviewActivity{{DateLabel: "02.08.2026", Weekday: "Sunday", Title: "Belém", Category: "POI"}}},
		{"activity_cards", []overviewActivity(nil)},
		{"week_cards", []weekCardGroup{{Label: "02.08.2026 – 08.08.2026", Cards: []overviewActivity{{DateLabel: "02.08.2026", Weekday: "Sunday", Title: "Belém"}}}}},
		{"week_cards", []weekCardGroup(nil)},
		{"travel_total", travelTotalView{Kind: models.TravelArrival, DistLabel: "1860 km", DurLabel: "2 h 20 min"}},
		{"travel_saved", map[string]any{"Step": travelEditorView{Seg: &v.TravelSegments[0], VID: v.ID.String(), Kind: models.TravelArrival, DistLabel: "1860 km", DurLabel: "2 h 20 min"}, "Total": travelTotalView{Kind: models.TravelArrival, DistLabel: "1860 km", DurLabel: "2 h 20 min", OOB: true}}},
		{"travel_block_wrap", map[string]any{"Block": travelBlockView{Kind: models.TravelArrival, VID: v.ID.String(), Steps: []travelEditorView{{Seg: &v.TravelSegments[0], VID: v.ID.String(), Kind: models.TravelArrival, Number: 1, StepOrder: 0}}}, "Total": travelTotalView{Kind: models.TravelArrival, OOB: true}}},
		{"ideas_backlog", []models.Item{v.Items[0]}},
		{"item_edit", map[string]any{"Item": v.Items[0], "Cats": []models.Category{{Name: "POI", Icon: "📍"}}, "CSRF": "tok"}},
		{"destination_info", destinationInfoView{Destination: "Lisbon", Description: "capital of Portugal", Extract: "Lisbon is the capital of Portugal.", URL: "https://en.wikipedia.org/wiki/Lisbon"}},
		{"form_error", "etwas ist schiefgelaufen"},
		{"lodging_row", lodgingView{ID: "l1", Name: "Hotel Central", Location: "Center", Nights: 3, CheckIn: "02.08.2026 15:00", CheckOut: "05.08.2026 11:00"}},
		{"attachments", attachmentsView{
			ListURL: "/items/" + v.ID.String() + "/documents",
			CSRF:    "tok",
			Docs: []documentView{
				{ID: "11111111-1111-1111-1111-111111111111", Filename: "ferry-ticket.pdf", Icon: "📄", Href: "/documents/11111111-1111-1111-1111-111111111111", Preview: "pdf"},
			},
		}},
		{"attachments", attachmentsView{ListURL: "/items/x/documents", CSRF: "tok", Error: "too large"}},
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
