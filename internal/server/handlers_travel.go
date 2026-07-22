package server

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

// travelBlockView is one kind's full multi-stop editor: the multi-stop toggle
// plus the ordered list of step editors.
type travelBlockView struct {
	Kind  models.TravelKind
	VID   string
	Multi bool
	Steps []travelEditorView
}

// travelEditorView is the data for one inline arrival/departure step editor.
type travelEditorView struct {
	Seg         *models.TravelSegment
	VID         string
	Kind        models.TravelKind
	Number      int  // 1-based position shown to the user
	StepOrder   int  // stable key within the kind
	Multi       bool // block has more than one step (shows connectors + remove)
	Home        string
	DepartDate  string // date input value (YYYY-MM-DD), defaulted to the trip start/end
	DepartTime  string // time input value (HH:MM), empty until the user sets it
	DistLabel   string // formatted distance, e.g. "210 km" (empty when unknown)
	DurLabel    string // formatted duration, e.g. "2 h 10 min" (empty when unknown)
	ArriveLabel string // formatted computed arrival (empty when unknown)
	Approx      bool   // duration is a straight-line estimate (not routed)
}

// stepsForKind returns the vacation's travel legs of a kind, ordered by step_order.
func stepsForKind(v *models.Vacation, kind models.TravelKind) []models.TravelSegment {
	var out []models.TravelSegment
	for _, ts := range v.TravelSegments {
		if ts.Kind == kind {
			out = append(out, ts)
		}
	}
	return out
}

// travelTotalView is the summed distance and time of a travel direction, shown
// next to the Arrival/Departure heading and refreshed out-of-band on changes.
type travelTotalView struct {
	Kind      models.TravelKind
	DistLabel string
	DurLabel  string
	OOB       bool // rendered as an hx-swap-oob update rather than in place
}

// travelTotalFor sums the located legs of a travel direction into a summary.
func travelTotalFor(v *models.Vacation, kind models.TravelKind, oob bool) travelTotalView {
	tv := travelTotalView{Kind: kind, OOB: oob}
	var distM float64
	var durS int
	var haveDist, haveDur bool
	for _, ts := range stepsForKind(v, kind) {
		if ts.DistanceM != nil {
			distM += *ts.DistanceM
			haveDist = true
		}
		if ts.DurationS != nil {
			durS += *ts.DurationS
			haveDur = true
		}
	}
	if haveDist {
		tv.DistLabel = formatDistance(distM)
	}
	if haveDur {
		tv.DurLabel = formatDuration(float64(durS))
	}
	return tv
}

// travelBlock builds the multi-stop editor for one kind. When no leg exists yet
// it renders a single blank step pre-filled with sensible endpoint defaults.
func (s *Server) travelBlock(ctx context.Context, tz *time.Location, v *models.Vacation, kind models.TravelKind) travelBlockView {
	steps := stepsForKind(v, kind)
	multi := len(steps) > 1
	bv := travelBlockView{Kind: kind, VID: v.ID.String(), Multi: multi}
	if len(steps) == 0 {
		seg := emptyTravelSegment(v.ID, kind)
		s.applyEndpointDefaults(ctx, v, seg)
		bv.Steps = []travelEditorView{s.newTravelStepView(ctx, tz, v, seg, 1, multi)}
		return bv
	}
	for i := range steps {
		clone := steps[i]
		bv.Steps = append(bv.Steps, s.newTravelStepView(ctx, tz, v, &clone, i+1, multi))
	}
	return bv
}

