package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	s.page(w, r, "settings", loc.T("page.settings.title"), map[string]any{
		"Languages": i18n.Supported(),
		"Current":   loc.Lang(),
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	lang, ok := i18n.ParseLang(r.FormValue("lang"))
	if !ok {
		lang = i18n.DefaultLang
	}
	i18n.SetLangCookie(w, lang, s.cfg.IsProduction())

	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/settings")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
