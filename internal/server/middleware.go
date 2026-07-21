package server

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

// securityHeaders applies a strict, self-hosted-friendly set of HTTP security
// headers, including a Content-Security-Policy that only permits same-origin
// scripts/styles plus OpenStreetMap map tiles.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"base-uri 'self'; " +
		"frame-ancestors 'none'; " +
		"form-action 'self'; " +
		"object-src 'none'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: https://tile.openstreetmap.org https://*.tile.openstreetmap.org; " +
		"connect-src 'self'; " +
		"font-src 'self'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// OpenStreetMap's tile servers require a Referer (they answer 403 otherwise);
		// send only the origin cross-origin to keep this privacy-friendly.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(self), camera=(), microphone=()")
		if s.cfg.IsProduction() {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimit caps the size of request bodies to protect against abuse.
func (s *Server) bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := s.cfg.MaxRequestBytes
		// Database restore uploads a full SQLite file, which legitimately exceeds
		// the default request body limit.
		if r.Method == http.MethodPost && r.URL.Path == "/settings/backups/restore" {
			limit = maxRestoreUploadBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// requestLogger emits a structured log line per request.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		s.log.Info("http request",
			"method", sanitizeLog(r.Method),
			"path", sanitizeLog(r.URL.Path),
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", middleware.GetReqID(r.Context()),
			"remote_ip", clientIP(r),
		)
	})
}

// staticHandler serves embedded assets with long-lived cache headers.
func staticHandler(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fileServer.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// localize resolves the request language and stores a Localizer in the context.
func (s *Server) localize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loc := i18n.FromRequest(r)
		next.ServeHTTP(w, r.WithContext(i18n.NewContext(r.Context(), loc)))
	})
}
