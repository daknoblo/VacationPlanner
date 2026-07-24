package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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
	"fmtDate":    fmtDate,
	"fmtDatePtr": fmtDatePtr,
	"dateInput":  dateInput,
	"datePtrIn":  datePtrInput,
	"dtInput":    dateTimeInput,
	"coord":      coordValue,
	"money":      money,
	"moneyF":     moneyF,
	"bmoney":     bmoney,
	"bmoneyp":    bmoneyP,
	// cost/costf format an amount with the configured currency symbol; they are
	// bound per request (placeholders here so templates parse).
	"cost":       func(*float64) string { return "" },
	"costf":      func(float64) string { return "" },
	"dict":       dict,
	"add":        addInt,
	"sub":        subInt,
	"mod":        modInt,
	"div":        divInt,
	"seq":        seq,
	"weekday":    weekdayKey,
	"sameDay":    sameDay,
	"catclass":   catClass,
	"colorStyle": colorStyleAttr,
	"uuidStr":    uuidString,
	"neg":        func(f float64) float64 { return -f },
	// t is a per-request placeholder; the real translator is bound at render time.
	"t": func(key string, _ ...any) string { return key },
}

// uuidString renders an optional UUID pointer as a plain string ("" when nil),
// used to preselect the current payer in a paid-by dropdown.
func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// colorStyleAttr returns a safe inline style setting the --pc custom property to
// a validated hex color, used to tint person markers/badges. Invalid input
// yields an empty attribute so nothing unsafe is ever emitted.
func colorStyleAttr(hex string) template.HTMLAttr {
	if !isHexColor(hex) {
		return ""
	}
	return template.HTMLAttr(`style="--pc:` + hex + `"`) //nolint:gosec // hex validated below
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
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

func (r *renderer) page(w http.ResponseWriter, name string, loc *i18n.Localizer, data viewData, tz *time.Location, currency string) error {
	tmpl, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("server: unknown page %q", name)
	}
	clone, err := tmpl.Clone()
	if err != nil {
		return fmt.Errorf("server: cloning page %q: %w", name, err)
	}
	clone.Funcs(template.FuncMap{
		"t":       loc.T,
		"dtInput": dateTimeInputIn(tz),
		"cost":    func(f *float64) string { return bmoneyP(f, currency) },
		"costf":   func(f float64) string { return bmoney(f, currency) },
	})

	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, "base", data); err != nil {
		return fmt.Errorf("server: rendering page %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err = buf.WriteTo(w)
	return err
}

func (r *renderer) fragment(w http.ResponseWriter, name string, loc *i18n.Localizer, data any, tz *time.Location, currency string) error {
	clone, err := r.fragments.Clone()
	if err != nil {
		return fmt.Errorf("server: cloning fragments: %w", err)
	}
	clone.Funcs(template.FuncMap{
		"t":       loc.T,
		"dtInput": dateTimeInputIn(tz),
		"cost":    func(f *float64) string { return bmoneyP(f, currency) },
		"costf":   func(f float64) string { return bmoney(f, currency) },
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
	if err := s.render.page(w, name, loc, vd, tz, s.currencySymbol(r.Context())); err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) fragment(w http.ResponseWriter, r *http.Request, name string, data any) {
	loc := i18n.FromContext(r.Context())
	_, tz := s.regionSettings(r.Context())
	if err := s.render.fragment(w, name, loc, data, tz, s.currencySymbol(r.Context())); err != nil {
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

// catColorCount is the number of category color variants defined in the CSS
// (.cat-0 … .cat-N-1).
const catColorCount = 10

// catClass maps a category (or travel-mode) label to a stable CSS color class so
// the same category always renders with the same color across the UI.
func catClass(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "cat-0"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("cat-%d", h.Sum32()%catColorCount)
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

// trimAmount formats an amount with up to two decimals, dropping a trailing
// ".00" (and any trailing zero) so whole values render without decimals.
func trimAmount(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

// bmoney formats an amount for the budget view: trimmed decimals plus the
// currency symbol (e.g. "2000 €", "182.50 €").
func bmoney(f float64, currency string) string {
	return trimAmount(f) + "\u00a0" + currency
}

// bmoneyP is the nil-safe *float64 variant of bmoney.
func bmoneyP(f *float64, currency string) string {
	if f == nil {
		return ""
	}
	return bmoney(*f, currency)
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
