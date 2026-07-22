package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/store"
)

// ---- request/response helpers ----

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func hxTrigger(w http.ResponseWriter, events string) {
	w.Header().Set("HX-Trigger", events)
}

var logSanitizer = strings.NewReplacer("\n", "", "\r", "", "\t", " ")

// sanitizeLog strips characters that could forge or split log entries (log injection).
func sanitizeLog(s string) string {
	return logSanitizer.Replace(s)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("internal error",
		"err", err,
		"method", sanitizeLog(r.Method),
		"path", sanitizeLog(r.URL.Path),
	)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not found", http.StatusNotFound)
}

// formError returns a validation message, retargeting the swap for HTMX clients.
func (s *Server) formError(w http.ResponseWriter, r *http.Request, target, msg string) {
	if isHTMX(r) {
		w.Header().Set("HX-Retarget", target)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.fragment(w, r, "form_error", msg)
		return
	}
	http.Error(w, msg, http.StatusUnprocessableEntity)
}

func urlUUID(r *http.Request, key string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, key))
}

func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}

// validationError is a user-facing validation message that should be shown to
// the user (HTTP 4xx) rather than logged as an internal error.
type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

// errValidation wraps a user-facing validation message.
func errValidation(msg string) error {
	return validationError{msg: msg}
}

// validationMessage returns the user-facing message if err is a validationError,
// or the empty string for a system error that should be treated as a 500.
func validationMessage(err error) string {
	var ve validationError
	if errors.As(err, &ve) {
		return ve.msg
	}
	return ""
}

// ---- form parsing/validation ----

func formStr(r *http.Request, key string) string {
	return strings.TrimSpace(r.FormValue(key))
}

func maxLen(s string, n int) bool {
	return len([]rune(s)) <= n
}

// parseCoords reads optional latitude/longitude fields. Both must be present
// together and within valid ranges.
func parseCoords(r *http.Request, latKey, lngKey string) (lat, lng *float64, err error) {
	loc := i18n.FromContext(r.Context())
	latRaw := formStr(r, latKey)
	lngRaw := formStr(r, lngKey)
	if latRaw == "" && lngRaw == "" {
		return nil, nil, nil
	}
	if latRaw == "" || lngRaw == "" {
		return nil, nil, errValidation(loc.T("error.coords_together"))
	}
	latV, err := strconv.ParseFloat(latRaw, 64)
	if err != nil || latV < -90 || latV > 90 {
		return nil, nil, errValidation(loc.T("error.lat_range"))
	}
	lngV, err := strconv.ParseFloat(lngRaw, 64)
	if err != nil || lngV < -180 || lngV > 180 {
		return nil, nil, errValidation(loc.T("error.lng_range"))
	}
	return &latV, &lngV, nil
}

func parseDate(r *http.Request, key string) (time.Time, error) {
	return time.Parse("2006-01-02", formStr(r, key))
}

func parseDatePtr(r *http.Request, key string) (*time.Time, error) {
	raw := formStr(r, key)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// parseDateTimeParts combines a date field (YYYY-MM-DD) and an optional time
// field (HH:MM) into a timestamp in the given timezone. It returns nil when
// either part is missing, so a departure is only scheduled once a time is set.
func parseDateTimeParts(r *http.Request, dateKey, timeKey string, tz *time.Location) (*time.Time, error) {
	date := formStr(r, dateKey)
	clock := formStr(r, timeKey)
	if date == "" || clock == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", date+"T"+clock, tz)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
