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
		{Name: "NoGeo", CheckIn: ci, CheckOut: co},
		{Name: "Hotel", Latitude: fptr(1), Longitude: fptr(2), CheckIn: ci, CheckOut: co},
	}

	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }
	if l := lodgingForDay(time.UTC, lodgings, day(3)); l == nil || l.Name != "Hotel" {
		t.Fatalf("covered day: want Hotel, got %v", l)
	}
	if l := lodgingForDay(time.UTC, lodgings, day(2)); l == nil {
		t.Fatal("check-in day: want Hotel, got nil")
	}
	if l := lodgingForDay(time.UTC, lodgings, day(5)); l == nil {
		t.Fatal("check-out day: want Hotel, got nil")
	}
	if l := lodgingForDay(time.UTC, lodgings, day(6)); l != nil {
		t.Fatalf("after check-out: want nil, got %v", l)
	}
}

func TestDayHotelChangeAndMissingCoordinates(t *testing.T) {
	loc := i18n.NewLocalizer(i18n.LangEN)
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	old := models.Lodging{ID: uuid.New(), Name: "Old hotel", CheckIn: day.Add(-72 * time.Hour), CheckOut: day.Add(11 * time.Hour), Latitude: fptr(1), Longitude: fptr(2)}
	next := models.Lodging{ID: uuid.New(), Name: "New hotel", CheckIn: day.Add(15 * time.Hour), CheckOut: day.Add(72 * time.Hour), Latitude: fptr(3), Longitude: fptr(4)}
	for _, stays := range [][]models.Lodging{{old, next}, {next, old}} {
		v := &models.Vacation{Lodgings: stays}
		pt, label := dayHotel(loc, time.UTC, v, day)
		if pt == nil || pt.Lat != 1 || label != "🛏 Old hotel" {
			t.Fatalf("checkout day's overnight hotel must win independently of input order: %v %q", pt, label)
		}
		pt, label = dayHotel(loc, time.UTC, v, day.AddDate(0, 0, 1))
		if pt == nil || pt.Lat != 3 || label != "🛏 New hotel" {
			t.Fatalf("following day must use new hotel: %v %q", pt, label)
		}
	}
	old.Latitude, old.Longitude = nil, nil
	v := &models.Vacation{Lodgings: []models.Lodging{old, next}, Latitude: fptr(10), Longitude: fptr(20)}
	pt, label := dayHotel(loc, time.UTC, v, day)
	if pt != nil || label != "🛏 Old hotel" {
		t.Fatalf("an unlocated booked hotel must not silently become the destination: %v %q", pt, label)
	}
}

func TestResolveOriginDoesNotSkipMissingStopsOrUseSelf(t *testing.T) {
	a := models.Item{ID: uuid.New(), Title: "Unlocated"}
	b := models.Item{ID: uuid.New(), Title: "Located", Latitude: fptr(2), Longitude: fptr(3)}
	items := []models.Item{a, b}
	hotel := &route.Point{Lat: 4, Lng: 5}
	for _, ref := range []string{"", a.ID.String(), b.ID.String()} {
		items[1].OriginRef = ref
		point, label := resolveOrigin(items, 1, hotel, "Hotel")
		if point != nil || label != a.Title {
			t.Fatalf("origin %q: want missing coordinates at Unlocated, got %v %q", ref, point, label)
		}
	}
}

func TestLodgingForDayUsesDisplayTimezone(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("timezone not available: %v", err)
	}
	// A 00:30 local check-in is still the previous day in UTC, so matching on the
	// raw UTC date would place the stay one day too early.
	ci := time.Date(2026, 8, 3, 0, 30, 0, 0, tz).UTC()
	co := time.Date(2026, 8, 5, 11, 0, 0, 0, tz).UTC()
	lodgings := []models.Lodging{{Name: "Hotel", Latitude: fptr(1), Longitude: fptr(2), CheckIn: ci, CheckOut: co}}
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC) }

	if l := lodgingForDay(tz, lodgings, day(2)); l != nil {
		t.Fatalf("day before check-in: want nil, got %v", l)
	}
	if l := lodgingForDay(tz, lodgings, day(3)); l == nil {
		t.Fatal("local check-in day: want Hotel, got nil")
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
