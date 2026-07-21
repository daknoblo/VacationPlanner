package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

// activityFromForm builds an Activity from the posted form.
func (s *Server) activityFromForm(r *http.Request) (*models.Activity, error) {
	loc := i18n.FromContext(r.Context())

	title := strings.TrimSpace(formStr(r, "title"))
	if title == "" || !maxLen(title, 200) {
		return nil, errValidation(loc.T("error.activity_title_required"))
	}
	day, err := time.Parse("2006-01-02", formStr(r, "day"))
	if err != nil {
		return nil, errValidation(loc.T("error.activity_day_invalid"))
	}
	start := parseMinutes(formStr(r, "start"), 540)
	end := parseMinutes(formStr(r, "end"), start+60)
	if end <= start {
		end = start + 30
	}
	return &models.Activity{
		Day:         day,
		Title:       title,
		Category:    strings.TrimSpace(formStr(r, "category")),
		StartMin:    start,
		EndMin:      end,
		Description: strings.TrimSpace(formStr(r, "description")),
		Location:    strings.TrimSpace(formStr(r, "location")),
	}, nil
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

func (s *Server) handleCreateActivity(w http.ResponseWriter, r *http.Request) {
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
	a, err := s.activityFromForm(r)
	if err != nil {
		s.formError(w, r, "#activity-error", err.Error())
		return
	}
	a.VacationID = id
	if err := s.store.CreateActivity(r.Context(), a); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.fragment(w, r, "planner_block", a)
}

// handleUpdateActivity updates an activity's time range (used by the planner's
// drag/resize) and optionally its title.
func (s *Server) handleUpdateActivity(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "activityID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	existing, err := s.store.GetActivity(r.Context(), id)
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
	if err := s.store.UpdateActivity(r.Context(), existing); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteActivity(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "activityID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteActivity(r.Context(), id); err != nil && !isNotFound(err) {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleActivitySuggest returns AI activity suggestions for the destination.
// It degrades gracefully to an empty list when AI is disabled.
func (s *Server) handleActivitySuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	dest := strings.TrimSpace(r.URL.Query().Get("dest"))
	results := []ai.ActivitySuggestion{}
	if len([]rune(q)) >= 2 && s.ai.Enabled() {
		baseURL, model, apiVersion := s.aiSettings(r.Context())
		if found, err := s.ai.SuggestActivities(r.Context(), baseURL, model, apiVersion, dest, q); err == nil {
			results = found
		} else {
			s.log.Warn("activity suggest failed", "err", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}
