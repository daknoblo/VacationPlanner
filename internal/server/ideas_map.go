package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/route"
)

type ideasMapPoint struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	DateRange string   `json:"date_range,omitempty"`
	Day       string   `json:"day,omitempty"`
}

type ideasMapPayload struct {
	Lodgings []ideasMapPoint `json:"lodgings"`
	Ideas    []ideasMapPoint `json:"ideas"`
	Routing  bool            `json:"routing"`
}

// The map reads all saved ideas, including scheduled and visited entries.
// Reading markers never starts geography or routing provider work.
func (s *Server) handleIdeasMap(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, err := s.store.GetVacation(r.Context(), id); err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	lodgings, err := s.store.ListLodgings(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items, err := s.store.ListItems(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	_, tz := s.regionSettings(r.Context())
	payload := ideasMapPayload{
		Lodgings: make([]ideasMapPoint, 0, len(lodgings)), Ideas: make([]ideasMapPoint, 0, len(items)),
		Routing: s.routing != nil && s.routing.Enabled(),
	}
	for _, lodging := range lodgings {
		payload.Lodgings = append(payload.Lodgings, ideasMapPoint{
			ID: lodging.ID.String(), Title: lodging.Name, Lat: lodging.Latitude, Lng: lodging.Longitude,
			DateRange: fmtDate(lodging.CheckIn.In(tz)) + " – " + fmtDate(lodging.CheckOut.In(tz)),
		})
	}
	for _, item := range items {
		point := ideasMapPoint{ID: item.ID.String(), Title: item.Title, Lat: item.Latitude, Lng: item.Longitude}
		if item.Day != nil {
			point.Day = fmtDate(*item.Day)
		}
		payload.Ideas = append(payload.Ideas, point)
	}
	s.ideasMapJSON(w, payload)
}

type ideaDrive struct {
	Status   string `json:"status"`
	Distance string `json:"distance,omitempty"`
	Duration string `json:"duration,omitempty"`
}

func (s *Server) ideasMapJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.log.Error("encoding ideas map", "err", err)
	}
}

// Each request resolves one directed accommodation-to-idea leg using current
// saved coordinates. The client requests legs sequentially only while visible.
func (s *Server) handleIdeaDrive(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	lodgingID, lodgingErr := uuid.Parse(r.URL.Query().Get("lodging"))
	itemID, itemErr := uuid.Parse(r.URL.Query().Get("item"))
	if err != nil || lodgingErr != nil || itemErr != nil {
		s.notFound(w, r)
		return
	}
	lodging, err := s.store.GetLodging(r.Context(), lodgingID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	if lodging.VacationID != id || item.VacationID != id {
		s.notFound(w, r)
		return
	}
	if !lodging.HasCoords() || !item.HasCoords() {
		s.ideasMapJSON(w, ideaDrive{Status: "missing"})
		return
	}
	start := route.Point{Lat: *lodging.Latitude, Lng: *lodging.Longitude}
	end := route.Point{Lat: *item.Latitude, Lng: *item.Longitude}
	if s.routing == nil || !s.routing.Enabled() {
		s.ideasMapJSON(w, ideaDrive{Status: "disabled"})
		return
	}
	select {
	case s.ideasRouteGate <- struct{}{}:
		defer func() { <-s.ideasRouteGate }()
	default:
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	settings, err := s.settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	result, err := activityRoute(ctx, activityRouteKey{client: s.routing, baseURL: settings[settingRouteBaseURL], from: start, to: end})
	if err != nil || !usableIdeaDrive(result) {
		s.log.Warn("idea driving route unavailable", "vacation_id", id, "item_id", item.ID, "err", err)
		s.ideasMapJSON(w, ideaDrive{Status: "unavailable"})
		return
	}
	s.ideasMapJSON(w, ideaDrive{
		Status: "ready", Distance: formatDistance(result.Legs[0].DistanceM), Duration: formatDuration(result.Legs[0].DurationS),
	})
}

func usableIdeaDrive(result route.Result) bool {
	if len(result.Legs) != 1 {
		return false
	}
	for _, value := range []float64{result.Legs[0].DistanceM, result.Legs[0].DurationS} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return false
		}
	}
	return true
}
