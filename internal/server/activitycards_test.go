package server

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func fptr(f float64) *float64 { return &f }

func TestLodgingForDay(t *testing.T) {
	ci := time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC)
	co := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)
	lodgings := []models.Lodging{
		{Name: "NoGeo", CheckIn: ci, CheckOut: co}, // no coords → skipped
		{Name: "Hotel", Latitude: fptr(1), Longitude: fptr(2), CheckIn: ci, CheckOut: co},
	}
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	if l := lodgingForDay(lodgings, day(3)); l == nil || l.Name != "Hotel" {
		t.Fatalf("covered day: want Hotel, got %v", l)
	}
	if l := lodgingForDay(lodgings, day(2)); l == nil {
		t.Fatal("check-in day: want Hotel, got nil")
	}
	if l := lodgingForDay(lodgings, day(5)); l == nil {
		t.Fatal("check-out day: want Hotel, got nil")
	}
	if l := lodgingForDay(lodgings, day(6)); l != nil {
		t.Fatalf("after check-out: want nil, got %v", l)
	}
}

func TestResolveOrigin(t *testing.T) {
	hotelPt := &route.Point{Lat: 0, Lng: 0}
	a := models.Item{ID: uuid.New(), Title: "A", Latitude: fptr(1), Longitude: fptr(1), StartMin: 540, EndMin: 600}
	b := models.Item{ID: uuid.New(), Title: "B", Latitude: fptr(2), Longitude: fptr(2), StartMin: 600, EndMin: 660}
	c := models.Item{ID: uuid.New(), Title: "C", Latitude: fptr(3), Longitude: fptr(3), StartMin: 660, EndMin: 720}
	items := []models.Item{a, b, c}

	if _, label := resolveOrigin(items, 1, hotelPt, "🏨 Hotel"); label != "A" {
		t.Fatalf("auto origin of B: want A, got %q", label)
	}
	if _, label := resolveOrigin(items, 0, hotelPt, "🏨 Hotel"); label != "🏨 Hotel" {
		t.Fatalf("auto origin of first: want hotel, got %q", label)
	}
	items[2].OriginRef = "hotel"
	if _, label := resolveOrigin(items, 2, hotelPt, "🏨 Hotel"); label != "🏨 Hotel" {
		t.Fatalf("explicit hotel: got %q", label)
	}
	items[2].OriginRef = a.ID.String()
	if _, label := resolveOrigin(items, 2, hotelPt, "🏨 Hotel"); label != "A" {
		t.Fatalf("explicit item ref: want A, got %q", label)
	}
	items[2].OriginRef = uuid.New().String() // stale → auto (previous = B)
	if _, label := resolveOrigin(items, 2, hotelPt, "🏨 Hotel"); label != "B" {
		t.Fatalf("stale ref falls back to auto: want B, got %q", label)
	}
}

func TestOrderDayItems(t *testing.T) {
	early := time.Now().Add(-time.Hour)
	late := time.Now()
	items := []models.Item{
		{Title: "UB", CreatedAt: late},
		{Title: "T1", StartMin: 600, EndMin: 660},
		{Title: "UA", CreatedAt: early},
		{Title: "T2", StartMin: 540, EndMin: 600},
	}
	got := orderDayItems(items)
	want := []string{"T2", "T1", "UA", "UB"} // timed by start, then untimed by created
	for i := range want {
		if got[i].Title != want[i] {
			t.Fatalf("order = %v, want %v", []models.Item{got[0], got[1], got[2], got[3]}, want)
		}
	}
}

func TestOriginOptionsFor(t *testing.T) {
	loc := i18n.NewLocalizer(i18n.LangEN)
	a := models.Item{ID: uuid.New(), Title: "A", Latitude: fptr(1), Longitude: fptr(1)}
	b := models.Item{ID: uuid.New(), Title: "B"} // no coords → not a candidate
	c := models.Item{ID: uuid.New(), Title: "C", Latitude: fptr(3), Longitude: fptr(3), OriginRef: "hotel"}
	items := []models.Item{a, b, c}

	opts := originOptionsFor(loc, items, c, "🏨 Hotel")
	if len(opts) != 3 { // auto, hotel, A (b skipped, c self)
		t.Fatalf("want 3 options, got %d: %+v", len(opts), opts)
	}
	if opts[1].Value != "hotel" || !opts[1].Selected {
		t.Fatalf("hotel option should be present and selected: %+v", opts[1])
	}
	if opts[2].Value != a.ID.String() {
		t.Fatalf("third option should be item A, got %q", opts[2].Value)
	}

	// Without a located hotel, the hotel option is omitted.
	opts = originOptionsFor(loc, items, a, "")
	if len(opts) != 2 || opts[0].Value != "" || opts[1].Value != c.ID.String() {
		t.Fatalf("no-hotel options mismatch: %+v", opts)
	}
}
