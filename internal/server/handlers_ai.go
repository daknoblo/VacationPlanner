package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

// suggestionView is one AI suggestion enriched with a validated website link and
// a Google Maps search link (which surfaces the real Google rating on click).
type suggestionView struct {
	Name        string
	Category    string
	Description string
	Reason      string
	Website     string
	MapsURL     string
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

	input := ai.RecommendInput{
		Destination: v.Destination,
		StartDate:   v.StartDate.Format("2006-01-02"),
		EndDate:     v.EndDate.Format("2006-01-02"),
		Interests:   interests,
		RadiusKm:    formInt(r, "radius", 25),
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
			out = append(out, suggestionView{
				Name:        sg.Name,
				Category:    sg.Category,
				Description: sg.Description,
				Reason:      sg.Reason,
				Website:     safeExternalURL(sg.Website),
				MapsURL:     googleMapsSearchURL(sg.Name, v.Destination),
			})
		}
		view.Suggestions = out
	}

	s.fragment(w, r, "ai_suggestions", view)
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
	q := strings.TrimSpace(name)
	if d := strings.TrimSpace(destination); d != "" {
		q += ", " + d
	}
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(q)
}
