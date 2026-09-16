package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/models"
)

const introductionResponse = `{"choices":[{"message":{"content":"{\"text\":\"Jeg hedder {name}\",\"pronunciation\":\"yai HED-er {name}\"}"}}]}`

func newIntroductionTest(t *testing.T, language string) (*Server, *cheatsheetBackend, *models.Vacation, *models.Cheatsheet, []models.Person) {
	t.Helper()
	s, backend, vacation := newCheatsheetTest(t)
	backend.response = []byte(introductionResponse)
	key, err := models.CheatsheetDestinationKey(vacation)
	if err != nil {
		t.Fatal(err)
	}
	sheet := &models.Cheatsheet{
		VacationID: vacation.ID, DestinationKey: key, SourceLanguage: language, Country: "Denmark", Language: "Danish",
	}
	for _, meaning := range models.TravelPhraseMeanings() {
		sheet.Phrases = append(sheet.Phrases, models.TravelPhrase{Key: meaning.Key, Text: "Existing translation", Pronunciation: "Existing pronunciation"})
	}
	if err := s.store.PutCheatsheet(t.Context(), sheet); err != nil {
		t.Fatal(err)
	}
	people := []models.Person{{Name: "Daniel", SortOrder: 0}, {Name: "Therese", SortOrder: 1}, {Name: "Not taking part", SortOrder: 2}}
	for idx := range people {
		if err := s.store.CreatePerson(t.Context(), &people[idx]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.SetVacationParticipants(t.Context(), vacation.ID, []uuid.UUID{people[0].ID, people[1].ID}); err != nil {
		t.Fatal(err)
	}
	return s, backend, vacation, sheet, people
}

func introductionPage(t *testing.T, s *Server, v *models.Vacation, language string) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+v.ID.String()+"/cheatsheet?fragment=rows", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: language})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestIntroductionsUseOnlySelectedParticipantsAndNeverGenerateOnReads(t *testing.T) {
	s, backend, vacation, sheet, people := newIntroductionTest(t, "de")
	before := introductionPage(t, s, vacation, "de")
	if !strings.Contains(before, "Ich heiße Daniel") || !strings.Contains(before, "Ich heiße Therese") ||
		strings.Contains(before, people[2].Name) || !strings.Contains(before, `hx-trigger="every 3s"`) {
		t.Fatal("current trip participants were not shown while waiting for the background frame")
	}
	jobs, err := s.store.CountPendingCheatsheetJobs(t.Context())
	if err != nil || jobs != 0 || backend.calls != 0 {
		t.Fatal("GET queued or generated an introduction")
	}
	s.queueCheatsheetIntroductions(t.Context())
	jobs, err = s.store.CountPendingCheatsheetJobs(t.Context())
	if err != nil || jobs != 1 || backend.calls != 0 {
		t.Fatal("worker must reserve one frame, not one paid call per person")
	}
	s.drainCheatsheets(t.Context())
	after := introductionPage(t, s, vacation, "de")
	if backend.calls != 1 || strings.Count(after, "data-introduction") != 2 ||
		!strings.Contains(after, "Jeg hedder Daniel") || !strings.Contains(after, "Jeg hedder Therese") ||
		strings.Contains(after, "{name}") || strings.Contains(after, `hx-trigger="every 3s"`) {
		t.Fatalf("incorrect introduction rendering: calls=%d %s", backend.calls, after)
	}
	stored, err := s.store.GetCheatsheet(t.Context(), vacation.ID, "de")
	if err != nil || stored.Introduction == nil || len(stored.Phrases) != 22 || !stored.CreatedAt.Equal(sheet.CreatedAt) {
		t.Fatalf("background frame modified standard vocabulary or generation time: %+v %v", stored, err)
	}
	if err := s.store.SetVacationParticipants(t.Context(), vacation.ID, []uuid.UUID{people[1].ID, people[2].ID}); err != nil {
		t.Fatal(err)
	}
	changed := introductionPage(t, s, vacation, "de")
	if strings.Contains(changed, "Ich heiße Daniel") || !strings.Contains(changed, "Ich heiße "+people[2].Name) {
		t.Fatal("participant changes did not update the standard name rows")
	}
	s.queueCheatsheetIntroductions(t.Context())
	s.drainCheatsheets(t.Context())
	if backend.calls != 1 {
		t.Fatal("participant changes or repeated scans generated a second frame")
	}
	if err := s.store.SetVacationParticipants(t.Context(), vacation.ID, nil); err != nil {
		t.Fatal(err)
	}
	if body := introductionPage(t, s, vacation, "de"); strings.Contains(body, "data-introduction") {
		t.Fatal("people from Settings were substituted for an empty trip participant list")
	}
}

