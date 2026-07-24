package server

import (
	"testing"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/route"
)

func TestFormatRating(t *testing.T) {
	cases := map[float64]string{
		0:    "",
		-1:   "",
		5.5:  "",
		4.5:  "4.5",
		4:    "4.0",
		3.27: "3.3",
	}
	for in, want := range cases {
		if got := formatRating(in); got != want {
			t.Errorf("formatRating(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeExternalURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com": "https://example.com",
		"http://x.io/path":    "http://x.io/path",
		"  https://trim.me  ": "https://trim.me",
		"javascript:alert(1)": "",
		"data:text/html,<x>":  "",
		"ftp://host/file":     "",
		"not a url":           "",
		"":                    "",
		"https://":            "",
	}
	for in, want := range cases {
		if got := safeExternalURL(in); got != want {
			t.Errorf("safeExternalURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNearestHotelDistance(t *testing.T) {
	hotels := []route.Point{{Lat: 41.4670, Lng: 12.9036}} // Latina

	// No hotels: always empty.
	if got := nearestHotelDistance(ai.Suggestion{Latitude: 41.47, Longitude: 12.90}, nil); got != "" {
		t.Errorf("no hotels: got %q, want empty", got)
	}
	// Missing coordinates: empty.
	if got := nearestHotelDistance(ai.Suggestion{}, hotels); got != "" {
		t.Errorf("no coords: got %q, want empty", got)
	}
	// Implausibly far (other continent): empty.
	if got := nearestHotelDistance(ai.Suggestion{Latitude: -33.87, Longitude: 151.21}, hotels); got != "" {
		t.Errorf("far point: got %q, want empty", got)
	}
	// A nearby point yields a non-empty distance.
	if got := nearestHotelDistance(ai.Suggestion{Latitude: 41.4900, Longitude: 12.8700}, hotels); got == "" {
		t.Error("nearby point: got empty, want a distance")
	}
}

func TestPlaceQuery(t *testing.T) {
	if got := placeQuery("Colosseum", "Rome"); got != "Colosseum, Rome" {
		t.Errorf("placeQuery = %q", got)
	}
	if got := placeQuery("Colosseum", ""); got != "Colosseum" {
		t.Errorf("placeQuery no dest = %q", got)
	}
}
