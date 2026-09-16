package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

type introductionRow struct {
	Original, Text, Pronunciation string
}

func introductionMatch(text string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "."))
}

func (s *Server) addCheatsheetIntroductions(ctx context.Context, view *cheatsheetView) error {
	if view.Sheet == nil {
		return nil
	}
	people, err := s.store.ListVacationParticipants(ctx, view.Sheet.VacationID)
	if err != nil {
		return err
	}
	loc := i18n.FromContext(ctx)
	used := make(map[string]bool)
	for _, person := range people {
		original := loc.T("cheatsheet.introduction_name", person.Name)
		key := introductionMatch(original)
		if used[key] {
			continue
		}
		used[key] = true
		row := introductionRow{Original: original}
		if frame := view.Sheet.Introduction; frame != nil {
			row.Text = strings.Replace(frame.Text, models.IntroductionPlaceholder, person.Name, 1)
			row.Pronunciation = strings.Replace(frame.Pronunciation, models.IntroductionPlaceholder, person.Name, 1)
		}
		for _, phrase := range view.Custom {
			if introductionMatch(phrase.Original) == key {
				row.Text, row.Pronunciation = phrase.Text, phrase.Pronunciation
				break
			}
		}
		view.Introductions = append(view.Introductions, row)
	}
	custom := view.Custom[:0]
	for _, phrase := range view.Custom {
		if !used[introductionMatch(phrase.Original)] {
			custom = append(custom, phrase)
		}
	}
	view.Custom = custom
	jobs := view.PhraseJobs[:0]
	for _, job := range view.PhraseJobs {
		if !used[introductionMatch(job.Original)] {
			jobs = append(jobs, job)
		}
	}
	view.PhraseJobs = jobs
	for _, row := range view.Introductions {
		if row.Text == "" {
			view.IntroductionMissing = true
		}
	}
	if !view.IntroductionMissing {
		return nil
	}
	job := introductionJob(view.Sheet)
	view.IntroductionStatus, err = s.store.GetCheatsheetJob(ctx, job)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	view.IntroductionAttempt = job.Attempt
	if view.IntroductionStatus == "" && view.CanGenerate {
		view.IntroductionStatus = "queued"
	}
	if view.IntroductionStatus == "queued" || view.IntroductionStatus == "running" {
		view.Active = true
	}
	return nil
}

func (s *Server) handleRetryCheatsheetIntroduction(w http.ResponseWriter, r *http.Request) {
	v := s.cheatsheetVacation(w, r)
	if v == nil {
		return
	}
	loc := i18n.FromContext(r.Context())
	view, err := s.cheatsheetData(r.Context(), v)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if view.Sheet == nil || !view.IntroductionMissing {
		s.renderCheatsheet(w, r, v, "")
		return
	}
	if !view.CanGenerate {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.configure_ai"))
		return
	}
	job := introductionJob(view.Sheet)
	job.Attempt = formStr(r, "retry_attempt")
	if formStr(r, "destination_key") != job.DestinationKey || formStr(r, "target_language") != job.TargetLanguage {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.destination_changed"))
		return
	}
	reserved, err := s.store.ReserveCheatsheetJob(r.Context(), job, true)
	if err != nil {
		s.log.Warn("cannot retry introduction", "err", err, "vacation_id", v.ID)
		if errors.Is(err, store.ErrCheatsheetQueueFull) {
			s.renderCheatsheet(w, r, v, loc.T("cheatsheet.queue_full"))
			return
		}
		if errors.Is(err, store.ErrCheatsheetDestinationChanged) {
			s.renderCheatsheet(w, r, v, loc.T("cheatsheet.destination_changed"))
			return
		}
	}
	if err != nil || !reserved {
		s.renderCheatsheet(w, r, v, loc.T("cheatsheet.introduction_failed"))
		return
	}
	select {
	case s.cheatsheetWake <- struct{}{}:
	default:
	}
	s.renderCheatsheet(w, r, v, "")
}
