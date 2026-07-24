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

	r.Get("/about", s.handleAbout)

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
	r.Post("/settings/people", s.handleCreatePerson)
	r.Delete("/settings/people/{personID}", s.handleDeletePerson)
	r.Post("/settings/optimize", s.handleOptimizeDB)
	r.Post("/settings/autovacuum", s.handleUpdateAutoVacuum)
	r.Delete("/settings/vacations/{vacationID}", s.handleDeleteVacationSettings)

	r.Route("/settings/backups", func(r chi.Router) {
		r.Post("/", s.handleCreateBackup)
		r.Post("/restore", s.handleRestoreBackup)
		r.Get("/{name}/download", s.handleDownloadBackup)
		r.Post("/{name}/restore", s.handleRestoreBackupNamed)
		r.Delete("/{name}", s.handleDeleteBackup)
	})

	r.Get("/api/geocode", s.handleGeocode)
	r.Get("/api/reverse-geocode", s.handleReverseGeocode)
	r.Get("/api/destination-image", s.handleDestinationImage)

	r.Route("/vacations", func(r chi.Router) {
		r.Get("/", s.handleIndex)
		r.Post("/", s.handleCreateVacation)
		r.Route("/{vacationID}", func(r chi.Router) {
			r.Get("/", s.handleVacationDetail)
			r.Post("/", s.handleUpdateVacation)
			r.Post("/notes", s.handleUpdateNotes)
			r.Delete("/", s.handleDeleteVacation)
			r.Get("/export", s.handleExport)
			r.Get("/export.pdf", s.handleExportPDF)
			r.Get("/export.ics", s.handleExportICS)
			r.Get("/api/items", s.handleItemsJSON)
			r.Get("/api/budget", s.handleBudgetFragment)
			r.Get("/api/overview", s.handleOverviewFragment)
			r.Get("/api/daycards", s.handleDayCards)
			r.Get("/api/ideas", s.handleIdeasFragment)
			r.Get("/api/destination-info", s.handleDestinationInfo)
			r.Post("/items", s.handleCreateItem)
			r.Post("/lodging", s.handleCreateLodging)
			r.Post("/travel", s.handleSaveTravel)
			r.Post("/travel/step", s.handleAddTravelStep)
			r.Post("/travel/multistop", s.handleToggleTravelMulti)
			r.Delete("/travel/{travelID}", s.handleRemoveTravelStep)
			r.Get("/traveldocs/{kind}/{step}", s.handleTravelDocuments)
			r.Post("/traveldocs/{kind}/{step}", s.handleUploadTravelDocument)
			r.Post("/ai/recommendations", s.handleAIRecommend)
		})
	})

	r.Route("/items/{itemID}", func(r chi.Router) {
		r.Get("/", s.handleItemRow)
		r.Get("/edit", s.handleEditItemForm)
		r.Get("/documents", s.handleItemDocuments)
		r.Post("/documents", s.handleUploadItemDocument)
		r.Post("/", s.handleUpdateItem)
		r.Post("/edit", s.handleEditItem)
		r.Post("/schedule", s.handleScheduleItem)
		r.Post("/origin", s.handleSetItemOrigin)
		r.Post("/visited", s.handleToggleVisited)
		r.Delete("/", s.handleDeleteItem)
	})

	r.Route("/lodging/{lodgingID}", func(r chi.Router) {
		r.Post("/", s.handleUpdateLodging)
		r.Delete("/", s.handleDeleteLodging)
		r.Get("/documents", s.handleLodgingDocuments)
		r.Post("/documents", s.handleUploadLodgingDocument)
	})

	r.Route("/documents/{docID}", func(r chi.Router) {
		r.Get("/", s.handleServeDocument)
		r.Delete("/", s.handleDeleteDocument)
	})

	s.router = r
}
