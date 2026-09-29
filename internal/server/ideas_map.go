package server

import (
	"encoding/json"
	"math"
	"net/http"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
)

type ideasMapPoint struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Lat               *float64 `json:"lat"`
	Lng               *float64 `json:"lng"`
	DateRange         string   `json:"date_range,omitempty"`
	Day               string   `json:"day,omitempty"`
	ScheduledDay      string   `json:"scheduled_day,omitempty"`
	Description       string   `json:"description,omitempty"`
	DescriptionStatus string   `json:"description_status,omitempty"`
}

type ideasMapDay struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ideasMapPayload struct {
	Lodgings      []ideasMapPoint          `json:"lodgings"`
	Ideas         []ideasMapPoint          `json:"ideas"`
	Routing       bool                     `json:"routing"`
	Routes        map[string]ideaDrive     `json:"routes"`
	Progress      models.IdeaRouteProgress `json:"progress"`
	ProgressLabel string                   `json:"progress_label,omitempty"`
	Days          []ideasMapDay            `json:"days"`
}

// The map reads all saved ideas, including scheduled and visited entries.
// Reading markers never starts geography or routing provider work.
func (s *Server) handleIdeasMap(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	vacation, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
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
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	descriptions, err := s.store.ListIdeaDescriptions(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	loc := i18n.FromContext(r.Context())
	payload := ideasMapPayload{
		Lodgings: make([]ideasMapPoint, 0, len(lodgings)), Ideas: make([]ideasMapPoint, 0, len(items)),
		Routing: s.routing != nil && s.routing.Enabled(),
		Days:    make([]ideasMapDay, 0),
	}
	for _, day := range vacation.Days() {
		payload.Days = append(payload.Days, ideasMapDay{Value: day.Format("2006-01-02"), Label: fmtDate(day)})
	}
	for _, lodging := range lodgings {
		payload.Lodgings = append(payload.Lodgings, ideasMapPoint{
			ID: lodging.ID.String(), Title: lodging.Name, Lat: lodging.Latitude, Lng: lodging.Longitude,
			DateRange: fmtDate(lodging.CheckIn.In(tz)) + " – " + fmtDate(lodging.CheckOut.In(tz)),
		})
	}
	for _, item := range items {
		point := ideasMapPoint{ID: item.ID.String(), Title: item.Title, Lat: item.Latitude, Lng: item.Longitude}
		point.Description = models.ShortDescription(item.Description)
		if point.Description == "" {
			if saved, ok := descriptions[item.ID]; ok {
				point.DescriptionStatus = saved.Status
				point.Description = saved.English
				if loc.Code() == "de" {
					point.Description = saved.German
				}
			} else if s.ai.Enabled() && s.foundryDeployment(settings) != "" {
				point.DescriptionStatus = "pending"
			}
		}
		if item.Day != nil {
			point.Day = fmtDate(*item.Day)
			point.ScheduledDay = item.Day.Format("2006-01-02")
		}
		payload.Ideas = append(payload.Ideas, point)
	}
	payload.Routes = make(map[string]ideaDrive)
	if payload.Routing {
		payload.Progress, err = s.store.IdeaRouteProgress(r.Context(), settings[settingRouteBaseURL], id)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		payload.ProgressLabel = i18n.FromContext(r.Context()).T("ideas.map.cache_status",
			payload.Progress.Completed-payload.Progress.Failed, payload.Progress.Failed,
			payload.Progress.Total-payload.Progress.Completed)
	}
	var lodgingID uuid.UUID
	if raw := r.URL.Query().Get("lodging"); raw != "" {
		lodgingID, err = uuid.Parse(raw)
		if err != nil || lodgingID == uuid.Nil {
			s.notFound(w, r)
			return
		}
	}
	if payload.Routing {
		routes, err := s.store.ListIdeaRoutes(r.Context(), settings[settingRouteBaseURL], id, lodgingID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		for _, value := range routes {
			payload.Routes[value.ItemID.String()] = savedIdeaDrive(value)
		}
	}
	s.ideasMapJSON(w, payload)
}

type ideaDrive struct {
	Status    string       `json:"status"`
	Distance  string       `json:"distance,omitempty"`
	Duration  string       `json:"duration,omitempty"`
	Geometry  [][2]float64 `json:"geometry,omitempty"`
	DistanceM *float64     `json:"distance_m,omitempty"`
	DurationS *float64     `json:"duration_s,omitempty"`
	LodgingID string       `json:"lodging_id,omitempty"`
}

func savedIdeaDrive(value models.IdeaRoute) ideaDrive {
	result := ideaDrive{Status: value.Status}
	if value.Status == "ready" {
		result.Distance, result.Duration, result.Geometry = formatDistance(value.DistanceM), formatDuration(value.DurationS), value.Geometry
		result.DistanceM, result.DurationS, result.LodgingID = &value.DistanceM, &value.DurationS, value.LodgingID.String()
	}
	return result
}

func (s *Server) ideasMapJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.log.Error("encoding ideas map", "err", err)
	}
}

// Reads never enqueue jobs or call providers, including for uncached pairs.
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
	if s.routing == nil || !s.routing.Enabled() {
		s.ideasMapJSON(w, ideaDrive{Status: "disabled"})
		return
	}
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	routes, err := s.store.ListIdeaRoutes(r.Context(), settings[settingRouteBaseURL], id, lodgingID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, value := range routes {
		if value.ItemID == itemID {
			s.ideasMapJSON(w, savedIdeaDrive(value))
			return
		}
	}
	s.ideasMapJSON(w, ideaDrive{Status: "pending"})
}

func (s *Server) handleRetryIdeaRoutes(w http.ResponseWriter, r *http.Request) {
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
	if err := s.store.RetryIdeaRoutes(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.ideasMapJSON(w, struct {
		Status string `json:"status"`
	}{Status: "queued"})
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
