package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func (s *Server) handleCreateTravel(w http.ResponseWriter, r *http.Request) {
	vacationID, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, err := s.store.GetVacation(r.Context(), vacationID); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	loc := i18n.FromContext(r.Context())
	kind := models.TravelKind(formStr(r, "kind"))
	if !kind.Valid() {
		s.formError(w, r, "#travel-error", loc.T("error.travel_kind_required"))
		return
	}
	mode := formStr(r, "mode")
	from := formStr(r, "from_location")
	to := formStr(r, "to_location")
	notes := formStr(r, "notes")
	if !maxLen(mode, 50) || !maxLen(from, 200) || !maxLen(to, 200) || !maxLen(notes, 2000) {
		s.formError(w, r, "#travel-error", loc.T("error.input_toolong"))
		return
	}

	departAt, err := parseDateTimePtr(r, "depart_at")
	if err != nil {
		s.formError(w, r, "#travel-error", loc.T("error.depart_invalid"))
		return
	}
	arriveAt, err := parseDateTimePtr(r, "arrive_at")
	if err != nil {
		s.formError(w, r, "#travel-error", loc.T("error.arrive_invalid"))
		return
	}

	segment := &models.TravelSegment{
		VacationID:   vacationID,
		Kind:         kind,
		Mode:         mode,
		FromLocation: from,
		ToLocation:   to,
		DepartAt:     departAt,
		ArriveAt:     arriveAt,
		Notes:        notes,
	}
	if err := s.store.CreateTravelSegment(r.Context(), segment); err != nil {
		s.serverError(w, r, err)
		return
	}

	s.fragment(w, r, "travel_item", segment)
}

func (s *Server) handleDeleteTravel(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "travelID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteTravelSegment(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
