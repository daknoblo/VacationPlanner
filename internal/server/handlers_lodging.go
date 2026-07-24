package server

import (
	"net/http"
	"strings"
	"time"

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
	cost, err := parseCostPtr(r, loc)
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
		PaidBy:    parsePaidBy(r),
		Notes:     notes,
	}, nil
}

func (s *Server) handleCreateLodging(w http.ResponseWriter, r *http.Request) {
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
	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())

	// A fresh card defaults to the trip's dates (check-in 15:00, check-out 11:00);
	// the user edits everything inline afterwards.
	checkIn := time.Date(v.StartDate.Year(), v.StartDate.Month(), v.StartDate.Day(), 15, 0, 0, 0, tz)
	checkOut := time.Date(v.EndDate.Year(), v.EndDate.Month(), v.EndDate.Day(), 11, 0, 0, 0, tz)
	if !checkOut.After(checkIn) {
		checkOut = checkIn.Add(24 * time.Hour)
	}
	lo := &models.Lodging{
		VacationID: id,
		Name:       loc.T("lodging.untitled"),
		CheckIn:    checkIn,
		CheckOut:   checkOut,
	}
	if err := s.store.CreateLodging(r.Context(), lo); err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Participants, _ = s.store.ListVacationParticipants(r.Context(), id)
	hxTrigger(w, "itemsChanged")
	s.fragment(w, r, "lodging_editor", newLodgingEditorView(tz, v, lo))
}

// handleUpdateLodging auto-saves an inline accommodation edit and returns the
// refreshed summary (nights + cost) for the card.
func (s *Server) handleUpdateLodging(w http.ResponseWriter, r *http.Request) {
	lid, err := urlUUID(r, "lodgingID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	existing, err := s.store.GetLodging(r.Context(), lid)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	v, err := s.store.GetVacation(r.Context(), existing.VacationID)
	if err != nil {
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
	lo.ID = lid
	lo.VacationID = existing.VacationID
	if err := s.store.UpdateLodging(r.Context(), lo); err != nil {
		s.serverError(w, r, err)
		return
	}
	hxTrigger(w, "itemsChanged")
	s.fragment(w, r, "lodging_out", newLodgingEditorView(tz, v, lo))
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
	if isHTMX(r) {
		// The card is swapped out (outerHTML) by the empty response body.
		hxTrigger(w, "itemsChanged")
		w.WriteHeader(http.StatusOK)
		return
	}
	//nolint:gosec // G710: target is a fixed prefix plus a canonical UUID (same-origin, not user-controlled)
	http.Redirect(w, r, "/vacations/"+lo.VacationID.String(), http.StatusSeeOther)
}

// lodgingStrip is a narrow accommodation bar spanning part of a single day.
type lodgingStrip struct {
	StartMin int
	EndMin   int
	Name     string
}

// lodgingEditorView is the data for one inline accommodation editor card,
// mirroring the arrival/departure step editor.
type lodgingEditorView struct {
	ID           string
	VID          string
	Name         string
	Location     string
	Lat          *float64
	Lng          *float64
	NearLat      *float64 // vacation coords used to bias the geocoder
	NearLng      *float64
	CheckInDate  string // YYYY-MM-DD
	CheckInTime  string // HH:MM
	CheckOutDate string
	CheckOutTime string
	Cost         *float64
	CostPerNight *float64
	PaidBy       string // current payer person ID ("" = unassigned)
	Participants []models.Person
	Notes        string
	Nights       int
}

func newLodgingEditorView(tz *time.Location, v *models.Vacation, lo *models.Lodging) lodgingEditorView {
	ci := lo.CheckIn.In(tz)
	co := lo.CheckOut.In(tz)
	nights := lo.Nights()
	var perNight *float64
	if lo.Cost != nil && nights > 0 {
		pn := *lo.Cost / float64(nights)
		perNight = &pn
	}
	return lodgingEditorView{
		ID:           lo.ID.String(),
		VID:          v.ID.String(),
		Name:         lo.Name,
		Location:     lo.Location,
		Lat:          lo.Latitude,
		Lng:          lo.Longitude,
		NearLat:      v.Latitude,
		NearLng:      v.Longitude,
		CheckInDate:  ci.Format("2006-01-02"),
		CheckInTime:  ci.Format("15:04"),
		CheckOutDate: co.Format("2006-01-02"),
		CheckOutTime: co.Format("15:04"),
		Cost:         lo.Cost,
		CostPerNight: perNight,
		PaidBy:       uuidString(lo.PaidBy),
		Participants: v.Participants,
		Notes:        lo.Notes,
		Nights:       nights,
	}
}

// lodgingBlock builds the editor cards for every accommodation of a vacation.
func lodgingBlock(tz *time.Location, v *models.Vacation) []lodgingEditorView {
	out := make([]lodgingEditorView, 0, len(v.Lodgings))
	for i := range v.Lodgings {
		lo := v.Lodgings[i]
		out = append(out, newLodgingEditorView(tz, v, &lo))
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
