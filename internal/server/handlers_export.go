package server

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/pdf"
)

// loadVacationFull loads a vacation together with its travel segments and items.
func (s *Server) loadVacationFull(ctx context.Context, id uuid.UUID) (*models.Vacation, error) {
	v, err := s.store.GetVacation(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.TravelSegments, err = s.store.ListTravelSegments(ctx, id); err != nil {
		return nil, err
	}
	if v.Items, err = s.store.ListItems(ctx, id); err != nil {
		return nil, err
	}
	return v, nil
}

// parseDayParam reads an optional ?day=YYYY-MM-DD filter from the request.
func parseDayParam(r *http.Request) *time.Time {
	ds := r.URL.Query().Get("day")
	if ds == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", ds)
	if err != nil {
		return nil
	}
	return &t
}

// handleExport renders a print-friendly overview of a vacation, optionally
// scoped to a single day via ?day=YYYY-MM-DD.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.loadVacationFull(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, "export", i18n.FromContext(r.Context()).T("export.title"), map[string]any{
		"Vacation": v,
		"Day":      parseDayParam(r),
	})
}

// handleExportPDF streams a server-generated PDF itinerary, optionally scoped to
// a single day via ?day=YYYY-MM-DD.
func (s *Server) handleExportPDF(w http.ResponseWriter, r *http.Request) {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return
	}
	v, err := s.loadVacationFull(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	day := parseDayParam(r)

	var buf bytes.Buffer
	if err := pdf.Vacation(&buf, v, day, i18n.FromContext(r.Context())); err != nil {
		s.serverError(w, r, err)
		return
	}

	name := "itinerary"
	if day != nil {
		name += "-" + day.Format("2006-01-02")
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.pdf"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, _ = buf.WriteTo(w)
}
