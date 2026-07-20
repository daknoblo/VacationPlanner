package server

import (
	"errors"
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/ai"
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

	interests := formStr(r, "interests")
	if !maxLen(interests, 500) {
		s.formError(w, r, "#ai-error", "Interessen dürfen höchstens 500 Zeichen haben.")
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

	suggestions, err := s.ai.Recommend(r.Context(), input)
	switch {
	case errors.Is(err, ai.ErrDisabled):
		view.Error = "KI-Empfehlungen sind nicht konfiguriert. Setze OPENAI_API_KEY, um sie zu aktivieren."
	case err != nil:
		s.log.Warn("ai recommendation failed", "err", err, "vacation_id", vacationID)
		view.Error = "Die KI-Empfehlungen konnten gerade nicht geladen werden. Bitte später erneut versuchen."
	default:
		view.Suggestions = suggestions
	}

	s.fragment(w, r, "ai_suggestions", view)
}
