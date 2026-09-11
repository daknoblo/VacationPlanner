package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

type backgroundStatusView struct {
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
	loc := i18n.FromContext(ctx)
	var tasks []string
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
	view.Active = len(tasks) > 0
	view.Determinate = geoJobs == 1 && view.Total > 0 && translations == 0 && discovery == 0
	view.Detail = strings.Join(tasks, " · ")
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
