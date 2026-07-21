package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// itemFromForm builds an Item from the posted form. Day, time range, coordinates
// and cost are all optional, so the same form serves timed day entries and
// untimed points of interest alike.
func (s *Server) itemFromForm(r *http.Request) (*models.Item, error) {
	loc := i18n.FromContext(r.Context())

	title := strings.TrimSpace(formStr(r, "title"))
	if title == "" || !maxLen(title, 200) {
		return nil, errValidation(loc.T("error.item_title_required"))
	}
	category := strings.TrimSpace(formStr(r, "category"))
	description := strings.TrimSpace(formStr(r, "description"))
	location := strings.TrimSpace(formStr(r, "location"))
	notes := strings.TrimSpace(formStr(r, "notes"))
	if !maxLen(category, 100) || !maxLen(description, 2000) || !maxLen(location, 200) || !maxLen(notes, 2000) {
		return nil, errValidation(loc.T("error.input_toolong"))
	}

	day, err := parseDatePtr(r, "day")
	if err != nil {
		return nil, errValidation(loc.T("error.planned_invalid"))
	}

	// Times are optional: without them the item is untimed and only appears in
	// the day's list, not on the hour grid.
	startStr := strings.TrimSpace(formStr(r, "start"))
	endStr := strings.TrimSpace(formStr(r, "end"))
	var startMin, endMin int
	if startStr != "" || endStr != "" {
		startMin = parseMinutes(startStr, 540)
		endMin = parseMinutes(endStr, startMin+60)
		if endMin <= startMin {
			endMin = startMin + 30
		}
	}

	lat, lng, err := parseCoords(r, "latitude", "longitude")
	if err != nil {
		return nil, err
	}
	cost, err := parseCostPtr(r, "cost", loc)
	if err != nil {
		return nil, err
	}

	return &models.Item{
		Category:    category,
		Title:       title,
		Description: description,
		Location:    location,
		Latitude:    lat,
		Longitude:   lng,
		Day:         day,
		StartMin:    startMin,
		EndMin:      endMin,
		Cost:        cost,
		Notes:       notes,
	}, nil
}

// parseCostPtr reads an optional, non-negative monetary amount.
func parseCostPtr(r *http.Request, field string, loc *i18n.Localizer) (*float64, error) {
	v := strings.TrimSpace(formStr(r, field))
	if v == "" {
		return nil, nil
	}
	v = strings.ReplaceAll(v, ",", ".")
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return nil, errValidation(loc.T("error.cost_invalid"))
	}
	return &f, nil
}

// parseMinutes parses "HH:MM" (or a bare minute count) into minutes from
// midnight, clamped to [0, 1440].
func parseMinutes(v string, fallback int) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return clampMinutes(fallback)
	}
	if h, m, ok := strings.Cut(v, ":"); ok {
		hi, err1 := strconv.Atoi(strings.TrimSpace(h))
		mi, err2 := strconv.Atoi(strings.TrimSpace(m))
		if err1 == nil && err2 == nil {
			return clampMinutes(hi*60 + mi)
		}
	}
	if n, err := strconv.Atoi(v); err == nil {
		return clampMinutes(n)
	}
	return clampMinutes(fallback)
}

func clampMinutes(m int) int {
	if m < 0 {
		return 0
	}
	if m > 24*60 {
		return 24 * 60
	}
	return m
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
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
	item, err := s.itemFromForm(r)
	if err != nil {
		s.formError(w, r, "#item-error", err.Error())
		return
	}
	item.VacationID = id
	if err := s.store.CreateItem(r.Context(), item); err != nil {
		s.serverError(w, r, err)
		return
	}

	hxTrigger(w, "itemsChanged")
	// A timed item is also mirrored onto the day's hour grid via an out-of-band swap.
	grid := ""
	if item.Day != nil && item.Timed() {
		grid = "#day-grid-" + item.Day.Format("2006-01-02")
	}
	s.fragment(w, r, "item_created", map[string]any{"Item": item, "Grid": grid})
}

// handleUpdateItem updates an item's time range (used by the planner's
// drag/resize) and optionally its title.
func (s *Server) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "itemID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	existing, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	existing.StartMin = parseMinutes(formStr(r, "start"), existing.StartMin)
	existing.EndMin = parseMinutes(formStr(r, "end"), existing.EndMin)
	if existing.EndMin <= existing.StartMin {
		existing.EndMin = existing.StartMin + 30
	}
	if t := strings.TrimSpace(formStr(r, "title")); t != "" && maxLen(t, 200) {
		existing.Title = t
	}
	if err := s.store.UpdateItem(r.Context(), existing); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleToggleVisited(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "itemID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	item, err := s.store.GetItem(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	item.Visited = !item.Visited
	if err := s.store.UpdateItem(r.Context(), item); err != nil {
		s.serverError(w, r, err)
		return
	}
	hxTrigger(w, "itemsChanged")
	s.fragment(w, r, "item_row", item)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "itemID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteItem(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	hxTrigger(w, "itemsChanged")
	w.WriteHeader(http.StatusOK)
}

type itemMarker struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Visited  bool    `json:"visited"`
}

type centerPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type itemsPayload struct {
	Center *centerPoint `json:"center,omitempty"`
	Items  []itemMarker `json:"items"`
}

// handleItemsJSON feeds the Leaflet map with marker data for items that have
// coordinates.
func (s *Server) handleItemsJSON(w http.ResponseWriter, r *http.Request) {
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
	items, err := s.store.ListItems(r.Context(), vacationID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	payload := itemsPayload{Items: make([]itemMarker, 0, len(items))}
	if v.HasCoords() {
		payload.Center = &centerPoint{Lat: *v.Latitude, Lng: *v.Longitude}
	}
	for _, it := range items {
		if !it.HasCoords() {
			continue
		}
		if payload.Center == nil {
			payload.Center = &centerPoint{Lat: *it.Latitude, Lng: *it.Longitude}
		}
		payload.Items = append(payload.Items, itemMarker{
			ID:       it.ID.String(),
			Title:    it.Title,
			Category: it.Category,
			Lat:      *it.Latitude,
			Lng:      *it.Longitude,
			Visited:  it.Visited,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.log.Error("encoding items json", "err", err)
	}
}
