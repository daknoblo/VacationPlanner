package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/store"
)

func (s *Server) handleArchiveVacation(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	if !v.Archived {
		_, tz := s.regionSettings(r.Context())
		if err := s.store.ArchiveVacation(r.Context(), id, time.Now().In(tz)); err != nil {
			switch {
			case errors.Is(err, store.ErrVacationNotEnded):
				s.formError(w, r, "#archive-error", i18n.FromContext(r.Context()).T("archive.not_ended"))
			case isNotFound(err):
				s.notFound(w, r)
			default:
				s.serverError(w, r, err)
			}
			return
		}
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
