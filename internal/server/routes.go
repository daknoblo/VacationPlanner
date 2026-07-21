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
	r.Post("/settings/geo", s.handleUpdateGeoSettings)
	r.Post("/settings/route", s.handleUpdateRouteSettings)
	r.Post("/settings/home", s.handleUpdateHomeSettings)
	r.Post("/settings/log", s.handleUpdateLogSettings)
	r.Get("/settings/logs", s.handleLogs)
	r.Post("/settings/categories", s.handleCreateCategory)
	r.Delete("/settings/categories/{categoryID}", s.handleDeleteCategory)

	r.Route("/settings/backups", func(r chi.Router) {
		r.Post("/", s.handleCreateBackup)
		r.Post("/restore", s.handleRestoreBackup)
		r.Get("/{name}/download", s.handleDownloadBackup)
		r.Post("/{name}/restore", s.handleRestoreBackupNamed)
		r.Delete("/{name}", s.handleDeleteBackup)
	})

	r.Get("/api/geocode", s.handleGeocode)
	r.Get("/api/destination-image", s.handleDestinationImage)

	r.Route("/vacations", func(r chi.Router) {
		r.Get("/", s.handleIndex)
		r.Post("/", s.handleCreateVacation)
		r.Route("/{vacationID}", func(r chi.Router) {
			r.Get("/", s.handleVacationDetail)
			r.Post("/", s.handleUpdateVacation)
			r.Delete("/", s.handleDeleteVacation)
			r.Get("/export", s.handleExport)
			r.Get("/export.pdf", s.handleExportPDF)
			r.Get("/api/items", s.handleItemsJSON)
			r.Get("/api/day-summary", s.handleDaySummary)
			r.Get("/api/budget", s.handleBudgetFragment)
			r.Get("/api/overview", s.handleOverviewFragment)
			r.Get("/api/ideas", s.handleIdeasFragment)
			r.Get("/api/destination-info", s.handleDestinationInfo)
			r.Post("/items", s.handleCreateItem)
			r.Post("/travel", s.handleSaveTravel)
			r.Post("/ai/recommendations", s.handleAIRecommend)
		})
	})

	r.Route("/items/{itemID}", func(r chi.Router) {
		r.Get("/", s.handleItemRow)
		r.Get("/edit", s.handleEditItemForm)
		r.Post("/", s.handleUpdateItem)
		r.Post("/edit", s.handleEditItem)
		r.Post("/schedule", s.handleScheduleItem)
		r.Post("/visited", s.handleToggleVisited)
		r.Delete("/", s.handleDeleteItem)
	})

	r.Route("/travel/{travelID}", func(r chi.Router) {
		r.Delete("/", s.handleDeleteTravel)
	})

	s.router = r
}
