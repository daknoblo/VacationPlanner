package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/web"
)

// renderer holds the parsed page and fragment templates.
type renderer struct {
	pages     map[string]*template.Template
	fragments *template.Template
	assetVer  string
}

// viewData is the envelope passed to every full page render.
type viewData struct {
	Title     string
	CSRFToken string
	Env       string
	Lang      string
	AssetVer  string
	Page      string
	WeekStart string
	CurrentID string
	Data      any
}

var funcMap = template.FuncMap{
	"fmtDate":     fmtDate,
	"fmtDatePtr":  fmtDatePtr,
	"dateInput":   dateInput,
	"datePtrIn":   datePtrInput,
	"dtInput":     dateTimeInput,
	"fmtDateTime": fmtDateTime,
	"coord":       coordValue,
	"money":       money,
	"moneyF":      moneyF,
	"dict":        dict,
	"add":         addInt,
	"sub":         subInt,
	"mod":         modInt,
	"div":         divInt,
	"seq":         seq,
	"weekday":     weekdayKey,
	"sameDay":     sameDay,
	// t is a per-request placeholder; the real translator is bound at render time.
	"t": func(key string, _ ...any) string { return key },
}

func newRenderer() (*renderer, error) {
	base, err := template.New("base").Funcs(funcMap).ParseFS(
		web.Templates,
		"templates/layout/*.html",
		"templates/partials/*.html",
	)
	if err != nil {
		return nil, fmt.Errorf("server: parsing base templates: %w", err)
	}

	pageFiles, err := fs.Glob(web.Templates, "templates/pages/*.html")
	if err != nil {
		return nil, fmt.Errorf("server: globbing pages: %w", err)
	}
	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("server: cloning base template: %w", err)
		}
		tmpl, err := clone.ParseFS(web.Templates, pf)
		if err != nil {
			return nil, fmt.Errorf("server: parsing page %s: %w", pf, err)
		}
		name := strings.TrimSuffix(path.Base(pf), ".html")
		pages[name] = tmpl
	}

	fragments, err := template.New("fragments").Funcs(funcMap).ParseFS(
		web.Templates, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("server: parsing fragment templates: %w", err)
	}

	return &renderer{pages: pages, fragments: fragments, assetVer: assetVersion()}, nil
}

// assetVersion returns a short content hash of the app's CSS and JS so their
// URLs can be cache-busted whenever the files change (vendored libraries keep
// their stable long-cached paths).
func assetVersion() string {
	h := sha256.New()
	for _, name := range []string{"static/css/app.css", "static/js/app.js"} {
		if b, err := fs.ReadFile(web.Static, name); err == nil {
			_, _ = h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

func (r *renderer) page(w http.ResponseWriter, name string, loc *i18n.Localizer, data viewData, tz *time.Location) error {
	tmpl, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("server: unknown page %q", name)
	}
	clone, err := tmpl.Clone()
	if err != nil {
		return fmt.Errorf("server: cloning page %q: %w", name, err)
	}
	clone.Funcs(template.FuncMap{
		"t":           loc.T,
		"fmtDateTime": fmtDateTimeIn(tz),
		"dtInput":     dateTimeInputIn(tz),
	})

	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, "base", data); err != nil {
		return fmt.Errorf("server: rendering page %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = buf.WriteTo(w)
	return err
}

func (r *renderer) fragment(w http.ResponseWriter, name string, loc *i18n.Localizer, data any, tz *time.Location) error {
	clone, err := r.fragments.Clone()
	if err != nil {
		return fmt.Errorf("server: cloning fragments: %w", err)
	}
	clone.Funcs(template.FuncMap{
		"t":           loc.T,
		"fmtDateTime": fmtDateTimeIn(tz),
		"dtInput":     dateTimeInputIn(tz),
	})

	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("server: rendering fragment %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = buf.WriteTo(w)
	return err
}

// ---- Server render helpers ----

func (s *Server) page(w http.ResponseWriter, r *http.Request, name, title string, data any) {
	loc := i18n.FromContext(r.Context())
	weekStart, tz := s.regionSettings(r.Context())
	vd := viewData{
		Title:     title,
		CSRFToken: csrfToken(r.Context()),
		Env:       s.cfg.Env,
		Lang:      loc.Code(),
		AssetVer:  s.render.assetVer,
		Page:      name,
		WeekStart: weekStart,
		CurrentID: chi.URLParam(r, "vacationID"),
		Data:      data,
	}
	if err := s.render.page(w, name, loc, vd, tz); err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) fragment(w http.ResponseWriter, r *http.Request, name string, data any) {
	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())
	if err := s.render.fragment(w, name, loc, data, tz); err != nil {
		s.serverError(w, r, err)
	}
}

// ---- template funcs ----

func fmtDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("02.01.2006")
}

// weekdayKey returns the lower-case English weekday name (e.g. "monday") for use
// as an i18n key for localized weekday labels.
func weekdayKey(t time.Time) string {
	return strings.ToLower(t.Weekday().String())
}

func fmtDatePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return fmtDate(*t)
}

func dateInput(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func datePtrInput(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func dateTimeInput(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("2006-01-02T15:04")
}

func fmtDateTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Local().Format("02.01.2006 15:04")
}

// fmtDateTimeIn formats a timestamp for display in the given timezone.
func fmtDateTimeIn(loc *time.Location) func(*time.Time) string {
	return func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.In(loc).Format("02.01.2006 15:04")
	}
}

// dateTimeInputIn formats a timestamp for a datetime-local input in the given timezone.
func dateTimeInputIn(loc *time.Location) func(*time.Time) string {
	return func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.In(loc).Format("2006-01-02T15:04")
	}
}

func coordValue(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

// money formats an optional monetary amount with two decimals; nil renders "".
func money(f *float64) string {
	if f == nil {
		return ""
	}
	return moneyF(*f)
}

// moneyF formats a monetary amount with two decimals.
func moneyF(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// addInt adds two integers (used for 1-based day numbering in templates).
func addInt(a, b int) int { return a + b }

// subInt subtracts two integers (used for activity block heights).
func subInt(a, b int) int { return a - b }

// modInt returns a mod b (0 when b == 0); used to group day tabs into weeks.
func modInt(a, b int) int {
	if b == 0 {
		return 0
	}
	return a % b
}

// divInt returns a / b (0 when b == 0); used for week numbering.
func divInt(a, b int) int {
	if b == 0 {
		return 0
	}
	return a / b
}

// seq returns the integers 0..n-1 (used to render the planner's hour rows).
func seq(n int) []int {
	if n < 0 {
		n = 0
	}
	if n > 1000 {
		n = 1000
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// sameDay reports whether an optional timestamp falls on the given calendar day.
func sameDay(a *time.Time, b time.Time) bool {
	if a == nil {
		return false
	}
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// dict builds a map from alternating key/value pairs for template composition.
func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: keys must be strings")
		}
		m[key] = values[i+1]
	}
	return m, nil
}
