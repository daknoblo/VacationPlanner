package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/google/uuid"
)

func TestConcurrentTravelFirstSaveHasOneSource(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	v := &models.Vacation{Title: "Trip", Destination: "Paris", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cost := 100.0
			leg := &models.TravelSegment{VacationID: v.ID, Kind: models.TravelArrival, Cost: &cost}
			if err := st.UpsertTravelSegment(ctx, leg); err != nil {
				errs <- err
				return
			}
			ids <- leg.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	legs, err := st.ListTravelSegments(ctx, v.ID)
	if err != nil || len(legs) != 1 {
		t.Fatalf("first saves duplicated bookings: %+v, %v", legs, err)
	}
	for id := range ids {
		if id != legs[0].ID {
			t.Error("autosaves did not resolve the same source")
		}
	}
	total, err := st.SpendByVacation(ctx)
	if err != nil || total[v.ID] != 100 {
		t.Fatalf("duplicate budget cost: %v, %v", total, err)
	}
}
