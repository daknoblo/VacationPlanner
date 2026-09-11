package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

func (s *Server) handleRefreshGeography(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, err := s.store.GetVacation(r.Context(), id); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	s.retryGeography(id, i18n.FromContext(r.Context()).Code())
	hxTrigger(w, "itemsChanged")
	w.WriteHeader(http.StatusNoContent)
}
