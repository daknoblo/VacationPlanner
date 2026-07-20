package server

import (
	"errors"
	"net/http"

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

	sights, err := s.store.ListSights(r.Context(), vacationID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	existing := make([]string, 0, len(sights))
	for _, sight := range sights {
		existing = append(existing, sight.Name)
	}

	input := ai.RecommendInput{
		Destination: v.Destination,
		StartDate:   v.StartDate.Format("2006-01-02"),
		EndDate:     v.EndDate.Format("2006-01-02"),
		Interests:   interests,
		Existing:    existing,
	}

	baseURL, model := s.aiSettings(r.Context())
	suggestions, err := s.ai.Recommend(r.Context(), baseURL, model, input)
	switch {
	case errors.Is(err, ai.ErrDisabled):
		view.Error = loc.T("ai.not_configured")
	case err != nil:
		s.log.Warn("ai recommendation failed", "err", err, "vacation_id", vacationID)
		view.Error = loc.T("ai.failed")
	default:
		view.Suggestions = suggestions
	}

	s.fragment(w, r, "ai_suggestions", view)
}
