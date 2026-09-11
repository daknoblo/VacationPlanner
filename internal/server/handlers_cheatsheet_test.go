package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

type cheatsheetBackend struct {
	stubFoundry
	response   []byte
	duringCall func()
}

func (b *cheatsheetBackend) DoChat(context.Context, foundry.Target, []byte) ([]byte, error) {
	b.calls++
	if b.duringCall != nil {
		b.duringCall()
	}
	if b.fail {
		return nil, errors.New("test provider unavailable")
	}
	return b.response, nil
}

func newCheatsheetTest(t *testing.T) (*Server, *cheatsheetBackend, *models.Vacation) {
	t.Helper()
	s, _ := foundryTestServer(t)
	v := &models.Vacation{Title: "Paris", Destination: "Paris, France", StartDate: time.Now(), EndDate: time.Now().Add(24 * time.Hour)}
	if err := s.store.CreateVacation(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	if err := s.store.PutSetting(context.Background(), s.foundrySettingKey("chat"), "production-chat"); err != nil {
		t.Fatal(err)
	}
	sheet := models.Cheatsheet{Country: "France", Language: "French"}
	for _, meaning := range models.TravelPhraseMeanings() {
		sheet.Phrases = append(sheet.Phrases, models.TravelPhrase{Key: meaning.Key, Text: "bonjour", Pronunciation: "bon-zhoor"})
	}
	sheet.Phrases[0].Text = "<script>not executable</script>"
	content, err := json.Marshal(sheet)
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
	if err != nil {
		t.Fatal(err)
	}
	backend := &cheatsheetBackend{response: response}
	s.ai = ai.New(backend)
	return s, backend, v
}

func getCheatsheetPage(s *Server, v *models.Vacation) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/vacations/"+v.ID.String()+"/cheatsheet", nil))
	return rec
}

