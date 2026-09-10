package server

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

// activityDriveKmh is the average speed used to estimate travel time between two
// activities from a straight-line distance when live routing is unavailable.
const activityDriveKmh = 40

type activityRouteKey struct {
	client   *route.Client
	baseURL  string
	from, to route.Point
}

type activityRouteFlight struct {
	done   chan struct{}
	result route.Result
	err    error
}

var activityRoutes = struct {
	sync.Mutex
	flights map[activityRouteKey]*activityRouteFlight
}{flights: make(map[activityRouteKey]*activityRouteFlight)}

// Route's cache avoids repeat requests; this short-lived coordination also
// prevents simultaneous overview/card/timeline refreshes from missing it together.
func activityRoute(ctx context.Context, key activityRouteKey) (route.Result, error) {
	activityRoutes.Lock()
	if flight, exists := activityRoutes.flights[key]; exists {
		activityRoutes.Unlock()
		select {
		case <-ctx.Done():
			return route.Result{}, ctx.Err()
		case <-flight.done:
			return flight.result, flight.err
		}
	}
	flight := &activityRouteFlight{done: make(chan struct{})}
	activityRoutes.flights[key] = flight
	activityRoutes.Unlock()

	flight.result, flight.err = key.client.Route(ctx, key.baseURL, route.DefaultProfile, []route.Point{key.from, key.to})
	activityRoutes.Lock()
	delete(activityRoutes.flights, key)
	close(flight.done)
	activityRoutes.Unlock()
	return flight.result, flight.err
}

// legBetween returns the distance (metres) and duration (seconds) from one point
// to another, using live routing (driving profile) when configured and falling
// back to a straight-line (Haversine) estimate otherwise. approx is true for the
// estimate; ok is false when either endpoint is missing.
func (s *Server) legBetween(ctx context.Context, from, to *route.Point) (distM, durS float64, approx, ok bool) {
	if from == nil || to == nil {
		return 0, 0, false, false
	}
	if s.routing != nil && s.routing.Enabled() && ctx.Err() == nil {
		res, err := activityRoute(ctx, activityRouteKey{client: s.routing, baseURL: s.routeBaseURL(ctx), from: *from, to: *to})
		if err == nil {
			return res.TotalDistanceM, res.TotalDurationS, false, true
		}
		s.log.Warn("activity route failed", "err", err)
	}
	d := route.Haversine(*from, *to)
	return d, d / (activityDriveKmh * 1000 / 3600), true, true
}

// lodgingForDay returns the lodging covering the given day, including checkout.
// On hotel-change days the stay occupied overnight wins over a new check-in;
// overlapping stays use the latest check-in, with a stable tie-breaker.
// Check-in/out are instants, so they are read in the display timezone — the same
// way lodgingDayStrips places them on the planner.
func lodgingForDay(tz *time.Location, lodgings []models.Lodging, day time.Time) *models.Lodging {
	dy, dm, dd := day.Date()
	target := time.Date(dy, dm, dd, 0, 0, 0, 0, time.UTC)
	var chosen *models.Lodging
	var chosenOvernight bool
	for i := range lodgings {
		l := &lodgings[i]
		ci := l.CheckIn.In(tz)
		co := l.CheckOut.In(tz)
		in := time.Date(ci.Year(), ci.Month(), ci.Day(), 0, 0, 0, 0, time.UTC)
		out := time.Date(co.Year(), co.Month(), co.Day(), 0, 0, 0, 0, time.UTC)
		if !target.Before(in) && !target.After(out) {
			overnight := in.Before(target)
			if chosen == nil || (overnight && !chosenOvernight) ||
				(overnight == chosenOvernight && (l.CheckIn.After(chosen.CheckIn) ||
					(l.CheckIn.Equal(chosen.CheckIn) && l.Name+l.ID.String() < chosen.Name+chosen.ID.String()))) {
				chosen, chosenOvernight = l, overnight
			}
		}
	}
	return chosen
}

// dayHotel resolves the "base" for a day — the lodging active that day, or the
// vacation destination — as a point and a display label. Either may be empty.
func dayHotel(loc *i18n.Localizer, tz *time.Location, v *models.Vacation, day time.Time) (*route.Point, string) {
	if l := lodgingForDay(tz, v.Lodgings, day); l != nil {
		if l.HasCoords() {
			return &route.Point{Lat: *l.Latitude, Lng: *l.Longitude}, "🛏 " + l.Name
		}
		return nil, "🛏 " + l.Name
	}
	if v.HasCoords() {
		label := v.Destination
		if label == "" {
			label = loc.T("day.hotel")
		}
		return &route.Point{Lat: *v.Latitude, Lng: *v.Longitude}, "🏨 " + label
	}
	return nil, ""
}

