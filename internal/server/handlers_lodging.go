package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// lodgingFromForm parses and validates the accommodation add form.
func (s *Server) lodgingFromForm(r *http.Request, tz *time.Location) (*models.Lodging, error) {
	loc := i18n.FromContext(r.Context())

	name := strings.TrimSpace(formStr(r, "name"))
	if name == "" || !maxLen(name, 200) {
		return nil, errValidation(loc.T("error.lodging_name_required"))
	}
	location := strings.TrimSpace(formStr(r, "location"))
	notes := strings.TrimSpace(formStr(r, "notes"))
	if !maxLen(location, 200) || !maxLen(notes, 2000) {
		return nil, errValidation(loc.T("error.input_toolong"))
	}

	checkIn, err := parseDateTimeParts(r, "checkin_date", "checkin_time", tz)
	if err != nil || checkIn == nil {
		return nil, errValidation(loc.T("error.lodging_checkin_invalid"))
	}
	checkOut, err := parseDateTimeParts(r, "checkout_date", "checkout_time", tz)
	if err != nil || checkOut == nil {
		return nil, errValidation(loc.T("error.lodging_checkout_invalid"))
	}
	if !checkOut.After(*checkIn) {
		return nil, errValidation(loc.T("error.lodging_checkout_after"))
	}

	lat, lng, err := parseCoords(r, "latitude", "longitude")
	if err != nil {
		return nil, err
	}
	cost, err := parseCostPtr(r, "cost", loc)
	if err != nil {
		return nil, err
	}

	return &models.Lodging{
		Name:      name,
		Location:  location,
		Latitude:  lat,
		Longitude: lng,
		CheckIn:   *checkIn,
		CheckOut:  *checkOut,
		Cost:      cost,
		Notes:     notes,
	}, nil
}

func (s *Server) handleCreateLodging(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, err := s.store.GetVacation(r.Context(), id); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	_, tz := s.regionSettings(r.Context())
	lo, err := s.lodgingFromForm(r, tz)
	if err != nil {
		s.formError(w, r, "#lodging-error", err.Error())
		return
	}
	lo.VacationID = id
	if err := s.store.CreateLodging(r.Context(), lo); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.refreshAfterLodging(w, r, id)
}

func (s *Server) handleDeleteLodging(w http.ResponseWriter, r *http.Request) {
	lid, err := urlUUID(r, "lodgingID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	lo, err := s.store.GetLodging(r.Context(), lid)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	if err := s.store.DeleteLodging(r.Context(), lid); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	s.refreshAfterLodging(w, r, lo.VacationID)
}

// refreshAfterLodging reloads the page so the tab list and the planner strips
// stay in sync (the active tab is restored from the URL hash on load).
func (s *Server) refreshAfterLodging(w http.ResponseWriter, r *http.Request, vacationID uuid.UUID) {
	if isHTMX(r) {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	//nolint:gosec // G710: target is a fixed prefix plus a canonical UUID (same-origin, not user-controlled)
	http.Redirect(w, r, "/vacations/"+vacationID.String(), http.StatusSeeOther)
}

// lodgingStrip is a narrow accommodation bar spanning part of a single day.
type lodgingStrip struct {
	StartMin int
	EndMin   int
	Name     string
}

// lodgingView is one accommodation shown in the Unterkunft tab list, with its
// check-in/out already formatted in the display timezone.
type lodgingView struct {
	ID        string
	Name      string
	Location  string
	Notes     string
	Nights    int
	CheckIn   string
	CheckOut  string
	Cost      *float64
	HasCoords bool
}

func lodgingViews(tz *time.Location, lodgings []models.Lodging) []lodgingView {
	out := make([]lodgingView, 0, len(lodgings))
	for _, lo := range lodgings {
		out = append(out, lodgingView{
			ID:        lo.ID.String(),
			Name:      lo.Name,
			Location:  lo.Location,
			Notes:     lo.Notes,
			Nights:    lo.Nights(),
			CheckIn:   lo.CheckIn.In(tz).Format("02.01.2006 15:04"),
			CheckOut:  lo.CheckOut.In(tz).Format("02.01.2006 15:04"),
			Cost:      lo.Cost,
			HasCoords: lo.HasCoords(),
		})
	}
	return out
}

// lodgingDayStrips maps each calendar day (in the display timezone) to the
// accommodation strips covering it: from the check-in time on the first day,
// full days in between, and up to the check-out time on the last day.
func lodgingDayStrips(tz *time.Location, lodgings []models.Lodging) map[string][]lodgingStrip {
	out := make(map[string][]lodgingStrip)
	for _, lo := range lodgings {
		ci := lo.CheckIn.In(tz)
		co := lo.CheckOut.In(tz)
		if !co.After(ci) {
			continue
		}
		ciMin := ci.Hour()*60 + ci.Minute()
		coMin := co.Hour()*60 + co.Minute()
		firstDay := time.Date(ci.Year(), ci.Month(), ci.Day(), 0, 0, 0, 0, tz)
		lastDay := time.Date(co.Year(), co.Month(), co.Day(), 0, 0, 0, 0, tz)
		day := firstDay
		for i := 0; !day.After(lastDay) && i < 400; i++ {
			startMin, endMin := 0, 1440
			if day.Equal(firstDay) {
				startMin = ciMin
			}
			if day.Equal(lastDay) {
				endMin = coMin
			}
			if endMin > startMin {
				key := day.Format("2006-01-02")
				out[key] = append(out[key], lodgingStrip{StartMin: startMin, EndMin: endMin, Name: lo.Name})
			}
			day = day.AddDate(0, 0, 1)
		}
	}
	return out
}
