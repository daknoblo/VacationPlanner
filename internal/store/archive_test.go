package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/google/uuid"
)

func TestArchiveVacationPreservesDataAndSurvivesEdits(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	today := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	v := &models.Vacation{Title: "Past trip", Destination: "Paris", StartDate: today.AddDate(0, 0, -5), EndDate: today.AddDate(0, 0, -1), Archived: true}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	if v.Archived {
		t.Fatal("new trips must not be archived")
	}
	cost := 25.0
	item := &models.Item{VacationID: v.ID, Title: "Museum", Cost: &cost}
	if err := st.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := st.ArchiveVacation(ctx, v.ID, today); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.GetVacation(ctx, v.ID)
	if err != nil || !got.Archived {
		t.Fatalf("archive not persisted: %+v %v", got, err)
	}
	// Ordinary updates do not control the archive flag.
	v.Title = "Edited title"
	if err := st.UpdateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetVacation(ctx, v.ID)
	if err != nil || !got.Archived || got.Title != v.Title {
		t.Fatal("edit lost archive state", err)
	}
	items, err := st.ListItems(ctx, v.ID)
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatal("archive changed trip items", err)
	}
	spend, err := st.SpendByVacation(ctx)
	if err != nil || spend[v.ID] != cost {
		t.Fatal("archive changed expenses", err)
	}
	all, err := st.ListVacations(ctx)
	if err != nil || len(all) != 1 || !all[0].Archived {
		t.Fatal("archived trip missing from full store list", err)
	}
}

func TestArchiveChecksCurrentStoredEndDate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	today := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	v := &models.Vacation{Title: "Trip", Destination: "Paris", StartDate: today.AddDate(0, 0, -2), EndDate: today}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveVacation(ctx, v.ID, today); !errors.Is(err, ErrVacationNotEnded) {
		t.Fatalf("end day must still be active: %v", err)
	}
	v.EndDate = today.AddDate(0, 0, 2)
	if err := st.UpdateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveVacation(ctx, v.ID, today); !errors.Is(err, ErrVacationNotEnded) {
		t.Fatalf("future trip archived: %v", err)
	}
	if err := st.ArchiveVacation(ctx, uuid.New(), today); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown trip error: %v", err)
	}
}
