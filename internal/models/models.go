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
	Notes       string
	Budget      *float64
	People      int
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Relations are populated on demand, not stored on this row.
	TravelSegments []TravelSegment
	Sights         []Sight
	Activities     []Activity
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
	Mode         string // e.g. flight, train, car, ferry
	FromLocation string
	ToLocation   string
	DepartAt     *time.Time
	ArriveAt     *time.Time
	Notes        string
	CreatedAt    time.Time
}

// Sight is a point of interest planned for a vacation.
type Sight struct {
	ID          uuid.UUID
	VacationID  uuid.UUID
	Name        string
	Category    string
	Description string
	Latitude    *float64
	Longitude   *float64
	PlannedDate *time.Time
	Visited     bool
	Notes       string
	CreatedAt   time.Time
}

// HasCoords reports whether the sight can be placed on the map.
func (s Sight) HasCoords() bool {
	return s.Latitude != nil && s.Longitude != nil
}

// Activity is a planned activity on a specific day of a vacation. StartMin and
// EndMin are minutes from midnight, which drives the day planner's hour grid.
type Activity struct {
	ID          uuid.UUID
	VacationID  uuid.UUID
	Day         time.Time
	Title       string
	Category    string
	StartMin    int
	EndMin      int
	Description string
	Location    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// OnDay reports whether the activity is planned for the given calendar day.
func (a Activity) OnDay(d time.Time) bool {
	ay, am, ad := a.Day.Date()
	by, bm, bd := d.Date()
	return ay == by && am == bm && ad == bd
}

// StartLabel returns the start time formatted as HH:MM.
func (a Activity) StartLabel() string { return minLabel(a.StartMin) }

// EndLabel returns the end time formatted as HH:MM.
func (a Activity) EndLabel() string { return minLabel(a.EndMin) }

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
