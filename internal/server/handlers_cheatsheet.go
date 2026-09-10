package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

type cheatsheetView struct {
	VacationID  string
	Destination string
	Sheet       *models.Cheatsheet
	CanGenerate bool
	Stale       bool
	Error       string
	CSRF        string
}

func cheatsheetDestinationKey(v *models.Vacation) (string, error) {
	data, err := json.Marshal(struct {
		Destination         string
		Latitude, Longitude *float64
		Version             int
	}{v.Destination, v.Latitude, v.Longitude, 1})
	if err != nil {
		return "", fmt.Errorf("encoding cheatsheet destination: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (s *Server) cheatsheetVacation(w http.ResponseWriter, r *http.Request) *models.Vacation {
	id, err := urlUUID(r, "vacationID")
	if err != nil {
		s.notFound(w, r)
		return nil
	}
	v, err := s.store.GetVacation(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			s.notFound(w, r)
		} else {
			s.serverError(w, r, err)
		}
		return nil
	}
	return v
}

func (s *Server) cheatsheetData(ctx context.Context, v *models.Vacation) (cheatsheetView, error) {
	view := cheatsheetView{VacationID: v.ID.String(), Destination: v.Destination}
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		return view, err
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return view, err
	}
	view.CanGenerate = s.ai.Enabled() && s.foundryDeployment(settings) != ""
	sheet, err := s.store.GetCheatsheet(ctx, v.ID, i18n.FromContext(ctx).Code())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return view, err
	}
	if sheet != nil {
		if sheet.DestinationKey == key {
			view.Sheet = sheet
		} else {
			view.Stale = true
		}
	}
	return view, nil
}

func (s *Server) renderCheatsheet(w http.ResponseWriter, r *http.Request, v *models.Vacation, message string) {
	view, err := s.cheatsheetData(r.Context(), v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	view.Error = message
	view.CSRF = csrfToken(r.Context())
	if message != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	s.fragment(w, r, "cheatsheet", view)
}

func (s *Server) handleCheatsheet(w http.ResponseWriter, r *http.Request) {
	if v := s.cheatsheetVacation(w, r); v != nil {
		s.renderCheatsheet(w, r, v, "")
	}
}

func (s *Server) handleGenerateCheatsheet(w http.ResponseWriter, r *http.Request) {
	v := s.cheatsheetVacation(w, r)
	if v == nil {
		return
	}
	loc := i18n.FromContext(r.Context())
	job := v.ID.String() + ":" + loc.Code()
	if _, busy := s.cheatsheetJobs.LoadOrStore(job, struct{}{}); busy {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.busy"))
		return
	}
	defer s.cheatsheetJobs.Delete(job)
	view, err := s.cheatsheetData(r.Context(), v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if view.Sheet != nil && formStr(r, "refresh") != "1" {
		s.renderCheatsheet(w, r, v, "")
		return
	}
	if !view.CanGenerate {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.configure_ai"))
		return
	}
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	settings, err := s.settings(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sheet, err := s.ai.GenerateCheatsheet(r.Context(), s.foundryDeployment(settings), v, loc.Code())
	if err != nil {
		s.log.Warn("cheatsheet generation failed", "err", err, "vacation_id", v.ID)
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.failed"))
		return
	}
	current, err := s.store.GetVacation(r.Context(), v.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	currentKey, err := cheatsheetDestinationKey(current)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if key != currentKey {
		s.renderCheatsheet(w, r, current, loc.T("cheatsheet.destination_changed"))
		return
	}
	sheet.VacationID, sheet.SourceLanguage, sheet.DestinationKey = v.ID, loc.Code(), key
	if err := s.store.PutCheatsheet(r.Context(), sheet); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderCheatsheet(w, r, v, "")
}
