package models

import "github.com/google/uuid"

// IdeaRoute is derived from one current accommodation/idea coordinate pair.
// Geometry contains provider road coordinates in Leaflet's latitude/longitude order.
type IdeaRoute struct {
	VacationID, LodgingID, ItemID  uuid.UUID
	Provider                       string
	FromLat, FromLng, ToLat, ToLng float64
	Status                         string
	DistanceM, DurationS           float64
	Geometry                       [][2]float64
}

type IdeaRouteProgress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}
