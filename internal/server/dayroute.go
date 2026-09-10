package server

import (
	"context"
	"net/http"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

type dayRouteLegView struct {
	ItemID, Title, Origin, VisitTime string
	Distance, Duration               string
	Approx, Missing, Override        bool
}

type dayRouteView struct {
	Day, Base, Distance, Duration string
	Legs                          []dayRouteLegView
	Missing, Approx, Routed       int
}

func (s *Server) dayRoute(ctx context.Context, loc *i18n.Localizer, tz *time.Location, v *models.Vacation, day time.Time) dayRouteView {
	view := dayRouteView{Day: day.Format("2006-01-02")}
	base, label := dayHotel(loc, tz, v, day)
	view.Base = label
	if view.Base == "" {
		view.Base = loc.T("dayroute.base.unknown")
	}
	var items []models.Item
	for _, it := range v.Items {
		if it.OnDay(day) {
			items = append(items, it)
		}
	}
	var distance, duration float64
	for _, leg := range s.dayItemLegs(ctx, orderDayItems(items), base, view.Base) {
		entry := dayRouteLegView{
			ItemID: leg.Item.ID.String(), Title: leg.Item.Title, Origin: leg.OriginLabel,
			Approx: leg.Approx, Missing: !leg.OK, Override: leg.Override,
		}
		if leg.Item.Timed() {
			entry.VisitTime = leg.Item.StartLabel() + "–" + leg.Item.EndLabel()
		}
		if !leg.OK {
			view.Missing++
		} else {
			distance += leg.DistanceM
			entry.Distance = formatDistance(leg.DistanceM)
			if leg.Approx {
				view.Approx++
			} else {
				view.Routed++
				duration += leg.DurationS
				entry.Duration = formatDuration(leg.DurationS)
			}
		}
		view.Legs = append(view.Legs, entry)
	}
	// A blank total is unknown, not zero. Estimated distances never contribute
	// fabricated driving times, and this read-only view creates no bookings.
	if view.Routed+view.Approx > 0 {
		view.Distance = formatDistance(distance)
	}
	if view.Routed > 0 {
		view.Duration = formatDuration(duration)
	}
	return view
}

// handleDayRoute is lazy: only the visible day panel requests this fragment.
// One operation-wide deadline bounds routing even with many assigned stops.
func (s *Server) handleDayRoute(w http.ResponseWriter, r *http.Request) {
	day := parseDayParam(r)
	if day == nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	v, err := s.loadVacationFull(ctx, id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	if day.Before(v.StartDate) || day.After(v.EndDate) {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	_, tz := s.regionSettings(ctx)
	loc := i18n.FromContext(ctx)
	view := s.dayRoute(ctx, loc, tz, v, *day)
	w.Header().Set("Cache-Control", "no-store")
	s.fragment(w, r, "day_route", view)
}