// itemPoint returns an item's coordinates as a route point, or nil.
func itemPoint(it models.Item) *route.Point {
	if !it.HasCoords() {
		return nil
	}
	return &route.Point{Lat: *it.Latitude, Lng: *it.Longitude}
}

// autoOrigin uses the preceding stop, even when unlocated: skipping it would
// imply a direct journey that is not the saved itinerary.
func autoOrigin(items []models.Item, idx int, hotelPt *route.Point, hotelLabel string) (*route.Point, string) {
	if idx > 0 {
		return itemPoint(items[idx-1]), items[idx-1].Title
	}
	return hotelPt, hotelLabel
}

// resolveOrigin picks the origin point and label for the item at index idx,
// honouring an explicit OriginRef ("hotel" or another same-day item id) and
// falling back to the automatic previous stop when the reference is unusable.
func resolveOrigin(items []models.Item, idx int, hotelPt *route.Point, hotelLabel string) (*route.Point, string) {
	it := items[idx]
	switch it.OriginRef {
	case "":
		return autoOrigin(items, idx, hotelPt, hotelLabel)
	case "hotel":
		return hotelPt, hotelLabel
	default:
		for j := range items {
			if j != idx && items[j].ID.String() == it.OriginRef {
				return itemPoint(items[j]), items[j].Title
			}
		}
		return autoOrigin(items, idx, hotelPt, hotelLabel)
	}
}

type dayItemLeg struct {
	Item                 models.Item
	OriginLabel          string
	DistanceM, DurationS float64
	Approx, OK, Override bool
}

// dayItemLegs shares visiting order, origins and routing cache keys with cards.
func (s *Server) dayItemLegs(ctx context.Context, ordered []models.Item, hotelPt *route.Point, hotelLabel string) []dayItemLeg {
	legs := make([]dayItemLeg, 0, len(ordered))
	for idx, it := range ordered {
		origin, label := resolveOrigin(ordered, idx, hotelPt, hotelLabel)
		dist, dur, approx, ok := s.legBetween(ctx, origin, itemPoint(it))
		override := it.OriginRef == "hotel"
		for j, other := range ordered {
			if j != idx && it.OriginRef == other.ID.String() {
				override = true
			}
		}
		legs = append(legs, dayItemLeg{Item: it, OriginLabel: label, DistanceM: dist, DurationS: dur, Approx: approx, OK: ok, Override: override})
	}
	return legs
}

// originOptionsFor builds the predecessor choices for an item's origin picker:
// automatic, the day's hotel (when located) and every other located item on the
// same day.
func originOptionsFor(loc *i18n.Localizer, items []models.Item, it models.Item, hotelLabel string) []originOption {
	opts := []originOption{{Value: "", Label: loc.T("activity.origin.auto"), Selected: it.OriginRef == ""}}
	if hotelLabel != "" {
		opts = append(opts, originOption{Value: "hotel", Label: hotelLabel, Selected: it.OriginRef == "hotel"})
	}
	for _, other := range items {
		if other.ID == it.ID || (!other.HasCoords() && it.OriginRef != other.ID.String()) {
			continue
		}
		label := other.Title
		if other.Timed() {
			label = other.StartLabel() + " " + other.Title
		}
		opts = append(opts, originOption{Value: other.ID.String(), Label: label, Selected: it.OriginRef == other.ID.String()})
	}
	return opts
}

