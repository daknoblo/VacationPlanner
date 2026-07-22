package server

import (
	"context"
	"net/http"
	"time"
)

const (
	settingAutoVacuum     = "db.autovacuum"
	settingAutoVacuumLast = "db.autovacuum.last"
	// maintenanceInterval is how often the background loop checks whether an
	// automatic vacuum is due. The configurable cadences are all whole days, so
	// an hourly check is more than fine.
	maintenanceInterval = time.Hour
)

// autoVacuumIntervals maps each selectable cadence to its duration.
var autoVacuumIntervals = map[string]time.Duration{
	"daily":    24 * time.Hour,
	"3days":    3 * 24 * time.Hour,
	"weekly":   7 * 24 * time.Hour,
	"biweekly": 14 * 24 * time.Hour,
	"monthly":  30 * 24 * time.Hour,
}

// autoVacuumOptions lists the cadence values in display order ("off" disables it).
var autoVacuumOptions = []string{"off", "daily", "3days", "weekly", "biweekly", "monthly"}

// validAutoVacuum reports whether v is a known cadence value ("off" included).
func validAutoVacuum(v string) bool {
	if v == "off" {
		return true
	}
	_, ok := autoVacuumIntervals[v]
	return ok
}

// autoVacuumSetting returns the persisted cadence, defaulting to "off".
func autoVacuumSetting(settings map[string]string) string {
	if v := settings[settingAutoVacuum]; validAutoVacuum(v) && v != "" {
		return v
	}
	return "off"
}

// StartMaintenance launches a background loop that runs the automatic database
// vacuum when it is due, until ctx is cancelled.
func (s *Server) StartMaintenance(ctx context.Context) {
	go func() {
		// A short initial delay lets startup settle before the first check.
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.maybeAutoVacuum(ctx)
				timer.Reset(maintenanceInterval)
			}
		}
	}()
}

// maybeAutoVacuum runs a vacuum when auto-vacuum is enabled and the configured
// interval has elapsed since the last (manual or automatic) run.
func (s *Server) maybeAutoVacuum(ctx context.Context) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return
	}
	interval, ok := autoVacuumIntervals[settings[settingAutoVacuum]]
	if !ok {
		return // off or unset
	}
	if raw := settings[settingAutoVacuumLast]; raw != "" {
		if last, perr := time.Parse(time.RFC3339, raw); perr == nil && time.Since(last) < interval {
			return // not due yet
		}
	}
	if err := s.store.Vacuum(ctx); err != nil {
		s.log.Error("auto-vacuum failed", "err", err)
		return
	}
	if err := s.store.PutSetting(ctx, settingAutoVacuumLast, time.Now().UTC().Format(time.RFC3339)); err != nil {
		s.log.Warn("recording auto-vacuum time", "err", err)
	}
	s.log.Info("auto-vacuum completed", "cadence", settings[settingAutoVacuum])
}

// handleUpdateAutoVacuum persists the auto-vacuum cadence. Enabling it also
// resets the clock so the first automatic run happens after one full interval.
func (s *Server) handleUpdateAutoVacuum(w http.ResponseWriter, r *http.Request) {
	interval := formStr(r, "interval")
	if !validAutoVacuum(interval) {
		interval = "off"
	}
	if err := s.store.PutSetting(r.Context(), settingAutoVacuum, interval); err != nil {
		s.serverError(w, r, err)
		return
	}
	if interval != "off" {
		if err := s.store.PutSetting(r.Context(), settingAutoVacuumLast, time.Now().UTC().Format(time.RFC3339)); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.settingSaved(w, r)
}
