package server

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

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

// budgetView is the computed budget breakdown shown on the Budget tab.
type budgetView struct {
	HasBudget      bool
	Total          *float64
	Spent          float64
	Remaining      float64
	Over           bool
	PercentClamped int
	People         int
	Nights         int
	PerPerson      float64
	PerNight       float64
}

// newBudgetView computes the budget breakdown for a vacation. spent is the sum
// of planned item costs (0 until per-item costs are tracked).
func newBudgetView(v *models.Vacation, spent float64) budgetView {
	b := budgetView{People: v.People, Nights: v.Nights()}
	if v.Budget == nil {
		return b
	}
	total := *v.Budget
	b.HasBudget = true
	b.Total = v.Budget
	b.Spent = spent
	b.Remaining = total - spent
	b.Over = spent > total
	if total > 0 {
		p := int(spent / total * 100)
		if p < 0 {
			p = 0
		}
		if p > 100 {
			p = 100
		}
		b.PercentClamped = p
	}
	if v.People > 0 {
		b.PerPerson = total / float64(v.People)
	}
	if b.Nights > 0 {
		b.PerNight = total / float64(b.Nights)
	}
	return b
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
	if v.Items, err = s.store.ListItems(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}

	categories, _ := s.store.ListCategories(r.Context())
	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())
	var spent float64
	for _, it := range v.Items {
		if it.Cost != nil {
			spent += *it.Cost
		}
	}
	activities, ideas := overviewLists(loc, tz, v)

	s.page(w, r, "vacation", v.Title, map[string]any{
		"Vacation":        v,
		"AIEnabled":       s.ai.Enabled(),
		"Budget":          newBudgetView(v, spent),
		"Categories":      categories,
		"HomeAddress":     s.homeAddress(r.Context()),
		"ActivityList":    activities,
		"Ideas":           ideas,
		"CalTravel":       travelCalBlocks(loc, tz, v),
		"ArrivalTravel":   s.travelEditor(r.Context(), tz, v, models.TravelArrival),
		"DepartureTravel": s.travelEditor(r.Context(), tz, v, models.TravelDeparture),
	})
}

// travelEditor builds the inline editor view for one travel kind from the
// vacation's segments, synthesizing an empty segment when none exists yet.
func (s *Server) travelEditor(ctx context.Context, tz *time.Location, v *models.Vacation, kind models.TravelKind) travelEditorView {
	var seg *models.TravelSegment
	for i := range v.TravelSegments {
		if v.TravelSegments[i].Kind == kind {
			seg = &v.TravelSegments[i]
			break
		}
	}
	if seg == nil {
		seg = emptyTravelSegment(v.ID, kind)
	}
	_, routed := routeProfileForMode(seg.Mode)
	return s.newTravelEditorView(ctx, tz, v, seg, !routed || !s.routing.Enabled())
}

// overviewActivity is a scheduled entry shown in the Overview activity list.
type overviewActivity struct {
	WhenLabel string
	Title     string
	Category  string
	Latitude  *float64
	Longitude *float64
	HasCoords bool
	sortKey   time.Time
}

// calTravelBlock is a read-only travel leg rendered on the day/week calendar.
type calTravelBlock struct {
	StartMin int
	EndMin   int
	Title    string
	Label    string
}

// travelLabel builds a human label for a travel segment, e.g. "Arrival · BER → LIS".
func travelLabel(loc *i18n.Localizer, t models.TravelSegment) string {
	label := loc.T("travel.kind." + string(t.Kind))
	switch {
	case t.FromLocation != "" && t.ToLocation != "":
		label += " · " + t.FromLocation + " → " + t.ToLocation
	case t.ToLocation != "":
		label += " · " + t.ToLocation
	case t.FromLocation != "":
		label += " · " + t.FromLocation
	}
	return label
}

// modeLabel returns the localized travel mode name (empty when no mode is set).
func modeLabel(loc *i18n.Localizer, mode string) string {
	if mode == "" {
		return ""
	}
	return loc.T("travel.mode." + mode)
}

// travelCalBlocks groups travel legs by their departure day (in the display
// timezone) so the calendar can render them as read-only blocks.
func travelCalBlocks(loc *i18n.Localizer, tz *time.Location, v *models.Vacation) map[string][]calTravelBlock {
	out := make(map[string][]calTravelBlock)
	for _, ts := range v.TravelSegments {
		if ts.DepartAt == nil {
			continue
		}
		dep := ts.DepartAt.In(tz)
		day := dep.Format("2006-01-02")
		startMin := dep.Hour()*60 + dep.Minute()
		endMin := startMin + 60
		label := dep.Format("15:04")
		if ts.ArriveAt != nil {
			arr := ts.ArriveAt.In(tz)
			label += "–" + arr.Format("15:04")
			if arr.Format("2006-01-02") == day && arr.Hour()*60+arr.Minute() > startMin {
				endMin = arr.Hour()*60 + arr.Minute()
			} else if ts.ArriveAt.After(*ts.DepartAt) {
				endMin = 1440
			}
		}
		if endMin > 1440 {
			endMin = 1440
		}
		out[day] = append(out[day], calTravelBlock{StartMin: startMin, EndMin: endMin, Title: travelLabel(loc, ts), Label: label})
	}
	return out
}

// overviewLists splits items into a chronological activity list (those with a
// day) and the unscheduled "ideas" bucket, and merges travel legs (by departure
// time) into the activity list. The result is sorted chronologically.
func overviewLists(loc *i18n.Localizer, tz *time.Location, v *models.Vacation) (activities []overviewActivity, ideas []models.Item) {
	for _, ts := range v.TravelSegments {
		if ts.DepartAt == nil {
			continue
		}
		dep := ts.DepartAt.In(tz)
		lat, lng := ts.ToLat, ts.ToLng
		if lat == nil {
			lat, lng = ts.FromLat, ts.FromLng
		}
		activities = append(activities, overviewActivity{
			WhenLabel: fmtDate(dep) + " · " + dep.Format("15:04"),
			Title:     travelLabel(loc, ts),
			Category:  modeLabel(loc, ts.Mode),
			Latitude:  lat,
			Longitude: lng,
			HasCoords: lat != nil && lng != nil,
			sortKey:   *ts.DepartAt,
		})
	}
	for _, it := range v.Items {
		if it.Day == nil {
			ideas = append(ideas, it)
			continue
		}
		when := fmtDate(*it.Day)
		key := *it.Day
		if it.Timed() {
			when += " · " + it.StartLabel()
			key = it.Day.Add(time.Duration(it.StartMin) * time.Minute)
		}
		activities = append(activities, overviewActivity{
			WhenLabel: when,
			Title:     it.Title,
			Category:  it.Category,
			Latitude:  it.Latitude,
			Longitude: it.Longitude,
			HasCoords: it.HasCoords(),
			sortKey:   key,
		})
	}
	sort.SliceStable(activities, func(i, j int) bool {
		return activities[i].sortKey.Before(activities[j].sortKey)
	})
	return activities, ideas
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

	// The dates/title/destination ripple through the header, overview, day plan
	// and travel defaults, so refresh the page to reflect them everywhere.
	if isHTMX(r) {
		w.Header().Set("HX-Refresh", "true")
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
