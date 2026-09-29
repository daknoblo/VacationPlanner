package store

import (
	"errors"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIdeaDescriptionDurabilityAndSourceIsolation(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	v := &models.Vacation{Title: "Trip", Destination: "Denmark", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	i := &models.Item{VacationID: v.ID, Title: "Museum", Notes: "Keep notes", StartMin: 600, EndMin: 660}
	if err := st.CreateItem(ctx, i); err != nil {
		t.Fatal(err)
	}
	job, err := st.ClaimIdeaDescription(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimIdeaDescription(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("running job claimed twice", err)
	}
	job.Status, job.English, job.German = "ready", "A history museum.", "Ein Geschichtsmuseum."
	if err := st.FinishIdeaDescription(ctx, job); err != nil {
		t.Fatal(err)
	}
	path := st.path
	st.Close()
	st, err = NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.ClaimIdeaDescription(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("cached job replayed after restart", err)
	}
	saved, err := st.ListIdeaDescriptions(ctx, v.ID)
	if err != nil || saved[i.ID].German != job.German {
		t.Fatal(saved, err)
	}
	i.Title = "Different place"
	if err := st.UpdateItem(ctx, i); err != nil {
		t.Fatal(err)
	}
	saved, err = st.ListIdeaDescriptions(ctx, v.ID)
	if err != nil || len(saved) != 0 {
		t.Fatal("stale description served", saved, err)
	}
	next, err := st.ClaimIdeaDescription(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishIdeaDescription(ctx, job); err != nil {
		t.Fatal(err)
	}
	saved, err = st.ListIdeaDescriptions(ctx, v.ID)
	if err != nil || saved[i.ID].Status != "running" || saved[i.ID].English != "" {
		t.Fatal("old attempt overwrote new one", saved, err)
	}
	i.Description = "My manual description."
	if err := st.UpdateItem(ctx, i); err != nil {
		t.Fatal(err)
	}
	next.Status, next.English, next.German = "ready", "Stale text.", "Alter Text."
	if err := st.FinishIdeaDescription(ctx, next); err != nil {
		t.Fatal(err)
	}
	saved, err = st.ListIdeaDescriptions(ctx, v.ID)
	if err != nil || len(saved) != 0 {
		t.Fatal("manual description not respected", saved, err)
	}
	got, err := st.GetItem(ctx, i.ID)
	if err != nil || got.Description != i.Description || got.Notes != i.Notes || got.StartMin != 600 {
		t.Fatal("source data overwritten", got, err)
	}
}

func TestInterruptedDescriptionIsNotAutomaticallyRetried(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	v := &models.Vacation{Title: "Trip", Destination: "Denmark", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	i := &models.Item{VacationID: v.ID, Title: "Museum"}
	if err := st.CreateItem(ctx, i); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimIdeaDescription(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InterruptIdeaDescriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimIdeaDescription(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("interrupted paid call retried", err)
	}
	if pending, err := st.CountPendingIdeaDescriptions(ctx); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
}
