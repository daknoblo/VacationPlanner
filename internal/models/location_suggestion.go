package models

import "github.com/google/uuid"

// LocationSuggestion never changes the item until the user selects and saves it.
type LocationSuggestion struct {
	ID        uuid.UUID `json:"id"`
	ItemID    uuid.UUID `json:"item_id"`
	Label     string    `json:"display_name"`
	Name      string    `json:"name"`
	Latitude  float64   `json:"lat"`
	Longitude float64   `json:"lng"`
	Rejected  bool      `json:"rejected"`
}
