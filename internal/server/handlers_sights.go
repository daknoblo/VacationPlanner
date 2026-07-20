package server

import (
	"encoding/json"
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func (s *Server) handleCreateSight(w http.ResponseWriter, r *http.Request) {
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
	name := formStr(r, "name")
	if name == "" || !maxLen(name, 200) {
		s.formError(w, r, "#sight-error", loc.T("error.sight_name_required"))
		return
	}
	category := formStr(r, "category")
	description := formStr(r, "description")
	notes := formStr(r, "notes")
	if !maxLen(category, 100) || !maxLen(description, 2000) || !maxLen(notes, 2000) {
		s.formError(w, r, "#sight-error", loc.T("error.input_toolong"))
		return
	}

	lat, lng, err := parseCoords(r, "latitude", "longitude")
	if err != nil {
		s.formError(w, r, "#sight-error", err.Error())
		return
	}
	plannedDate, err := parseDatePtr(r, "planned_date")
	if err != nil {
		s.formError(w, r, "#sight-error", loc.T("error.planned_invalid"))
		return
	}

	sight := &models.Sight{
		VacationID:  vacationID,
		Name:        name,
		Category:    category,
		Description: description,
		Latitude:    lat,
		Longitude:   lng,
		PlannedDate: plannedDate,
		Notes:       notes,
	}
	if err := s.store.CreateSight(r.Context(), sight); err != nil {
		s.serverError(w, r, err)
		return
	}

	hxTrigger(w, "sightsChanged")
	s.fragment(w, r, "sight_item", sight)
}

func (s *Server) handleToggleVisited(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "sightID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	sight, err := s.store.GetSight(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	sight.Visited = !sight.Visited
	if err := s.store.UpdateSight(r.Context(), sight); err != nil {
		s.serverError(w, r, err)
		return
	}

	hxTrigger(w, "sightsChanged")
	s.fragment(w, r, "sight_item", sight)
}

func (s *Server) handleDeleteSight(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "sightID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteSight(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	hxTrigger(w, "sightsChanged")
	w.WriteHeader(http.StatusOK)
}

type sightMarker struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Category string  `json:"category"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Visited  bool    `json:"visited"`
}

type sightsPayload struct {
	Center *centerPoint  `json:"center,omitempty"`
	Sights []sightMarker `json:"sights"`
}

type centerPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// handleSightsJSON feeds the Leaflet map with marker data.
func (s *Server) handleSightsJSON(w http.ResponseWriter, r *http.Request) {
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
	sights, err := s.store.ListSights(r.Context(), vacationID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	payload := sightsPayload{Sights: make([]sightMarker, 0, len(sights))}
	if v.HasCoords() {
		payload.Center = &centerPoint{Lat: *v.Latitude, Lng: *v.Longitude}
	}
	for _, sight := range sights {
		if !sight.HasCoords() {
			continue
		}
		if payload.Center == nil {
			payload.Center = &centerPoint{Lat: *sight.Latitude, Lng: *sight.Longitude}
		}
		payload.Sights = append(payload.Sights, sightMarker{
			ID:       sight.ID.String(),
			Name:     sight.Name,
			Category: sight.Category,
			Lat:      *sight.Latitude,
			Lng:      *sight.Longitude,
			Visited:  sight.Visited,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.log.Error("encoding sights json", "err", err)
	}
}
