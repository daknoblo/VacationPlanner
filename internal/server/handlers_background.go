package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

type backgroundStatusView struct {
	Checking    bool
	Active      bool
	Determinate bool
	Completed   int
	Total       int
	Detail      string
	Error       bool
}

// backgroundStatus reads local worker state only. In particular, polling must
// never enqueue geography work, refresh ARM metadata or invoke a model.
func (s *Server) backgroundStatus(ctx context.Context) (backgroundStatusView, error) {
	var view backgroundStatusView
	var geoJobs int
	if s.geography != nil {
		s.geography.mu.Lock()
		for _, state := range s.geography.states {
			if state.status.Pending {
				geoJobs++
				view.Completed += state.status.Completed
				view.Total += state.status.Total
			}
		}
		s.geography.mu.Unlock()
	}
	translations, err := s.store.CountPendingCheatsheetJobs(ctx)
	if err != nil {
		return view, err
	}
	discovery := s.aiDiscoveries.Load()
	var descriptions int
	if s.ai != nil && s.ai.Enabled() {
		settings, err := s.settings(ctx)
		if err != nil {
			return view, err
		}
		if s.foundryDeployment(settings) != "" {
			descriptions, err = s.store.CountPendingIdeaDescriptions(ctx)
			if err != nil {
				return view, err
			}
		}
	}
	var routePending, routeCompleted, routeTotal int
	if s.routing != nil && s.routing.Enabled() {
		settings, err := s.store.GetSettings(ctx)
		if err != nil {
			return view, err
		}
		progress, err := s.store.IdeaRouteProgress(ctx, settings[settingRouteBaseURL], uuid.Nil)
		if err != nil {
			return view, err
		}
		routeCompleted, routeTotal = progress.Completed, progress.Total
		routePending = routeTotal - routeCompleted
	}
	loc := i18n.FromContext(ctx)
	weatherPending := 0
	if s.weatherEnabled() {
		caches, err := s.store.ListWeather(ctx)
		if err != nil {
			return view, err
		}
		for _, c := range caches {
			if c.Status == "queued" || c.Status == "running" {
				weatherPending++
			}
		}
		settings, err := s.settings(ctx)
		if err != nil {
			return view, err
		}
		if raw := settings[settingWeatherBlockedUntil]; raw != "" {
			until, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return view, err
			}
			if time.Now().Before(until) {
				weatherPending = 0
			}
		}
	}
	var tasks []string
	if weatherPending > 0 {
		tasks = append(tasks, loc.T("background.weather", weatherPending))
	}
	if geoJobs > 0 {
		if geoJobs == 1 && view.Total > 0 {
			tasks = append(tasks, loc.T("background.geography_progress", view.Completed, view.Total))
		} else {
			tasks = append(tasks, loc.T("background.geography", geoJobs))
		}
	}
	if translations > 0 {
		tasks = append(tasks, loc.T("background.translations", translations))
	}
	if discovery > 0 {
		tasks = append(tasks, loc.T("background.discovery"))
	}
	if descriptions > 0 {
		tasks = append(tasks, loc.T("background.descriptions", descriptions))
	}
	if routePending > 0 {
		tasks = append(tasks, loc.T("background.routes", routeCompleted, routeTotal))
	}
	view.Active = len(tasks) > 0
	view.Determinate = geoJobs == 1 && view.Total > 0 && translations == 0 && discovery == 0 && routePending == 0 && descriptions == 0
	if routePending > 0 && geoJobs == 0 && translations == 0 && discovery == 0 && descriptions == 0 {
		view.Determinate, view.Completed, view.Total = true, routeCompleted, routeTotal
	}
	view.Detail = strings.Join(tasks, " · ")
	if weatherPending > 0 {
		view.Determinate = false
	}
	return view, nil
}

func (s *Server) handleBackgroundStatus(w http.ResponseWriter, r *http.Request) {
	view, err := s.backgroundStatus(r.Context())
	if err != nil {
		s.log.Warn("background status unavailable", "err", err)
		view = backgroundStatusView{Error: true}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.fragment(w, r, "background_status", view)
}
