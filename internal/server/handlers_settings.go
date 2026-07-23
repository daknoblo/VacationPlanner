package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/applog"
	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/route"
)

const (
	settingAIBaseURL    = "ai.base_url"
	settingAIModel      = "ai.model"
	settingAIAPIVersion = "ai.api_version"
	settingWeekStart    = "region.week_start"
	settingTimezone     = "region.timezone"
	settingCurrency     = "region.currency"
	settingHomeAddress  = "home.address"
)

// supportedCurrencies are the currency symbols offered in Settings.
var supportedCurrencies = []string{"€", "$"}

// currencySymbol returns the configured budget currency symbol, defaulting to €.
func (s *Server) currencySymbol(ctx context.Context) string {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return "€"
	}
	return normalizeCurrency(settings[settingCurrency])
}

// normalizeCurrency validates a currency symbol, falling back to €.
func normalizeCurrency(v string) string {
	v = strings.TrimSpace(v)
	for _, c := range supportedCurrencies {
		if v == c {
			return v
		}
	}
	return "€"
}

// commonTimezones is a curated list of IANA zones offered in Settings.
var commonTimezones = []string{
	"UTC",
	"Europe/London", "Europe/Dublin", "Europe/Lisbon", "Europe/Madrid", "Europe/Paris",
	"Europe/Berlin", "Europe/Amsterdam", "Europe/Brussels", "Europe/Zurich", "Europe/Rome",
	"Europe/Vienna", "Europe/Prague", "Europe/Warsaw", "Europe/Athens", "Europe/Helsinki",
	"Europe/Istanbul", "Europe/Moscow",
	"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles",
	"America/Toronto", "America/Mexico_City", "America/Sao_Paulo",
	"America/Argentina/Buenos_Aires",
	"Africa/Cairo", "Africa/Johannesburg", "Africa/Lagos", "Africa/Nairobi",
	"Asia/Dubai", "Asia/Jerusalem", "Asia/Kolkata", "Asia/Bangkok", "Asia/Singapore",
	"Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo", "Asia/Seoul",
	"Australia/Perth", "Australia/Sydney", "Pacific/Auckland", "Pacific/Honolulu",
}

// aiSettings returns the effective AI endpoint URL and model, falling back to
// the package defaults when nothing is configured.
func (s *Server) aiSettings(ctx context.Context) (baseURL, model, apiVersion string) {
	baseURL, model = ai.DefaultBaseURL, ai.DefaultModel
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.log.Warn("loading settings", "err", err)
		return baseURL, model, apiVersion
	}
	if v := strings.TrimSpace(settings[settingAIBaseURL]); v != "" {
		baseURL = v
	}
	if v := strings.TrimSpace(settings[settingAIModel]); v != "" {
		model = v
	}
	apiVersion = strings.TrimSpace(settings[settingAIAPIVersion])
	return baseURL, model, apiVersion
}

