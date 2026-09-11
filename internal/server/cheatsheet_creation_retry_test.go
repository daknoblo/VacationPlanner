package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
	"github.com/google/uuid"
)

func TestVacationCreationAutomaticallyQueuesCheatsheet(t *testing.T) {
	s, original, old := newCheatsheetTest(t)
	b := &blockingCheatsheetBackend{response: original.response, entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.ai = ai.New(b)
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	form := url.Values{"title": {"New trip"}, "destination": {"Paris, France"}, "start_date": {"2026-10-09"}, "end_date": {"2026-10-10"}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/vacations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", s.newCSRFToken())
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Accept-Language", "de")
	rec := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() {
		s.ServeHTTP(rec, req)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("vacation creation waited for the blocked AI call")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	id, err := uuid.Parse(strings.TrimPrefix(rec.Header().Get("HX-Redirect"), "/vacations/"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.store.GetVacation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("creating the vacation did not start automatic generation")
	}
	close(b.release)
	waitCheatsheetStatus(t, s, workerJob(t, v, "de"), "ready")
	if sheet, err := s.store.GetCheatsheet(context.Background(), id, "de"); err != nil || sheet.SourceLanguage != "de" {
		t.Fatalf("automatic result missing: %+v %v", sheet, err)
	}
	if _, err := s.store.GetCheatsheet(context.Background(), old.ID, "de"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("creation generated sheets for existing vacations")
	}
}

func TestCustomTranslationExplicitRetryIsAttemptBound(t *testing.T) {
	s, b, v, path := customPhraseTest(t)
	b.fail = true
	form := url.Values{"phrase": {"Please help"}}
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusOK || b.calls != 1 {
		t.Fatal("phrase was not queued")
	}
	s.drainCheatsheets(context.Background())
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	phrase := &models.CustomTravelPhrase{TargetLanguage: "French", Original: "Please help"}
	job := &models.CheatsheetJob{VacationID: v.ID, SourceLanguage: "en", DestinationKey: key, Key: phrase.JobKey()}
	if status, err := s.store.GetCheatsheetJob(context.Background(), job); err != nil || status != "failed" || job.Attempt == "" {
		t.Fatal("failed attempt has no retry identity")
	}
	firstAttempt := job.Attempt
	form.Set("retry_attempt", firstAttempt)
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusOK || b.calls != 2 {
		t.Fatalf("explicit retry did not enqueue: %d calls=%d", rec.Code, b.calls)
	}
	s.drainCheatsheets(context.Background())
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusUnprocessableEntity || b.calls != 3 {
		t.Fatal("replaying the same failed retry invoked the provider again")
	}
	if _, err := s.store.GetCheatsheetJob(context.Background(), job); err != nil || job.Attempt == firstAttempt {
		t.Fatal("retry did not advance the attempt identity")
	}
	b.fail = false
	form.Set("retry_attempt", job.Attempt)
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusOK || b.calls != 3 {
		t.Fatalf("translation could not recover: %d %s", rec.Code, rec.Body.String())
	}
	s.drainCheatsheets(context.Background())
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusOK || b.calls != 4 {
		t.Fatal("a saved translation was generated again")
	}
}

type flakyCheatsheetRecovery struct {
	store.Store
	attempts atomic.Int32
}

func (s *flakyCheatsheetRecovery) InterruptCheatsheetJobs(ctx context.Context) error {
	if s.attempts.Add(1) == 1 {
		return errors.New("temporary database failure")
	}
	return s.Store.InterruptCheatsheetJobs(ctx)
}

func TestCheatsheetWorkerRecoversFromTemporaryStartupFailure(t *testing.T) {
	s, _, v := newCheatsheetTest(t)
	s.queueVacationCheatsheet(context.Background(), v, "en")
	flaky := &flakyCheatsheetRecovery{Store: s.store}
	s.store = flaky
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	waitCheatsheetStatus(t, s, workerJob(t, v, "en"), "ready")
	if flaky.attempts.Load() < 2 {
		t.Fatal("worker was left stopped after a temporary recovery failure")
	}
}
