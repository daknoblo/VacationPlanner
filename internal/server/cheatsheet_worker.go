package server

import (
	"context"
	"errors"
	"time"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

const cheatsheetTimeout = 90 * time.Second

// queueVacationCheatsheet is called only after creating a vacation and saving its
// participants. It never calls AI or turns a successfully saved trip into an HTTP
// failure. The durable queue outlives the creating request and its redirect.
func (s *Server) queueVacationCheatsheet(ctx context.Context, v *models.Vacation, language string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		s.log.Warn("cannot queue cheatsheet", "err", err, "vacation_id", v.ID)
		return
	}
	if language != "de" {
		language = "en"
	}
	sheet, err := s.store.GetCheatsheet(ctx, v.ID, language)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("cannot read cheatsheet cache", "err", err, "vacation_id", v.ID)
		return
	}
	if sheet != nil && sheet.DestinationKey == key {
		return
	}
	status := "queued"
	settings, settingsErr := s.settings(ctx)
	if settingsErr != nil {
		s.log.Warn("cannot load cheatsheet configuration", "err", settingsErr, "vacation_id", v.ID)
		status = "failed"
	} else if !s.ai.Enabled() || s.foundryDeployment(settings) == "" {
		status = "setup"
	}
	job := &models.CheatsheetJob{
		VacationID: v.ID, SourceLanguage: language, DestinationKey: key, Key: "sheet", Status: status,
	}
	if _, err := s.store.ReserveCheatsheetJob(ctx, job, false); err != nil {
		s.log.Warn("cannot queue cheatsheet", "err", err, "vacation_id", v.ID)
		return
	}
	select {
	case s.cheatsheetWake <- struct{}{}:
	default:
	}
}

// StartCheatsheetWorker starts one lifecycle-bound worker. Call the returned
// cancellation-and-join function before closing the store. Calling Start twice
// does not start another worker or replay running jobs.
func (s *Server) StartCheatsheetWorker(ctx context.Context) func() {
	s.cheatsheetStart.Do(func() {
		ctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		s.cheatsheetStop = func() {
			cancel()
			<-done
		}
		recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.store.InterruptCheatsheetJobs(recoveryCtx)
		recoveryCancel()
		recovered := err == nil
		if err != nil {
			s.log.Warn("cannot recover cheatsheet queue", "err", err)
		}
		go func() {
			defer close(done)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for ctx.Err() == nil {
				if !recovered {
					recovered = s.recoverCheatsheets(ctx)
				}
				if recovered {
					s.queueCheatsheetIntroductions(ctx)
					s.drainCheatsheets(ctx)
				}
				select {
				case <-ctx.Done():
					return
				case <-s.cheatsheetWake:
				case <-ticker.C:
				}
			}
		}()
	})
	return s.cheatsheetStop
}

func (s *Server) setCheatsheetStatus(ctx context.Context, job *models.CheatsheetJob, status string) {
	// Provider cancellation still needs a durable terminal state. Never retry a
	// call just because its result (or the request that initiated it) was lost.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.store.SetCheatsheetJobStatus(ctx, job, status); err != nil {
		s.log.Warn("cannot save cheatsheet job state", "err", err, "vacation_id", job.VacationID)
	}
}

func (s *Server) drainCheatsheets(ctx context.Context) {
	jobs, err := s.store.ListQueuedCheatsheetJobs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("cannot read cheatsheet queue", "err", err)
		}
		return
	}
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		case s.cheatsheetGate <- struct{}{}:
		}
		s.runCheatsheetJob(ctx, &job)
		<-s.cheatsheetGate
	}
}

func (s *Server) runCheatsheetJob(ctx context.Context, job *models.CheatsheetJob) {
	claimed, err := s.store.ClaimCheatsheetJob(ctx, job)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("cannot claim cheatsheet job", "err", err, "vacation_id", job.VacationID)
		}
		return
	}
	if !claimed {
		return
	}
	status := "failed"
	defer func() { s.setCheatsheetStatus(ctx, job, status) }()
	ctx, cancel := context.WithTimeout(ctx, cheatsheetTimeout)
	defer cancel()
	v, err := s.store.GetVacation(ctx, job.VacationID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("cannot load cheatsheet vacation", "err", err, "vacation_id", job.VacationID)
		}
		return
	}
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		s.log.Warn("cannot identify cheatsheet destination", "err", err, "vacation_id", v.ID)
		return
	}
	if key != job.DestinationKey {
		return
	}
	sheet, err := s.store.GetCheatsheet(ctx, v.ID, job.SourceLanguage)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("cannot load cached cheatsheet", "err", err, "vacation_id", v.ID)
		return
	}
	if job.Key != "sheet" {
		if job.IsIntroduction() {
			if s.runCheatsheetIntroduction(ctx, job, sheet) {
				status = "ready"
			}
		} else if s.runCheatsheetPhrase(ctx, job, sheet) {
			status = "ready"
		}
		return
	}
	if sheet != nil && sheet.DestinationKey == key {
		status = "ready"
		return
	}
	settings, err := s.settings(ctx)
	if err != nil {
		s.log.Warn("cannot load cheatsheet configuration", "err", err, "vacation_id", v.ID)
		return
	}
	if !s.ai.Enabled() || s.foundryDeployment(settings) == "" {
		status = "setup"
		return
	}
	sheet, err = s.ai.GenerateCheatsheet(ctx, s.foundryDeployment(settings), v, job.SourceLanguage)
	if err != nil {
		s.log.Warn("automatic cheatsheet generation failed", "err", err, "vacation_id", v.ID)
		return
	}
	sheet.VacationID, sheet.SourceLanguage, sheet.DestinationKey = v.ID, job.SourceLanguage, key
	if err := s.store.PutCheatsheet(ctx, sheet); err != nil {
		s.log.Warn("cannot save automatic cheatsheet", "err", err, "vacation_id", v.ID)
		return
	}
	status = "ready"
}

