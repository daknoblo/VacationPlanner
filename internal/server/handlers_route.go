package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/route"
)

const settingRouteBaseURL = "route.base_url"

// routeBaseURL returns the configured routing base URL or the built-in default.
func (s *Server) routeBaseURL(ctx context.Context) string {
	settings, err := s.settings(ctx)
	if err != nil {
		return route.DefaultBaseURL
	}
	if v := strings.TrimSpace(settings[settingRouteBaseURL]); v != "" {
		return v
	}
	return route.DefaultBaseURL
}

// handleUpdateRouteSettings persists the routing base URL. The optional API key
// is never stored here; it comes from ROUTER_API_KEY.
func (s *Server) handleUpdateRouteSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	baseURL := formStr(r, "base_url")
	if !maxLen(baseURL, 500) {
		s.formError(w, r, "#route-settings-error", loc.T("error.input_toolong"))
		return
	}
	if baseURL != "" && !validBaseURL(baseURL) {
		s.formError(w, r, "#route-settings-error", loc.T("error.ai_base_url_invalid"))
		return
	}
	if err := s.putSetting(r.Context(), settingRouteBaseURL, baseURL); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}

func (s *Server) handleRetryRouteSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	id, err := uuid.Parse(formStr(r, "vacation_id"))
	if err != nil {
		s.formError(w, r, "#route-retry-status", loc.T("settings.route.retry_choose"))
		return
	}
	vacation, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.formError(w, r, "#route-retry-status", loc.T("settings.route.retry_choose"))
		} else {
			s.serverError(w, r, err)
		}
		return
	}
	routing := s.routing != nil && s.routing.Enabled()
	if routing {
		if err := s.store.RetryIdeaRoutes(r.Context(), id); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.retryGeography(id, loc.Code())
	if status := s.geographyStatus(id); !status.Pending && status.Error {
		s.log.Error("settings location refresh could not be queued", "vacation_id", id)
		http.Error(w, loc.T("planner.regions.lookup_error"), http.StatusServiceUnavailable)
		return
	}
	if !isHTMX(r) {
		http.Redirect(w, r, "/settings#settings-route-retry", http.StatusSeeOther)
		return
	}
	message := "settings.route.retry_queued"
	if !routing {
		message = "settings.route.retry_locations_only"
	}
	s.fragment(w, r, "route_retry_result", loc.T(message, vacation.Title))
}

// categoryIcons maps lower-cased category names to their emoji icon.
func (s *Server) categoryIcons(ctx context.Context) map[string]string {
	out := make(map[string]string)
	cats, err := s.store.ListCategories(ctx)
	if err != nil {
		return out
	}
	for _, c := range cats {
		if c.Icon != "" {
			out[strings.ToLower(c.Name)] = c.Icon
		}
	}
	return out
}

func formatDistance(m float64) string {
	if m < 1000 {
		return fmt.Sprintf("%.0f m", m)
	}
	return fmt.Sprintf("%.1f km", m/1000)
}

func formatDuration(sec float64) string {
	mins := int(math.Round(sec / 60))
	if mins < 60 {
		return fmt.Sprintf("%d min", mins)
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}
