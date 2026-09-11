package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func phraseWorkerJob(t *testing.T, v *models.Vacation, original string) *models.CheatsheetJob {
	t.Helper()
	job := workerJob(t, v, "en")
	job.Original, job.TargetLanguage = original, "French"
	job.Key = job.Phrase().JobKey()
	return job
}

func getCheatsheetRows(s *Server, v *models.Vacation) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/vacations/"+v.ID.String()+"/cheatsheet?fragment=rows", nil)
	req.Header.Set("HX-Request", "true")
	s.ServeHTTP(rec, req)
	return rec
}

func TestCheatsheetPhraseQueueAcceptsMoreWhileProviderBlocked(t *testing.T) {
	s, original, v, path := customPhraseTest(t)
	before, err := s.store.GetCheatsheet(context.Background(), v.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	b := &blockingCheatsheetBackend{response: original.response, entered: make(chan struct{}, 2), release: make(chan struct{})}
	s.ai = ai.New(b)
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	enqueue := func(text string) {
		t.Helper()
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			done <- postAISettings(s, path+"?fragment=rows", url.Values{"phrase": {text}}, true)
		}()
		select {
		case rec := <-done:
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), text) ||
				strings.Contains(rec.Body.String(), "<textarea") {
				t.Fatalf("enqueue must return rows, without replacing the input: %d %s", rec.Code, rec.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("enqueue waited for blocked inference")
		}
	}
	enqueue("First phrase")
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not dispatch queued phrase")
	}
	enqueue("Second phrase")
	enqueue("First phrase")
	first, second := phraseWorkerJob(t, v, "First phrase"), phraseWorkerJob(t, v, "Second phrase")
	waitCheatsheetStatus(t, s, first, "running")
	waitCheatsheetStatus(t, s, second, "queued")
	for range 3 {
		rec := getCheatsheetRows(s, v)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `data-phrase-status="running"`) ||
			!strings.Contains(body, `data-phrase-status="queued"`) || strings.Contains(body, "<textarea") ||
			!strings.Contains(body, `id="cheatsheet-status" hx-swap-oob="outerHTML"`) || b.count.Load() != 1 {
			t.Fatalf("poll lost progress, replaced input or dispatched inference: %s", body)
		}
	}
	page := getCheatsheetPage(s, v).Body.String()
	if strings.Count(page, "<table ") != 1 || strings.Count(page, "<textarea") != 1 ||
		!strings.Contains(page, `hx-target="#cheatsheet-rows"`) ||
		!strings.Contains(page, `hx-disabled-elt="button"`) ||
		strings.Contains(page, `hx-target="#cheatsheet-panel"`) ||
		!strings.Contains(page, "data-reset") {
		t.Fatalf("pending phrases must share one table and keep the input usable: %s", page)
	}
	close(b.release)
	waitCheatsheetStatus(t, s, first, "ready")
	waitCheatsheetStatus(t, s, second, "ready")
	enqueue("First phrase")
	stop()
	if b.count.Load() != 2 {
		t.Fatalf("two unique phrases require exactly two inferences, got %d", b.count.Load())
	}
	after, err := s.store.GetCheatsheet(context.Background(), v.ID, "en")
	if err != nil || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatal("custom queue regenerated or overwrote the standard vocabulary")
	}
	page = getCheatsheetPage(s, v).Body.String()
	if strings.Count(page, "<table ") != 1 || strings.Count(page, "<th scope=\"row\">First phrase</th>") != 1 ||
		strings.Count(page, "<th scope=\"row\">Second phrase</th>") != 1 ||
		strings.Contains(page, `hx-trigger="every 3s"`) {
		t.Fatalf("ready phrases duplicated or continue polling: %s", page)
	}
}

