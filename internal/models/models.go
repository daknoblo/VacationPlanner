// Package models defines the core domain types for the vacation planner.
package models

import (
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
