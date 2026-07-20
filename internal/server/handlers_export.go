package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

// handleExport renders a print-friendly overview of a single vacation.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}

	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	if v.TravelSegments, err = s.store.ListTravelSegments(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.Sights, err = s.store.ListSights(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}

	s.page(w, r, "export", i18n.FromContext(r.Context()).T("export.title"), map[string]any{
		"Vacation": v,
	})
}
