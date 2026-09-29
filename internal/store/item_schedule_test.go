package store

import (
	"errors"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/google/uuid"
)

func TestScheduleItemRangePreservesOtherFields(t *testing.T) {
	s := newTestStore(t)
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	v := &models.Vacation{Title: "Trip", Destination: "Denmark", StartDate: day, EndDate: day.AddDate(0, 0, 2)}
	if err := s.CreateVacation(t.Context(), v); err != nil {
		t.Fatal(err)
	}
	item := &models.Item{VacationID: v.ID, Title: "Museum", Day: &day, StartMin: 540, EndMin: 600}
	if err := s.CreateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	// A separate edit completed since the scheduling handler read the item.
	item.Notes = "Recently edited"
	item.Title = "New title"
	cost := 40.0
	item.Cost = &cost
	if err := s.UpdateItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := s.ScheduleItemRange(t.Context(), item.ID, v.EndDate, 1410, 1440); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetItem(t.Context(), item.ID)
	if err != nil || !got.OnDay(v.EndDate) || got.StartMin != 1410 || got.EndMin != 1440 ||
		got.Notes != item.Notes || got.Title != item.Title || got.Cost == nil || *got.Cost != cost {
		t.Fatal("atomic schedule lost data", got, err)
	}
	for _, invalid := range [][2]int{{-5, 60}, {0, 15}, {1410, 1445}, {600, 500}} {
		if err := s.ScheduleItemRange(t.Context(), item.ID, day, invalid[0], invalid[1]); err == nil {
			t.Fatal("invalid range accepted", invalid)
		}
	}
	if err := s.ScheduleItemRange(t.Context(), item.ID, day.AddDate(0, 0, -1), 540, 600); !errors.Is(err, ErrNotFound) {
		t.Fatal("out-of-trip schedule accepted", err)
	}
	if err := s.ScheduleItemRange(t.Context(), uuid.New(), day, 540, 600); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing item accepted", err)
	}
}