// regionSettings returns the configured week start and timezone, defaulting to
// Monday and UTC when unset or invalid.
func (s *Server) regionSettings(ctx context.Context) (weekStart string, loc *time.Location) {
	weekStart, loc = "monday", time.UTC
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.log.Warn("loading settings", "err", err)
		return weekStart, loc
	}
	if v := strings.TrimSpace(settings[settingWeekStart]); v == "sunday" || v == "monday" {
		weekStart = v
	}
	if v := strings.TrimSpace(settings[settingTimezone]); v != "" {
		if l, err := time.LoadLocation(v); err == nil {
			loc = l
		}
	}
	return weekStart, loc
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	weekStart := "monday"
	if v := strings.TrimSpace(settings[settingWeekStart]); v == "sunday" || v == "monday" {
		weekStart = v
	}
	timezone := "UTC"
	if v := strings.TrimSpace(settings[settingTimezone]); v != "" {
		timezone = v
	}
	stats, _ := s.store.Stats(r.Context())
	categories, _ := s.store.ListCategories(r.Context())
	vacations, _ := s.store.ListVacations(r.Context())
	s.page(w, r, "settings", loc.T("page.settings.title"), map[string]any{
		"Languages":        i18n.Supported(),
		"Current":          loc.Lang(),
		"AIBaseURL":        settings[settingAIBaseURL],
		"AIModel":          settings[settingAIModel],
		"AIAPIVersion":     settings[settingAIAPIVersion],
		"AIDefaultBaseURL": ai.DefaultBaseURL,
		"AIDefaultModel":   ai.DefaultModel,
		"AIKeyConfigured":  s.ai.Enabled(),
		"WeekStart":        weekStart,
		"Timezone":         timezone,
		"Currency":         normalizeCurrency(settings[settingCurrency]),
		"Currencies":       supportedCurrencies,
		"Timezones":        commonTimezones, "HomeAddress": settings[settingHomeAddress], "GeoBaseURL": settings[settingGeoBaseURL],
		"GeoDefaultBaseURL":   geo.DefaultBaseURL,
		"GeoKeyConfigured":    s.cfg.GeocoderAPIKey != "",
		"RouteBaseURL":        settings[settingRouteBaseURL],
		"RouteDefaultBaseURL": route.DefaultBaseURL,
		"RouteKeyConfigured":  s.cfg.RouterAPIKey != "",
		"LogLevel":            s.logs.LevelName(),
		"LogLevels":           applog.Levels(),
		"Categories":          categories,
		"CategoryIcons":       defaultCategoryIcons,
		"Stats":               stats,
		"DBSize":              humanBytes(s.dbSizeBytes()),
		"Backups":             s.listBackups(),
		"AutoVacuum":          autoVacuumSetting(settings),
		"AutoVacuumOptions":   autoVacuumOptions,
		"Vacations":           vacations,
	})
}

// handleUpdateSettings stores the UI language (cookie) plus the week start and
// timezone (DB). It is auto-saved from the combined language/region form.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	lang, ok := i18n.ParseLang(r.FormValue("lang"))
	if !ok {
		lang = i18n.DefaultLang
	}
	i18n.SetLangCookie(w, lang, s.cfg.IsProduction())

	weekStart := formStr(r, "week_start")
	if weekStart != "sunday" && weekStart != "monday" {
		weekStart = "monday"
	}
	if err := s.store.PutSetting(r.Context(), settingWeekStart, weekStart); err != nil {
		s.serverError(w, r, err)
		return
	}
	timezone := formStr(r, "timezone")
	if _, err := time.LoadLocation(timezone); err != nil {
		timezone = "UTC"
	}
	if err := s.store.PutSetting(r.Context(), settingTimezone, timezone); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.PutSetting(r.Context(), settingCurrency, normalizeCurrency(formStr(r, "currency"))); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

// handleUpdateAISettings persists the OpenAI-compatible endpoint URL, model and
// optional API version. The API key itself is never stored here; it comes from
// VP_API_KEY.
func (s *Server) handleUpdateAISettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())

	baseURL := formStr(r, "base_url")
	model := formStr(r, "model")
	apiVersion := formStr(r, "api_version")
	if !maxLen(baseURL, 500) || !maxLen(model, 200) || !maxLen(apiVersion, 100) {
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
	if err := s.store.PutSetting(r.Context(), settingAIAPIVersion, apiVersion); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}

// homeAddress returns the configured home address (empty if unset).
func (s *Server) homeAddress(ctx context.Context) string {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(settings[settingHomeAddress])
}

// handleUpdateHomeSettings persists the user's home address, offered as a quick
// fill when planning arrival/departure.
func (s *Server) handleUpdateHomeSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	addr := formStr(r, "address")
	if !maxLen(addr, 200) {
		s.formError(w, r, "#home-settings-error", loc.T("error.input_toolong"))
		return
	}
	if err := s.store.PutSetting(r.Context(), settingHomeAddress, addr); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}

func (s *Server) redirectSettings(w http.ResponseWriter, r *http.Request) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/settings")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// settingSaved acknowledges an auto-saved setting: HTMX clients get a "saved"
// toast and no reload; plain form posts fall back to a redirect.
func (s *Server) settingSaved(w http.ResponseWriter, r *http.Request) {
	if isHTMX(r) {
		hxTrigger(w, "saved")
		w.WriteHeader(http.StatusNoContent)
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