// newTravelStepView builds the view for one step, formatting the computed
// distance, duration and arrival and defaulting the departure date to the trip's
// start (arrival) or end (departure) when none is set yet.
func (s *Server) newTravelStepView(ctx context.Context, tz *time.Location, v *models.Vacation, seg *models.TravelSegment, number int, multi bool) travelEditorView {
	_, routed := routeProfileForMode(seg.Mode)
	ev := travelEditorView{
		Seg:       seg,
		VID:       seg.VacationID.String(),
		Kind:      seg.Kind,
		Number:    number,
		StepOrder: seg.StepOrder,
		Multi:     multi,
		Home:      s.homeAddress(ctx),
		Approx:    !routed || !s.routing.Enabled(),
	}
	if seg.DistanceM != nil {
		ev.DistLabel = formatDistance(*seg.DistanceM)
	}
	if seg.DurationS != nil {
		ev.DurLabel = formatDuration(float64(*seg.DurationS))
	}
	if seg.DepartAt != nil {
		dep := seg.DepartAt.In(tz)
		ev.DepartDate = dep.Format("2006-01-02")
		ev.DepartTime = dep.Format("15:04")
	} else {
		def := v.StartDate
		if seg.Kind == models.TravelDeparture {
			def = v.EndDate
		}
		ev.DepartDate = def.Format("2006-01-02")
	}
	if seg.ArriveAt != nil {
		ev.ArriveLabel = seg.ArriveAt.In(tz).Format("02.01.2006 15:04")
	}
	return ev
}

// emptyTravelSegment returns a blank first-step segment for a kind so the editor
// can render when no arrival/departure has been saved yet.
func emptyTravelSegment(vacationID uuid.UUID, kind models.TravelKind) *models.TravelSegment {
	return &models.TravelSegment{VacationID: vacationID, Kind: kind}
}

// formInt reads an optional non-negative integer form value.
func formInt(r *http.Request, name string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(formStr(r, name))); err == nil && n >= 0 {
		return n
	}
	return def
}

