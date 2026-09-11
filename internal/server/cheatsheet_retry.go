package server

import (
	"errors"
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

func (s *Server) renderCustomPhraseFailure(w http.ResponseWriter, r *http.Request, v *models.Vacation, phrase *models.CustomTravelPhrase, job *models.CheatsheetJob) {
	view, err := s.cheatsheetData(r.Context(), v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	state, err := s.store.GetCheatsheetJob(r.Context(), job)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	if state == "failed" && view.Sheet != nil && view.Sheet.DestinationKey == phrase.DestinationKey && view.Sheet.Language == phrase.TargetLanguage {
		view.RetryPhrase, view.RetryToken = phrase.Original, job.Attempt
	}
	view.Error = i18n.FromContext(r.Context()).T("cheatsheet.custom_failed")
	view.CSRF = csrfToken(r.Context())
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.fragment(w, r, "cheatsheet", view)
}
