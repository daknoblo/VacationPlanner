package server

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/daknoblo/vacationplanner/web"
)

func (s *Server) routes() {
	r := chi.NewRouter()

	// Cross-cutting middleware (order matters).
	r.Use(middleware.RequestID)
	r.Use(s.requestLogger)
	r.Use(middleware.Recoverer)
	r.Use(s.securityHeaders)
	r.Use(middleware.Timeout(s.cfg.RequestTimeout))
	r.Use(s.bodyLimit)
	r.Use(s.rateLimit)
	r.Use(s.csrf)

	// Static assets (embedded).
	staticFS, err := fs.Sub(web.Static, "static")
	if err == nil {
		r.Handle("/static/*", http.StripPrefix("/static/", staticHandler(http.FS(staticFS))))
	}

	// Health/observability.
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	// Pages & actions.
	r.Get("/", s.handleIndex)

	r.Route("/vacations", func(r chi.Router) {
		r.Post("/", s.handleCreateVacation)
		r.Route("/{vacationID}", func(r chi.Router) {
			r.Get("/", s.handleVacationDetail)
			r.Post("/", s.handleUpdateVacation)
			r.Delete("/", s.handleDeleteVacation)
			r.Get("/api/sights", s.handleSightsJSON)
			r.Post("/sights", s.handleCreateSight)
			r.Post("/travel", s.handleCreateTravel)
			r.Post("/ai/recommendations", s.handleAIRecommend)
		})
	})

	r.Route("/sights/{sightID}", func(r chi.Router) {
		r.Delete("/", s.handleDeleteSight)
		r.Post("/visited", s.handleToggleVisited)
	})

	r.Route("/travel/{travelID}", func(r chi.Router) {
		r.Delete("/", s.handleDeleteTravel)
	})

	s.router = r
}
