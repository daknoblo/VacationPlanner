package server

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestStaleLodgingFormKeepsAutomaticallyResolvedCoordinates(t *testing.T) {
	s := newIntegrationServer(t)
	ctx := context.Background()
	v := sampleVacation()
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	l := &models.Lodging{VacationID: v.ID, Name: "Hotel", Location: "Harbour Road 12, Sampletown", CheckIn: v.StartDate, CheckOut: v.EndDate}
	if err := s.store.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.store.UpdateLodgingCoordinates(ctx, l, 54.2, 10.1); err != nil || !changed {
		t.Fatal("coordinates not enriched", err)
	}
	form := url.Values{
		"name": {l.Name}, "location": {l.Location}, "latitude": {""}, "longitude": {""}, "cost": {"250"},
		"checkin_date": {l.CheckIn.Format("2006-01-02")}, "checkin_time": {"15:00"},
		"checkout_date": {l.CheckOut.Format("2006-01-02")}, "checkout_time": {"10:00"},
	}
	if rec := postAISettings(s, "/lodging/"+l.ID.String(), form, true); rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	got, err := s.store.GetLodging(ctx, l.ID)
	if err != nil || !got.HasCoords() || *got.Latitude != 54.2 || got.Cost == nil || *got.Cost != 250 {
		t.Fatalf("background coordinates or cost lost: %+v %v", got, err)
	}
	form.Set("location", "Different address")
	form.Set("checkout_date", l.CheckOut.Add(24*time.Hour).Format("2006-01-02"))
	if rec := postAISettings(s, "/lodging/"+l.ID.String(), form, true); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	got, err = s.store.GetLodging(ctx, l.ID)
	if err != nil || got.HasCoords() {
		t.Fatal("coordinates from an old address were retained")
	}
}
