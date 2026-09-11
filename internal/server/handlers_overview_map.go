package server

import (
	"encoding/json"
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

type overviewMapPayload struct {
	Center    *centerPoint    `json:"center,omitempty"`
	Lodgings  []itemMarker    `json:"lodgings"`
	Geography geographyStatus `json:"geography"`
}

// handleOverviewMap uses accommodation records only. POIs, even ones labeled
// "Hotel", and travel endpoints do not establish an accommodation booking.
func (s *Server) handleOverviewMap(w http.ResponseWriter, r *http.Request) {
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
	s.queueGeography(id, i18n.FromContext(r.Context()).Code())
	status := s.geographyStatus(id)
	lodgings, err := s.store.ListLodgings(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	payload := overviewMapPayload{Lodgings: make([]itemMarker, 0, len(lodgings)), Geography: status}
	if v.HasCoords() {
		payload.Center = &centerPoint{Lat: *v.Latitude, Lng: *v.Longitude}
	}
	for _, lodging := range lodgings {
		if !lodging.HasCoords() {
			continue
		}
		payload.Lodgings = append(payload.Lodgings, itemMarker{
			ID: lodging.ID.String(), Title: lodging.Name, Lat: *lodging.Latitude, Lng: *lodging.Longitude,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.log.Error("encoding overview accommodations", "err", err)
	}
}
