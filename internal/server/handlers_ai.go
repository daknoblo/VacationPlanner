package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

type aiSuggestionsView struct {
	VacationID  string
	Suggestions []ai.Suggestion
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
		out := make([]ai.Suggestion, 0, len(suggestions))
		for _, sg := range suggestions {
			if seen[strings.ToLower(strings.TrimSpace(sg.Name))] {
				continue
			}
			out = append(out, sg)
		}
		view.Suggestions = out
	}

	s.fragment(w, r, "ai_suggestions", view)
}
