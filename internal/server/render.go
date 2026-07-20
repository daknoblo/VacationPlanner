package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/web"
)

// renderer holds the parsed page and fragment templates.
type renderer struct {
	pages     map[string]*template.Template
	fragments *template.Template
}

// viewData is the envelope passed to every full page render.
type viewData struct {
	Title     string
	CSRFToken string
	Env       string
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
	"kindLabel":   kindLabel,
	"modeLabel":   modeLabel,
	"dict":        dict,
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

	return &renderer{pages: pages, fragments: fragments}, nil
}

func (r *renderer) page(w http.ResponseWriter, name string, data viewData) error {
	tmpl, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("server: unknown page %q", name)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", data); err != nil {
		return fmt.Errorf("server: rendering page %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)
	return err
}

func (r *renderer) fragment(w http.ResponseWriter, name string, data any) error {
	var buf bytes.Buffer
	if err := r.fragments.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("server: rendering fragment %q: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)
	return err
}

// ---- Server render helpers ----

func (s *Server) page(w http.ResponseWriter, r *http.Request, name, title string, data any) {
	vd := viewData{
		Title:     title,
		CSRFToken: csrfToken(r.Context()),
		Env:       s.cfg.Env,
		Data:      data,
	}
	if err := s.render.page(w, name, vd); err != nil {
		s.serverError(w, r, err)
	}
}

func (s *Server) fragment(w http.ResponseWriter, r *http.Request, name string, data any) {
	if err := s.render.fragment(w, name, data); err != nil {
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

func coordValue(f *float64) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

func kindLabel(k models.TravelKind) string {
	switch k {
	case models.TravelArrival:
		return "Anreise"
	case models.TravelDeparture:
		return "Abreise"
	default:
		return string(k)
	}
}

func modeLabel(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "flight", "flug":
		return "✈️ Flug"
	case "train", "zug", "bahn":
		return "🚆 Zug"
	case "car", "auto":
		return "🚗 Auto"
	case "bus":
		return "🚌 Bus"
	case "ferry", "fähre", "faehre", "schiff":
		return "⛴️ Fähre"
	case "":
		return ""
	default:
		return mode
	}
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
