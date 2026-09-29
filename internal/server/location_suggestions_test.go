package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestLocationSuggestionEditorAndExplicitSave(t *testing.T) {
	s := newIntegrationServer(t)
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Skyline", Notes: "Keep notes"}
	if err := s.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SaveLocationSuggestion(t.Context(), item, v, models.LocationSuggestion{
		Label: "Skyline, Hamburg", Name: "Skyline", Latitude: 53.55, Longitude: 10,
	}); err != nil {
		t.Fatal(err)
	}
	path := "/items/" + item.ID.String()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+"/edit", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `data-label="Skyline, Hamburg"`) ||
		!strings.Contains(rec.Body.String(), "data-location-accept") || !strings.Contains(rec.Body.String(), "data-location-reject") {
		t.Fatal("editor lost saved proposal", rec.Code, rec.Body.String())
	}
	records, err := s.store.ListLocationSuggestions(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	reject := url.Values{"suggestion_id": {records[item.ID].ID.String()}}
	if rec := postAISettings(s, path+"/location-suggestion/reject", reject, false); rec.Code != http.StatusForbidden {
		t.Fatal("unprotected rejection", rec.Code)
	}
	if rec := postAISettings(s, path+"/location-suggestion/reject", reject, true); rec.Code != http.StatusNoContent {
		t.Fatal("rejection failed", rec.Code, rec.Body.String())
	}
	got, err := s.store.GetItem(t.Context(), item.ID)
	if err != nil || got.HasCoords() || got.Location != "" {
		t.Fatal("reading/rejecting proposal changed location", got, err)
	}
	form := url.Values{"title": {item.Title}, "location": {"Skyline, Hamburg"}, "latitude": {"53.55"}, "longitude": {"10"}}
	if rec := postAISettings(s, path+"/edit", form, true); rec.Code != http.StatusOK {
		t.Fatal("explicit location save failed", rec.Code, rec.Body.String())
	}
	got, err = s.store.GetItem(t.Context(), item.ID)
	if err != nil || !got.HasCoords() || *got.Latitude != 53.55 || got.Title != item.Title || got.Notes != item.Notes {
		t.Fatal("explicit save failed to preserve source", got, err)
	}
	records, err = s.store.ListLocationSuggestions(t.Context(), v.ID)
	if err != nil || len(records) != 0 {
		t.Fatal("accepted location still has an active proposal", records, err)
	}
}

func TestSpellingSuggestionsRemainLocalAndUnambiguous(t *testing.T) {
	v := &models.Vacation{Destination: "Hamburg", Latitude: fptr(53.55), Longitude: fptr(10)}
	anchors := ideaGeographyAnchors(v, nil)
	result := geo.Result{Name: "Skyline Restaurant", DisplayName: "Skyline Restaurant, Hamburg", City: "Hamburg",
		Type: "restaurant", Lat: 53.55, Lng: 10}
	item := &models.Item{Title: "Restaurant Skylien Hamburg"}
	if _, ok, _ := suggestIdeaPlace(item, v, anchors, []geo.Result{result}); !ok {
		t.Fatal("transposed spelling not proposed")
	}
	distant := result
	distant.Lat, distant.Lng = 42, -78
	for _, results := range [][]geo.Result{{result, result}, {distant}, {{Name: "Something unrelated", Lat: 53.55, Lng: 10}}} {
		if _, ok, _ := suggestIdeaPlace(item, v, anchors, results); ok {
			t.Fatal("ambiguous, distant or unrelated spelling guessed")
		}
	}
}
