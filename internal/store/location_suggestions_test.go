package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/google/uuid"
)

func TestLocationSuggestionsRequireConfirmationAndTrackSource(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "suggestions.db")
	s, err := NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	v := &models.Vacation{Title: "Trip", Destination: "Hamburg", StartDate: day, EndDate: day}
	if err := s.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Skyline", Notes: "Private notes", Region: "Manual", RegionManual: true}
	if err := s.CreateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	candidate := models.LocationSuggestion{Label: "Skyline, Hamburg", Name: "Skyline", Latitude: 53.55, Longitude: 10}
	if changed, err := s.SaveLocationSuggestion(ctx, item, v, candidate); err != nil || !changed {
		t.Fatal("proposal not saved", changed, err)
	}
	records, err := s.ListLocationSuggestions(ctx, v.ID)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	proposal := records[item.ID]
	got, err := s.GetItem(ctx, item.ID)
	if err != nil || got.HasCoords() || got.Location != "" || got.Region != "Manual" || got.Notes != item.Notes {
		t.Fatal("proposal modified source", got, err)
	}
	if err := s.RejectLocationSuggestion(ctx, item.ID, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale rejection accepted", err)
	}
	if err := s.RejectLocationSuggestion(ctx, item.ID, proposal.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = NewSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if changed, err := s.SaveLocationSuggestion(ctx, item, v, candidate); err != nil || changed {
		t.Fatal("retry replaced a rejected suggestion", changed, err)
	}
	item.Notes = "Edited while proposal was visible"
	if err := s.UpdateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	records, err = s.ListLocationSuggestions(ctx, v.ID)
	if err != nil || !records[item.ID].Rejected {
		t.Fatal("unrelated edit invalidated rejection", records, err)
	}
	stale := *item
	item.Title = "New restaurant"
	if err := s.UpdateItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	records, err = s.ListLocationSuggestions(ctx, v.ID)
	if err != nil || len(records) != 0 {
		t.Fatal("obsolete suggestion returned", records, err)
	}
	if changed, err := s.SaveLocationSuggestion(ctx, &stale, v, candidate); err != nil || changed {
		t.Fatal("stale provider response saved", changed, err)
	}
	if err := s.RejectLocationSuggestion(ctx, item.ID, proposal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("changed source rejection accepted", err)
	}
	if changed, err := s.SaveLocationSuggestion(ctx, item, v, candidate); err != nil || !changed {
		t.Fatal("edited source cannot receive a new proposal", changed, err)
	}
	v.Destination = "Berlin"
	if err := s.UpdateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	records, err = s.ListLocationSuggestions(ctx, v.ID)
	if err != nil || len(records) != 0 {
		t.Fatal("old destination proposal returned", records, err)
	}
}
