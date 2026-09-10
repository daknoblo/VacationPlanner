package server

import (
	"context"
	"net"
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
	settings, err := s.settings(ctx)
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
func (s *Server) aiSettings(ctx context.Context) (baseURL, model, apiVersion string, err error) {
	baseURL, model = ai.DefaultBaseURL, ai.DefaultModel
	settings, err := s.settings(ctx)
	if err != nil {
		return "", "", "", err
	}
	if s.foundry != nil {
		return "", s.foundryDeployment(settings), "", nil
	}
	if v := strings.TrimSpace(settings[settingAIBaseURL]); v != "" {
		baseURL = v
	}
	if v := strings.TrimSpace(settings[settingAIModel]); v != "" {
		model = v
	}
	apiVersion = strings.TrimSpace(settings[settingAIAPIVersion])
	baseURL, model, apiVersion = s.aiOverrides(baseURL, model, apiVersion)
	return baseURL, model, apiVersion, nil
}

func (s *Server) aiOverrides(baseURL, model, apiVersion string) (string, string, string) {
	if s.cfg.Azure.Endpoint != "" {
		baseURL = s.cfg.Azure.Endpoint
	}
	if s.cfg.Azure.Deployment != "" {
		model = s.cfg.Azure.Deployment
	}
	if s.cfg.Azure.APIVersion != "" {
		apiVersion = s.cfg.Azure.APIVersion
	}
	return baseURL, model, apiVersion
}

// regionSettings returns the configured week start and timezone, defaulting to
// Monday and UTC when unset or invalid.
func (s *Server) regionSettings(ctx context.Context) (weekStart string, loc *time.Location) {
	weekStart, loc = "monday", time.UTC
	settings, err := s.settings(ctx)
	if err != nil {
		s.log.Warn("loading settings", "err", err)
		return weekStart, loc
	}
	if v := strings.TrimSpace(settings[settingWeekStart]); v == "sunday" || v == "monday" {
		weekStart = v
	}
	if v := strings.TrimSpace(settings[settingTimezone]); v != "" {
		if l, err := loadLocation(v); err == nil {
			loc = l
		}
	}
	return weekStart, loc
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	settings, err := s.settings(r.Context())
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
	people, _ := s.store.ListPeople(r.Context())
	vacations, _ := s.store.ListVacations(r.Context())
	baseURL, model, apiVersion := s.aiOverrides(settings[settingAIBaseURL], settings[settingAIModel], settings[settingAIAPIVersion])
	foundryView, err := s.foundrySettings(r.Context(), settings)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, "settings", loc.T("page.settings.title"), map[string]any{
		"Languages":        i18n.Supported(),
		"Current":          loc.Lang(),
		"AIBaseURL":        baseURL,
		"AIModel":          model,
		"AIAPIVersion":     apiVersion,
		"AIEndpointLocked": s.cfg.Azure.Endpoint != "",
		"AIModelLocked":    s.cfg.Azure.Deployment != "",
		"AIVersionLocked":  s.cfg.Azure.APIVersion != "",
		"Foundry":          foundryView,
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
		"People":              people,
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
	if err := s.putSetting(r.Context(), settingWeekStart, weekStart); err != nil {
		s.serverError(w, r, err)
		return
	}
	timezone := formStr(r, "timezone")
	if _, err := loadLocation(timezone); err != nil {
		timezone = "UTC"
	}
	if err := s.putSetting(r.Context(), settingTimezone, timezone); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.putSetting(r.Context(), settingCurrency, normalizeCurrency(formStr(r, "currency"))); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.redirectSettings(w, r)
}

// handleUpdateAISettings persists the OpenAI-compatible endpoint URL, model and
// optional API version. The API key itself is never stored here; it comes from
// VP_API_KEY.
func (s *Server) handleUpdateAISettings(w http.ResponseWriter, r *http.Request) {
	if s.foundry != nil {
		s.handleUpdateFoundrySettings(w, r)
		return
	}
	loc := i18n.FromContext(r.Context())

	baseURL := formStr(r, "base_url")
	model := formStr(r, "model")
	apiVersion := formStr(r, "api_version")
	if !maxLen(baseURL, 500) || !maxLen(model, 200) || !maxLen(apiVersion, 100) {
		s.formError(w, r, "#ai-settings-error", loc.T("error.input_toolong"))
		return
	}
	if baseURL != "" && !validAIBaseURL(baseURL) {
		s.formError(w, r, "#ai-settings-error", loc.T("error.ai_base_url_invalid"))
		return
	}

	if err := s.saveUnlockedAISetting(r.Context(), settingAIBaseURL, baseURL, s.cfg.Azure.Endpoint); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.saveUnlockedAISetting(r.Context(), settingAIModel, model, s.cfg.Azure.Deployment); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.saveUnlockedAISetting(r.Context(), settingAIAPIVersion, apiVersion, s.cfg.Azure.APIVersion); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}

func (s *Server) saveUnlockedAISetting(ctx context.Context, key, value, override string) error {
	if override != "" {
		return nil
	}
	return s.putSetting(ctx, key, value)
}

func validAIBaseURL(raw string) bool {
	if !validBaseURL(raw) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

// homeAddress returns the configured home address (empty if unset).
func (s *Server) homeAddress(ctx context.Context) string {
	settings, err := s.settings(ctx)
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
	if err := s.putSetting(r.Context(), settingHomeAddress, addr); err != nil {
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

// validBaseURL reports whether a user-configured service endpoint is usable.
// Loopback and private addresses stay allowed on purpose — self-hosted AI,
// geocoding and routing services (Ollama, LocalAI, a local Nominatim) are a
// primary use case. Link-local, unspecified and multicast addresses are
// rejected because they are never a legitimate endpoint but do cover the cloud
// instance metadata services (169.254.169.254, fd00:ec2::254).
func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return false
		}
	}
	return true
}