func introductionJob(sheet *models.Cheatsheet) *models.CheatsheetJob {
	lang, _ := i18n.ParseLang(sheet.SourceLanguage)
	job := &models.CheatsheetJob{
		VacationID: sheet.VacationID, SourceLanguage: sheet.SourceLanguage, DestinationKey: sheet.DestinationKey,
		TargetLanguage: sheet.Language, Status: "queued",
		Original: i18n.NewLocalizer(lang).T("cheatsheet.introduction_name", models.IntroductionPlaceholder),
	}
	job.Key = job.IntroductionKey()
	return job
}

// Only the lifecycle worker discovers missing frames. Reads and polling never
// enqueue work; capacity and idempotency remain governed by the durable queue.
func (s *Server) queueCheatsheetIntroductions(parent context.Context) {
	if !s.ai.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	settings, err := s.settings(ctx)
	if err != nil {
		s.log.Warn("cannot load introduction configuration", "err", err)
		return
	}
	if s.foundryDeployment(settings) == "" {
		return
	}
	sheets, err := s.store.ListCheatsheetsForIntroductions(ctx, s.cheatsheetIntroductionCursor)
	if err != nil {
		s.log.Warn("cannot find missing introductions", "err", err)
		return
	}
	for _, sheet := range sheets {
		s.cheatsheetIntroductionCursor = sheet.VacationID.String() + "/" + sheet.SourceLanguage
		if _, err := s.store.ReserveCheatsheetJob(ctx, introductionJob(&sheet), false); err != nil {
			if errors.Is(err, store.ErrCheatsheetQueueFull) {
				return
			}
			if !errors.Is(err, store.ErrCheatsheetDestinationChanged) {
				s.log.Warn("cannot queue introduction", "err", err, "vacation_id", sheet.VacationID)
			}
		}
	}
	if len(sheets) < 16 {
		s.cheatsheetIntroductionCursor = ""
	}
}

func (s *Server) runCheatsheetIntroduction(ctx context.Context, job *models.CheatsheetJob, sheet *models.Cheatsheet) bool {
	if err := job.ValidatePhrase(); err != nil {
		s.log.Warn("invalid introduction job", "err", err, "vacation_id", job.VacationID)
		return false
	}
	if sheet == nil || sheet.DestinationKey != job.DestinationKey || sheet.Language != job.TargetLanguage {
		s.log.Warn("introduction profile changed", "vacation_id", job.VacationID)
		return false
	}
	if sheet.Introduction != nil {
		return true
	}
	settings, err := s.settings(ctx)
	if err != nil {
		s.log.Warn("cannot load introduction configuration", "err", err)
		return false
	}
	if !s.ai.Enabled() || s.foundryDeployment(settings) == "" {
		s.log.Warn("introduction translation is not configured", "vacation_id", job.VacationID)
		return false
	}
	phrase := job.Phrase()
	introduction, err := s.ai.TranslateCheatsheetIntroduction(ctx, s.foundryDeployment(settings), phrase)
	if err != nil {
		s.log.Warn("introduction translation failed", "err", err, "vacation_id", job.VacationID)
		return false
	}
	if err := s.store.PutCheatsheetIntroduction(ctx, phrase, introduction); err != nil {
		s.log.Warn("cannot save introduction translation", "err", err, "vacation_id", job.VacationID)
		return false
	}
	return true
}

func (s *Server) runCheatsheetPhrase(ctx context.Context, job *models.CheatsheetJob, sheet *models.Cheatsheet) bool {
	// A queued phrase contains data, not a provider address or a request to
	// regenerate the vocabulary. Validate the persisted profile before dispatch.
	if err := job.ValidatePhrase(); err != nil || sheet == nil ||
		sheet.DestinationKey != job.DestinationKey || sheet.Language != job.TargetLanguage {
		return false
	}
	phrase := job.Phrase()
	cached, err := s.store.ListCustomCheatsheetPhrases(ctx, phrase)
	if err != nil {
		s.log.Warn("cannot read custom phrase cache", "err", err, "vacation_id", job.VacationID)
		return false
	}
	for _, saved := range cached {
		if saved.Original == phrase.Original {
			return true
		}
	}
	settings, err := s.settings(ctx)
	if err != nil || !s.ai.Enabled() || s.foundryDeployment(settings) == "" {
		return false
	}
	if err := s.ai.TranslateCheatsheetPhrase(ctx, s.foundryDeployment(settings), phrase); err != nil {
		s.log.Warn("custom phrase translation failed", "err", err, "vacation_id", job.VacationID)
		return false
	}
	if err := s.store.PutCustomCheatsheetPhrase(ctx, phrase); err != nil {
		s.log.Warn("cannot save custom phrase", "err", err, "vacation_id", job.VacationID)
		return false
	}
	return true
}

func (s *Server) recoverCheatsheets(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case s.cheatsheetGate <- struct{}{}:
	}
	defer func() { <-s.cheatsheetGate }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.store.InterruptCheatsheetJobs(ctx); err != nil {
		s.log.Warn("cannot recover cheatsheet queue", "err", err)
		return false
	}
	return true
}
