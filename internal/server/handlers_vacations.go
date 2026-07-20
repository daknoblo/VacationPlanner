package server

import (
	"net/http"
	"strconv"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	vacations, err := s.store.ListVacations(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, "index", i18n.FromContext(r.Context()).T("page.vacations.title"), map[string]any{
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
	loc := i18n.FromContext(r.Context())
	title := formStr(r, "title")
	destination := formStr(r, "destination")
	notes := formStr(r, "notes")

	if title == "" || destination == "" {
		return nil, errValidation(loc.T("error.title_destination_required"))
	}
	if !maxLen(title, 200) || !maxLen(destination, 200) {
		return nil, errValidation(loc.T("error.title_destination_toolong"))
	}
	if !maxLen(notes, 5000) {
		return nil, errValidation(loc.T("error.notes_toolong"))
	}

	start, err := parseDate(r, "start_date")
	if err != nil {
		return nil, errValidation(loc.T("error.start_invalid"))
	}
	end, err := parseDate(r, "end_date")
	if err != nil {
		return nil, errValidation(loc.T("error.end_invalid"))
	}
	if end.Before(start) {
		return nil, errValidation(loc.T("error.end_before_start"))
	}

	lat, lng, err := parseCoords(r, "latitude", "longitude")
	if err != nil {
		return nil, err
	}

	var budget *float64
	if raw := formStr(r, "budget"); raw != "" {
		bv, err := strconv.ParseFloat(raw, 64)
		if err != nil || bv < 0 {
			return nil, errValidation(loc.T("error.budget_invalid"))
		}
		budget = &bv
	}
	people := 1
	if raw := formStr(r, "people"); raw != "" {
		if pv, err := strconv.Atoi(raw); err == nil && pv >= 1 {
			people = pv
		}
	}
	if people > 999 {
		people = 999
	}

	return &models.Vacation{
		Title:       title,
		Destination: destination,
		StartDate:   start,
		EndDate:     end,
		Latitude:    lat,
		Longitude:   lng,
		Notes:       notes,
		Budget:      budget,
		People:      people,
	}, nil
}
