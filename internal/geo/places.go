package geo

import (
	"context"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// SearchPlaces resolves an explicit leading/trailing town before searching its
// POIs. Unlike a location bias, a bounding box cannot return namesakes overseas.
// It makes at most three paced, cached searches and never invents places.
func (c *Client) SearchPlaces(ctx context.Context, baseURL, query, lang string, limit int, lat, lng float64, category string) ([]Result, error) {
	kind := PlaceSearchKind(query, category)
	words := strings.Fields(strings.TrimSpace(query))
	type townQuery struct {
		town     Result
		name     string
		distance float64
	}
	var candidates []townQuery
	hasBias := lat != 0 || lng != 0
	if len(words) >= 2 {
		for _, index := range []int{len(words) - 1, 0} {
			word := strings.TrimFunc(words[index], func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
			if len([]rune(word)) < 3 || genericPlaceWord(word) {
				continue
			}
			towns, err := c.Search(ctx, baseURL, word, lang, 5, lat, lng)
			if err != nil {
				return nil, err
			}
			for _, town := range towns {
				if !strings.EqualFold(town.Name, word) ||
					(town.Type != "city" && town.Type != "town" && town.Type != "village") {
					continue
				}
				name := append([]string(nil), words[:index]...)
				name = append(name, words[index+1:]...)
				if kind != "" && len(name) > 1 {
					filtered := make([]string, 0, len(name))
					for _, token := range name {
						if PlaceKind(token) != kind {
							filtered = append(filtered, token)
						}
					}
					if len(filtered) > 0 {
						name = filtered
					}
				}
				placeName := strings.Trim(strings.Join(name, " "), ", ")
				if genericPlaceWord(placeName) {
					continue
				}
				switch strings.ToLower(placeName) {
				case "new", "old", "north", "south", "east", "west", "bad", "saint", "sankt", "st",
					"san", "santa", "santo", "sao", "são", "los", "las", "la", "el":
					continue
				}
				distance := 0.0
				if hasBias {
					distance = 111.32 * math.Hypot(town.Lat-lat, (town.Lng-lng)*math.Cos((town.Lat+lat)*math.Pi/360))
					if distance > 500 {
						continue
					}
				}
				candidates = append(candidates, townQuery{town: town, name: placeName, distance: distance})
				break
			}
		}
	}
	if len(candidates) > 0 && (hasBias || len(candidates) == 1) {
		best := candidates[0]
		for _, candidate := range candidates[1:] {
			if candidate.distance < best.distance {
				best = candidate
			}
		}
		// 50 km covers larger cities and nearby suburbs, not an entire country.
		dLat := 50.0 / 111.32
		dLng := math.Min(180, dLat/math.Max(0.01, math.Cos(best.town.Lat*math.Pi/180)))
		bounds := fmt.Sprintf("%.6f,%.6f,%.6f,%.6f",
			math.Max(-180, best.town.Lng-dLng), math.Max(-90, best.town.Lat-dLat),
			math.Min(180, best.town.Lng+dLng), math.Min(90, best.town.Lat+dLat))
		return c.search(ctx, baseURL, best.name, lang, limit, best.town.Lat, best.town.Lng, bounds, kind)
	}
	return c.search(ctx, baseURL, query, lang, limit, lat, lng, "", kind)
}

// Numeric address searches must still find houses/streets when an activity has
// a POI category. Their result need not itself carry the business's OSM tag.
func PlaceSearchKind(query, category string) string {
	if strings.IndexFunc(query, unicode.IsDigit) >= 0 {
		return ""
	}
	if kind := PlaceKind(category); kind != "" {
		return kind
	}
	return PlaceKind(query)
}

// PlaceKind accepts a small, explicit set of OSM POI kinds; other user-defined
// categories retain the unrestricted place search.
func PlaceKind(text string) string {
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		switch word {
		case "restaurant", "restaurants":
			return "restaurant"
		case "cafe", "café", "cafes", "cafés":
			return "cafe"
		case "museum", "museums", "museen":
			return "museum"
		case "hotel", "hotels":
			return "hotel"
		case "park", "parks":
			return "park"
		}
	}
	return ""
}

func placeTag(kind string) string {
	switch kind {
	case "museum", "hotel":
		return "tourism:" + kind
	case "park":
		return "leisure:park"
	default:
		return "amenity:" + kind
	}
}

func genericPlaceWord(word string) bool {
	switch strings.ToLower(word) {
	case "restaurant", "restaurants", "cafe", "café", "bar", "museum", "hotel", "park", "beach", "strand":
		return true
	}
	return false
}
