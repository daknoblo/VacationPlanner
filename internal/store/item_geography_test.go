package store

import (
	"math"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestItemGeographyProtectsConcurrentEdits(t *testing.T) {
	for _, change := range []string{"booking", "title", "location", "coordinates", "manual region", "links", "destination"} {
		t.Run(change, func(t *testing.T) {
			st := newTestStore(t)
			v := &models.Vacation{Title: "Trip", Destination: "Denmark", StartDate: time.Now(), EndDate: time.Now()}
			if err := st.CreateVacation(t.Context(), v); err != nil {
				t.Fatal(err)
			}
			original := &models.Item{VacationID: v.ID, Title: "Blue Museum", Notes: "Notes",
				Links: []models.ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/Blue_Museum"}}}
			if err := st.CreateItem(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			current := *original
			cost, lat, lng := 125.0, 50.0, 8.0
			switch change {
			case "booking":
				current.Cost, current.Notes, current.Visited = &cost, "New notes", true
				day := time.Now().UTC()
				current.Day, current.StartMin, current.EndMin = &day, 600, 660
			case "title":
				current.Title = "A different museum"
			case "location":
				current.Location = "Changed location"
			case "coordinates":
				current.Latitude, current.Longitude = &lat, &lng
			case "manual region":
				current.Region, current.RegionManual = "My area", true
			case "links":
				current.Links = []models.ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/Another_Museum"}}
			case "destination":
				moved := *v
				moved.Destination = "Australia"
				if err := st.UpdateVacation(t.Context(), &moved); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.UpdateItem(t.Context(), &current); err != nil {
				t.Fatal(err)
			}
			changed, err := st.UpdateItemGeography(t.Context(), original, v, 55, 9, "Blue Museum, Example city", "Region")
			if err != nil || changed != (change == "booking") {
				t.Fatalf("CAS result=%v err=%v for %s", changed, err, change)
			}
			got, err := st.GetItem(t.Context(), original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change == "booking" {
				if !got.HasCoords() || got.Location != "Blue Museum, Example city" || got.Region != "Region" ||
					got.Cost == nil || *got.Cost != cost || got.Notes != "New notes" || !got.Visited ||
					got.Day == nil || got.StartMin != 600 || got.Links[0].URL != original.Links[0].URL || got.Title != original.Title {
					t.Fatalf("geography overwrote non-geographic data: %+v", got)
				}
			} else if got.Title != current.Title || got.Location != current.Location || got.Region != current.Region ||
				got.RegionManual != current.RegionManual || got.Links[0].URL != current.Links[0].URL {
				t.Fatal("concurrent geographic edit was overwritten")
			}
		})
	}
}

func TestItemGeographyKeepsManualRegionAndRejectsBadCoordinates(t *testing.T) {
	st := newTestStore(t)
	v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Museum", Region: "My region", RegionManual: true}
	if err := st.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateItemGeography(t.Context(), item, v, math.NaN(), 9, "Place", "Automatic"); err == nil {
		t.Fatal("invalid coordinates accepted")
	}
	if changed, err := st.UpdateItemGeography(t.Context(), item, v, 55, 9, "Place", "Automatic"); err != nil || !changed {
		t.Fatalf("coordinates not populated: %v %v", changed, err)
	}
	got, err := st.GetItem(t.Context(), item.ID)
	if err != nil || !got.HasCoords() || got.Region != "My region" || !got.RegionManual {
		t.Fatal("manual region overwritten")
	}
}