func TestIntroductionsReuseCustomNamesAndEscapeParticipants(t *testing.T) {
	s, _, vacation, sheet, people := newIntroductionTest(t, "de")
	frame := &models.IntroductionPhrase{Text: "Jeg hedder {name}", Pronunciation: "yai HED-er {name}"}
	profile := &models.CustomTravelPhrase{
		VacationID: vacation.ID, SourceLanguage: "de", DestinationKey: sheet.DestinationKey, TargetLanguage: sheet.Language,
	}
	if err := s.store.PutCheatsheetIntroduction(t.Context(), profile, frame); err != nil {
		t.Fatal(err)
	}
	for _, original := range []string{"Ich heiße Daniel.", "Wie spät ist es?"} {
		phrase := *profile
		phrase.Original, phrase.Text, phrase.Pronunciation = original, "Saved custom translation", "Saved pronunciation"
		if err := s.store.PutCustomCheatsheetPhrase(t.Context(), &phrase); err != nil {
			t.Fatal(err)
		}
	}
	hostile := &models.Person{Name: `<script>alert("name")</script>`}
	if err := s.store.CreatePerson(t.Context(), hostile); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetVacationParticipants(t.Context(), vacation.ID, []uuid.UUID{people[0].ID, people[0].ID, hostile.ID}); err != nil {
		t.Fatal(err)
	}
	pending := *profile
	pending.Original = "Ich heiße Daniel"
	if _, err := s.store.ReserveCheatsheetJob(t.Context(), &models.CheatsheetJob{
		VacationID: vacation.ID, SourceLanguage: "de", DestinationKey: sheet.DestinationKey,
		Key: pending.JobKey(), Original: pending.Original, TargetLanguage: sheet.Language, Status: "queued",
	}, false); err != nil {
		t.Fatal(err)
	}
	body := introductionPage(t, s, vacation, "de")
	if strings.Count(body, "Ich heiße Daniel") != 1 || strings.Count(body, "Saved custom translation") != 2 ||
		strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") || strings.Count(body, "data-introduction") != 2 {
		t.Fatalf("duplicate or unsafe introduction: %s", body)
	}
	custom, err := s.store.ListCustomCheatsheetPhrases(t.Context(), profile)
	if err != nil || len(custom) != 2 {
		t.Fatal("rendering removed or rewrote saved custom phrases")
	}
}

