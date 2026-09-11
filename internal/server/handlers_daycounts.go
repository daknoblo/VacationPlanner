package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func plannedDayCounts(days []time.Time, items []models.Item) map[string]int {
	counts := make(map[string]int, len(days))
	for _, day := range days {
		counts[day.Format("2006-01-02")] = 0
	}
	for _, item := range items {
		if item.Day == nil {
			continue
		}
		key := item.Day.Format("2006-01-02")
		if _, inTrip := counts[key]; inTrip {
			counts[key]++
		}
	}
	return counts
}

func (s *Server) handleDayCounts(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	items, err := s.store.ListItems(r.Context(), id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(plannedDayCounts(v.Days(), items)); err != nil {
		s.log.Error("encoding planned activity counts", "err", err)
	}
}
