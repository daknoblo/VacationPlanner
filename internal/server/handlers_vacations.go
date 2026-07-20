package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	vacations, err := s.store.ListVacations(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, "index", "Meine Urlaube", map[string]any{
		"Vacations": vacations,
	})
}

func (s *Server) handleVacationDetail(w http.ResponseWriter, r *http.Request) {
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

	s.page(w, r, "vacation", v.Title, map[string]any{
		"Vacation":  v,
		"AIEnabled": s.ai.Enabled(),
	})
}

func (s *Server) handleCreateVacation(w http.ResponseWriter, r *http.Request) {
	v, err := s.vacationFromForm(r)
	if err != nil {
		s.formError(w, r, "#form-error", err.Error())
		return
	}
	if err := s.store.CreateVacation(r.Context(), v); err != nil {
		s.serverError(w, r, err)
		return
	}

	target := "/vacations/" + v.ID.String()
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleUpdateVacation(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}

	existing, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}

	updated, err := s.vacationFromForm(r)
	if err != nil {
		s.formError(w, r, "#meta-error", err.Error())
		return
	}
	updated.ID = existing.ID

	if err := s.store.UpdateVacation(r.Context(), updated); err != nil {
		s.serverError(w, r, err)
		return
	}

	if isHTMX(r) {
		hxTrigger(w, "saved, sightsChanged")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteVacation(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteVacation(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// vacationFromForm parses and validates the shared create/update form.
func (s *Server) vacationFromForm(r *http.Request) (*models.Vacation, error) {
	title := formStr(r, "title")
	destination := formStr(r, "destination")
	notes := formStr(r, "notes")

	if title == "" || destination == "" {
		return nil, errValidation("Titel und Reiseziel sind erforderlich.")
	}
	if !maxLen(title, 200) || !maxLen(destination, 200) {
		return nil, errValidation("Titel und Reiseziel dürfen höchstens 200 Zeichen haben.")
	}
	if !maxLen(notes, 5000) {
		return nil, errValidation("Notizen dürfen höchstens 5000 Zeichen haben.")
	}

	start, err := parseDate(r, "start_date")
	if err != nil {
		return nil, errValidation("Startdatum ist ungültig.")
	}
	end, err := parseDate(r, "end_date")
	if err != nil {
		return nil, errValidation("Enddatum ist ungültig.")
	}
	if end.Before(start) {
		return nil, errValidation("Das Enddatum darf nicht vor dem Startdatum liegen.")
	}

	lat, lng, err := parseCoords(r, "latitude", "longitude")
	if err != nil {
		return nil, errValidation(err.Error())
	}

	return &models.Vacation{
		Title:       title,
		Destination: destination,
		StartDate:   start,
		EndDate:     end,
		Latitude:    lat,
		Longitude:   lng,
		Notes:       notes,
	}, nil
}