// orderDayItems sorts a day's items into visiting order: timed entries by start
// time, then untimed entries by creation. It returns a new slice.
func orderDayItems(items []models.Item) []models.Item {
	out := make([]models.Item, len(items))
	copy(out, items)
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].Timed(), out[j].Timed()
		if ti != tj {
			return ti // timed items come first
		}
		if ti {
			return out[i].StartMin < out[j].StartMin
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// dayCards builds the activity cards for a single day's items, resolving each
// item's origin (start point) and the distance and time to reach it. It also
// prepends the arrival journey on the trip's first day and appends the departure
// journey on its last day, so the route reflects the drives to/from the base.
func (s *Server) dayCards(ctx context.Context, loc *i18n.Localizer, tz *time.Location, day time.Time, v *models.Vacation, items []models.Item) []overviewActivity {
	ordered := orderDayItems(items)
	hotelPt, hotelLabel := dayHotel(loc, tz, v, day)
	dayKey := day.Format("2006-01-02")

	cards := make([]overviewActivity, 0, len(ordered)+2)
	for _, leg := range s.dayItemLegs(ctx, ordered, hotelPt, hotelLabel) {
		it := leg.Item
		tm := ""
		key := day.Add(23*time.Hour + 59*time.Minute) // untimed items sort last that day
		if it.Timed() {
			tm = it.StartLabel()
			key = day.Add(time.Duration(it.StartMin) * time.Minute)
		}
		card := overviewActivity{
			ItemID:    it.ID.String(),
			Weekday:   weekdayLabel(loc, day),
			DateLabel: fmtDate(day),
			TimeLabel: tm,
			Title:     it.Title,
			Links:     it.Links,
			Category:  it.Category,
			Cost:      it.Cost,
			DayKey:    dayKey,
			Origins:   originOptionsFor(loc, ordered, it, hotelLabel),
			Latitude:  it.Latitude,
			Longitude: it.Longitude,
			HasCoords: it.HasCoords(),
			sortKey:   key,
		}
		if leg.OK {
			card.OriginLabel = leg.OriginLabel
			card.DistanceLabel = formatDistance(leg.DistanceM)
			card.DurationLabel = formatDuration(leg.DurationS)
			card.Approx = leg.Approx
		}
		cards = append(cards, card)
	}

	// The arrival lands you on the first day, the departure leaves on the last.
	cards = append(cards, dayTravelCards(loc, tz, day, v)...)

	sort.SliceStable(cards, func(i, j int) bool { return cards[i].sortKey.Before(cards[j].sortKey) })
	return cards
}

// dayTravelCards returns the travel journeys that belong to the given day: the
// arrival on the trip's first day and the departure on its last day. Each is a
// single collapsed card, positioned by its departure time (or first/last).
func dayTravelCards(loc *i18n.Localizer, tz *time.Location, day time.Time, v *models.Vacation) []overviewActivity {
	dayKey := day.Format("2006-01-02")
	var out []overviewActivity

	add := func(kind models.TravelKind, belongsKey string) {
		if dayKey != belongsKey {
			return
		}
		card, ok := travelSummaryCard(loc, tz, v, kind)
		if !ok {
			return
		}
		card.Weekday = weekdayLabel(loc, day)
		card.DateLabel = fmtDate(day)
		legs := stepsForKind(v, kind)
		switch {
		case legs[0].DepartAt != nil:
			dep := legs[0].DepartAt.In(tz)
			card.TimeLabel = dep.Format("15:04")
			card.sortKey = day.Add(time.Duration(dep.Hour())*time.Hour + time.Duration(dep.Minute())*time.Minute)
		case kind == models.TravelArrival:
			card.TimeLabel = ""
			card.sortKey = day // arrival sorts first
		default:
			card.TimeLabel = ""
			card.sortKey = day.Add(23*time.Hour + 59*time.Minute) // departure sorts last
		}
		out = append(out, card)
	}

	add(models.TravelArrival, v.StartDate.Format("2006-01-02"))
	add(models.TravelDeparture, v.EndDate.Format("2006-01-02"))
	return out
}

// dayCardMap groups a vacation's scheduled items by day (dateInput key) and
// builds the activity cards for each day. The trip's first and last day are
// always included so the arrival/departure journeys show even with no items.
func (s *Server) dayCardMap(ctx context.Context, loc *i18n.Localizer, tz *time.Location, v *models.Vacation) map[string][]overviewActivity {
	byDay := make(map[string][]models.Item)
	for _, it := range v.Items {
		if it.Day == nil {
			continue
		}
		key := it.Day.Format("2006-01-02")
		byDay[key] = append(byDay[key], it)
	}
	if len(stepsForKind(v, models.TravelArrival)) > 0 {
		k := v.StartDate.Format("2006-01-02")
		if _, ok := byDay[k]; !ok {
			byDay[k] = nil
		}
	}
	if len(stepsForKind(v, models.TravelDeparture)) > 0 {
		k := v.EndDate.Format("2006-01-02")
		if _, ok := byDay[k]; !ok {
			byDay[k] = nil
		}
	}
	out := make(map[string][]overviewActivity, len(byDay))
	for key, items := range byDay {
		day, err := time.Parse("2006-01-02", key)
		if err != nil {
			continue
		}
		out[key] = s.dayCards(ctx, loc, tz, day, v, items)
	}
	return out
}
