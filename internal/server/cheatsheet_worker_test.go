package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/models"
)

type blockingCheatsheetBackend struct {
	stubFoundry
	response []byte
	entered  chan struct{}
	release  chan struct{}
	count    atomic.Int32
}

func (b *blockingCheatsheetBackend) DoChat(ctx context.Context, _ foundry.Target, _ []byte) ([]byte, error) {
	b.count.Add(1)
	select {
	case b.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return b.response, nil
	}
}

func waitCheatsheetStatus(t *testing.T, s *Server, job *models.CheatsheetJob, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.store.GetCheatsheetJob(context.Background(), job)
		if err == nil && status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, err := s.store.GetCheatsheetJob(context.Background(), job)
	t.Fatalf("job status = %s, %v; want %s", status, err, want)
}

func workerJob(t *testing.T, v *models.Vacation, language string) *models.CheatsheetJob {
	t.Helper()
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	return &models.CheatsheetJob{
		VacationID: v.ID, SourceLanguage: language, DestinationKey: key, Key: "sheet", Status: "queued",
	}
}

func TestCheatsheetCreationQueueSurvivesRedirectAndPolls(t *testing.T) {
	s, original, v := newCheatsheetTest(t)
	b := &blockingCheatsheetBackend{response: original.response, entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.ai = ai.New(b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Creation schedules only metadata; even the already-cancelled creating
	// request cannot cancel the durable job or cause a paid call here.
	s.queueVacationCheatsheet(ctx, v, "de")
	s.queueVacationCheatsheet(ctx, v, "de")
	job := workerJob(t, v, "de")
	waitCheatsheetStatus(t, s, job, "queued")
	if b.count.Load() != 0 {
		t.Fatal("queueing synchronously called the provider")
	}
	getGerman := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/vacations/"+v.ID.String()+"/cheatsheet", nil)
		req.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec
	}
	if rec := getGerman(); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `hx-trigger="every 3s"`) {
		t.Fatalf("queued job does not poll: %d %s", rec.Code, rec.Body.String())
	}
	stop := s.StartCheatsheetWorker(context.Background())
	t.Cleanup(stop)
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start queued job")
	}
	waitCheatsheetStatus(t, s, job, "running")
	for range 2 {
		if rec := getGerman(); rec.Code != http.StatusOK || b.count.Load() != 1 {
			t.Fatal("polling called AI again")
		}
		s.queueVacationCheatsheet(ctx, v, "de")
	}
	close(b.release)
	waitCheatsheetStatus(t, s, job, "ready")
	if sheet, err := s.store.GetCheatsheet(context.Background(), v.ID, "de"); err != nil || sheet.SourceLanguage != "de" {
		t.Fatalf("creating request language was not persisted: %+v %v", sheet, err)
	}
	s.queueVacationCheatsheet(ctx, v, "de")
	if rec := getGerman(); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `hx-trigger="every 3s"`) ||
		strings.Contains(rec.Body.String(), "<script>not executable</script>") || b.count.Load() != 1 {
		t.Fatalf("ready cache escaped incorrectly, still polls or generated twice: %d %s", rec.Code, rec.Body.String())
	}
	stop()
}

func TestCheatsheetWorkerShutdownAndRestartNeverReplayRunningCalls(t *testing.T) {
	s, original, v := newCheatsheetTest(t)
	b := &blockingCheatsheetBackend{response: original.response, entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.ai = ai.New(b)
	s.queueVacationCheatsheet(context.Background(), v, "en")
	job := workerJob(t, v, "en")
	stop := s.StartCheatsheetWorker(context.Background())
	t.Cleanup(stop)
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never called provider")
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("worker cancellation did not join")
	}
	waitCheatsheetStatus(t, s, job, "failed")
	// Simulate a crash with an ambiguous persisted running state.
	if err := s.store.SetCheatsheetJobStatus(context.Background(), job, "running"); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{
		store: s.store, log: s.log, cfg: s.cfg, foundry: s.foundry, ai: s.ai,
		cheatsheetGate: make(chan struct{}, 1), cheatsheetWake: make(chan struct{}, 1),
	}
	restartStop := restarted.StartCheatsheetWorker(context.Background())
	defer restartStop()
	waitCheatsheetStatus(t, s, job, "failed")
	restarted.queueVacationCheatsheet(context.Background(), v, "en")
	restartStop()
	if b.count.Load() != 1 {
		t.Fatal("restart or repeated delivery replayed an ambiguous paid call")
	}
}

func TestCheatsheetCreationWithoutAIRequiresSetup(t *testing.T) {
	s, b, v := newCheatsheetTest(t)
	s.ai = ai.New(nil)
	s.queueVacationCheatsheet(context.Background(), v, "en")
	waitCheatsheetStatus(t, s, workerJob(t, v, "en"), "setup")
	if _, err := s.store.GetVacation(context.Background(), v.ID); err != nil {
		t.Fatal("unavailable AI affected vacation creation")
	}
	rec := getCheatsheetPage(s, v)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/settings") || b.calls != 0 ||
		strings.Contains(rec.Body.String(), `hx-trigger="every 3s"`) {
		t.Fatalf("setup state not shown: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCheatsheetWorkerDoesNotGenerateExistingVacations(t *testing.T) {
	s, b, v := newCheatsheetTest(t)
	stop := s.StartCheatsheetWorker(context.Background())
	stop()
	if b.calls != 0 || getCheatsheetPage(s, v).Code != http.StatusOK {
		t.Fatal("starting the worker generated an unqueued existing vacation")
	}
}

func TestCheatsheetWorkerSkipsCacheAfterInterruptedStatusWrite(t *testing.T) {
	s, b, v := newCheatsheetTest(t)
	path := "/vacations/" + v.ID.String() + "/cheatsheet"
	if rec := postAISettings(s, path, nil, true); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	job := workerJob(t, v, "en")
	if err := s.store.SetCheatsheetJobStatus(context.Background(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	waitCheatsheetStatus(t, s, job, "ready")
	stop()
	if b.calls != 1 {
		t.Fatal("queued delivery regenerated an already cached sheet")
	}
}

func TestCheatsheetWorkerDiscardsInFlightDestinationEdit(t *testing.T) {
	s, original, v := newCheatsheetTest(t)
	b := &blockingCheatsheetBackend{response: original.response, entered: make(chan struct{}, 1), release: make(chan struct{})}
	s.ai = ai.New(b)
	job := workerJob(t, v, "en")
	s.queueVacationCheatsheet(context.Background(), v, "en")
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	v.Destination = "Tokyo"
	if err := s.store.UpdateVacation(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	close(b.release)
	waitCheatsheetStatus(t, s, job, "failed")
	if sheet, err := s.store.GetCheatsheet(context.Background(), v.ID, "en"); err == nil || sheet != nil {
		t.Fatal("worker saved an obsolete destination response")
	}
}
