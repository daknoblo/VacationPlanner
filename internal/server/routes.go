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
	r.Use(s.localize)

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

	r.Get("/settings", s.handleSettings)
	r.Post("/settings", s.handleUpdateSettings)
	r.Post("/settings/ai", s.handleUpdateAISettings)
	r.Post("/settings/region", s.handleUpdateRegionSettings)
	r.Post("/settings/geo", s.handleUpdateGeoSettings)

	r.Get("/api/geocode", s.handleGeocode)
	r.Get("/api/activities/suggest", s.handleActivitySuggest)

	r.Route("/vacations", func(r chi.Router) {
		r.Get("/", s.handleIndex)
		r.Post("/", s.handleCreateVacation)
		r.Route("/{vacationID}", func(r chi.Router) {
			r.Get("/", s.handleVacationDetail)
			r.Post("/", s.handleUpdateVacation)
			r.Delete("/", s.handleDeleteVacation)
			r.Get("/export", s.handleExport)
			r.Get("/export.pdf", s.handleExportPDF)
			r.Get("/api/sights", s.handleSightsJSON)
			r.Post("/sights", s.handleCreateSight)
			r.Post("/travel", s.handleCreateTravel)
			r.Post("/activities", s.handleCreateActivity)
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

	r.Route("/activities/{activityID}", func(r chi.Router) {
		r.Post("/", s.handleUpdateActivity)
		r.Delete("/", s.handleDeleteActivity)
	})

	s.router = r
}