func TestCheatsheetPhraseQueueShutdownKeepsOnlyUndispatchedJobs(t *testing.T) {
	s, original, v, path := customPhraseTest(t)
	b := &blockingCheatsheetBackend{response: original.response, entered: make(chan struct{}, 2), release: make(chan struct{})}
	s.ai = ai.New(b)
	for _, phrase := range []string{"In flight", "Still queued"} {
		if rec := postAISettings(s, path, url.Values{"phrase": {phrase}}, true); rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
	}
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not dispatch")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("phrase worker failed to cancel and join")
	}
	first, second := phraseWorkerJob(t, v, "In flight"), phraseWorkerJob(t, v, "Still queued")
	waitCheatsheetStatus(t, s, first, "failed")
	waitCheatsheetStatus(t, s, second, "queued")
	// A crash could leave the first job marked running. Recovery must not
	// replay it, but the second job has never reached the provider.
	if err := s.store.SetCheatsheetJobStatus(context.Background(), first, "running"); err != nil {
		t.Fatal(err)
	}
	close(b.release)
	restarted := &Server{
		store: s.store, log: s.log, cfg: s.cfg, foundry: s.foundry, ai: s.ai,
		cheatsheetGate: make(chan struct{}, 1), cheatsheetWake: make(chan struct{}, 1),
	}
	restartStop := restarted.StartCheatsheetWorker(context.Background())
	defer restartStop()
	waitCheatsheetStatus(t, s, first, "failed")
	waitCheatsheetStatus(t, s, second, "ready")
	restartStop()
	if b.count.Load() != 2 {
		t.Fatal("restart replayed an ambiguous inference")
	}
	if rec := postAISettings(s, path+"?fragment=rows", url.Values{"phrase": {"In flight"}}, true); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), `name="retry_attempt"`) || strings.Contains(rec.Body.String(), "<textarea") {
		t.Fatal("failed phrase does not expose an explicit row retry")
	}
}

func TestCheatsheetPhraseQueueRejectsStaleProfilesAndDeletion(t *testing.T) {
	for _, change := range []string{"destination", "target", "delete"} {
		t.Run(change, func(t *testing.T) {
			s, b, v, path := customPhraseTest(t)
			job := phraseWorkerJob(t, v, "Queued text")
			if rec := postAISettings(s, path, url.Values{"phrase": {job.Original}}, true); rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			switch change {
			case "destination":
				v.Destination = "Tokyo"
				if err := s.store.UpdateVacation(context.Background(), v); err != nil {
					t.Fatal(err)
				}
			case "target":
				sheet, err := s.store.GetCheatsheet(context.Background(), v.ID, "en")
				if err != nil {
					t.Fatal(err)
				}
				sheet.Language = "Japanese"
				if err := s.store.PutCheatsheet(context.Background(), sheet); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := s.store.DeleteVacation(context.Background(), v.ID); err != nil {
					t.Fatal(err)
				}
			}
			s.drainCheatsheets(context.Background())
			if b.calls != 1 {
				t.Fatal("stale or deleted job reached provider")
			}
			if change == "delete" {
				if rec := postAISettings(s, path, url.Values{"phrase": {"new"}}, true); rec.Code != http.StatusNotFound {
					t.Fatal("deleted vacation accepted a phrase")
				}
			} else {
				waitCheatsheetStatus(t, s, job, "failed")
				if strings.Contains(getCheatsheetRows(s, v).Body.String(), job.Original) {
					t.Fatal("old destination/language phrase displayed")
				}
			}
		})
	}
}

func TestCheatsheetPhraseQueueFullPreservesCacheAndReturnsRowsError(t *testing.T) {
	s, b, v, path := customPhraseTest(t)
	for i := range 16 {
		rec := postAISettings(s, path+"?fragment=rows", url.Values{"phrase": {fmt.Sprintf("queued %d", i)}}, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("enqueue %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := postAISettings(s, path+"?fragment=rows", url.Values{"phrase": {"overflow"}}, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) ||
		strings.Contains(rec.Body.String(), "<textarea") || b.calls != 1 {
		t.Fatalf("queue overflow must return a visible rows-only error: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := s.store.GetVacation(context.Background(), v.ID); err != nil {
		t.Fatal("queue overflow lost vacation")
	}
	if _, err := s.store.GetCheatsheet(context.Background(), v.ID, "en"); err != nil {
		t.Fatal("queue overflow lost standard vocabulary")
	}
	if _, err := s.store.GetCheatsheetJob(context.Background(), phraseWorkerJob(t, v, "overflow")); err == nil {
		t.Fatal("overflow reserved a phrase that could not be queued")
	}
	if rec := postAISettings(s, path, url.Values{"phrase": {"queued 0"}}, true); rec.Code != http.StatusOK {
		t.Fatal("duplicate enqueue consumed additional queue capacity")
	}
}

func TestCheatsheetPhraseSubmissionBindsVisibleProfile(t *testing.T) {
	s, b, _, path := customPhraseTest(t)
	for key, value := range map[string]string{"destination_key": "old", "source_language": "de", "target_language": "German"} {
		form := url.Values{"phrase": {"Do not dispatch"}, key: {value}}
		if rec := postAISettings(s, path+"?fragment=rows", form, true); rec.Code != http.StatusUnprocessableEntity || b.calls != 1 {
			t.Fatalf("stale %s accepted", key)
		}
	}
}
