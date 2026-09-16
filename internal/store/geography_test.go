package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestLodgingRegionMigrationPreservesExistingBookings(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	lat, lng, cost := 50.0, -1.0, 300.0
	l := &models.Lodging{VacationID: v.ID, Name: "Existing booking", Location: "Quay", Latitude: &lat, Longitude: &lng,
		Cost: &cost, Notes: "Keep this", CheckIn: time.Now(), CheckOut: time.Now()}
	if err := st.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	// Restore the version-24 lodging schema to exercise an existing database.
	if _, err := st.db.ExecContext(ctx, `ALTER TABLE lodging DROP COLUMN region;
		DELETE FROM schema_migrations WHERE version = 25`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.GetLodging(ctx, l.ID)
	if err != nil || got.Region != "" || got.Name != l.Name || got.Location != l.Location ||
		got.Notes != l.Notes || got.Cost == nil || *got.Cost != cost || !got.HasCoords() ||
		*got.Latitude != lat || *got.Longitude != lng || !got.CheckIn.Equal(l.CheckIn) || !got.CheckOut.Equal(l.CheckOut) {
		t.Fatalf("existing booking changed during migration: %+v %v", got, err)
	}
}

func TestLodgingRegionRoundtripAndInvalidation(t *testing.T) {
	for _, field := range []string{"booking", "name", "location formatting", "location", "latitude", "longitude", "cleared coordinates"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			st := newTestStore(t)
			v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
			if err := st.CreateVacation(ctx, v); err != nil {
				t.Fatal(err)
			}
			lat, lng, cost := 50.0, -1.0, 125.0
			l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Location: "Quay 12", Latitude: &lat, Longitude: &lng,
				Region: "Sussex, United Kingdom", CheckIn: time.Now(), CheckOut: time.Now()}
			if err := st.CreateLodging(ctx, l); err != nil {
				t.Fatal(err)
			}
			list, err := st.ListLodgings(ctx, v.ID)
			if err != nil || len(list) != 1 || list[0].Region != l.Region {
				t.Fatalf("region not listed: %+v %v", list, err)
			}
			// Like an inline form, this update has no region value of its own.
			l.Region = ""
			want := "Sussex, United Kingdom"
			moved := 12.0
			switch field {
			case "booking":
				l.Cost, l.Notes = &cost, "Updated booking"
				l.CheckIn = l.CheckIn.Add(time.Hour)
				l.CheckOut = l.CheckOut.Add(24 * time.Hour)
			case "name":
				l.Name = "Renamed hotel"
			case "location formatting":
				l.Location = " QUAY 12 "
			case "location":
				l.Location, want = "Other address", ""
			case "latitude":
				l.Latitude, want = &moved, ""
			case "longitude":
				l.Longitude, want = &moved, ""
			case "cleared coordinates":
				l.Latitude, l.Longitude, want = nil, nil, ""
			}
			if err := st.UpdateLodging(ctx, l); err != nil {
				t.Fatal(err)
			}
			got, err := st.GetLodging(ctx, l.ID)
			if err != nil || got.Region != want || l.Region != want {
				t.Fatalf("region invalidation: got %+v, in-memory %q, want %q: %v", got, l.Region, want, err)
			}
		})
	}
}

func TestLodgingRegionConcurrentBookingUpdate(t *testing.T) {
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
	for range 10 {
		lat, lng, cost := 50.0, -1.0, 300.0
		l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Location: "Quay", Latitude: &lat, Longitude: &lng,
			CheckIn: time.Now(), CheckOut: time.Now()}
		if err := st.CreateLodging(ctx, l); err != nil {
			t.Fatal(err)
		}
		original := *l
		l.Cost, l.PaidBy, l.Notes = &cost, &person.ID, "Concurrent edit"
		l.CheckOut = l.CheckOut.Add(48 * time.Hour)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if changed, err := st.UpdateLodgingRegion(ctx, &original, " Sussex, United Kingdom "); err != nil || !changed {
				t.Errorf("region enrichment: %v %v", changed, err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := st.UpdateLodging(ctx, l); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		got, err := st.GetLodging(ctx, l.ID)
		if err != nil || got.Region != "Sussex, United Kingdom" || got.Cost == nil || *got.Cost != cost ||
			got.PaidBy == nil || *got.PaidBy != person.ID || got.Notes != l.Notes || !got.CheckOut.Equal(l.CheckOut) {
			t.Fatalf("concurrent edit lost: %+v %v", got, err)
		}
	}
}

func TestLodgingRegionRefusesChangedSources(t *testing.T) {
	for _, field := range []string{"name", "location", "latitude", "longitude", "region", "partial coordinates"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			st := newTestStore(t)
			v := &models.Vacation{Title: "Trip", StartDate: time.Now(), EndDate: time.Now()}
			if err := st.CreateVacation(ctx, v); err != nil {
				t.Fatal(err)
			}
			lat, lng, moved := 50.0, -1.0, 12.0
			l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Location: "Quay", Latitude: &lat, Longitude: &lng,
				CheckIn: time.Now(), CheckOut: time.Now()}
			if err := st.CreateLodging(ctx, l); err != nil {
				t.Fatal(err)
			}
			original := *l
			switch field {
			case "name":
				l.Name = "New hotel"
			case "location":
				l.Location = "New address"
			case "latitude":
				l.Latitude = &moved
			case "longitude":
				l.Longitude = &moved
			case "partial coordinates":
				l.Longitude = nil
			case "region":
				if changed, err := st.UpdateLodgingRegion(ctx, l, "Resolved first"); err != nil || !changed {
					t.Fatalf("first lookup: %v %v", changed, err)
				}
			}
			if err := st.UpdateLodging(ctx, l); err != nil {
				t.Fatal(err)
			}
			if changed, err := st.UpdateLodgingRegion(ctx, &original, "Stale result"); err != nil || changed {
				t.Fatalf("stale region accepted: %v %v", changed, err)
			}
		})
	}
}

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