func TestCheatsheetCacheAndFailedRefresh(t *testing.T) {
	s, b, v := newCheatsheetTest(t)
	path := "/vacations/" + v.ID.String() + "/cheatsheet"
	if rec := getCheatsheetPage(s, v); rec.Code != http.StatusOK || b.calls != 0 {
		t.Fatal("reading an empty cheatsheet generated content")
	}
	if rec := postAISettings(s, path, nil, false); rec.Code != http.StatusForbidden || b.calls != 0 {
		t.Fatal("generation without CSRF accepted")
	}
	for range 2 {
		rec := postAISettings(s, path, nil, true)
		if rec.Code != http.StatusOK || b.calls != 1 {
			t.Fatalf("duplicate generation or failure: %d %d %s", rec.Code, b.calls, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<script>not executable</script>") || strings.Contains(rec.Body.String(), "Charges") {
			t.Fatal("unsafe text or unwanted cost notice in cheatsheet")
		}
	}
	original, err := s.store.GetCheatsheet(context.Background(), v.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	b.fail = true
	rec := postAISettings(s, path, url.Values{"refresh": {"1"}}, true)
	if rec.Code != http.StatusUnprocessableEntity || b.calls != 2 {
		t.Fatal("failed explicit regeneration not surfaced")
	}
	retained, err := s.store.GetCheatsheet(context.Background(), v.ID, "en")
	if err != nil || !original.CreatedAt.Equal(retained.CreatedAt) {
		t.Fatal("failed regeneration overwrote cached content")
	}
	if _, err := s.store.GetCheatsheet(context.Background(), v.ID, "de"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("source languages share a cache")
	}
}

func TestCheatsheetDestinationChangesAndBusyRequests(t *testing.T) {
	s, b, v := newCheatsheetTest(t)
	path := "/vacations/" + v.ID.String() + "/cheatsheet"
	job := v.ID.String() + ":en"
	s.cheatsheetJobs.Store(job, struct{}{})
	if rec := postAISettings(s, path, nil, true); rec.Code != http.StatusUnprocessableEntity || b.calls != 0 {
		t.Fatal("concurrent request generated content")
	}
	s.cheatsheetJobs.Delete(job)
	b.duringCall = func() {
		v.Destination = "Tokyo, Japan"
		if err := s.store.UpdateVacation(context.Background(), v); err != nil {
			t.Error(err)
		}
	}
	if rec := postAISettings(s, path, nil, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("stale in-flight destination result accepted")
	}
	if _, err := s.store.GetCheatsheet(context.Background(), v.ID, "en"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("old destination result was saved")
	}
}

func customPhraseTest(t *testing.T) (*Server, *cheatsheetBackend, *models.Vacation, string) {
	t.Helper()
	s, b, v := newCheatsheetTest(t)
	if rec := postAISettings(s, "/vacations/"+v.ID.String()+"/cheatsheet", nil, true); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	b.response = []byte(`{"choices":[{"message":{"content":"{\"text\":\"<script>translation</script>\",\"pronunciation\":\"bon-zhoor\"}"}}]}`)
	return s, b, v, "/vacations/" + v.ID.String() + "/cheatsheet/phrases"
}

func TestCheatsheetCustomValidationCacheAndEscaping(t *testing.T) {
	s, b, v, path := customPhraseTest(t)
	for _, text := range []string{"", " \t ", strings.Repeat("ü", 501), "hello\x00"} {
		if rec := postAISettings(s, path, url.Values{"phrase": {text}}, true); rec.Code != http.StatusUnprocessableEntity || b.calls != 1 {
			t.Fatalf("invalid custom phrase reached provider: %d %d", rec.Code, b.calls)
		}
	}
	input := "<script>original</script>"
	if rec := postAISettings(s, path, url.Values{"phrase": {input}}, false); rec.Code != http.StatusForbidden || b.calls != 1 {
		t.Fatal("custom translation bypasses CSRF")
	}
	for range 2 {
		rec := postAISettings(s, path, url.Values{"phrase": {input}}, true)
		if rec.Code != http.StatusOK || b.calls != 2 {
			t.Fatalf("custom phrase duplicated or failed: %d %d %s", rec.Code, b.calls, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<script>") || !strings.Contains(rec.Body.String(), "&lt;script&gt;original") ||
			!strings.Contains(rec.Body.String(), "&lt;script&gt;translation") {
			t.Fatal("custom original or generated output rendered unsafely")
		}
	}
	// Cached translations remain available without configured AI.
	s.ai = ai.New(nil)
	if rec := postAISettings(s, path, url.Values{"phrase": {input}}, true); rec.Code != http.StatusOK || b.calls != 2 {
		t.Fatal("cached custom phrase needs AI")
	}
	if rec := getCheatsheetPage(s, v); rec.Code != http.StatusOK || b.calls != 2 {
		t.Fatal("GET generated a custom phrase")
	}
}

func TestCheatsheetCustomCachePreservesOriginalCase(t *testing.T) {
	s, b, v, path := customPhraseTest(t)
	for i, text := range []string{"Sie", "sie", " Sie ", " sie "} {
		rec := postAISettings(s, path, url.Values{"phrase": {text}}, true)
		wantCalls := 3
		if i == 0 {
			wantCalls = 2
		}
		if rec.Code != http.StatusOK || b.calls != wantCalls {
			t.Fatalf("case-sensitive cache for %q: status=%d calls=%d, want %d",
				text, rec.Code, b.calls, wantCalls)
		}
	}
	key, err := cheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	profile := &models.CustomTravelPhrase{
		VacationID: v.ID, SourceLanguage: "en", DestinationKey: key, TargetLanguage: "French",
	}
	saved, err := s.store.ListCustomCheatsheetPhrases(context.Background(), profile)
	if err != nil || len(saved) != 2 || saved[0].Original != "Sie" || saved[1].Original != "sie" {
		t.Fatalf("original casing was not independently persisted: %+v %v", saved, err)
	}
}

func TestCheatsheetCustomFailureAndConcurrentRequestDoNotRetry(t *testing.T) {
	s, b, _, path := customPhraseTest(t)
	input := url.Values{"phrase": {"Please help"}}
	b.duringCall = func() {
		rec := postAISettings(s, path, input, true)
		if rec.Code != http.StatusUnprocessableEntity || b.calls != 2 {
			t.Errorf("concurrent duplicate custom phrase: %d %d", rec.Code, b.calls)
		}
	}
	b.fail = true
	for range 2 {
		rec := postAISettings(s, path, input, true)
		if rec.Code != http.StatusUnprocessableEntity || b.calls != 2 {
			t.Fatalf("unknown provider outcome was retried: %d %d", rec.Code, b.calls)
		}
	}
}

func TestCheatsheetCustomRegenerationAndDestinationChanges(t *testing.T) {
	s, b, v, path := customPhraseTest(t)
	input := url.Values{"phrase": {"My custom phrase"}}
	if rec := postAISettings(s, path, input, true); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	_, sheetBackend, _ := newCheatsheetTest(t)
	b.response = sheetBackend.response
	if rec := postAISettings(s, "/vacations/"+v.ID.String()+"/cheatsheet", url.Values{"refresh": {"1"}}, true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "My custom phrase") {
		t.Fatal("regeneration lost saved custom phrase")
	}
	v.Destination = "Tokyo, Japan"
	if err := s.store.UpdateVacation(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	rec := getCheatsheetPage(s, v)
	if strings.Contains(rec.Body.String(), "My custom phrase") {
		t.Fatal("destination change displayed an old-language custom translation")
	}
	calls := b.calls
	if rec := postAISettings(s, path, input, true); rec.Code != http.StatusUnprocessableEntity || b.calls != calls {
		t.Fatal("custom translation accepted without a current sheet")
	}
}

func TestCheatsheetCustomDestinationEditDuringProviderCall(t *testing.T) {
	s, b, v, path := customPhraseTest(t)
	oldKey, err := cheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	b.duringCall = func() {
		v.Destination = "Tokyo"
		if err := s.store.UpdateVacation(context.Background(), v); err != nil {
			t.Error(err)
		}
	}
	if rec := postAISettings(s, path, url.Values{"phrase": {"Do not save me"}}, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatal("stale phrase accepted")
	}
	profile := &models.CustomTravelPhrase{VacationID: v.ID, SourceLanguage: "en", DestinationKey: oldKey, TargetLanguage: "French"}
	if phrases, err := s.store.ListCustomCheatsheetPhrases(context.Background(), profile); err != nil || len(phrases) != 0 {
		t.Fatalf("stale in-flight phrase saved: %+v %v", phrases, err)
	}
}
