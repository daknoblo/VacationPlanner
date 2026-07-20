// Package server wires together HTTP routing, rendering and the domain stores.
package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/config"
	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/store"
)

// Server is the top-level HTTP application.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	store   store.Store
	ai      *ai.Client
	geo     *geo.Client
	render  *renderer
	limiter *ipRateLimiter
	router  chi.Router
}

// New constructs a Server and wires up all routes.
func New(cfg *config.Config, log *slog.Logger, st store.Store, aiClient *ai.Client) (*Server, error) {
	r, err := newRenderer()
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:     cfg,
		log:     log,
		store:   st,
		ai:      aiClient,
		geo:     geo.New(cfg.GeocoderAPIKey),
		render:  r,
		limiter: newIPRateLimiter(120, 300), // ~120 req/min sustained, burst 300
	}
	s.routes()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}
