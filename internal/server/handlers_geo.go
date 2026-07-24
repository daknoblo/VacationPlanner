package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

const settingGeoBaseURL = "geo.base_url"

// queryFloat reads a float query parameter, returning 0 when absent or invalid.
func queryFloat(r *http.Request, key string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get(key)), 64)
	if err != nil {
		return 0
	}
	return f
}

// geoBaseURL returns the configured geocoder base URL or the built-in default.
func (s *Server) geoBaseURL(ctx context.Context) string {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return geo.DefaultBaseURL
	}
	if v := strings.TrimSpace(settings[settingGeoBaseURL]); v != "" {
		return v
	}
	return geo.DefaultBaseURL
}

// handleGeocode proxies forward-geocoding to the configured provider so the
// browser never calls a third party directly. This keeps the strict CSP intact
// and any API key server-side.
func (s *Server) handleGeocode(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 200 {
		q = string([]rune(q)[:200])
	}
	results := []geo.Result{}
	if len([]rune(q)) >= 2 {
		loc := i18n.FromContext(r.Context())
		found, err := s.geo.Search(r.Context(), s.geoBaseURL(r.Context()), q, loc.Code(), 5, queryFloat(r, "lat"), queryFloat(r, "lon"))
		if err != nil {
			s.log.Warn("geocode failed", "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "error": true})
			return
		}
		results = found
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// handleReverseGeocode resolves a clicked map point to a place label via the
// configured provider (server-side, keeping the strict CSP and any API key
// off the client). Used by the AI search-center picker.
func (s *Server) handleReverseGeocode(w http.ResponseWriter, r *http.Request) {
	lat := queryFloat(r, "lat")
	lon := queryFloat(r, "lon")
	out := map[string]any{}
	if (lat != 0 || lon != 0) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
		loc := i18n.FromContext(r.Context())
		res, ok, err := s.geo.Reverse(r.Context(), s.geoBaseURL(r.Context()), lat, lon, loc.Code())
		if err != nil {
			s.log.Warn("reverse geocode failed", "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": true})
			return
		}
		if ok {
			out["display_name"] = res.DisplayName
			out["lat"] = res.Lat
			out["lng"] = res.Lng
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleUpdateGeoSettings persists the geocoder base URL. The optional API key
// is never stored here; it comes from GEOCODER_API_KEY.
func (s *Server) handleUpdateGeoSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	baseURL := formStr(r, "base_url")
	if !maxLen(baseURL, 500) {
		s.formError(w, r, "#geo-settings-error", loc.T("error.input_toolong"))
		return
	}
	if baseURL != "" && !validBaseURL(baseURL) {
		s.formError(w, r, "#geo-settings-error", loc.T("error.ai_base_url_invalid"))
		return
	}
	if err := s.store.PutSetting(r.Context(), settingGeoBaseURL, baseURL); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}