func (s *Server) handleSaveTravel(w http.ResponseWriter, r *http.Request) {
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

	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())
	kind := models.TravelKind(formStr(r, "kind"))
	if !kind.Valid() {
		s.formError(w, r, "#travel-error-arrival", loc.T("error.travel_kind_required"))
		return
	}
	errTarget := "#travel-error-" + string(kind)
	stepOrder := formInt(r, "step_order", 0)
	mode := formStr(r, "mode")
	from := formStr(r, "from_location")
	to := formStr(r, "to_location")
	notes := formStr(r, "notes")
	if !maxLen(mode, 50) || !maxLen(from, 200) || !maxLen(to, 200) || !maxLen(notes, 2000) {
		s.formError(w, r, errTarget, loc.T("error.input_toolong"))
		return
	}
	departAt, err := parseDateTimeParts(r, "depart_date", "depart_time", tz)
	if err != nil {
		s.formError(w, r, errTarget, loc.T("error.depart_invalid"))
		return
	}
	fromLat, fromLng, _ := parseCoords(r, "from_lat", "from_lng")
	toLat, toLng, _ := parseCoords(r, "to_lat", "to_lng")

	seg := &models.TravelSegment{
		VacationID:   vacationID,
		Kind:         kind,
		StepOrder:    stepOrder,
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
	s.computeTravel(r.Context(), seg)
	if err := s.store.UpsertTravelSegment(r.Context(), seg); err != nil {
		s.serverError(w, r, err)
		return
	}

	hxTrigger(w, "itemsChanged")
	if v.TravelSegments, err = s.store.ListTravelSegments(r.Context(), vacationID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.fragment(w, r, "travel_saved", map[string]any{
		"Step":  s.newTravelStepView(r.Context(), tz, v, seg, stepOrder+1, false),
		"Total": travelTotalFor(v, kind, true),
	})
}

// handleAddTravelStep appends an empty leg to a kind and re-renders its block.
func (s *Server) handleAddTravelStep(w http.ResponseWriter, r *http.Request) {
	vacationID, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	kind := models.TravelKind(r.URL.Query().Get("kind"))
	if !kind.Valid() {
		s.notFound(w, r)
		return
	}
	v, err := s.loadVacationFull(r.Context(), vacationID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	if err := s.appendTravelStep(r.Context(), v, kind); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderTravelBlock(w, r, vacationID, kind)
}

// appendTravelStep creates a new leg after the last one, chaining its start
// point from the previous leg's destination.
func (s *Server) appendTravelStep(ctx context.Context, v *models.Vacation, kind models.TravelKind) error {
	steps := stepsForKind(v, kind)
	seg := &models.TravelSegment{VacationID: v.ID, Kind: kind}
	if len(steps) > 0 {
		last := steps[len(steps)-1]
		seg.StepOrder = last.StepOrder + 1
		seg.FromLocation, seg.FromLat, seg.FromLng = last.ToLocation, last.ToLat, last.ToLng
	}
	return s.store.CreateTravelSegment(ctx, seg)
}

// handleToggleTravelMulti enables or disables multi-stop for a kind. Enabling
// ensures a first leg exists and adds a second; disabling drops the extra legs.
func (s *Server) handleToggleTravelMulti(w http.ResponseWriter, r *http.Request) {
	vacationID, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	kind := models.TravelKind(r.URL.Query().Get("kind"))
	if !kind.Valid() {
		s.notFound(w, r)
		return
	}
	v, err := s.loadVacationFull(r.Context(), vacationID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	steps := stepsForKind(v, kind)
	// Toggle based on the current server state so the result never depends on
	// how the browser serialises the (un)checked box.
	enable := len(steps) <= 1
	if enable {
		if len(steps) == 0 {
			seg0 := emptyTravelSegment(vacationID, kind)
			s.applyEndpointDefaults(r.Context(), v, seg0)
			s.computeTravel(r.Context(), seg0)
			if err := s.store.CreateTravelSegment(r.Context(), seg0); err != nil {
				s.serverError(w, r, err)
				return
			}
			if v, err = s.loadVacationFull(r.Context(), vacationID); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
		if err := s.appendTravelStep(r.Context(), v, kind); err != nil {
			s.serverError(w, r, err)
			return
		}
	} else {
		for i := 1; i < len(steps); i++ {
			if err := s.store.DeleteTravelSegment(r.Context(), steps[i].ID); err != nil && !isNotFound(err) {
				s.serverError(w, r, err)
				return
			}
			if err := s.store.DeleteTravelStepDocuments(r.Context(), vacationID, kind, steps[i].StepOrder); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	s.renderTravelBlock(w, r, vacationID, kind)
}

// handleRemoveTravelStep deletes one leg and re-renders the kind's block.
func (s *Server) handleRemoveTravelStep(w http.ResponseWriter, r *http.Request) {
	vacationID, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	travelID, err := urlUUID(r, "travelID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	kind := models.TravelKind(r.URL.Query().Get("kind"))
	if !kind.Valid() {
		s.notFound(w, r)
		return
	}
	// Look up the leg's step order first so its documents can be removed too;
	// otherwise they could resurface on a later leg reusing the same order.
	step := -1
	if v, lerr := s.loadVacationFull(r.Context(), vacationID); lerr == nil {
		for _, ts := range v.TravelSegments {
			if ts.ID == travelID {
				step = ts.StepOrder
				break
			}
		}
	}
	if err := s.store.DeleteTravelSegment(r.Context(), travelID); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	if step >= 0 {
		if err := s.store.DeleteTravelStepDocuments(r.Context(), vacationID, kind, step); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.renderTravelBlock(w, r, vacationID, kind)
}

// renderTravelBlock reloads the vacation and renders one kind's editor block.
func (s *Server) renderTravelBlock(w http.ResponseWriter, r *http.Request, vacationID uuid.UUID, kind models.TravelKind) {
	v, err := s.loadVacationFull(r.Context(), vacationID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	_, tz := s.regionSettings(r.Context())
	hxTrigger(w, "itemsChanged")
	s.fragment(w, r, "travel_block_wrap", map[string]any{
		"Block": s.travelBlock(r.Context(), tz, v, kind),
		"Total": travelTotalFor(v, kind, true),
	})
}

// computeTravel resolves the segment endpoints (geocoding free text when no
// coordinates were supplied) and fills distance, duration and the auto-computed
// arrival time. Whether the duration is a straight-line estimate is derived
// separately from the mode when building the step view.
func (s *Server) computeTravel(ctx context.Context, seg *models.TravelSegment) {
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
		return
	}

	var distM, durS float64
	if profile, ok := routeProfileForMode(seg.Mode); ok && s.routing.Enabled() {
		if res, rerr := s.routing.Route(ctx, s.routeBaseURL(ctx), profile, []route.Point{*from, *to}); rerr == nil {
			distM, durS = res.TotalDistanceM, res.TotalDurationS
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
	res, err := s.geo.Search(ctx, s.geoBaseURL(ctx), text, loc.Code(), 1, 0, 0)
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
