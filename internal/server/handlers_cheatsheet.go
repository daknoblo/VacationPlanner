package server

import (
	"context"
	"errors"
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
	Active      bool
	SheetActive bool
	Status      string
	Custom      []models.CustomTravelPhrase
	PhraseJobs  []models.CheatsheetJob
	Profile     *models.CustomTravelPhrase
	RowsOnly    bool
}

func cheatsheetDestinationKey(v *models.Vacation) (string, error) {
	return models.CheatsheetDestinationKey(v)
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
			view.Profile = &models.CustomTravelPhrase{
				VacationID: v.ID, SourceLanguage: sheet.SourceLanguage, DestinationKey: key, TargetLanguage: sheet.Language,
			}
			view.Custom, err = s.store.ListCustomCheatsheetPhrases(ctx, view.Profile)
			if err != nil {
				return view, err
			}
			jobs, err := s.store.ListCheatsheetPhraseJobs(ctx, view.Profile)
			if err != nil {
				return view, err
			}
			saved := make(map[string]bool, len(view.Custom))
			for _, phrase := range view.Custom {
				saved[phrase.Original] = true
			}
			for _, job := range jobs {
				if saved[job.Original] || job.Status == "ready" {
					continue
				}
				view.PhraseJobs = append(view.PhraseJobs, job)
				view.Active = view.Active || job.Status == "queued" || job.Status == "running"
			}
		} else {
			view.Stale = true
		}
	}
	view.Status, err = s.store.GetCheatsheetJob(ctx, &models.CheatsheetJob{
		VacationID: v.ID, SourceLanguage: i18n.FromContext(ctx).Code(), DestinationKey: key, Key: "sheet",
	})
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return view, err
	}
	view.SheetActive = view.Status == "queued" || view.Status == "running"
	view.Active = view.Active || view.SheetActive
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
	view.RowsOnly = r.URL.Query().Get("fragment") == "rows"
	if message != "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	if view.RowsOnly {
		s.fragment(w, r, "cheatsheet-rows-response", view)
	} else {
		s.fragment(w, r, "cheatsheet", view)
	}
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
	select {
	case s.cheatsheetGate <- struct{}{}:
		defer func() { <-s.cheatsheetGate }()
	default:
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.busy"))
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
	reservation := &models.CheatsheetJob{
		VacationID: v.ID, SourceLanguage: loc.Code(), DestinationKey: key, Key: "sheet", Status: "running",
	}
	reserved, err := s.store.ReserveCheatsheetJob(r.Context(), reservation, true)
	if err != nil {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.failed"))
		return
	}
	if !reserved {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.busy"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cheatsheetTimeout)
	defer cancel()
	status := "failed"
	defer func() { s.setCheatsheetStatus(ctx, reservation, status) }()
	sheet, err := s.ai.GenerateCheatsheet(ctx, s.foundryDeployment(settings), v, loc.Code())
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
		if errors.Is(err, store.ErrCheatsheetDestinationChanged) {
			s.renderCheatsheet(w, r, current, loc.T("cheatsheet.destination_changed"))
			return
		}
		s.serverError(w, r, err)
		return
	}
	status = "ready"
	s.setCheatsheetStatus(r.Context(), reservation, status)
	s.renderCheatsheet(w, r, v, "")
}

func (s *Server) handleTranslateCheatsheetPhrase(w http.ResponseWriter, r *http.Request) {
	v := s.cheatsheetVacation(w, r)
	if v == nil {
		return
	}
	loc := i18n.FromContext(r.Context())
	original, err := models.NormalizeCustomPhrase(formStr(r, "phrase"))
	if err != nil {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.custom_invalid"))
		return
	}
	view, err := s.cheatsheetData(r.Context(), v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if view.Sheet == nil {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.custom_needs_sheet"))
		return
	}
	if (formStr(r, "destination_key") != "" && formStr(r, "destination_key") != view.Sheet.DestinationKey) ||
		(formStr(r, "source_language") != "" && formStr(r, "source_language") != loc.Code()) ||
		(formStr(r, "target_language") != "" && formStr(r, "target_language") != view.Sheet.Language) {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.destination_changed"))
		return
	}
	for _, cached := range view.Custom {
		if cached.Original == original {
			s.renderCheatsheet(w, r, v, "")
			return
		}
	}
	if !view.CanGenerate {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.configure_ai"))
		return
	}
	phrase := &models.CustomTravelPhrase{
		VacationID: v.ID, SourceLanguage: loc.Code(), DestinationKey: view.Sheet.DestinationKey,
		TargetLanguage: view.Sheet.Language, Original: original,
	}
	job := &models.CheatsheetJob{
		VacationID: v.ID, SourceLanguage: loc.Code(), DestinationKey: phrase.DestinationKey,
		Key: phrase.JobKey(), Status: "queued", Original: original, TargetLanguage: phrase.TargetLanguage,
		Attempt: formStr(r, "retry_attempt"),
	}
	reserved, err := s.store.ReserveCheatsheetJob(r.Context(), job, job.Attempt != "")
	if err != nil || !reserved {
		if err == nil {
			// Another request may have enqueued or finished this phrase since
			// the cache read. Neither case should dispatch another inference.
			cached, cacheErr := s.store.ListCustomCheatsheetPhrases(r.Context(), phrase)
			if cacheErr != nil {
				s.serverError(w, r, cacheErr)
				return
			}
			for _, saved := range cached {
				if saved.Original == original {
					s.renderCheatsheet(w, r, v, "")
					return
				}
			}
			state, stateErr := s.store.GetCheatsheetJob(r.Context(), job)
			if stateErr == nil && (state == "queued" || state == "running") {
				s.renderCheatsheet(w, r, v, "")
				return
			}
		}
		if err != nil {
			s.log.Warn("cannot reserve custom translation", "err", err, "vacation_id", v.ID)
			if errors.Is(err, store.ErrCheatsheetQueueFull) {
				s.renderCheatsheet(w, r, v, loc.T("cheatsheet.queue_full"))
				return
			}
			if errors.Is(err, store.ErrCheatsheetDestinationChanged) {
				s.renderCheatsheet(w, r, v, loc.T("cheatsheet.destination_changed"))
				return
			}
		}
		s.renderCustomPhraseFailure(w, r, v, phrase, job)
		return
	}
	select {
	case s.cheatsheetWake <- struct{}{}:
	default:
	}
	s.renderCheatsheet(w, r, v, "")
}
