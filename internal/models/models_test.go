package models

import (
	"testing"
	"time"
)

func TestVacationNights(t *testing.T) {
	// Regular range: 9 nights
	v := Vacation{
		StartDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
	}
	if got := v.Nights(); got != 9 {
		t.Fatalf("Nights() = %d, erwartet 9", got)
	}

	// Same day => 0 nights
	same := Vacation{StartDate: v.StartDate, EndDate: v.StartDate}
	if got := same.Nights(); got != 0 {
		t.Fatalf("Nights() = %d, erwartet 0", got)
	}

	// End before start => never negative
	neg := Vacation{
		StartDate: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	if got := neg.Nights(); got != 0 {
		t.Fatalf("Nights() = %d, erwartet 0 (nie negativ)", got)
	}
}

func TestVacationHasCoords(t *testing.T) {
	lat, lng := 38.7, -9.1
	if (Vacation{}).HasCoords() {
		t.Fatal("HasCoords() ohne Koordinaten muss false sein")
	}
	if !(Vacation{Latitude: &lat, Longitude: &lng}).HasCoords() {
		t.Fatal("HasCoords() mit beiden Koordinaten muss true sein")
	}
	if (Vacation{Latitude: &lat}).HasCoords() {
		t.Fatal("HasCoords() mit nur einer Koordinate muss false sein")
	}
}

func TestSightHasCoords(t *testing.T) {
	lat, lng := 1.0, 2.0
	if (Sight{}).HasCoords() {
		t.Fatal("Sight.HasCoords() ohne Koordinaten muss false sein")
	}
	if !(Sight{Latitude: &lat, Longitude: &lng}).HasCoords() {
		t.Fatal("Sight.HasCoords() mit Koordinaten muss true sein")
	}
}

func TestTravelKindValid(t *testing.T) {
	if !TravelArrival.Valid() || !TravelDeparture.Valid() {
		t.Fatal("arrival/departure müssen gültig sein")
	}
	if TravelKind("bogus").Valid() {
		t.Fatal("unbekannte Art darf nicht gültig sein")
	}
	if TravelKind("").Valid() {
		t.Fatal("leere Art darf nicht gültig sein")
	}
}
