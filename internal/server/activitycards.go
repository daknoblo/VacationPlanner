package server

import (
	"context"
	"sort"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

// activityDriveKmh is the average speed used to estimate travel time between two
// activities from a straight-line distance when live routing is unavailable.
const activityDriveKmh = 40

// legBetween returns the distance (metres) and duration (seconds) from one point
// to another, using live routing (driving profile) when configured and falling
// back to a straight-line (Haversine) estimate otherwise. approx is true for the
// estimate; ok is false when either endpoint is missing.
func (s *Server) legBetween(ctx context.Context, from, to *route.Point) (distM, durS float64, approx, ok bool) {
	if from == nil || to == nil {
		return 0, 0, false, false
	}
	if s.routing.Enabled() {
		res, err := s.routing.Route(ctx, s.routeBaseURL(ctx), route.DefaultProfile, []route.Point{*from, *to})
		if err == nil {
			return res.TotalDistanceM, res.TotalDurationS, false, true
		}
		s.log.Warn("activity route failed", "err", err)
	}
	d := route.Haversine(*from, *to)
	return d, d / (activityDriveKmh * 1000 / 3600), true, true
}

// lodgingForDay returns the located lodging covering the given day (check-in day
// through check-out day, inclusive), or nil when none has coordinates that day.
func lodgingForDay(lodgings []models.Lodging, day time.Time) *models.Lodging {
	dy, dm, dd := day.Date()
	target := time.Date(dy, dm, dd, 0, 0, 0, 0, time.UTC)
	for i := range lodgings {
		l := lodgings[i]
		if !l.HasCoords() {
			continue
		}
		ci := l.CheckIn
		co := l.CheckOut
		in := time.Date(ci.Year(), ci.Month(), ci.Day(), 0, 0, 0, 0, time.UTC)
		out := time.Date(co.Year(), co.Month(), co.Day(), 0, 0, 0, 0, time.UTC)
		if !target.Before(in) && !target.After(out) {
			return &l
		}
	}
	return nil
}

// dayHotel resolves the "base" for a day — the lodging active that day, or the
// vacation destination — as a point and a display label. Either may be empty.
func dayHotel(loc *i18n.Localizer, v *models.Vacation, day time.Time) (*route.Point, string) {
	if l := lodgingForDay(v.Lodgings, day); l != nil {
		return &route.Point{Lat: *l.Latitude, Lng: *l.Longitude}, "🛏 " + l.Name
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

// autoOrigin returns the automatic origin for the item at index idx: the nearest
// preceding located stop that day, or the day's hotel when none precedes.
func autoOrigin(items []models.Item, idx int, hotelPt *route.Point, hotelLabel string) (*route.Point, string) {
	for j := idx - 1; j >= 0; j-- {
		if items[j].HasCoords() {
			return itemPoint(items[j]), items[j].Title
		}
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
			if items[j].ID.String() == it.OriginRef && items[j].HasCoords() {
				return itemPoint(items[j]), items[j].Title
			}
		}
		return autoOrigin(items, idx, hotelPt, hotelLabel)
	}
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
		if other.ID == it.ID || !other.HasCoords() {
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
// item's origin (start point) and the distance and time to reach it.
func (s *Server) dayCards(ctx context.Context, loc *i18n.Localizer, day time.Time, v *models.Vacation, items []models.Item) []overviewActivity {
	ordered := orderDayItems(items)
	hotelPt, hotelLabel := dayHotel(loc, v, day)
	dayKey := day.Format("2006-01-02")

	cards := make([]overviewActivity, 0, len(ordered))
	for idx, it := range ordered {
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
			Category:  it.Category,
			Cost:      it.Cost,
			DayKey:    dayKey,
			Origins:   originOptionsFor(loc, ordered, it, hotelLabel),
			Latitude:  it.Latitude,
			Longitude: it.Longitude,
			HasCoords: it.HasCoords(),
			sortKey:   key,
		}
		if to := itemPoint(it); to != nil {
			origin, originLabel := resolveOrigin(ordered, idx, hotelPt, hotelLabel)
			if distM, durS, approx, ok := s.legBetween(ctx, origin, to); ok {
				card.OriginLabel = originLabel
				card.DistanceLabel = formatDistance(distM)
				card.DurationLabel = formatDuration(durS)
				card.Approx = approx
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// dayCardMap groups a vacation's scheduled items by day (dateInput key) and
// builds the activity cards for each day.
func (s *Server) dayCardMap(ctx context.Context, loc *i18n.Localizer, v *models.Vacation) map[string][]overviewActivity {
	byDay := make(map[string][]models.Item)
	for _, it := range v.Items {
		if it.Day == nil {
			continue
		}
		key := it.Day.Format("2006-01-02")
		byDay[key] = append(byDay[key], it)
	}
	out := make(map[string][]overviewActivity, len(byDay))
	for key, items := range byDay {
		day, err := time.Parse("2006-01-02", key)
		if err != nil {
			continue
		}
		out[key] = s.dayCards(ctx, loc, day, v, items)
	}
	return out
}
