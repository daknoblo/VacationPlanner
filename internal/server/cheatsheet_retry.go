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
		// Pre-migration failed reservations have no payload. An explicit
		// submission can still expose their attempt-bound retry action.
		present := false
		for _, pending := range view.PhraseJobs {
			present = present || pending.Key == job.Key
		}
		if !present {
			job.Status, job.Original, job.TargetLanguage = state, phrase.Original, phrase.TargetLanguage
			view.PhraseJobs = append(view.PhraseJobs, *job)
		}
	}
	view.Error = i18n.FromContext(r.Context()).T("cheatsheet.custom_failed")
	view.CSRF = csrfToken(r.Context())
	view.RowsOnly = r.URL.Query().Get("fragment") == "rows"
	w.WriteHeader(http.StatusUnprocessableEntity)
	if view.RowsOnly {
		s.fragment(w, r, "cheatsheet-rows-response", view)
	} else {
		s.fragment(w, r, "cheatsheet", view)
	}
}