func TestIntroductionFailureRequiresExplicitAttemptBoundRetry(t *testing.T) {
	s, backend, vacation, sheet, _ := newIntroductionTest(t, "en")
	backend.fail = true
	s.queueCheatsheetIntroductions(t.Context())
	s.drainCheatsheets(t.Context())
	s.queueCheatsheetIntroductions(t.Context())
	s.drainCheatsheets(t.Context())
	if backend.calls != 1 {
		t.Fatal("failed provider call was automatically replayed")
	}
	job := introductionJob(sheet)
	status, err := s.store.GetCheatsheetJob(t.Context(), job)
	if err != nil || status != "failed" {
		t.Fatalf("failure not persisted: %s %v", status, err)
	}
	body := introductionPage(t, s, vacation, "en")
	if !strings.Contains(body, "Retry self-introduction") || strings.Contains(body, `hx-trigger="every 3s"`) {
		t.Fatal("failed frame must stop polling and expose explicit retry")
	}
	path := "/vacations/" + vacation.ID.String() + "/cheatsheet/introductions?fragment=rows"
	values := url.Values{"retry_attempt": {job.Attempt}, "destination_key": {sheet.DestinationKey}, "target_language": {sheet.Language}}
	if rec := postAISettings(s, path, values, false); rec.Code != http.StatusForbidden {
		t.Fatal("retry accepted without CSRF")
	}
	backend.fail = false
	if rec := postAISettings(s, path, values, true); rec.Code != http.StatusOK {
		t.Fatalf("retry failed: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postAISettings(s, path, values, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("replayed attempt reserved another call")
	}
	s.drainCheatsheets(t.Context())
	if backend.calls != 2 {
		t.Fatal("explicit retry did not dispatch exactly once")
	}
	if body := introductionPage(t, s, vacation, "en"); !strings.Contains(body, "Jeg hedder Daniel") {
		t.Fatal("retry did not save/render the frame")
	}
}

func TestIntroductionReconciliationHandlesQueueCapacity(t *testing.T) {
	s, backend, vacation, sheet, people := newIntroductionTest(t, "en")
	for range 18 {
		v := *vacation
		v.ID = uuid.New()
		if err := s.store.CreateVacation(t.Context(), &v); err != nil {
			t.Fatal(err)
		}
		if err := s.store.SetVacationParticipants(t.Context(), v.ID, []uuid.UUID{people[0].ID}); err != nil {
			t.Fatal(err)
		}
		copy := *sheet
		copy.VacationID = v.ID
		if err := s.store.PutCheatsheet(t.Context(), &copy); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		s.queueCheatsheetIntroductions(t.Context())
		count, err := s.store.CountPendingCheatsheetJobs(t.Context())
		if err != nil || count > 16 {
			t.Fatalf("queue limit exceeded: %d %v", count, err)
		}
		s.drainCheatsheets(t.Context())
	}
	if backend.calls != 19 {
		t.Fatalf("bounded scan starved pending sheets or duplicated calls: %d", backend.calls)
	}
}

func TestIntroductionWorkerBackfillsExistingSheets(t *testing.T) {
	s, _, vacation, sheet, _ := newIntroductionTest(t, "en")
	stop := s.StartCheatsheetWorker(context.Background())
	defer stop()
	waitCheatsheetStatus(t, s, introductionJob(sheet), "ready")
	if body := introductionPage(t, s, vacation, "en"); !strings.Contains(body, "Jeg hedder Therese") {
		t.Fatal("existing sheet was not filled by lifecycle worker")
	}
}

func TestIntroductionFramePreservedAcrossStandardRegeneration(t *testing.T) {
	s, _, vacation, sheet, _ := newIntroductionTest(t, "en")
	s.queueCheatsheetIntroductions(t.Context())
	s.drainCheatsheets(t.Context())
	profile := introductionJob(sheet).Phrase()
	phrase := *profile
	phrase.Original, phrase.Text, phrase.Pronunciation = "Keep custom", "Translation", "Pronunciation"
	if err := s.store.PutCustomCheatsheetPhrase(t.Context(), &phrase); err != nil {
		t.Fatal(err)
	}

	sheet.Phrases[0].Text = "Regenerated hello"
	if err := s.store.PutCheatsheet(t.Context(), sheet); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.GetCheatsheet(t.Context(), vacation.ID, "en")
	if err != nil || got.Introduction == nil || got.Phrases[0].Text != "Regenerated hello" {
		t.Fatalf("regeneration discarded frame or vocabulary: %+v %v", got, err)
	}
	custom, err := s.store.ListCustomCheatsheetPhrases(t.Context(), profile)
	if err != nil || len(custom) != 1 {
		t.Fatal("regeneration discarded custom cache")
	}
	sheet.Language = "German"
	if err := s.store.PutCheatsheet(t.Context(), sheet); err != nil {
		t.Fatal(err)
	}
	if err := s.store.PutCheatsheetIntroduction(t.Context(), profile, got.Introduction); err == nil {
		t.Fatal("stale target-language frame was saved")
	}
	current, err := s.store.GetCheatsheet(t.Context(), vacation.ID, "en")
	if err != nil || current.Introduction != nil {
		t.Fatal("old target-language frame survived language change")
	}
}

func TestIntroductionCompletionRejectsChangedDestination(t *testing.T) {
	s, backend, vacation, sheet, _ := newIntroductionTest(t, "en")
	s.queueCheatsheetIntroductions(t.Context())
	backend.duringCall = func() {
		vacation.Destination = "A different country"
		if err := s.store.UpdateVacation(t.Context(), vacation); err != nil {
			t.Fatal(err)
		}
	}
	s.drainCheatsheets(t.Context())
	got, err := s.store.GetCheatsheet(t.Context(), vacation.ID, "en")
	if err != nil || got.Introduction != nil {
		t.Fatal("stale introduction was attached after a destination edit")
	}
	if status, err := s.store.GetCheatsheetJob(t.Context(), introductionJob(sheet)); err != nil || status != "failed" {
		t.Fatal("stale completion did not terminate explicitly")
	}
}
