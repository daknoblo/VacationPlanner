// Package models defines the core domain types for the vacation planner.
package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Vacation is a single planned trip with a time frame and location.
type Vacation struct {
	ID          uuid.UUID
	Title       string
	Destination string
	StartDate   time.Time
	EndDate     time.Time
	Latitude    *float64
	Longitude   *float64
	MapZoom     *int
	Notes       string
	Budget      *float64
	People      int
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Relations are populated on demand, not stored on this row.
	TravelSegments []TravelSegment
	Items          []Item
	Lodgings       []Lodging
}

// Nights returns the number of nights between start and end date.
func (v Vacation) Nights() int {
	d := v.EndDate.Sub(v.StartDate).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}

// HasCoords reports whether the vacation has map coordinates.
func (v Vacation) HasCoords() bool {
	return v.Latitude != nil && v.Longitude != nil
}

// Days returns each calendar day of the trip from StartDate to EndDate
// inclusive, normalized to UTC midnight. It returns nil for an invalid range
// and is capped to keep the per-day UI bounded for accidental huge ranges.
func (v Vacation) Days() []time.Time {
	if v.EndDate.Before(v.StartDate) {
		return nil
	}
	const maxDays = 366
	d := time.Date(v.StartDate.Year(), v.StartDate.Month(), v.StartDate.Day(), 0, 0, 0, 0, time.UTC)
	end := time.Date(v.EndDate.Year(), v.EndDate.Month(), v.EndDate.Day(), 0, 0, 0, 0, time.UTC)
	var days []time.Time
	for !d.After(end) && len(days) < maxDays {
		days = append(days, d)
		d = d.AddDate(0, 0, 1)
	}
	return days
}

// TravelKind distinguishes arrival from departure legs.
type TravelKind string

const (
	// TravelArrival is the journey to the destination.
	TravelArrival TravelKind = "arrival"
	// TravelDeparture is the journey back home.
	TravelDeparture TravelKind = "departure"
)

// Valid reports whether the travel kind is one of the known values.
func (k TravelKind) Valid() bool {
	return k == TravelArrival || k == TravelDeparture
}

// TravelSegment describes an arrival or departure leg of a vacation.
type TravelSegment struct {
	ID           uuid.UUID
	VacationID   uuid.UUID
	Kind         TravelKind
	StepOrder    int    // position within a multi-stop arrival/departure (0-based)
	Mode         string // e.g. flight, train, car, ferry
	FromLocation string
	ToLocation   string
	FromLat      *float64
	FromLng      *float64
	ToLat        *float64
	ToLng        *float64
	DepartAt     *time.Time
	ArriveAt     *time.Time
	DistanceM    *float64
	DurationS    *int
	Notes        string
	CreatedAt    time.Time
}

// FromHasCoords reports whether the departure point has map coordinates.
func (t TravelSegment) FromHasCoords() bool { return t.FromLat != nil && t.FromLng != nil }

// ToHasCoords reports whether the arrival point has map coordinates.
func (t TravelSegment) ToHasCoords() bool { return t.ToLat != nil && t.ToLng != nil }

// Item is a single planned entry for a vacation — a sight, activity, meal or any
// user-defined category. It optionally has a day, a time range, map coordinates
// and a cost, unifying the former Sight and Activity concepts.
type Item struct {
	ID          uuid.UUID
	VacationID  uuid.UUID
	Category    string
	Title       string
	Description string
	Location    string
	Latitude    *float64
	Longitude   *float64
	Day         *time.Time
	StartMin    int
	EndMin      int
	Cost        *float64
	Visited     bool
	Notes       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// HasCoords reports whether the item can be placed on the map.
func (i Item) HasCoords() bool { return i.Latitude != nil && i.Longitude != nil }

// Timed reports whether the item has a real time range and thus renders as a
// block on the day planner (untimed items only appear in the day's list).
func (i Item) Timed() bool { return i.EndMin > i.StartMin }

// OnDay reports whether the item is planned for the given calendar day.
func (i Item) OnDay(d time.Time) bool {
	if i.Day == nil {
		return false
	}
	ay, am, ad := i.Day.Date()
	by, bm, bd := d.Date()
	return ay == by && am == bm && ad == bd
}

// StartLabel returns the start time formatted as HH:MM.
func (i Item) StartLabel() string { return minLabel(i.StartMin) }

// EndLabel returns the end time formatted as HH:MM.
func (i Item) EndLabel() string { return minLabel(i.EndMin) }

func minLabel(m int) string {
	if m < 0 {
		m = 0
	}
	if m > 24*60 {
		m = 24 * 60
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// Category is a user-managed label offered when adding sights and activities.
type Category struct {
	ID        uuid.UUID
	Name      string
	Icon      string
	SortOrder int
	CreatedAt time.Time
}

// Lodging is a place to stay for part of the trip, spanning a check-in to a
// check-out date and time. It is shown as a narrow strip on the day/week
// planner over the hours it covers on each day.
type Lodging struct {
	ID         uuid.UUID
	VacationID uuid.UUID
	Name       string
	Location   string
	Latitude   *float64
	Longitude  *float64
	CheckIn    time.Time
	CheckOut   time.Time
	Cost       *float64
	Notes      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Nights returns the number of nights between check-in and check-out.
func (l Lodging) Nights() int {
	d := l.CheckOut.Sub(l.CheckIn).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}

// HasCoords reports whether the lodging can be placed on the map.
func (l Lodging) HasCoords() bool { return l.Latitude != nil && l.Longitude != nil }

// Document is an uploaded file (e.g. a ferry ticket PDF or a booking) attached
// either to an Item (activity) or to a single travel leg. Exactly one owner is
// set: ItemID for item documents, or VacationID + TravelKind + TravelStep for
// travel documents. The raw bytes in Data are only populated when the file is
// served, not when listing a document's metadata.
type Document struct {
	ID          uuid.UUID
	ItemID      *uuid.UUID
	VacationID  *uuid.UUID
	TravelKind  TravelKind
	TravelStep  int
	Filename    string
	ContentType string
	Size        int64
	CreatedAt   time.Time
	Data        []byte
}

// IsImage reports whether the document is a raster image (used to pick an icon).
func (d Document) IsImage() bool {
	return len(d.ContentType) >= 6 && d.ContentType[:6] == "image/"
}
