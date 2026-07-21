package server

import (
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

// travelEditorView is the data for one inline arrival/departure editor.
type travelEditorView struct {
	Seg       *models.TravelSegment
	VID       string
	Home      string
	DistLabel string // formatted distance, e.g. "210 km" (empty when unknown)
	DurLabel  string // formatted duration, e.g. "2 h 10 min" (empty when unknown)
	Approx    bool   // duration is a straight-line estimate (not routed)
}

// newTravelEditorView builds the editor view for a segment, formatting the
// computed distance and duration for display.
func (s *Server) newTravelEditorView(ctx context.Context, seg *models.TravelSegment, approx bool) travelEditorView {
	v := travelEditorView{
		Seg:    seg,
		VID:    seg.VacationID.String(),
		Home:   s.homeAddress(ctx),
		Approx: approx,
	}
	if seg.DistanceM != nil {
		v.DistLabel = formatDistance(*seg.DistanceM)
	}
	if seg.DurationS != nil {
		v.DurLabel = formatDuration(float64(*seg.DurationS))
	}
	return v
}

// emptyTravelSegment returns a blank segment for a kind so the editor can render
// when no arrival/departure has been saved yet.
func emptyTravelSegment(vacationID uuid.UUID, kind models.TravelKind) *models.TravelSegment {
	return &models.TravelSegment{VacationID: vacationID, Kind: kind}
}

func (s *Server) handleSaveTravel(w http.ResponseWriter, r *http.Request) {
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
	kind := models.TravelKind(formStr(r, "kind"))
	if !kind.Valid() {
		s.formError(w, r, "#travel-error-arrival", loc.T("error.travel_kind_required"))
		return
	}
	errTarget := "#travel-error-" + string(kind)
	mode := formStr(r, "mode")
	from := formStr(r, "from_location")
	to := formStr(r, "to_location")
	notes := formStr(r, "notes")
	if !maxLen(mode, 50) || !maxLen(from, 200) || !maxLen(to, 200) || !maxLen(notes, 2000) {
		s.formError(w, r, errTarget, loc.T("error.input_toolong"))
		return
	}
	departAt, err := parseDateTimePtr(r, "depart_at")
	if err != nil {
		s.formError(w, r, errTarget, loc.T("error.depart_invalid"))
		return
	}
	fromLat, fromLng, _ := parseCoords(r, "from_lat", "from_lng")
	toLat, toLng, _ := parseCoords(r, "to_lat", "to_lng")

	seg := &models.TravelSegment{
		VacationID:   vacationID,
		Kind:         kind,
		Mode:         mode,
		FromLocation: from,
		ToLocation:   to,
		FromLat:      fromLat,
		FromLng:      fromLng,
		ToLat:        toLat,
		ToLng:        toLng,
		DepartAt:     departAt,
		Notes:        notes,
	}
	approx := s.computeTravel(r.Context(), seg)
	if err := s.store.UpsertTravelSegment(r.Context(), seg); err != nil {
		s.serverError(w, r, err)
		return
	}

	s.fragment(w, r, "travel_editor", s.newTravelEditorView(r.Context(), seg, approx))
}

// computeTravel resolves the segment endpoints (geocoding free text when no
// coordinates were supplied) and fills distance, duration and the auto-computed
// arrival time. It reports whether the duration is a straight-line estimate.
func (s *Server) computeTravel(ctx context.Context, seg *models.TravelSegment) (approx bool) {
	from := s.resolvePoint(ctx, seg.FromLat, seg.FromLng, seg.FromLocation)
	to := s.resolvePoint(ctx, seg.ToLat, seg.ToLng, seg.ToLocation)
	if from != nil {
		seg.FromLat, seg.FromLng = &from.Lat, &from.Lng
	}
	if to != nil {
		seg.ToLat, seg.ToLng = &to.Lat, &to.Lng
	}
	seg.DistanceM, seg.DurationS = nil, nil
	if from == nil || to == nil {
		return false
	}

	var distM, durS float64
	approx = true
	if profile, ok := routeProfileForMode(seg.Mode); ok && s.routing.Enabled() {
		if res, rerr := s.routing.Route(ctx, s.routeBaseURL(ctx), profile, []route.Point{*from, *to}); rerr == nil {
			distM, durS, approx = res.TotalDistanceM, res.TotalDurationS, false
		} else {
			s.log.Warn("travel route failed", "err", rerr)
		}
	}
	if distM == 0 {
		distM = route.Haversine(*from, *to)
		durS = distM / (modeSpeedKmh(seg.Mode) * 1000 / 3600)
	}
	seg.DistanceM = &distM
	d := int(math.Round(durS))
	seg.DurationS = &d
	if seg.DepartAt != nil {
		arr := seg.DepartAt.Add(time.Duration(d) * time.Second)
		seg.ArriveAt = &arr
	}
	return approx
}

// resolvePoint returns coordinates for an endpoint, preferring the supplied
// coordinates and falling back to a forward geocode of the free-text location.
func (s *Server) resolvePoint(ctx context.Context, lat, lng *float64, text string) *route.Point {
	if lat != nil && lng != nil {
		return &route.Point{Lat: *lat, Lng: *lng}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	loc := i18n.FromContext(ctx)
	res, err := s.geo.Search(ctx, s.geoBaseURL(ctx), text, loc.Code(), 1)
	if err != nil || len(res) == 0 {
		return nil
	}
	return &route.Point{Lat: res[0].Lat, Lng: res[0].Lng}
}

// routeProfileForMode maps a travel mode to a road-routing profile. Only ground
// modes that follow roads use live routing; the rest fall back to Haversine.
func routeProfileForMode(mode string) (string, bool) {
	switch mode {
	case "car", "bus":
		return route.DefaultProfile, true
	default:
		return "", false
	}
}

// modeSpeedKmh is the average speed used to estimate travel time from a
// straight-line distance when live routing is unavailable.
func modeSpeedKmh(mode string) float64 {
	switch mode {
	case "flight":
		return 800
	case "train":
		return 120
	case "bus":
		return 80
	case "ferry":
		return 40
	case "car":
		return 70
	default:
		return 60
	}
}

func (s *Server) handleDeleteTravel(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "travelID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteTravelSegment(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
