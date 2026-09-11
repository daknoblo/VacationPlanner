package store

import (
	"context"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestGeographyLodgingCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	person := &models.Person{Name: "Payer"}
	if err := st.CreatePerson(ctx, person); err != nil {
		t.Fatal(err)
	}
	l := &models.Lodging{VacationID: v.ID, Name: "Blue Harbour Hotel", Location: "Quay 12, Brighton", CheckIn: time.Now(), CheckOut: time.Now()}
	if err := st.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	original := *l
	cost := 250.0
	l.Cost, l.PaidBy, l.Notes = &cost, &person.ID, "Booking updated concurrently"
	if err := st.UpdateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	changed, err := st.UpdateLodgingCoordinates(ctx, &original, 50, -1)
	if err != nil || !changed {
		t.Fatalf("enrichment failed: %v %v", changed, err)
	}
	got, err := st.GetLodging(ctx, l.ID)
	if err != nil || !got.HasCoords() || *got.Latitude != 50 || *got.Longitude != -1 || *got.Cost != cost || *got.PaidBy != person.ID || got.Notes != l.Notes {
		t.Fatalf("booking data changed: %+v %v", got, err)
	}
	if changed, err := st.UpdateLodgingCoordinates(ctx, &original, 51, -2); err != nil || changed {
		t.Fatalf("existing coordinates overwritten: %v %v", changed, err)
	}
}

func TestGeographyLodgingRefusesChangedSources(t *testing.T) {
	for _, field := range []string{"name", "location", "latitude", "longitude"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			st := newTestStore(t)
			v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
			if err := st.CreateVacation(ctx, v); err != nil {
				t.Fatal(err)
			}
			l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Location: "Address", CheckIn: time.Now(), CheckOut: time.Now()}
			if err := st.CreateLodging(ctx, l); err != nil {
				t.Fatal(err)
			}
			original := *l
			coordinate := 12.0
			switch field {
			case "name":
				l.Name = "New hotel"
			case "location":
				l.Location = "New address"
			case "latitude":
				l.Latitude = &coordinate
			case "longitude":
				l.Longitude = &coordinate
			}
			if err := st.UpdateLodging(ctx, l); err != nil {
				t.Fatal(err)
			}
			if changed, err := st.UpdateLodgingCoordinates(ctx, &original, 50, -1); err != nil || changed {
				t.Fatalf("stale enrichment accepted: %v %v", changed, err)
			}
		})
	}
}

func TestGeographyItemRegionPersistenceAndCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	person := &models.Person{Name: "Payer"}
	if err := st.CreatePerson(ctx, person); err != nil {
		t.Fatal(err)
	}
	lat, lng, cost := 50.0, -1.0, 45.0
	item := &models.Item{VacationID: v.ID, Title: "Walk", Latitude: &lat, Longitude: &lng}
	if err := st.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	original := *item
	item.Cost, item.PaidBy = &cost, &person.ID
	if err := st.UpdateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	if changed, err := st.UpdateItemRegion(ctx, &original, "Sussex, United Kingdom"); err != nil || !changed {
		t.Fatalf("region enrichment failed: %v %v", changed, err)
	}
	got, err := st.GetItem(ctx, item.ID)
	if err != nil || got.Region != "Sussex, United Kingdom" || got.RegionManual || got.Cost == nil || *got.Cost != cost || got.PaidBy == nil || *got.PaidBy != person.ID {
		t.Fatalf("region/cost roundtrip failed: %+v %v", got, err)
	}
	if changed, err := st.UpdateItemRegion(ctx, &original, "Other"); err != nil || changed {
		t.Fatalf("existing region overwritten: %v %v", changed, err)
	}
	got.Region, got.RegionManual = "My region", true
	if err := st.UpdateItem(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListItems(ctx, v.ID)
	if err != nil || len(list) != 1 || !list[0].RegionManual || list[0].Region != "My region" {
		t.Fatalf("manual region roundtrip failed: %+v %v", list, err)
	}
}

func TestGeographyItemRefusesChangedSources(t *testing.T) {
	for _, field := range []string{"title", "location", "latitude", "longitude", "region", "manual"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			st := newTestStore(t)
			v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
			if err := st.CreateVacation(ctx, v); err != nil {
				t.Fatal(err)
			}
			lat, lng := 50.0, -1.0
			item := &models.Item{VacationID: v.ID, Title: "Walk", Location: "Old", Latitude: &lat, Longitude: &lng}
			if err := st.CreateItem(ctx, item); err != nil {
				t.Fatal(err)
			}
			original := *item
			moved := 12.0
			switch field {
			case "title":
				item.Title = "New title"
			case "location":
				item.Location = "New location"
			case "latitude":
				item.Latitude = &moved
			case "longitude":
				item.Longitude = &moved
			case "region":
				item.Region = "New region"
			case "manual":
				item.RegionManual = true
			}
			if err := st.UpdateItem(ctx, item); err != nil {
				t.Fatal(err)
			}
			if changed, err := st.UpdateItemRegion(ctx, &original, "Sussex"); err != nil || changed {
				t.Fatalf("stale/manual region overwritten: %v %v", changed, err)
			}
		})
	}
}
