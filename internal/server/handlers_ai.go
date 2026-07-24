package server

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/route"
)

// suggestionLink is one labelled external link shown on a suggestion tile.
type suggestionLink struct {
	Icon  string
	Label string
	URL   string
}

// suggestionView is one AI suggestion enriched with a rating badge, a set of
// labelled external links (Google Maps, website, Tripadvisor) and the straight-
// line distance to the nearest accommodation.
type suggestionView struct {
	Name        string
	Category    string
	Description string
	Reason      string
	Rating      string
	Distance    string
	Links       []suggestionLink
}

type aiSuggestionsView struct {
	VacationID  string
	Suggestions []suggestionView
	Error       string
}

func (s *Server) handleAIRecommend(w http.ResponseWriter, r *http.Request) {
	vacationID, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.store.GetVacation(r.Context(), vacationID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	loc := i18n.FromContext(r.Context())
	interests := formStr(r, "interests")
	if !maxLen(interests, 500) {
		s.formError(w, r, "#ai-error", loc.T("error.interests_toolong"))
		return
	}

	// Search center: defaults to the trip destination, but the user can move it
	// with the location picker. The radius is measured from this point.
	locationName := formStr(r, "ai_location")
	if !maxLen(locationName, 200) {
		s.formError(w, r, "#ai-error", loc.T("error.interests_toolong"))
		return
	}
	if locationName == "" {
		locationName = v.Destination
	}
	originLat, originLng, err := parseCoords(r, "ai_lat", "ai_lng")
	if err != nil {
		s.formError(w, r, "#ai-error", validationMessage(err))
		return
	}

	view := aiSuggestionsView{VacationID: vacationID.String()}

	items, err := s.store.ListItems(r.Context(), vacationID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	existing := make([]string, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		existing = append(existing, it.Title)
		seen[strings.ToLower(strings.TrimSpace(it.Title))] = true
	}

	// Accommodation points, used to show each suggestion's distance to the
	// nearest hotel (straight-line).
	lodgings, err := s.store.ListLodgings(r.Context(), vacationID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hotels := make([]route.Point, 0, len(lodgings))
	for _, lo := range lodgings {
		if lo.HasCoords() {
			hotels = append(hotels, route.Point{Lat: *lo.Latitude, Lng: *lo.Longitude})
		}
	}

	input := ai.RecommendInput{
		Destination: v.Destination,
		StartDate:   v.StartDate.Format("2006-01-02"),
		EndDate:     v.EndDate.Format("2006-01-02"),
		Interests:   interests,
		RadiusKm:    formInt(r, "radius", 25),
		Count:       clampSuggestionCount(formInt(r, "count", 5)),
		Existing:    existing,
		Origin:      locationName,
	}
	if originLat != nil && originLng != nil {
		input.OriginLat = *originLat
		input.OriginLng = *originLng
		input.HasOrigin = true
	}

	baseURL, model, apiVersion := s.aiSettings(r.Context())
	suggestions, err := s.ai.Recommend(r.Context(), baseURL, model, apiVersion, input)
	switch {
	case errors.Is(err, ai.ErrDisabled):
		view.Error = loc.T("ai.not_configured")
	case err != nil:
		s.log.Warn("ai recommendation failed",
			"err", err,
			"vacation_id", vacationID,
			"base_url", baseURL,
			"model", model,
			"api_version_set", apiVersion != "",
		)
		view.Error = loc.T("ai.failed")
	default:
		// Drop anything already on the trip so added suggestions don't reappear
		// (the model is also told not to repeat them, but this guarantees it).
		out := make([]suggestionView, 0, len(suggestions))
		for _, sg := range suggestions {
			if seen[strings.ToLower(strings.TrimSpace(sg.Name))] {
				continue
			}
			links := []suggestionLink{
				{Icon: "📍", Label: loc.T("ai.google_maps"), URL: googleMapsSearchURL(sg.Name, v.Destination)},
			}
			if web := safeExternalURL(sg.Website); web != "" {
				links = append(links, suggestionLink{Icon: "🔗", Label: loc.T("ai.website"), URL: web})
			}
			links = append(links, suggestionLink{Icon: "🧳", Label: loc.T("ai.tripadvisor"), URL: tripadvisorSearchURL(sg.Name, v.Destination)})

			out = append(out, suggestionView{
				Name:        sg.Name,
				Category:    sg.Category,
				Description: sg.Description,
				Reason:      sg.Reason,
				Rating:      formatRating(sg.Rating),
				Distance:    nearestHotelDistance(sg, hotels),
				Links:       links,
			})
		}
		view.Suggestions = out
	}

	s.fragment(w, r, "ai_suggestions", view)
}

// clampSuggestionCount snaps the requested number of suggestions to the offered
// range (5–25 in steps of 5), defaulting to 5 for out-of-range values.
func clampSuggestionCount(n int) int {
	if n < 5 || n > 25 {
		return 5
	}
	return (n / 5) * 5
}

// formatRating renders a 0–5 rating with one decimal, or "" when unavailable or
// out of range.
func formatRating(rating float64) string {
	if rating <= 0 || rating > 5 {
		return ""
	}
	return fmt.Sprintf("%.1f", rating)
}

// nearestHotelDistance returns the straight-line distance from a suggestion to
// the closest accommodation, or "" when the coordinates are missing or the model
// returned an implausible point (far outside any plausible trip radius).
func nearestHotelDistance(sg ai.Suggestion, hotels []route.Point) string {
	if len(hotels) == 0 {
		return ""
	}
	if sg.Latitude == 0 && sg.Longitude == 0 {
		return ""
	}
	if sg.Latitude < -90 || sg.Latitude > 90 || sg.Longitude < -180 || sg.Longitude > 180 {
		return ""
	}
	p := route.Point{Lat: sg.Latitude, Lng: sg.Longitude}
	best := math.MaxFloat64
	for _, h := range hotels {
		if d := route.Haversine(p, h); d < best {
			best = d
		}
	}
	if best > 500_000 { // >500 km: coordinates are almost certainly wrong
		return ""
	}
	return formatDistance(best)
}

// safeExternalURL returns raw only if it is a syntactically valid absolute
// http(s) URL with a host; otherwise it returns "". This prevents rendering a
// javascript:/data: link or a malformed URL supplied by the model.
func safeExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return u.String()
}

// googleMapsSearchURL builds a Google Maps search link for a place. Opened in a
// new tab, it shows the place together with its real Google rating and reviews.
func googleMapsSearchURL(name, destination string) string {
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(placeQuery(name, destination))
}

// tripadvisorSearchURL builds a Tripadvisor search link for a place.
func tripadvisorSearchURL(name, destination string) string {
	return "https://www.tripadvisor.com/Search?q=" + url.QueryEscape(placeQuery(name, destination))
}

// placeQuery combines a place name with its destination for an external search.
func placeQuery(name, destination string) string {
	q := strings.TrimSpace(name)
	if d := strings.TrimSpace(destination); d != "" {
		q += ", " + d
	}
	return q
}
