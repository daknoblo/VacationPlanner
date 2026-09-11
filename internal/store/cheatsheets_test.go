package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func cheatsheetStoreFixture(t *testing.T) (*SQLite, *models.Vacation, *models.Cheatsheet) {
	t.Helper()
	st := newTestStore(t)
	v := &models.Vacation{Title: "Paris", Destination: "Paris", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	key, err := models.CheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	sheet := &models.Cheatsheet{
		VacationID: v.ID, DestinationKey: key, SourceLanguage: "en", Country: "France", Language: "French",
	}
	for _, m := range models.TravelPhraseMeanings() {
		sheet.Phrases = append(sheet.Phrases, models.TravelPhrase{Key: m.Key, Text: "bonjour"})
	}
	if err := st.PutCheatsheet(context.Background(), sheet); err != nil {
		t.Fatal(err)
	}
	return st, v, sheet
}

func TestCheatsheetCustomCacheProfilesAndRegeneration(t *testing.T) {
	st, v, sheet := cheatsheetStoreFixture(t)
	ctx := context.Background()
	p := &models.CustomTravelPhrase{
		VacationID: v.ID, DestinationKey: sheet.DestinationKey, SourceLanguage: "en",
		TargetLanguage: "French", Original: "My phrase", Text: "Ma phrase", Pronunciation: "ma fraz",
	}
	for range 2 {
		if err := st.PutCustomCheatsheetPhrase(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutCheatsheet(ctx, sheet); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListCustomCheatsheetPhrases(ctx, p)
	if err != nil || len(rows) != 1 || rows[0].Original != p.Original {
		t.Fatalf("custom phrase lost or duplicated during regeneration: %+v, %v", rows, err)
	}
	for _, change := range []func(*models.CustomTravelPhrase){
		func(profile *models.CustomTravelPhrase) { profile.SourceLanguage = "de" },
		func(profile *models.CustomTravelPhrase) { profile.TargetLanguage = "German" },
		func(profile *models.CustomTravelPhrase) { profile.DestinationKey = "other" },
	} {
		profile := *p
		change(&profile)
		rows, err := st.ListCustomCheatsheetPhrases(ctx, &profile)
		if err != nil || len(rows) != 0 {
			t.Fatalf("independent cache profiles leak: %+v, %v", rows, err)
		}
	}
	sheet.Language = "German"
	if err := st.PutCheatsheet(ctx, sheet); err != nil {
		t.Fatal(err)
	}
	if err := st.PutCustomCheatsheetPhrase(ctx, p); !errors.Is(err, ErrCheatsheetDestinationChanged) {
		t.Fatalf("in-flight target-language change accepted: %v", err)
	}
	v.Destination = "Tokyo"
	if err := st.UpdateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := st.PutCheatsheet(ctx, sheet); !errors.Is(err, ErrCheatsheetDestinationChanged) {
		t.Fatalf("in-flight destination change accepted: %v", err)
	}
	if err := st.PutCustomCheatsheetPhrase(ctx, p); !errors.Is(err, ErrCheatsheetDestinationChanged) {
		t.Fatalf("stale custom phrase accepted: %v", err)
	}
}

func TestCheatsheetDurableReservationAndBoundedQueue(t *testing.T) {
	st, v, sheet := cheatsheetStoreFixture(t)
	ctx := context.Background()
	job := &models.CheatsheetJob{
		VacationID: v.ID, SourceLanguage: "en", DestinationKey: sheet.DestinationKey, Key: "sheet", Status: "queued",
	}
	ok, err := st.ReserveCheatsheetJob(ctx, job, false)
	if err != nil || !ok {
		t.Fatalf("reserve: %v %v", ok, err)
	}
	ok, err = st.ReserveCheatsheetJob(ctx, job, true)
	if err != nil || ok {
		t.Fatalf("duplicate reservation: %v %v", ok, err)
	}
	if ok, err := st.ClaimCheatsheetJob(ctx, job); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, err := st.ClaimCheatsheetJob(ctx, job); err != nil || ok {
		t.Fatalf("duplicate claim: %v %v", ok, err)
	}
	if err := st.InterruptCheatsheetJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err := st.GetCheatsheetJob(ctx, job); err != nil || status != "failed" {
		t.Fatalf("interrupted paid call must fail, not replay: %s %v", status, err)
	}
	if ok, err := st.ReserveCheatsheetJob(ctx, job, false); err != nil || ok {
		t.Fatalf("implicit retry after unknown outcome: %v %v", ok, err)
	}
	if ok, err := st.ReserveCheatsheetJob(ctx, job, true); err != nil || !ok {
		t.Fatalf("explicit regeneration disallowed: %v %v", ok, err)
	}
	for i := range 16 {
		next := *job
		next.Key = fmt.Sprintf("phrase:%d", i)
		ok, err := st.ReserveCheatsheetJob(ctx, &next, false)
		if i < 15 && (err != nil || !ok) {
			t.Fatalf("queue %d: %v %v", i, ok, err)
		}
		if i == 15 && !errors.Is(err, ErrCheatsheetQueueFull) {
			t.Fatalf("unbounded queue: %v %v", ok, err)
		}
	}
	if err := st.DeleteVacation(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetCheatsheetJob(ctx, job); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job not cascaded on vacation deletion: %v", err)
	}
}
