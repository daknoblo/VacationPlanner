package server

import (
	"context"
	"math"
	"net/url"
	"strings"
	"unicode"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

type wikipediaLookup interface {
	Lookup(context.Context, string, []string) ([]geo.Result, error)
}

func ideaWikipediaNames(item *models.Item) []string {
	names := make([]string, 0, 8)
	seen := make(map[string]bool)
	for _, name := range ideaPlaceNames(item) {
		// Single words commonly name broad areas or ambiguous concepts; the
		// geocoder handles explicit city ideas without this fallback.
		if len(strings.Fields(name)) < 2 || !distinctiveIdeaName(name) ||
			strings.ContainsAny(name, "|:\r\n") || !maxLen(name, 200) || seen[name] {
			continue
		}
		names = append(names, name)
		seen[name] = true
		if len(names) == 8 {
			break
		}
	}
	return names
}

func ideaWikipediaLanguages(item *models.Item, language string) []string {
	var languages []string
	seen := make(map[string]bool)
	add := func(lang string) {
		if geo.ValidWikipediaLanguage(lang) && !seen[lang] && len(languages) < 3 {
			languages = append(languages, lang)
			seen[lang] = true
		}
	}
	for _, link := range item.Links {
		u, err := url.Parse(link.SafeURL())
		if err == nil && u != nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".wikipedia.org") {
			add(strings.TrimSuffix(strings.ToLower(u.Hostname()), ".wikipedia.org"))
		}
	}
	add(language)
	add("en")
	return languages
}

// Parenthesized travel notes/translations are not part of a place's name.
// Text outside parentheses is retained, so "Town (...) / Museum" stays ambiguous.
func ideaPlaceName(value string) string {
	var name strings.Builder
	depth := 0
	for _, r := range value {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				name.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(name.String()), " ")
}

func ideaPlaceNames(item *models.Item) []string {
	names := []string{ideaPlaceName(item.Title)}
	for _, link := range item.Links {
		// Reference URLs supply only an article title; arbitrary links are
		// never fetched server-side as part of geography enrichment.
		u, err := url.Parse(link.SafeURL())
		if err != nil || u == nil || u.Scheme != "https" || u.RawQuery != "" ||
			!strings.HasSuffix(strings.ToLower(u.Hostname()), ".wikipedia.org") ||
			!strings.HasPrefix(u.Path, "/wiki/") {
			continue
		}
		title := strings.TrimPrefix(u.Path, "/wiki/")
		if title == "" || strings.ContainsAny(title, ":/") {
			continue
		}
		names = append(names, ideaPlaceName(strings.ReplaceAll(title, "_", " ")))
	}
	return names
}

func ideaGeographyQueries(item *models.Item, vacation *models.Vacation) []string {
	names := ideaPlaceNames(item)
	if strings.TrimSpace(item.Location) != "" {
		names = append([]string{strings.TrimSpace(item.Location)}, names...)
	}
	queries := make([]string, 0, 3)
	seen := make(map[string]bool)
	for _, name := range names {
		key := normalizeGeography(name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		query := name
		if !vacation.HasCoords() && item.Location == "" && strings.TrimSpace(vacation.Destination) != "" {
			query += ", " + vacation.Destination
		}
		queries = append(queries, query)
		if len(queries) == 3 {
			break
		}
	}
	return queries
}

func ideaGeographyAnchors(vacation *models.Vacation, lodgings []models.Lodging) []route.Point {
	points := make([]route.Point, 0, len(lodgings)+1)
	if vacation.HasCoords() {
		points = append(points, route.Point{Lat: *vacation.Latitude, Lng: *vacation.Longitude})
	}
	for _, lodging := range lodgings {
		if lodging.HasCoords() {
			points = append(points, route.Point{Lat: *lodging.Latitude, Lng: *lodging.Longitude})
		}
	}
	return points
}

func distinctiveIdeaName(name string) bool {
	for _, word := range strings.Fields(normalizeGeography(name)) {
		switch word {
		case "the", "der", "die", "das", "museum", "strand", "beach", "park", "restaurant", "hotel", "city", "town", "stadt", "viewpoint", "aussichtspunkt", "walk", "spaziergang":
			continue
		}
		if len([]rune(word)) >= 3 {
			return true
		}
	}
	return false
}

// A match needs an exact place name (or an explicit full street address) and
// corroborating locality/trip geography. We never choose the first fuzzy result.
func matchIdeaPlace(item *models.Item, vacation *models.Vacation, anchors []route.Point, results []geo.Result) (geo.Result, bool, bool) {
	names := ideaPlaceNames(item)
	var matches []geo.Result
	for _, result := range results {
		if math.IsNaN(result.Lat) || math.IsNaN(result.Lng) || math.IsInf(result.Lat, 0) || math.IsInf(result.Lng, 0) ||
			math.Abs(result.Lat) > 90 || math.Abs(result.Lng) > 180 {
			continue
		}
		kind := strings.ToLower(result.Type)
		if kind == "" {
			kind = strings.ToLower(result.OSMValue)
		}
		switch kind {
		case "country", "state", "county", "administrative", "region", "continent", "sea", "ocean", "street":
			continue
		}
		location := normalizeGeography(item.Location)
		address := containsGeography(location, result.Street) && containsGeography(location, result.HouseNumber) &&
			(containsGeography(location, result.City) || containsGeography(location, result.Postcode))
		named := false
		if distinctiveIdeaName(result.Name) {
			if location == normalizeGeography(result.Name) && kind != "city" && kind != "town" && kind != "village" {
				named = true
			}
			for _, name := range names {
				normalized := normalizeGeography(name)
				if normalized == normalizeGeography(result.Name) ||
					(result.City != "" && (normalized == normalizeGeography(result.City+" "+result.Name) ||
						normalized == normalizeGeography(result.Name+" "+result.City))) {
					named = true
				}
			}
		}
		if !named && !address {
			continue
		}
		local := address || ((containsGeography(location, result.City) || containsGeography(location, result.Postcode)) &&
			containsGeography(location, result.Name))
		for _, anchor := range anchors {
			if route.Haversine(anchor, route.Point{Lat: result.Lat, Lng: result.Lng}) <= 500_000 {
				local = true
			}
		}
		if len(anchors) == 0 && containsGeography(normalizeGeography(vacation.Destination), result.Country) {
			local = true
		}
		if !local {
			continue
		}
		duplicate := false
		for _, previous := range matches {
			sameName := normalizeGeography(previous.Name) == normalizeGeography(result.Name) ||
				(previous.Class == "wikipedia" && result.Class == "wikipedia" && previous.DisplayName == result.DisplayName)
			if sameName &&
				route.Haversine(route.Point{Lat: previous.Lat, Lng: previous.Lng}, route.Point{Lat: result.Lat, Lng: result.Lng}) < 50 {
				duplicate = true
			}
		}
		if !duplicate {
			matches = append(matches, result)
		}
	}
	if len(matches) != 1 {
		return geo.Result{}, false, len(matches) > 1
	}
	if len(results) >= 10 {
		return geo.Result{}, false, true
	}
	result := matches[0]
	if strings.TrimSpace(result.DisplayName) == "" || !maxLen(result.DisplayName, 200) {
		result.DisplayName = strings.TrimSpace(result.Name)
	}
	if !maxLen(result.DisplayName, 200) || !maxLen(result.Region, 200) ||
		strings.IndexFunc(result.DisplayName+result.Region, unicode.IsControl) >= 0 {
		return geo.Result{}, false, false
	}
	return result, true, false
}
