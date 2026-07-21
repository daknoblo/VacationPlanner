package server

import (
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/applog"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

const settingLogLevel = "log.level"

// logLine is a preformatted log record for the diagnostics view.
type logLine struct {
	Time    string
	Level   string
	Message string
	Attrs   string
}

// handleUpdateLogSettings changes the active log level and persists it so it
// survives restarts.
func (s *Server) handleUpdateLogSettings(w http.ResponseWriter, r *http.Request) {
	loc := i18n.FromContext(r.Context())
	lvl, ok := applog.ParseLevel(formStr(r, "level"))
	if !ok {
		s.formError(w, r, "#log-settings-error", loc.T("error.log_level_invalid"))
		return
	}
	s.logs.SetLevel(lvl)
	if err := s.store.PutSetting(r.Context(), settingLogLevel, s.logs.LevelName()); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.settingSaved(w, r)
}

// handleLogs renders the most recent log records for the auto-refreshing viewer.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	entries := s.logs.Recent(200)
	lines := make([]logLine, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, logLine{
			Time:    e.Time.Format("15:04:05"),
			Level:   e.Level,
			Message: e.Message,
			Attrs:   e.Attrs,
		})
	}
	s.fragment(w, r, "log_view", map[string]any{"Lines": lines})
}
