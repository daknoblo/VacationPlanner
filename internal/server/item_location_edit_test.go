package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIdeaLocationEditEnablesRegionLookupWithoutLosingBooking(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := t.Context()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	person := &models.Person{Name: "Example payer"}
	if err := s.store.CreatePerson(ctx, person); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{
		VacationID: v.ID, Title: "Keep my idea title", Category: "Museum", Notes: "Keep notes",
		Links: []models.ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/Ribe"}},
		Cost:  fptr(25), PaidBy: &person.ID,
	}
	if err := s.store.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/items/"+item.ID.String()+"/edit", nil))
	for _, want := range []string{`name="location"`, `data-geo-lite`, `name="latitude"`, `name="longitude"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("location editor lacks %s", want)
		}
	}
	form := url.Values{
		"title": {item.Title}, "category": {item.Category}, "cost": {"25"}, "paid_by": {person.ID.String()},
		"location": {"Ribe, Denmark"}, "latitude": {"55.328"}, "longitude": {"8.762"},
	}
	rec = postAISettings(s, "/items/"+item.ID.String()+"/edit", form, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	got, err := s.store.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasCoords() || *got.Latitude != 55.328 || got.Location != "Ribe, Denmark" || got.Region != "" ||
		got.Title != item.Title || len(got.Links) != 1 || got.Links[0] != item.Links[0] ||
		got.Notes != item.Notes || got.Cost == nil || *got.Cost != 25 || got.PaidBy == nil || *got.PaidBy != person.ID {
		t.Fatalf("location edit lost saved booking data: %+v", got)
	}
	if !s.geographyStatus(v.ID).Pending {
		t.Fatal("saving a located idea did not queue its region lookup")
	}
}

func TestIdeaLocationEditPreservesOrInvalidatesRegions(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual"}[manual], func(t *testing.T) {
			s := newIntegrationServer(t)
			v := sampleVacation()
			if err := s.store.CreateVacation(t.Context(), v); err != nil {
				t.Fatal(err)
			}
			item := &models.Item{
				VacationID: v.ID, Title: "Museum", Location: "Old address", Latitude: fptr(55), Longitude: fptr(9),
				Region: "Old region", RegionManual: manual,
			}
			if err := s.store.CreateItem(t.Context(), item); err != nil {
				t.Fatal(err)
			}
			edit := func(values url.Values) *models.Item {
				t.Helper()
				values.Set("title", item.Title)
				rec := postAISettings(s, "/items/"+item.ID.String()+"/edit", values, true)
				if rec.Code != http.StatusOK {
					t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
				}
				got, err := s.store.GetItem(t.Context(), item.ID)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			if got := edit(url.Values{}); got.Region != "Old region" || !got.HasCoords() || got.Location != item.Location {
				t.Fatalf("legacy edit dropped coordinates or region: %+v", got)
			}
			got := edit(url.Values{"location": {item.Location}, "latitude": {"55"}, "longitude": {"9"}, "region": {"Old region"}})
			if got.Region != "Old region" || got.RegionManual != manual {
				t.Fatal("unchanged location changed cached region ownership")
			}
			got = edit(url.Values{"location": {"New address"}, "latitude": {"56"}, "longitude": {"10"}, "region": {"Old region"}})
			if got.RegionManual != manual || manual && got.Region != "Old region" || !manual && got.Region != "" {
				t.Fatalf("wrong invalidation after moving idea: %+v", got)
			}
			got = edit(url.Values{"location": {""}, "latitude": {""}, "longitude": {""}})
			if got.HasCoords() || got.Location != "" {
				t.Fatal("clearing a location retained stale coordinates")
			}
		})
	}
}

func TestIdeaLocationEditRejectsInvalidCoordinates(t *testing.T) {
	s := newIntegrationServer(t)
	v := sampleVacation()
	if err := s.store.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Museum", Region: "Original"}
	if err := s.store.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	for _, coords := range [][2]string{{"NaN", "8"}, {"55", "NaN"}, {"91", "8"}, {"55", "181"}, {"55", ""}} {
		rec := postAISettings(s, "/items/"+item.ID.String()+"/edit", url.Values{
			"title": {item.Title}, "latitude": {coords[0]}, "longitude": {coords[1]},
		}, true)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("accepted %v: %d %s", coords, rec.Code, rec.Body.String())
		}
		got, err := s.store.GetItem(t.Context(), item.ID)
		if err != nil || got.HasCoords() || got.Region != "Original" {
			t.Fatalf("invalid edit modified stored data: %+v, %v", got, err)
		}
	}
}
