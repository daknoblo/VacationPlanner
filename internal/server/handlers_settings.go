package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

const (
	settingAIBaseURL = "ai.base_url"
	settingAIModel   = "ai.model"
)

// aiSettings returns the effective AI endpoint URL and model, falling back to
// the package defaults when nothing is configured.
func (s *Server) aiSettings(ctx context.Context) (baseURL, model string) {
	baseURL, model = ai.DefaultBaseURL, ai.DefaultModel
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.log.Warn("loading settings", "err", err)
		return baseURL, model
	}
	if v := strings.TrimSpace(settings[settingAIBaseURL]); v != "" {
		baseURL = v
	}
	if v := strings.TrimSpace(settings[settingAIModel]); v != "" {
		model = v
	}
	return baseURL, model
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, "settings", loc.T("page.settings.title"), map[string]any{
		"Languages":        i18n.Supported(),
		"Current":          loc.Lang(),
		"AIBaseURL":        settings[settingAIBaseURL],
		"AIModel":          settings[settingAIModel],
		"AIDefaultBaseURL": ai.DefaultBaseURL,
		"AIDefaultModel":   ai.DefaultModel,
		"AIKeyConfigured":  s.ai.Enabled(),
	})
}

// handleUpdateSettings stores the UI language preference in the lang cookie.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	lang, ok := i18n.ParseLang(r.FormValue("lang"))
	if !ok {
		lang = i18n.DefaultLang
	}
	i18n.SetLangCookie(w, lang, s.cfg.IsProduction())
	s.redirectSettings(w, r)
}

// handleUpdateAISettings persists the OpenAI-compatible endpoint URL and model.
// The API key itself is never stored here; it comes from OPENAI_API_KEY.
func (s *Server) handleUpdateAISettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())

	baseURL := formStr(r, "base_url")
	model := formStr(r, "model")
	if !maxLen(baseURL, 500) || !maxLen(model, 200) {
		s.formError(w, r, "#ai-settings-error", loc.T("error.input_toolong"))
		return
	}
	if baseURL != "" && !validBaseURL(baseURL) {
		s.formError(w, r, "#ai-settings-error", loc.T("error.ai_base_url_invalid"))
		return
	}

	if err := s.store.PutSetting(r.Context(), settingAIBaseURL, baseURL); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.PutSetting(r.Context(), settingAIModel, model); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

func (s *Server) redirectSettings(w http.ResponseWriter, r *http.Request) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/settings")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
